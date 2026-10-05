package main

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/Nomadcxx/sysc-lock/internal/inhibit"
	"github.com/Nomadcxx/sysc-lock/internal/lockd"
	"github.com/Nomadcxx/sysc-lock/internal/session"
	"github.com/godbus/dbus/v5"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"time"
)

func runSession() error {
	logind, err := inhibit.NewLogind()
	if err != nil {
		return err
	}
	defer logind.Release()
	identity, err := session.ValidateNiri(logind.Connection())
	if err != nil {
		return err
	}
	owner, err := session.New(identity, filepath.Join(os.Getenv("XDG_RUNTIME_DIR"), "sysc-lock", "state.json"))
	if err != nil {
		return err
	}
	defer owner.Close()
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return err
	}
	defer conn.Close()
	if err = session.Serve(conn, owner); err != nil {
		return err
	}
	sys := logind.Connection()
	var sessionPath dbus.ObjectPath
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	err = sys.Object("org.freedesktop.login1", "/org/freedesktop/login1").CallWithContext(ctx, "org.freedesktop.login1.Manager.GetSession", 0, identity.Session).Store(&sessionPath)
	cancel()
	if err != nil {
		return err
	}
	signals := make(chan *dbus.Signal, 16)
	sys.Signal(signals)
	defer sys.RemoveSignal(signals)
	if err = sys.AddMatchSignal(dbus.WithMatchSender("org.freedesktop.login1"), dbus.WithMatchObjectPath("/org/freedesktop/login1"), dbus.WithMatchInterface("org.freedesktop.login1.Manager"), dbus.WithMatchMember("PrepareForSleep")); err != nil {
		return err
	}
	if err = sys.AddMatchSignal(dbus.WithMatchSender("org.freedesktop.login1"), dbus.WithMatchObjectPath(sessionPath), dbus.WithMatchInterface("org.freedesktop.login1.Session"), dbus.WithMatchMember("Lock")); err != nil {
		return err
	}
	limit, limitErr := logind.SleepDelayLimit()
	take := func() (func(), error) {
		if limitErr != nil {
			return nil, limitErr
		}
		return logind.Delay()
	}
	owner.Arm(take)
	if err = session.Notify("READY=1"); err != nil {
		return err
	}
	type completed struct {
		generation uint64
		phase      lockd.Phase
		err        error
	}
	done := make(chan completed, 1)
	// ponytail: a 100 ms deadline watchdog covers logind's finite delay, not animation.
	watchdog := time.NewTicker(100 * time.Millisecond)
	defer watchdog.Stop()
	hinted := false
	var clearedUnlock uint64
	for {
		select {
		case <-conn.Context().Done():
			return fmt.Errorf("session bus connection lost; lock state retained")
		case <-sys.Context().Done():
			return fmt.Errorf("logind connection lost; lock state retained")
		case generation := <-owner.Requests:
			go func() {
				var reportErr error
				phase, err := runLocker(func(event lockd.Snapshot) {
					if e := owner.Report(generation, event); e != nil {
						reportErr = e
					}
				}, func() error { return owner.BeginUnlock(generation) })
				if reportErr != nil {
					err = reportErr
				}
				done <- completed{generation, phase, err}
			}()
		case result := <-done:
			err = owner.End(result.generation, result.phase, result.err)
			if result.phase == lockd.Done && err == nil {
				owner.Arm(take)
			}
			if err != nil || owner.Snapshot().Phase == "sealed/unknown" {
				return fmt.Errorf("lock state uncertain; native recovery required (phase %v): %v", result.phase, err)
			}
		case <-owner.Changed:
			v := owner.Snapshot()
			if v.Phase == "sealed" && !hinted {
				if err = logind.SetLocked(sessionPath, true); err != nil {
					fmt.Fprintln(os.Stderr, "sysc-lock: LockedHint unavailable")
				}
				hinted = true
			}
			if v.Phase == "idle" && v.ConfirmedUnlock != 0 && (hinted || clearedUnlock != v.ConfirmedUnlock) {
				if err = logind.SetLocked(sessionPath, false); err != nil {
					fmt.Fprintln(os.Stderr, "sysc-lock: clearing LockedHint failed")
				}
				hinted = false
				clearedUnlock = v.ConfirmedUnlock
			}
			if err = session.Publish(conn, owner); err != nil {
				return err
			}
		case sig, ok := <-signals:
			if !ok || sig == nil {
				return fmt.Errorf("logind event connection lost")
			}
			if sig.Name == "org.freedesktop.login1.Manager.PrepareForSleep" && sig.Path == "/org/freedesktop/login1" && len(sig.Body) == 1 {
				sleeping, ok := sig.Body[0].(bool)
				if !ok {
					continue
				}
				if sleeping {
					if err = owner.PrepareSleep(time.Now(), limit); err != nil {
						fmt.Fprintln(os.Stderr, "sysc-lock: sleep acquisition failed")
					}
				} else {
					if err = owner.Resume(take); err != nil {
						fmt.Fprintln(os.Stderr, "sysc-lock: resume acquisition failed")
					}
				}
			} else if sig.Name == "org.freedesktop.login1.Session.Lock" && sig.Path == sessionPath {
				if _, err = owner.Lock(); err != nil {
					fmt.Fprintln(os.Stderr, "sysc-lock: logind lock failed")
				}
			}
		case now := <-watchdog.C:
			owner.CheckSleepDeadline(now)
		}
	}
}

func requestLock() error {
	identity, err := requestIdentity()
	if err != nil {
		return err
	}
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return err
	}
	defer conn.Close()
	signals := make(chan *dbus.Signal, 16)
	conn.Signal(signals)
	defer conn.RemoveSignal(signals)
	if err = conn.AddMatchSignal(dbus.WithMatchSender(session.BusName), dbus.WithMatchObjectPath(session.BusPath), dbus.WithMatchInterface(session.BusName), dbus.WithMatchMember("Changed")); err != nil {
		return err
	}
	if err = conn.AddMatchSignal(dbus.WithMatchSender("org.freedesktop.DBus"), dbus.WithMatchInterface("org.freedesktop.DBus"), dbus.WithMatchMember("NameOwnerChanged"), dbus.WithMatchArg(0, session.BusName)); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var unique string
	if err = conn.BusObject().CallWithContext(ctx, "org.freedesktop.DBus.GetNameOwner", 0, session.BusName).Store(&unique); err != nil {
		return fmt.Errorf("session owner unavailable: %w", err)
	}
	var uid uint32
	if err = conn.BusObject().CallWithContext(ctx, "org.freedesktop.DBus.GetConnectionUnixUser", 0, unique).Store(&uid); err != nil {
		return err
	}
	if uid != identity.UID {
		return fmt.Errorf("session owner UID differs")
	}
	// The unique address identifies the reply sender; reconcile the registered
	// owner around RPCs and before success so queued owner loss cannot confirm it.
	checkOwner := func() error {
		checkCtx, checkCancel := context.WithTimeout(context.Background(), time.Second)
		defer checkCancel()
		var current string
		if err := conn.BusObject().CallWithContext(checkCtx, "org.freedesktop.DBus.GetNameOwner", 0, session.BusName).Store(&current); err != nil {
			return fmt.Errorf("session owner unavailable; unlock unconfirmed: %w", err)
		}
		return validateRequestOwner(unique, current)
	}
	object := conn.Object(unique, session.BusPath)
	var data string
	if err = object.CallWithContext(ctx, session.BusName+".GetState", 0).Store(&data); err != nil {
		return err
	}
	var v session.Snapshot
	if err = decodeRequestSnapshot(data, identity, &v); err != nil {
		return err
	}
	if err = checkOwner(); err != nil {
		return err
	}
	// Validate the owner's session before mutating it through Lock.
	if err = object.CallWithContext(ctx, session.BusName+".Lock", 0).Store(&data); err != nil {
		return err
	}
	var requested session.Snapshot
	if err = decodeRequestSnapshot(data, identity, &requested); err != nil {
		return err
	}
	if err = checkOwner(); err != nil {
		return err
	}
	if err = validateRequestProgress(v, requested); err != nil {
		return err
	}
	v = requested
	if v.Generation == 0 {
		return fmt.Errorf("lock acquisition has no generation")
	}
	generation := v.Generation
	sealed := false
	for {
		if (v.Generation == generation && v.Phase == "sealed" && !sealed) || v.ConfirmedUnlock == generation {
			if err = checkOwner(); err != nil {
				return err
			}
		}
		if v.Generation == generation && v.Phase == "sealed" && !sealed {
			emitLockedHandshake(os.Stdout)
			sealed = true
		}
		if v.ConfirmedUnlock == generation && generation != 0 {
			fmt.Println("sysc-lock: unlocked")
			return nil
		}
		if v.Phase == "failed-before-acquisition" || v.Phase == "sealed/unknown" {
			return fmt.Errorf("lock %s", v.Phase)
		}
		var sig *dbus.Signal
		var ok bool
		select {
		case <-conn.Context().Done():
			return fmt.Errorf("session connection lost; unlock unconfirmed")
		case sig, ok = <-signals:
		}
		if !ok || sig == nil {
			return fmt.Errorf("session connection lost; unlock unconfirmed")
		}
		if sig.Name == "org.freedesktop.DBus.NameOwnerChanged" {
			return fmt.Errorf("session owner changed; unlock unconfirmed")
		}
		if sig.Sender != unique || sig.Path != session.BusPath || sig.Name != session.BusName+".Changed" || len(sig.Body) != 1 {
			continue
		}
		text, ok := sig.Body[0].(string)
		if !ok || len(text) > 16384 {
			return fmt.Errorf("invalid session snapshot")
		}
		var next session.Snapshot
		if err = json.Unmarshal([]byte(text), &next); err != nil {
			return err
		}
		if err = validateRequestSnapshot(next, identity); err != nil {
			return err
		}
		if next.Sequence < v.Sequence {
			continue
		}
		if err = validateRequestProgress(v, next); err != nil {
			return err
		}
		v = next
	}
}

// requestIdentity ties the client to the registered socket instance, never a scan.
func requestIdentity() (session.Identity, error) {
	id := session.Identity{UID: uint32(os.Getuid()), Session: os.Getenv("XDG_SESSION_ID")}
	socket := os.Getenv("NIRI_SOCKET")
	runtime := filepath.Join("/run/user", strconv.Itoa(os.Getuid()))
	if id.Session == "" || !filepath.IsAbs(socket) || filepath.Dir(socket) != runtime {
		return id, fmt.Errorf("missing Niri startup session registration")
	}
	info, err := os.Lstat(socket)
	if err != nil {
		return id, err
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || info.Mode()&os.ModeSocket == 0 || st.Uid != id.UID {
		return id, fmt.Errorf("invalid registered Niri socket")
	}
	id.Compositor = fmt.Sprintf("%s:%d:%d", socket, st.Dev, st.Ino)
	return id, nil
}
func validateRequestSnapshot(v session.Snapshot, identity session.Identity) error {
	if identity.Session == "" || identity.Compositor == "" || v.Session != identity.Session || v.Compositor != identity.Compositor || (v.Generation == 0 && v.Phase != "idle") || v.ConfirmedUnlock > v.Generation {
		return fmt.Errorf("invalid state for registered graphical session")
	}
	switch v.Phase {
	case "idle", "requesting", "sealed", "unlocking", "recovering", "failed-before-acquisition", "sealed/unknown":
		return nil
	default:
		return fmt.Errorf("invalid session phase")
	}
}

func decodeRequestSnapshot(data string, identity session.Identity, value *session.Snapshot) error {
	if len(data) > 16384 {
		return fmt.Errorf("invalid session snapshot")
	}
	var next session.Snapshot
	if err := json.Unmarshal([]byte(data), &next); err != nil {
		return err
	}
	if err := validateRequestSnapshot(next, identity); err != nil {
		return err
	}
	*value = next
	return nil
}

func validateRequestOwner(expected, current string) error {
	if expected == "" || current != expected {
		return fmt.Errorf("session owner changed; unlock unconfirmed")
	}
	return nil
}

func validateRequestProgress(previous, next session.Snapshot) error {
	if next.Sequence < previous.Sequence || next.Generation < previous.Generation || next.ConfirmedUnlock < previous.ConfirmedUnlock {
		return fmt.Errorf("session snapshot regressed; unlock unconfirmed")
	}
	return nil
}

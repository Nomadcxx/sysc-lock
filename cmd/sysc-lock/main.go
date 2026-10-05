// Command sysc-lock requests the supervised Wayland session owner.
// The --session owner acquires ext-session-lock-v1 and authenticates with PAM.
package main

import (
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Nomadcxx/sysc-lock/internal/auth"
	"github.com/Nomadcxx/sysc-lock/internal/config"
	"github.com/Nomadcxx/sysc-lock/internal/input"
	"github.com/Nomadcxx/sysc-lock/internal/lockd"
	"github.com/Nomadcxx/sysc-lock/internal/render"
	"github.com/Nomadcxx/sysc-lock/internal/theme"
)

// The request command exits 0 after confirmed authenticated unlock; failures exit 1.
// The persistent owner reports acquisition/unlock outcomes through its snapshots.
const version = "0.1.0-dev"

func main() {
	if len(os.Args) == 2 && os.Args[1] == "--version" {
		fmt.Println("sysc-lock", version)
		return
	}
	var err error
	switch {
	case len(os.Args) == 1:
		err = requestLock()
	case len(os.Args) == 2 && os.Args[1] == "--session":
		err = runSession()
	default:
		err = fmt.Errorf("usage: sysc-lock [--session|--version]")
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "sysc-lock:", err)
		os.Exit(1)
	}
}

// runLocker owns a single acquisition. The persistent session owns supervision.
func runLocker(report func(lockd.Snapshot), beforeUnlock func() error) (lockd.Phase, error) {
	if os.Getenv("WAYLAND_DISPLAY") == "" {
		return lockd.Idle, fmt.Errorf("WAYLAND_DISPLAY not set")
	}

	// SIGTERM before the compositor confirms "locked": abandon the lock
	// request (the session was never sealed). After
	// confirmation the lock MUST stay; ignore termination signals — the only
	// way out is authentication.
	sigs := make(chan os.Signal, 2)
	defer signal.Stop(sigs)
	signal.Notify(sigs, syscall.SIGTERM, syscall.SIGINT)

	user, err := currentUser()
	if err != nil {
		fmt.Fprintln(os.Stderr, "sysc-lock:", err)
		return lockd.Idle, err
	}
	host, _ := os.Hostname()
	pal, err := theme.Load(palettePath())
	if err != nil {
		pal = theme.Default()
	}
	view := lockd.NewView(pal, user, host)
	view.TextScale = lockd.SystemTextScale()

	model := &input.Model{}
	defer model.Clear()
	view.Entry = model

	authenticator := newAuthenticator(user)
	gate := &enterGate{}

	st := lockd.New()
	var client *lockd.Client
	client, err = lockd.Connect(st, func(k lockd.Key) {
		// Runs on the pump goroutine — sole mutator of model/view.
		view.Caps = k.CapsLock
		view.Layout = k.Layout
		view.Num = k.NumLock
		if view.Terminal() || st.Phase() != lockd.Locked {
			client.Repaint()
			return
		}
		submit, editErr := gate.press(model, &view.Reveal, k, time.Now())
		if editErr != nil {
			view.SetError(editErr.Error(), time.Now())
		}
		if submit {
			view.Busy = true
			go authenticate(authenticator, model.Password(), client, view, model, gate, gate.generation)
		}
		client.SetMotionFrozen(gate.busy, time.Now())
		client.Repaint()
	}, func(fb *render.Framebuffer, scale float64, background []byte) error {
		view.Scale = scale
		if background == nil {
			view.Render(fb, time.Now())
		} else {
			copy(fb.Pix, background)
			lockd.DimBackground(fb.Pix)
			view.RenderForeground(fb, time.Now())
		}
		return nil
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "sysc-lock:", err)
		return lockd.Idle, err
	}
	stopSignals := make(chan struct{})
	defer close(stopSignals)
	go func() {
		for {
			select {
			case <-stopSignals:
				return
			case <-sigs:
				client.Post(func() { client.AbortBeforeLocked() })
			}
		}
	}()
	client.OnEvent = report
	client.BeforeUnlock = beforeUnlock
	defer client.Close()
	client.OnDeadline = view.NextDeadline
	cfg, configErr := config.Load(config.Path())
	if configErr != nil {
		fmt.Fprintln(os.Stderr, "sysc-lock: invalid presentation config; using fallback")
		cfg = config.Default()
		cfg.ReducedMotion = true
	}
	view.StyleName, view.Clock24, view.Reduced = cfg.ClockStyle, cfg.Clock24h, cfg.ReducedMotion
	client.SetEffectRate(cfg.EffectFPS)
	client.EnableBackground(cfg.Effect, cfg.Palette, cfg.ReducedMotion)
	client.EnableWallpaper(os.Getenv("SYSC_LOCK_WALLPAPER"))

	if err := client.Lock(); err != nil {
		fmt.Fprintln(os.Stderr, "sysc-lock:", err)
		return lockd.Idle, err
	}
	if err := client.Run(); err != nil {

		fmt.Fprintln(os.Stderr, "sysc-lock: connection lost:", err)
		return client.State().Phase(), err
	}
	return client.State().Phase(), client.WaitBackground()
}

func authenticate(a authenticator, pass string, client *lockd.Client, view *lockd.View, model *input.Model, gate *enterGate, generation uint64) {
	res, err := a.Verify(a.User(), auth.PasswordPrompt(pass))
	pass = "" // immutable runtime/PAM copies cannot be reliably erased
	client.Post(func() {
		if !gate.accept(generation, client.State().Phase()) {
			return
		}
		gate.release()
		view.Busy = false
		model.Clear()
		switch {
		case err != nil:
			view.SetError("Authentication unavailable", time.Now())
		case res.OK:
			view.SetError("", time.Now())
			if err := client.UnlockAndQuit(); err != nil {
				if errors.Is(err, lockd.ErrUnlockDeferred) {
					view.SetError("Resume before unlocking", time.Now())
				} else {
					view.SetErrorTerminal("Unlock confirmation failed", time.Now())
				}
			}
		case res.Terminal:
			view.SetErrorTerminal(res.Message, time.Now())
			view.NoteAttempt(time.Now())
		default:
			view.Reject(res.Message, time.Now())
		}
		client.SetMotionFrozen(false, time.Now())
		client.Repaint()
	})
}

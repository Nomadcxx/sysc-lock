package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	"github.com/Nomadcxx/sysc-lock/internal/ambient"
)

// runAmbient collects the ambient snapshot once a second until SIGTERM. The
// locker owner spawns it after sealing and kills it on the way out.
func runAmbient() error {
	path := ambient.Path()
	if path == "" {
		return fmt.Errorf("XDG_RUNTIME_DIR not set")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return ambient.Run(ctx, path, time.Second, ambient.NewGather())
}

// loadAmbient reads the snapshot file and formats the status line. Every
// failure (missing, stale, junk, empty) is one empty string: the owner stays
// silent rather than guessing.
func loadAmbient(path string, now time.Time, maxRunes int) string {
	if path == "" || maxRunes < 1 {
		return ""
	}
	snap, err := ambient.Load(path, now)
	if err != nil {
		return ""
	}
	return snap.Line(maxRunes)
}

// ambientRow caches the status line so the owner touches the snapshot file at
// most once a second on its existing repaints — it never polls in a loop of
// its own.
type ambientRow struct {
	path string
	at   time.Time
	line string
}

func (r *ambientRow) Get(now time.Time, maxRunes int) string {
	if r.at.IsZero() || now.Sub(r.at) >= time.Second {
		r.at = now
		r.line = loadAmbient(r.path, now, maxRunes)
	}
	return r.line
}

// ambientChild is the running collector, or the zero value when it never
// started; stop is safe either way.
type ambientChild struct{ cmd *exec.Cmd }

// startAmbient re-execs this binary with --ambient in its own process group
// so the whole group dies with the locker. A start failure is one stderr line
// and a no-op child: the lock must still work without the row.
func startAmbient() ambientChild {
	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "sysc-lock: ambient collector not started: %v\n", err)
		return ambientChild{}
	}
	cmd := exec.Command(exe, "--ambient")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "sysc-lock: ambient collector not started: %v\n", err)
		return ambientChild{}
	}
	return ambientChild{cmd}
}

func (c ambientChild) stop() { killAmbient(c.cmd)() }

// killAmbient terminates the collector's process group and reaps it. The
// returned func is always non-nil and safe on a nil command.
func killAmbient(cmd *exec.Cmd) func() {
	return func() {
		if cmd == nil || cmd.Process == nil {
			return
		}
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
		_ = cmd.Wait()
	}
}

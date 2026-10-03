// Command sysc-lock locks the Wayland session via ext-session-lock-v1 and
// unlocks it with in-process PAM (service "login"). The default build has no
// bypass of any kind: no flag or env disables authentication or the inhibitor
// requirement (the lockdev build tag is a dev-harness-only exception, removed
// in Task 14).
package main

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Nomadcxx/sysc-lock/internal/auth"
	"github.com/Nomadcxx/sysc-lock/internal/input"
	"github.com/Nomadcxx/sysc-lock/internal/lockd"
	"github.com/Nomadcxx/sysc-lock/internal/render"
	"github.com/Nomadcxx/sysc-lock/internal/theme"
)

// Exit codes (contract with sysc-shell):
//
//	0 unlocked (PAM success)
//	2 refused  (compositor already locked / finished before locked)
//	3 aborted  (SIGTERM before the lock was confirmed; session never sealed)
//	4 no-inhibitor (logind unavailable; refusing to lock without sleep guard)
//	5 terminated (compositor ended the lock unexpectedly; respawn-eligible)
//	1 any other failure (connection lost, protocol error)
const version = "0.1.0-dev"

func main() {
	if len(os.Args) > 1 && os.Args[1] == "--version" {
		fmt.Println("sysc-lock", version)
		return
	}
	if os.Getenv("WAYLAND_DISPLAY") == "" {
		fmt.Fprintln(os.Stderr, "sysc-lock: WAYLAND_DISPLAY not set")
		os.Exit(1)
	}

	release := takeInhibit()

	// SIGTERM before the compositor confirms "locked": abandon the lock
	// request (spec-legal: the session was never sealed) and exit 3. After
	// confirmation the lock MUST stay; ignore termination signals — the only
	// way out is authentication.
	sigs := make(chan os.Signal, 2)
	signal.Notify(sigs, syscall.SIGTERM, syscall.SIGINT)

	user := currentUser()
	host, _ := os.Hostname()
	pal, err := theme.Load(palettePath())
	if err != nil {
		pal = theme.Default()
	}
	view := lockd.NewView(pal, user, host)
	view.Background = os.Getenv("SYSC_LOCK_WALLPAPER")
	view.Layout = os.Getenv("SYSC_LOCK_LAYOUT")
	model := &input.Model{}
	view.Entry = model

	authenticator := newAuthenticator()

	st := lockd.New()
	var client *lockd.Client
	client, err = lockd.Connect(st, func(k lockd.Key) {
		// Runs on the pump goroutine — sole mutator of model/view.
		if k.Enter {
			pass := model.Password()
			if pass == "" {
				return
			}
			go authenticate(authenticator, pass, client, view, model)
		} else if k.Backspace {
			model.Backspace()
		} else if k.Escape {
			model.Clear()
		} else if k.Text != "" {
			model.Append(k.Text)
		}
		view.Caps = k.CapsLock
		client.Repaint()
	}, func(w, h int) ([]byte, error) {
		fb := render.New(w, h)
		view.Render(fb, time.Now())
		return fb.Pix, nil
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "sysc-lock:", err)
		release()
		os.Exit(1)
	}
	go watchSignals(sigs, client, release)
	go watchLocked(client, release)

	if err := client.Lock(); err != nil {
		fmt.Fprintln(os.Stderr, "sysc-lock:", err)
		release()
		os.Exit(1)
	}
	if err := client.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "sysc-lock: connection lost:", err)
		release()
		os.Exit(1)
	}
	release()
	switch client.State().Phase() {
	case lockd.Done:
		fmt.Println("sysc-lock: unlocked")
		os.Exit(0)
	case lockd.Refused:
		fmt.Fprintln(os.Stderr, "sysc-lock: refused (session already locked)")
		os.Exit(2)
	case lockd.Terminated:
		fmt.Fprintln(os.Stderr, "sysc-lock: terminated by compositor")
		os.Exit(5)
	default:
		os.Exit(1)
	}
}

func authenticate(a authenticator, pass string, client *lockd.Client, view *lockd.View, model *input.Model) {
	res, err := a.Verify(a.User(), func(prompt string, echo bool) (string, error) {
		if echo { // PAM asked for something besides the password: refuse
			return "", fmt.Errorf("unexpected echo prompt: %s", prompt)
		}
		return pass, nil
	})
	client.Post(func() {
		model.Clear() // zero the entry regardless of outcome
		switch {
		case err != nil:
			view.SetError(err.Error(), time.Now())
		case res.OK:
			view.SetError("", time.Now())
			client.UnlockAndQuit()
		case res.Terminal:
			view.SetErrorTerminal(res.Message, time.Now())
			view.NoteAttempt(time.Now())
		default:
			view.SetError(res.Message, time.Now())
			view.NoteAttempt(time.Now())
		}
		client.Repaint()
	})
	auth.Zero([]byte(pass))
}

package main

import (
	"fmt"
	"github.com/Nomadcxx/sysc-lock/internal/input"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"strconv"

	"github.com/Nomadcxx/sysc-lock/internal/auth"
	"github.com/Nomadcxx/sysc-lock/internal/lockd"
)

// authenticator is the seam; only the real PAM implementation exists here —
type authenticator interface {
	Verify(user string, response auth.PromptFunc) (auth.Result, error)
	User() string
}

func currentUser() (string, error) {
	u, err := user.LookupId(strconv.Itoa(os.Getuid()))
	if err != nil || u.Username == "" {
		return "", fmt.Errorf("cannot resolve real UID %d", os.Getuid())
	}
	return u.Username, nil
}

func palettePath() string {
	if p := os.Getenv("SYSC_LOCK_PALETTE"); p != "" {
		return p
	}
	if c := os.Getenv("XDG_CONFIG_HOME"); c != "" {
		return filepath.Join(c, "sysc-shell", "palette.json")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "sysc-shell", "palette.json")
}

// watchSignals: SIGTERM/SIGINT are legal to honor only before the compositor
// confirms "locked" (abandon request, exit 3). After confirmation they are
// ignored — the session is sealed and must stay sealed; authentication is the
// only exit.
func watchSignals(sigs chan os.Signal, c *lockd.Client, release func()) {
	for range sigs {
		c.Post(func() { c.AbortBeforeLocked() })
	}
}

const lockedHandshakeLine = "sysc-lock: locked"

func emitLockedHandshake(w io.Writer) {
	fmt.Fprintln(w, lockedHandshakeLine)
}

// enterGate serializes PAM: the pump goroutine is the only mutator.
type enterGate struct {
	busy       bool
	generation uint64
}

func (g *enterGate) try(pass string) bool {
	if g.busy || pass == "" {
		return false
	}
	g.busy = true
	g.generation++
	return true
}

func (g *enterGate) release() { g.busy = false }

// watchLocked installs an owner callback; the protocol event is the handshake.
func watchLocked(c *lockd.Client, release func()) {
	emitted := false
	c.OnEvent = func(v lockd.Snapshot) {
		if v.Phase == lockd.Locked && !emitted {
			emitted = true
			emitLockedHandshake(os.Stdout)
			release()
		}
	}
}

func (g *enterGate) handle(m *input.Model, k lockd.Key) (bool, error) {
	if g.busy {
		return false, nil
	}
	switch {
	case k.Enter:
		return g.try(m.Password()), nil
	case k.Backspace:
		m.Backspace()
	case k.Escape:
		m.Clear()
	case k.Text != "":
		return false, m.Append(k.Text)
	}
	return false, nil
}
func (g *enterGate) accept(generation uint64, phase lockd.Phase) bool {
	return g.busy && g.generation == generation && phase == lockd.Locked
}

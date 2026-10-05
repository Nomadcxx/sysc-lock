package main

import (
	"fmt"
	"github.com/Nomadcxx/sysc-lock/internal/input"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"time"

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

const lockedHandshakeLine = "sysc-lock: locked"

func emitLockedHandshake(w io.Writer) {
	fmt.Fprintln(w, lockedHandshakeLine)
}

// enterGate serializes PAM: the pump goroutine is the only mutator.
type enterGate struct {
	busy       bool
	generation uint64
}

func (g *enterGate) try(hasEntry bool) bool {
	if g.busy || !hasEntry {
		return false
	}
	g.busy = true
	g.generation++
	return true
}

func (g *enterGate) release() { g.busy = false }

func (g *enterGate) handle(m *input.Model, k lockd.Key) (bool, error) {
	if g.busy {
		return false, nil
	}
	switch {
	case k.Enter:
		return g.try(len(m.Pass) > 0), nil
	case k.Backspace:
		m.Backspace()
	case k.Escape:
		m.Clear()
	case k.Text != "":
		return false, m.Append(k.Text)
	}
	return false, nil
}

// press applies one key to the entry. A key on a hidden entry only reveals it
// and never reaches the password buffer. Any key restarts the idle timer. Esc
// clears the field and hides the entry unless a verification is running.
func (g *enterGate) press(m *input.Model, r *input.Reveal, k lockd.Key, now time.Time) (bool, error) {
	visible := r.Tick(now, len(m.Pass) > 0 || g.busy)
	r.Show(now)
	if !visible {
		return false, nil
	}
	submit, err := g.handle(m, k)
	if k.Escape && !g.busy {
		r.Hide()
	}
	return submit, err
}
func (g *enterGate) accept(generation uint64, phase lockd.Phase) bool {
	return g.busy && g.generation == generation && phase == lockd.Locked
}

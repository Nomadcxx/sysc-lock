package main

import (
	"fmt"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"time"

	"github.com/Nomadcxx/sysc-lock/internal/auth"
	"github.com/Nomadcxx/sysc-lock/internal/lockd"
)

// authenticator is the seam; only the real PAM implementation exists here —
type authenticator interface {
	Verify(user string, response auth.PromptFunc) (auth.Result, error)
	User() string
}

func currentUser() string {
	if u, err := user.Current(); err == nil && u.Username != "" {
		return u.Username
	}
	if n := os.Getenv("USER"); n != "" {
		return n
	}
	return "unknown"
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
		if !c.HandshakeReady() {
			release()
			os.Exit(3)
		}
	}
}

const lockedHandshakeLine = "sysc-lock: locked"

func emitLockedHandshake(w io.Writer) {
	fmt.Fprintln(w, lockedHandshakeLine)
}

// enterGate serializes PAM: the pump goroutine is the only mutator.
type enterGate struct{ busy bool }

func (g *enterGate) try(pass string) bool {
	if g.busy || pass == "" {
		return false
	}
	g.busy = true
	return true
}

func (g *enterGate) release() { g.busy = false }

// watchLocked prints the handshake line the shell waits for, then drops the
// sleep inhibitor: locked frames are presented on every output, so suspend
// (if it comes) happens behind the lock.
func watchLocked(c *lockd.Client, release func()) {
	for {
		if c.HandshakeReady() {
			emitLockedHandshake(os.Stdout)
			os.Stdout.Sync()
			release()
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
}

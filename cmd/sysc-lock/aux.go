package main

import (
	"errors"
	"fmt"
	"github.com/Nomadcxx/sysc-lock/internal/input"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/Nomadcxx/sysc-lock/internal/auth"
	"github.com/Nomadcxx/sysc-lock/internal/lockd"
	"github.com/Nomadcxx/sysc-lock/internal/options"
	"github.com/Nomadcxx/sysc-lock/internal/power"
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

// promptAnswer is one reply to a PAM prompt; the once guard makes the first
// responder win (Enter, Escape, timeout or stale generation) and the rest no-ops.
type promptAnswer struct {
	text string
	err  error
}

type promptWait struct {
	reply chan promptAnswer
	done  sync.Once
}

func (w *promptWait) send(ans promptAnswer) { w.done.Do(func() { w.reply <- ans }) }

var (
	errPromptCancelled = errors.New("prompt cancelled")
	errPromptTimeout   = errors.New("prompt timed out")
)

// promptDeadline caps how long one secret prompt may wait. ponytail: one
// knob; fprintd self-times the finger wait inside the PAM stack.
var promptDeadline = 60 * time.Second

// enterGate serializes PAM: the pump goroutine is the only mutator.
type enterGate struct {
	busy       bool
	generation uint64
	paste      func() string // clipboard text, set to lockd.Client.Paste
	prompt     *promptWait   // non-nil while Verify awaits an answer
}

func (g *enterGate) try() bool {
	if g.busy {
		return false
	}
	g.busy = true
	g.generation++
	return true
}

func (g *enterGate) release() { g.busy = false }

func (g *enterGate) handle(m *input.Model, k lockd.Key) (bool, error) {
	// A key going up never edits the entry. Only the power menu consumes
	// releases (hold-to-confirm), and those arrive through pressMenu. Without
	// this, releasing Enter after cancelling the popup submits the password.
	if g.busy || k.Released {
		return false, nil
	}
	switch {
	case k.Ctrl, k.Insert, k.Backspace, k.Text != "":
		return false, g.edit(m, k)
	case k.Enter:
		// An empty Enter still starts the transaction: a fingerprint-only PAM
		// stack may answer it without ever asking for a secret.
		return g.try(), nil
	case k.Escape:
		m.Clear()
	}
	return false, nil
}

// edit applies typing and paste keys, shared by the password entry and the
// prompt entry paths. Ctrl and Shift+Insert paste; Ctrl combos never type.
func (g *enterGate) edit(m *input.Model, k lockd.Key) error {
	switch {
	case k.Ctrl:
		if (k.Text == "v" || k.Text == "V") && g.paste != nil {
			if text := g.paste(); text != "" {
				return m.Append(text)
			}
		}
	case k.Insert:
		if k.Shift && g.paste != nil {
			if text := g.paste(); text != "" {
				return m.Append(text)
			}
		}
	case k.Backspace:
		m.Backspace()
	case k.Text != "":
		return m.Append(k.Text)
	}
	return nil
}

// promptKey routes one key while a PAM prompt waits for an answer. Enter
// answers, Escape aborts the conversation; everything else edits the field.
func (g *enterGate) promptKey(m *input.Model, k lockd.Key) error {
	w := g.prompt
	if w == nil || k.Released {
		return nil
	}
	switch {
	case k.Enter:
		g.prompt = nil
		w.send(promptAnswer{text: m.Password()})
		m.Clear()
	case k.Escape:
		g.prompt = nil
		w.send(promptAnswer{err: errPromptCancelled})
		m.Clear()
	default:
		return g.edit(m, k)
	}
	return nil
}

// displayPrompt makes PAM text safe for the hint strip: printable only,
// collapsed, capped. The raw message never reaches logs.
func displayPrompt(msg, def string) string {
	var b strings.Builder
	space := false
	for _, r := range msg {
		if unicode.Is(unicode.Zs, r) {
			r = ' '
		}
		if !unicode.IsPrint(r) {
			continue
		}
		if r == ' ' {
			space = b.Len() > 0 && !space
			continue
		}
		if space {
			b.WriteByte(' ')
			space = false
		}
		b.WriteRune(r)
	}
	out := []rune(strings.TrimRight(b.String(), " "))
	if len(out) == 0 {
		return def
	}
	if len(out) > 60 {
		return string(out[:59]) + "…"
	}
	return string(out)
}

// press applies one key to the entry. A key on a hidden entry only reveals it
// and never reaches the password buffer. Any key restarts the idle timer. Esc
// clears the field and hides the entry unless a verification is running.
func (g *enterGate) press(m *input.Model, r *input.Reveal, k lockd.Key, now time.Time) (bool, error) {
	visible := g.visible(m, r, now, false)
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

func (g *enterGate) visible(m *input.Model, r *input.Reveal, now time.Time, hold bool) bool {
	return r.Tick(now, hold || len(m.Pass) > 0 || g.busy)
}

func (g *enterGate) pressMenu(m *input.Model, r *input.Reveal, k power.Key, menu *power.Menu, now time.Time) (bool, error) {
	if g.busy {
		return false, nil
	}
	r.Show(now)
	menu.Press(k, now)
	return false, nil
}

func (g *enterGate) pressOptions(r *input.Reveal, k options.Key, o *options.Options, now time.Time) bool {
	if g.busy {
		return false
	}
	r.Show(now)
	return o.Press(k)
}

func (g *enterGate) accept(generation uint64, phase lockd.Phase) bool {
	return g.busy && g.generation == generation && phase == lockd.Locked
}

// armAmbient is true on the first Locked snapshot: the collector must not
// run while the compositor is still deciding, and must not restart.
func armAmbient(phase lockd.Phase, started bool) bool {
	return !started && phase == lockd.Locked
}

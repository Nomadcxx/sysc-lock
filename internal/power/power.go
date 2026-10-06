// Package power owns the Power Options popup: which rows it offers, which one
// is selected, and the hold-to-confirm timer. It is pure — no bus, no clock of
// its own — so every behaviour is table-testable and no test touches logind.
package power

import "time"

// Action is one configurable row. Cancel is not one: it closes the popup and
// never reaches logind.
type Action string

const (
	Logout    Action = "logout"
	Reboot    Action = "reboot"
	Shutdown  Action = "shutdown"
	Suspend   Action = "suspend"
	Hibernate Action = "hibernate"
	Cancel    Action = "cancel"
)

// DefaultOrder is the menu order when the config says nothing.
var DefaultOrder = []Action{Logout, Reboot, Shutdown}

const (
	// HoldDuration is how long Enter must stay down before a power action
	// commits. Cancel never holds.
	HoldDuration = 1500 * time.Millisecond
	// Timeout bounds one logind call.
	Timeout = 5 * time.Second
	Title   = "Power Options"
	// Help is greet's popup help line, verbatim.
	Help = "↑↓ Navigate • Enter Select • Esc Cancel"
	// ScreenHelp is the bottom strip when an action is available.
	ScreenHelp = "F4 Power • Enter Unlock"
	// ScreenHelpPlain is the bottom strip when nothing is available.
	ScreenHelpPlain = "Enter Unlock"
)

// Label is the row text, in greet's words.
func (a Action) Label() string {
	switch a {
	case Logout:
		return "Log out"
	case Reboot:
		return "Reboot"
	case Shutdown:
		return "Shutdown"
	case Suspend:
		return "Suspend"
	case Hibernate:
		return "Hibernate"
	case Cancel:
		return "Cancel"
	}
	return string(a)
}

// Status is what the status line says once an action is under way, in greet's
// words. Cancel has none.
func (a Action) Status() string {
	switch a {
	case Reboot:
		return "Rebooting..."
	case Shutdown:
		return "Shutting down..."
	case Logout:
		return "Logging out..."
	case Suspend:
		return "Suspending..."
	case Hibernate:
		return "Hibernating..."
	}
	return ""
}

// Normalize drops unknown names and duplicates and keeps the listed order.
func Normalize(in []Action) []Action {
	seen := map[Action]bool{}
	out := make([]Action, 0, len(in))
	for _, a := range in {
		switch a {
		case Logout, Reboot, Shutdown, Suspend, Hibernate:
		default:
			continue
		}
		if seen[a] {
			continue
		}
		seen[a] = true
		out = append(out, a)
	}
	return out
}

// Availability is what logind permits for this acquisition. Log out is not
// here: it needs a session id, not a Can* property.
type Availability struct{ Reboot, Shutdown, Suspend, Hibernate bool }

// Menu is the popup state. Every entry point takes now so a test can inject
// the clock; it is touched only from the owner loop.
type Menu struct {
	items   []Action
	session string
	open    bool
	sel     int
	hold    bool
	since   time.Time
	busy    bool
}

// Key is the part of a lockd.Key the menu needs.
type Key struct {
	Up, Down, Enter, Escape, F4, Released bool
}

// New builds the menu for one acquisition. order comes from the config, avail
// from a single logind query and session from the environment; Log out is
// dropped without a session to terminate.
func New(order []Action, avail Availability, session string) *Menu {
	keep := map[Action]bool{
		Logout:    session != "",
		Reboot:    avail.Reboot,
		Shutdown:  avail.Shutdown,
		Suspend:   avail.Suspend,
		Hibernate: avail.Hibernate,
	}
	m := &Menu{session: session}
	for _, a := range Normalize(order) {
		if keep[a] {
			m.items = append(m.items, a)
		}
	}
	return m
}

// Items returns the rows, Cancel last.
func (m *Menu) Items() []Action {
	return append(append([]Action{}, m.items...), Cancel)
}

// Available reports whether any real action is offered. When it is false F4
// and its hint disappear.
func (m *Menu) Available() bool { return len(m.items) > 0 }

// Open, Selected and Holding are the renderer's inputs.
func (m *Menu) Open() bool    { return m.open }
func (m *Menu) Selected() int { return m.sel }
func (m *Menu) Holding() bool { return m.hold }

// Progress is how full the hold bar is, or -1 when no hold is running.
func (m *Menu) Progress(now time.Time) int {
	if !m.hold {
		return -1
	}
	return min(100, max(0, int(now.Sub(m.since)*100/HoldDuration)))
}

// Toggle opens a closed popup and closes an open one.
func (m *Menu) Toggle() {
	if m.open {
		m.Close()
		return
	}
	if !m.Available() || m.busy {
		return
	}
	m.open, m.sel = true, 0
}

// Close closes the popup and resets any hold. It does not clear busy: a
// successful action stays in flight until the machine goes down.
func (m *Menu) Close() { m.open, m.hold, m.since = false, false, time.Time{} }

// Recover is the failure path: the popup closes and a later F4 can open it.
func (m *Menu) Recover() { m.busy = false; m.Close() }

func (m *Menu) move(d int) {
	m.hold, m.since = false, time.Time{}
	if n := m.sel + d; n >= 0 && n < len(m.items)+1 {
		m.sel = n
	}
}

// Press applies one key and returns an Action only when the key committed one.
func (m *Menu) Press(k Key, now time.Time) Action {
	if !m.open {
		if k.F4 {
			m.Toggle()
		}
		return ""
	}
	if m.busy {
		return ""
	}
	if k.F4 {
		m.hold, m.since, m.sel = false, time.Time{}, 0
		return ""
	}
	switch {
	case k.Released:
		m.hold, m.since = false, time.Time{}
	case k.Escape:
		m.Close()
	case k.Up:
		m.move(-1)
	case k.Down:
		m.move(1)
	case k.Enter:
		if m.sel == len(m.items) {
			m.Close()
			return Cancel
		}
		m.hold, m.since = true, now
	}
	return ""
}

// Tick completes a hold that has run its course. It is called from the owner
// loop, which is why the bar repaints on the same short deadlines as the
// print reveal.
func (m *Menu) Tick(now time.Time) Action {
	if !m.hold || now.Sub(m.since) < HoldDuration {
		return ""
	}
	m.hold, m.since = false, time.Time{}
	a := m.items[m.sel]
	m.open, m.busy = false, true
	return a
}

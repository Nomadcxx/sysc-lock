package power

import (
	"testing"
	"time"
)

func labels(m *Menu) []string {
	var out []string
	for _, a := range m.Items() {
		out = append(out, a.Label())
	}
	return out
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestNormalizeDropsUnknownAndDuplicatesKeepingOrder(t *testing.T) {
	got := Normalize([]Action{"shutdown", "nope", "logout", "shutdown", "logout", ""})
	if len(got) != 2 || got[0] != Shutdown || got[1] != Logout {
		t.Fatalf("normalize kept %v", got)
	}
	if len(Normalize(nil)) != 0 {
		t.Fatal("an absent list is empty, not the default")
	}
}

func TestNewDropsUnavailableItemsAndAMissingSession(t *testing.T) {
	all := Availability{true, true}
	m := New(DefaultOrder, all, "c2")
	if !equal(labels(m), []string{"Log out", "Reboot", "Shutdown", "Cancel"}) {
		t.Fatal(labels(m))
	}
	if got := labels(New(DefaultOrder, Availability{}, "c2")); !equal(got, []string{"Log out", "Cancel"}) {
		t.Fatal("log out needs no permission:", got)
	}
	if got := labels(New(DefaultOrder, all, "")); !equal(got, []string{"Reboot", "Shutdown", "Cancel"}) {
		t.Fatal("no session means no log out:", got)
	}
	none := New(DefaultOrder, Availability{}, "")
	if none.Available() {
		t.Fatal("nothing left means F4 is inert")
	}
	none.Toggle()
	if none.Open() {
		t.Fatal("an empty menu must not open")
	}
}

func TestToggleAndSelectionStartsOnTheFirstRow(t *testing.T) {
	m := New(DefaultOrder, Availability{true, true}, "c2")
	m.Toggle()
	if !m.Open() || m.Selected() != 0 {
		t.Fatal("the popup opens on the first row", m.Selected())
	}
	m.Toggle()
	if m.Open() {
		t.Fatal("F4 closes an open popup")
	}
}

func TestNavigationClampsAndNeverWraps(t *testing.T) {
	m := New(DefaultOrder, Availability{true, true}, "c2")
	m.Toggle()
	at := time.Now()
	m.Press(Key{Up: true}, at)
	if m.Selected() != 0 {
		t.Fatal("up on the first row stays put")
	}
	for range 9 {
		m.Press(Key{Down: true}, at)
	}
	if m.Selected() != len(m.Items())-1 {
		t.Fatal("down stops on Cancel", m.Selected())
	}
	m.Press(Key{Down: true}, at)
	if m.Selected() != len(m.Items())-1 {
		t.Fatal("down on the last row stays put")
	}
	m.Press(Key{Up: true}, at)
	if m.Selected() != len(m.Items())-2 {
		t.Fatal("up moves back one", m.Selected())
	}
}

func TestCancelActsOnOnePress(t *testing.T) {
	m := New(DefaultOrder, Availability{true, true}, "c2")
	m.Toggle()
	m.Press(Key{Down: true}, time.Now())
	m.Press(Key{Down: true}, time.Now())
	m.Press(Key{Down: true}, time.Now())
	if a := m.Press(Key{Enter: true}, time.Now()); a != Cancel {
		t.Fatalf("Cancel must commit immediately, got %q", a)
	}
	if m.Open() || m.Holding() {
		t.Fatal("Cancel closes the popup and starts no hold")
	}
}

func TestEscapeClosesWithoutCommitting(t *testing.T) {
	m := New(DefaultOrder, Availability{true, true}, "c2")
	m.Toggle()
	if a := m.Press(Key{Escape: true}, time.Now()); a != "" {
		t.Fatalf("escape commits nothing, got %q", a)
	}
	if m.Open() {
		t.Fatal("escape closes the popup")
	}
}

func TestHoldCompletesAtTheThreshold(t *testing.T) {
	m := New(DefaultOrder, Availability{true, true}, "c2")
	m.Toggle()
	start := time.Now()
	if a := m.Press(Key{Enter: true}, start); a != "" {
		t.Fatal("the first press only starts the hold", a)
	}
	if !m.Holding() {
		t.Fatal("the hold is running")
	}
	if m.Progress(start) != 0 {
		t.Fatal("the bar starts empty", m.Progress(start))
	}
	if a := m.Tick(start.Add(HoldDuration - time.Millisecond)); a != "" {
		t.Fatal("a hold is not done early", a)
	}
	if a := m.Tick(start.Add(HoldDuration)); a != Logout {
		t.Fatalf("the hold commits at the threshold, got %q", a)
	}
	if m.Open() || m.Holding() {
		t.Fatal("the popup closes and the hold clears")
	}
	if got := m.Progress(start.Add(time.Minute)); got != -1 {
		t.Fatal("no hold means no bar", got)
	}
}

func TestProgressTracksTheHoldAndSaturates(t *testing.T) {
	m := New(DefaultOrder, Availability{true, true}, "c2")
	m.Toggle()
	start := time.Now()
	m.Press(Key{Enter: true}, start)
	if got := m.Progress(start.Add(HoldDuration / 2)); got != 50 {
		t.Fatal("halfway is half full", got)
	}
	if got := m.Progress(start.Add(time.Hour)); got != 100 {
		t.Fatal("the bar saturates", got)
	}
}

func TestReleaseAndKeyboardLeaveResetTheHold(t *testing.T) {
	for name, k := range map[string]Key{
		"enter release": {Enter: true, Released: true},
		"leave marker":  {Released: true},
	} {
		m := New(DefaultOrder, Availability{true, true}, "c2")
		m.Toggle()
		start := time.Now()
		m.Press(Key{Enter: true}, start)
		if a := m.Press(k, start.Add(time.Second)); a != "" {
			t.Fatalf("%s commits nothing, got %q", name, a)
		}
		if m.Holding() {
			t.Fatalf("%s must reset the hold", name)
		}
		if a := m.Tick(start.Add(time.Hour)); a != "" {
			t.Fatalf("%s must not leave a hold that can fire later, got %q", name, a)
		}
	}
}

func TestNavigationAndCloseResetTheHold(t *testing.T) {
	for name, k := range map[string]Key{"move": {Down: true}, "escape": {Escape: true}} {
		m := New(DefaultOrder, Availability{true, true}, "c2")
		m.Toggle()
		start := time.Now()
		m.Press(Key{Enter: true}, start)
		m.Press(k, start.Add(time.Second))
		if a := m.Tick(start.Add(time.Hour)); a != "" {
			t.Fatalf("%s must reset the hold, got %q", name, a)
		}
	}
	m := New(DefaultOrder, Availability{true, true}, "c2")
	m.Toggle()
	start := time.Now()
	m.Press(Key{Enter: true}, start)
	m.Close()
	if a := m.Tick(start.Add(time.Hour)); a != "" {
		t.Fatal("closing the popup must reset the hold", a)
	}
}

func TestOneActionInFlightAtATime(t *testing.T) {
	m := New(DefaultOrder, Availability{true, true}, "c2")
	m.Toggle()
	start := time.Now()
	m.Press(Key{Enter: true}, start)
	if a := m.Tick(start.Add(HoldDuration)); a != Logout {
		t.Fatalf("first action %q", a)
	}
	m.Toggle()
	for _, k := range []Key{{Down: true}, {Enter: true}, {F4: true}} {
		if a := m.Press(k, start.Add(2*HoldDuration)); a != "" {
			t.Fatal("keys are ignored while an action is in flight", a)
		}
	}
	if a := m.Tick(start.Add(time.Hour)); a != "" {
		t.Fatal("no second action while one is in flight", a)
	}
}

func TestKeysBeforeOpenOnlyToggle(t *testing.T) {
	m := New(DefaultOrder, Availability{true, true}, "c2")
	at := time.Now()
	if a := m.Press(Key{Enter: true}, at); a != "" || m.Holding() {
		t.Fatal("a closed menu never starts a hold")
	}
	if a := m.Press(Key{Down: true}, at); a != "" || m.Selected() != 0 {
		t.Fatal("a closed menu never moves the selection")
	}
	m.Press(Key{F4: true}, at)
	if !m.Open() {
		t.Fatal("F4 opens")
	}
}

func TestF4WhileOpenResetsToTheFirstRow(t *testing.T) {
	m := New(DefaultOrder, Availability{true, true}, "c2")
	m.Press(Key{F4: true}, time.Now())
	m.Press(Key{Down: true}, time.Now())
	m.Press(Key{Enter: true}, time.Now())
	if !m.Holding() {
		t.Fatal("hold running")
	}
	if a := m.Press(Key{F4: true}, time.Now()); a != "" || !m.Open() || m.Selected() != 0 || m.Holding() {
		t.Fatal("F4 while open resets to row 0 and cancels the hold, it does not close")
	}
}

func TestRecoverClearsBusySoAFailedActionCanBeRetried(t *testing.T) {
	m := New(DefaultOrder, Availability{true, true}, "c2")
	m.Toggle()
	start := time.Now()
	m.Press(Key{Enter: true}, start)
	if a := m.Tick(start.Add(HoldDuration)); a != Logout {
		t.Fatalf("first action %q", a)
	}
	if a := m.Press(Key{F4: true}, start.Add(2*HoldDuration)); a != "" || m.Open() {
		t.Fatal("busy blocks F4 until Recover", a, m.Open())
	}
	m.Recover()
	m.Press(Key{F4: true}, start.Add(3*HoldDuration))
	if !m.Open() {
		t.Fatal("after Recover the menu can open again")
	}
}

func TestStatusUsesGreetWording(t *testing.T) {
	for a, want := range map[Action]string{
		Reboot: "Rebooting...", Shutdown: "Shutting down...", Logout: "Logging out...", Cancel: "",
	} {
		if got := a.Status(); got != want {
			t.Fatalf("%s status %q", a, got)
		}
	}
	if Help != "↑↓ Navigate • Enter Select • Esc Cancel" {
		t.Fatal(Help)
	}
	if ScreenHelp != "F4 Power • Enter Unlock" || ScreenHelpPlain != "Enter Unlock" {
		t.Fatal(ScreenHelp, ScreenHelpPlain)
	}
}

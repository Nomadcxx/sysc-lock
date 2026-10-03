package input

import "testing"

func TestAppendBackspaceMask(t *testing.T) {
	var m Model
	m.Append("h")
	m.Append("i")
	if got := m.Password(); got != "hi" {
		t.Fatalf("pass = %q", got)
	}
	m.Backspace()
	if got := string(m.Pass); got != "h" {
		t.Fatalf("after backspace = %q", got)
	}
	m.Append("ello")
	if m.Mask() != "●●●●●" {
		t.Fatalf("mask = %q", m.Mask())
	}
	m.Backspace() // at end
	m.Backspace()
	m.Backspace()
	if len(m.Pass) != 2 {
		t.Fatalf("len = %d", len(m.Pass))
	}
}

func TestBackspaceOnEmptyAndClear(t *testing.T) {
	var m Model
	m.Backspace()
	m.Append("abc")
	m.Clear()
	if len(m.Pass) != 0 || m.Mask() != "" {
		t.Fatal("clear failed")
	}
}

func TestZeroOnClear(t *testing.T) {
	var m Model
	m.Append("secret")
	p := m.Password()
	if p != "secret" {
		t.Fatal("copy")
	}
	m.Clear()
	if got := m.Pass[:cap(m.Pass)]; got[0] != 0 || got[5] != 0 {
		t.Fatal("buffer not zeroed")
	}
}

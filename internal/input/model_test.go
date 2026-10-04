package input

import (
	"strings"
	"testing"
)

func TestMutablePasswordErasure(t *testing.T) {
	m := &Model{}
	m.Append("a界c")
	backing := m.Pass[:cap(m.Pass)]
	m.Backspace()
	if backing[2] != 0 {
		t.Fatal("deleted password rune retained")
	}
	m.Clear()
	for i, r := range backing {
		if r != 0 {
			t.Fatalf("password storage retained at %d", i)
		}
	}
}

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
func TestPasswordUTF8Boundary(t *testing.T) {
	for _, prefix := range []string{strings.Repeat("a", 4095), strings.Repeat("界", 1365)} {
		m := &Model{}
		if err := m.Append(prefix); err != nil {
			t.Fatal(err)
		}
		if err := m.Append("界"); err == nil {
			t.Fatal("accepted oversized password")
		}
		if m.Password() != prefix {
			t.Fatal("truncated existing input")
		}
		if err := m.Append("a"); err != nil {
			t.Fatal(err)
		}
		if err := m.Append("a"); err == nil {
			t.Fatal("accepted 4097 bytes")
		}
		m.Backspace()
		if err := m.Append("b"); err != nil {
			t.Fatal("backspace failed to reclaim capacity")
		}
	}
}

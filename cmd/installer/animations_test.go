package main

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestBeamsCanvasStartsBlank(t *testing.T) {
	b := NewBeamsTextEffect(80, 11, "SYSC")
	lines := strings.Split(ansi.Strip(b.Render()), "\n")
	if len(lines) != 11 {
		t.Fatalf("render before first Update = %d lines, want 11", len(lines))
	}
	for i, line := range lines {
		if strings.TrimSpace(line) != "" {
			t.Fatalf("line %d not blank before first Update: %q", i, line)
		}
	}
}

func TestBeamsResizeKeepsRowCount(t *testing.T) {
	b := NewBeamsTextEffect(80, 11, "SYSC")
	b.Resize(120, 11)
	if got := len(strings.Split(b.Render(), "\n")); got != 11 {
		t.Fatalf("banner rows after resize = %d, want 11", got)
	}
}

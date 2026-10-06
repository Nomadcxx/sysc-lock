package main

import (
	"os"
	"strings"
	"testing"
)

func TestPinnedUIModules(t *testing.T) {
	data, err := os.ReadFile("../../go.mod")
	if err != nil {
		t.Fatal(err)
	}
	for _, pin := range []string{
		"bubbletea v1.3.10",
		"bubbles v0.21.0",
		"lipgloss v1.1.0",
		"x/ansi v0.10.1",
		"x/term v0.2.1",
	} {
		found := false
		for _, line := range strings.Split(string(data), "\n") {
			if strings.Contains(line, pin) && !strings.Contains(line, "// indirect") {
				found = true
			}
		}
		if !found {
			t.Errorf("go.mod missing direct pin %q", pin)
		}
	}
}

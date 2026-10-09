package main

import (
	"os"
	"os/exec"
	"path/filepath"
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

// The installer is a user-scope tool: it must never reach for a privilege
// escalation path or edit PAM, and it reaches systemd only through userCtl,
// which is pinned to the user's own manager.
func TestInstallerNeverTouchesSystem(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		body, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		files[name] = string(body)
	}
	sh, err := os.ReadFile("../../install.sh")
	if err != nil {
		t.Fatal(err)
	}
	files["install.sh"] = string(sh)

	const seam = `"systemctl", append([]string{"--user"}`
	if n := strings.Count(strings.Join(mapValues(files), "\n"), `"systemctl"`); n != 1 || !strings.Contains(files["activate.go"], seam) {
		t.Errorf("systemctl is run %d times; the only allowed call is userCtl's %s", n, seam)
	}
	for _, banned := range []string{"/etc/pam.d", "sudo ", "pkexec", "doas"} {
		for name, body := range files {
			if strings.Contains(body, banned) {
				t.Errorf("%s contains %q", name, banned)
			}
		}
	}
}

func mapValues(m map[string]string) []string {
	var out []string
	for _, v := range m {
		out = append(out, v)
	}
	return out
}

// The installer must build anywhere: no internal packages (cgo, PAM), no PAM.
func TestInstallerStaysOutsideInternal(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", ".").CombinedOutput()
	if err != nil {
		t.Fatal(string(out), err)
	}
	for _, banned := range []string{"github.com/Nomadcxx/sysc-lock/internal", "github.com/msteinert/pam"} {
		if strings.Contains(string(out), banned) {
			t.Errorf("installer depends on %s", banned)
		}
	}
}

func TestInstallShIsExecutable(t *testing.T) {
	info, err := os.Stat(filepath.Join("..", "..", "install.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Errorf("install.sh mode %v is not executable", info.Mode().Perm())
	}
}

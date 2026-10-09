package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

const unitName = "sysc-lock-session.service"

// shellIdleLock matches the sysc-shell Settings default for When idle.
const shellIdleLock = 5 * time.Minute

// userCtl talks to the user's own service manager. It is the installer's only
// route to systemd: always --user, never the system manager. Tests replace it.
var userCtl = func(ctx context.Context, args ...string) *exec.Cmd {
	return exec.CommandContext(ctx, "systemctl", append([]string{"--user"}, args...)...)
}

func (r *runner) userCtl(ctx context.Context, task string, args ...string) (string, error) {
	return r.runLogged(task, userCtl(ctx, args...))
}

func (r *runner) note(s string) {
	r.mu.Lock()
	r.notes = append(r.notes, s)
	r.mu.Unlock()
}

func configHome() (string, error) {
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return dir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config"), nil
}

func dataHome() (string, error) {
	if dir := os.Getenv("XDG_DATA_HOME"); dir != "" {
		return dir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "share"), nil
}

// unitOnSearchPath reports whether the unit this prefix gets is one the user
// manager loads: $XDG_DATA_HOME/systemd/user is on its search path, any other
// prefix's share/systemd/user is not.
func unitOnSearchPath(prefix string) bool {
	data, err := dataHome()
	return err == nil && filepath.Clean(prefix+"/share") == filepath.Clean(data)
}

// activateService enables the session owner and, inside a running Niri
// session, (re)starts it so the new binary is the one that answers Lock.
// Outside Niri the service waits for the next graphical session; starting it
// there would fail its environment checks.
func activateService(ctx context.Context, r *runner) error {
	const task = "Activate service"
	restarted := false
	defer func() {
		if restarted {
			return
		}
		for _, o := range runningLockOwners(r.opts.prefix + "/bin/sysc-lock") {
			r.note(fmt.Sprintf("A running sysc-lock (pid %d) keeps the previous binary until %s restarts.", o.pid, unitName))
		}
	}()
	if !unitOnSearchPath(r.opts.prefix) {
		r.note("Enable it yourself: link " + r.opts.prefix + "/share/systemd/user/" + unitName +
			" into ~/.config/systemd/user, then systemctl --user enable --now " + unitName + ".")
		return skipError{"unit is outside the systemd user search path"}
	}
	if _, err := r.userCtl(ctx, task, "daemon-reload"); err != nil {
		r.note("No systemd user manager answered. From your Niri session run: systemctl --user enable --now " + unitName + ".")
		return skipError{"no systemd user manager"}
	}
	if out, err := r.userCtl(ctx, task, "enable", unitName); err != nil {
		return fmt.Errorf("enable %s: %s", unitName, firstNonEmpty(lastLine(out, 20), err.Error()))
	}
	r.mu.Lock()
	r.activated = true
	r.mu.Unlock()
	if _, err := r.userCtl(ctx, task, "is-active", "--quiet", "niri.service"); err != nil {
		r.note(unitName + " is enabled and starts with your next Niri session.")
		return nil
	}
	if out, err := r.userCtl(ctx, task, "restart", unitName); err != nil {
		return fmt.Errorf("start %s: %s", unitName, firstNonEmpty(lastLine(out, 20), err.Error()))
	}
	restarted = true
	return nil
}

// stopService stops and disables the session owner before its files go. If
// the user manager cannot do it, the caller's refusal checks still stand.
func stopService(ctx context.Context, r *runner) {
	links, _ := enabledUnitLinks()
	if len(links) == 0 && len(runningLockOwners(r.opts.prefix+"/bin/sysc-lock")) == 0 {
		return
	}
	_, _ = r.userCtl(ctx, "Check service", "disable", "--now", unitName)
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// useWithShell makes sysc-lock the sysc-shell locker when the shell config
// names none, with the shell's default idle lock unless idle.lock is already
// set or the sysc-walls screensaver owns idle (the shell's When idle setting
// is one or the other). A locker already chosen, a missing config, or one
// that does not parse is left untouched.
func useWithShell(r *runner) error {
	r.mu.Lock()
	activated := r.activated
	r.mu.Unlock()
	if !activated {
		return skipError{"service not enabled"}
	}
	dir, err := configHome()
	if err != nil {
		return err
	}
	path := filepath.Join(dir, "sysc-shell", "config.json")
	fi, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return skipError{"no sysc-shell config"}
	}
	if err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var cfg map[string]any
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if dec.Decode(&cfg) != nil || cfg == nil {
		return skipError{"sysc-shell config does not parse"}
	}
	session, _ := cfg["session"].(map[string]any)
	if locker, _ := session["locker"].(string); locker != "" {
		return skipError{"sysc-shell already uses " + locker}
	}
	if session == nil {
		session = map[string]any{}
	}
	session["locker"] = "sysc-lock"
	cfg["session"] = session
	idle, _ := cfg["idle"].(map[string]any)
	if _, set := idle["lock"]; !set && !wallsEnabled(dir) {
		if idle == nil {
			idle = map[string]any{}
		}
		idle["lock"] = shellIdleLock.String()
		cfg["idle"] = idle
		r.note("sysc-shell locks after 5 minutes idle; change it in Settings → When idle.")
	}
	var out bytes.Buffer
	enc := json.NewEncoder(&out)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(cfg); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".config-*")
	if err != nil {
		return err
	}
	_, werr := tmp.Write(out.Bytes())
	if err := errors.Join(werr, tmp.Chmod(fi.Mode().Perm()), tmp.Close()); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func wallsEnabled(configDir string) bool {
	links, _ := filepath.Glob(configDir + "/systemd/user/*.wants/sysc-walls.service")
	return len(links) > 0
}

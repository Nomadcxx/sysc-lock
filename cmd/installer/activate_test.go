package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// fakeManager stands in for the user's systemd. Every test gets one through
// TestMain, so no test can reach the real session's service manager.
type fakeManager struct {
	mu    sync.Mutex
	calls []string
	fail  map[string]bool // first argument -> exit 1
	on    func(args []string)
}

var manager *fakeManager

func (f *fakeManager) cmd(ctx context.Context, args ...string) *exec.Cmd {
	f.mu.Lock()
	f.calls = append(f.calls, strings.Join(args, " "))
	fail := f.fail[args[0]]
	on := f.on
	f.mu.Unlock()
	if on != nil {
		on(args)
	}
	if fail {
		return exec.CommandContext(ctx, "sh", "-c", "echo 'Failed to connect to bus' >&2; exit 1")
	}
	return exec.CommandContext(ctx, "true")
}

func (f *fakeManager) log() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return strings.Join(f.calls, "\n")
}

func TestMain(m *testing.M) {
	userCtl = func(ctx context.Context, args ...string) *exec.Cmd { return manager.cmd(ctx, args...) }
	manager = &fakeManager{fail: map[string]bool{"daemon-reload": true}}
	os.Exit(m.Run())
}

// useManager installs a fresh fake for one test; the default (TestMain's)
// has no user manager, the safe answer for tests that never set one.
func useManager(t *testing.T, fail ...string) *fakeManager {
	t.Helper()
	old := manager
	f := &fakeManager{fail: map[string]bool{}}
	for _, a := range fail {
		f.fail[a] = true
	}
	manager = f
	t.Cleanup(func() { manager = old })
	return f
}

// activationHome puts the prefix where the user manager looks for units and
// gives the test its own config directory.
func activationHome(t *testing.T) options {
	t.Helper()
	opts := testOpts(t)
	t.Setenv("XDG_DATA_HOME", opts.prefix+"/share")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	return opts
}

func writeShellConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "sysc-shell", "config.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o640); err != nil {
		t.Fatal(err)
	}
	return path
}

func installWithCandidate(t *testing.T, opts options) *runner {
	t.Helper()
	restoreFakes(t)
	opts.candidate = fakeCandidate(t)
	r := newRunner(opts, testLogger(t))
	if err := r.runAll(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestInstallEnablesAndStartsInsideNiri(t *testing.T) {
	opts := activationHome(t)
	f := useManager(t)
	path := writeShellConfig(t, `{"theme":{"preset":"expressive"},"weather":{"latitude":-37.6}}`)
	r := installWithCandidate(t, opts)

	want := "daemon-reload\nenable sysc-lock-session.service\nis-active --quiet niri.service\nrestart sysc-lock-session.service"
	if got := f.log(); got != want {
		t.Fatalf("systemctl --user calls:\n%s\nwant:\n%s", got, want)
	}
	var cfg map[string]map[string]any
	data, _ := os.ReadFile(path)
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg["session"]["locker"] != "sysc-lock" || cfg["idle"]["lock"] != "5m0s" {
		t.Fatalf("shell config not activated: %s", data)
	}
	if cfg["theme"]["preset"] != "expressive" || cfg["weather"]["latitude"] != -37.6 {
		t.Fatalf("other shell settings changed: %s", data)
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o640 {
		t.Fatalf("config mode = %v", fi.Mode().Perm())
	}
	for i, s := range r.snapshot().status[3:] {
		i += 3
		if s != statusDone {
			t.Errorf("task %d (%s) = %v", i, r.tasks[i].name, s)
		}
	}
}

func TestInstallOutsideNiriEnablesOnly(t *testing.T) {
	opts := activationHome(t)
	f := useManager(t, "is-active")
	r := installWithCandidate(t, opts)
	if strings.Contains(f.log(), "restart") {
		t.Fatalf("started outside Niri: %s", f.log())
	}
	if !strings.Contains(strings.Join(r.snapshot().notes, "\n"), "starts with your next Niri session") {
		t.Fatalf("notes = %v", r.snapshot().notes)
	}
}

func TestInstallWithoutUserManagerSkipsActivation(t *testing.T) {
	opts := activationHome(t)
	useManager(t, "daemon-reload")
	path := writeShellConfig(t, `{}`)
	r := installWithCandidate(t, opts)
	st := r.snapshot()
	n := len(st.status)
	if st.skips[n-2] != "no systemd user manager" || st.skips[n-1] != "service not enabled" {
		t.Fatalf("skips = %v", st.skips)
	}
	if data, _ := os.ReadFile(path); string(data) != `{}` {
		t.Fatalf("shell pointed at a locker that is not enabled: %s", data)
	}
}

func TestInstallOffSearchPathSkipsActivation(t *testing.T) {
	opts := activationHome(t)
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	f := useManager(t)
	r := installWithCandidate(t, opts)
	if f.log() != "" {
		t.Fatalf("systemctl called for an unloadable unit: %s", f.log())
	}
	if st := r.snapshot(); st.skips[len(st.status)-2] != "unit is outside the systemd user search path" {
		t.Fatalf("skips = %v", st.skips)
	}
}

func TestInstallEnableFailureFails(t *testing.T) {
	opts := activationHome(t)
	useManager(t, "enable")
	restoreFakes(t)
	opts.candidate = fakeCandidate(t)
	r := newRunner(opts, testLogger(t))
	err := r.runAll(context.Background(), nil)
	if err == nil || !strings.Contains(err.Error(), "enable sysc-lock-session.service: Failed to connect to bus") {
		t.Fatalf("err = %v", err)
	}
}

// A locker or idle choice the user already made is theirs.
func TestUseWithShellKeepsExistingChoices(t *testing.T) {
	for _, tc := range []struct{ body, skip string }{
		{`{"session":{"locker":"swaylock"}}`, "sysc-shell already uses swaylock"},
		{`not json`, "sysc-shell config does not parse"},
	} {
		opts := activationHome(t)
		useManager(t)
		path := writeShellConfig(t, tc.body)
		r := installWithCandidate(t, opts)
		if st := r.snapshot(); st.skips[len(st.status)-1] != tc.skip {
			t.Errorf("%s: skips = %v", tc.body, st.skips)
		}
		if data, _ := os.ReadFile(path); string(data) != tc.body {
			t.Errorf("%s rewritten as %s", tc.body, data)
		}
	}

	opts := activationHome(t)
	useManager(t)
	path := writeShellConfig(t, `{"idle":{"lock":"15m0s"}}`)
	installWithCandidate(t, opts)
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), `"lock": "15m0s"`) || !strings.Contains(string(data), `"locker": "sysc-lock"`) {
		t.Fatalf("config = %s", data)
	}
}

// The shell's When idle is lock or screensaver: an enabled sysc-walls keeps it.
func TestUseWithShellLeavesIdleToTheScreensaver(t *testing.T) {
	opts := activationHome(t)
	useManager(t)
	wants := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "systemd/user/graphical-session.target.wants")
	if err := os.MkdirAll(wants, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/dev/null", wants+"/sysc-walls.service"); err != nil {
		t.Fatal(err)
	}
	path := writeShellConfig(t, `{}`)
	installWithCandidate(t, opts)
	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), "idle") || !strings.Contains(string(data), `"locker": "sysc-lock"`) {
		t.Fatalf("config = %s", data)
	}
}

func TestUninstallStopsAndDisablesFirst(t *testing.T) {
	opts := uninstallOpts(t)
	seedInstall(t, opts.prefix)
	wants := os.Getenv("XDG_CONFIG_HOME") + "/systemd/user/graphical-session.target.wants"
	if err := os.MkdirAll(wants, 0o755); err != nil {
		t.Fatal(err)
	}
	link := wants + "/" + unitName
	if err := os.Symlink(opts.prefix+"/share/systemd/user/"+unitName, link); err != nil {
		t.Fatal(err)
	}
	f := useManager(t)
	f.on = func(args []string) {
		if args[0] == "disable" {
			os.Remove(link)
		}
	}
	r := newRunner(opts, testLogger(t))
	if err := r.runAll(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if f.log() != "disable --now "+unitName {
		t.Fatalf("calls = %q", f.log())
	}
	if _, err := os.Stat(opts.prefix + "/bin/sysc-lock"); !os.IsNotExist(err) {
		t.Fatal("binary survived")
	}
}

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testLogger(t *testing.T) *logger {
	t.Helper()
	l, err := openLog(filepath.Join(t.TempDir(), "installer.log"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	return l
}

func testOpts(t *testing.T) options {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(root+"/contrib/systemd", 0o755); err != nil {
		t.Fatal(err)
	}
	unit := "[Unit]\nDescription=x\nExecStart=%h/.local/bin/sysc-lock --session\n"
	if err := os.WriteFile(root+"/contrib/systemd/sysc-lock-session.service", []byte(unit), 0o644); err != nil {
		t.Fatal(err)
	}
	return options{prefix: t.TempDir() + "/.local", root: root}
}

func fakeGoBuildOK(ctx context.Context, _ string, args ...string) *exec.Cmd {
	return exec.CommandContext(ctx, "sh", "-c", `echo '#!/bin/sh' > "$1" && chmod 755 "$1"`, "fake", args[2])
}

func fakeGoBuildFail(ctx context.Context, _ string, _ ...string) *exec.Cmd {
	return exec.CommandContext(ctx, "sh", "-c", "echo boom >&2; exit 1")
}

func fakeGoBuildSleep(pidsFile string) func(context.Context, string, ...string) *exec.Cmd {
	return func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
		return exec.CommandContext(ctx, "sh", "-c",
			`sleep 60 & echo $! >> "`+pidsFile+`"; echo $$ >> "`+pidsFile+`"; wait`)
	}
}

func fakeToolchain(cc string, ccExit int, build func(context.Context, string, ...string) *exec.Cmd) func(context.Context, string, ...string) *exec.Cmd {
	return func(ctx context.Context, name string, args ...string) *exec.Cmd {
		if name == "go" && len(args) > 0 && args[0] == "env" {
			return exec.Command("sh", "-c", "echo "+cc)
		}
		if name == "go" {
			return build(ctx, name, args...)
		}
		return exec.Command("sh", "-c", "exit "+string(rune('0'+ccExit)))
	}
}

func restoreFakes(t *testing.T) {
	t.Helper()
	oldGo, oldLook := goCmd, lookPath
	t.Cleanup(func() { goCmd, lookPath = oldGo, oldLook })
}

func TestRunnerBuildSuccessThenInstall(t *testing.T) {
	restoreFakes(t)
	opts := testOpts(t)
	r := newRunner(opts, testLogger(t))
	goCmd = fakeToolchain("fakecc", 0, fakeGoBuildOK)
	if err := r.runAll(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	bin, err := os.ReadFile(opts.prefix + "/bin/sysc-lock")
	if err != nil {
		t.Fatal(err)
	}
	if string(bin) != "#!/bin/sh\n" {
		t.Errorf("binary = %q", bin)
	}
	unit, err := os.ReadFile(opts.prefix + "/share/systemd/user/sysc-lock-session.service")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(unit), "ExecStart="+opts.prefix+"/bin/sysc-lock --session") {
		t.Errorf("unit = %q", unit)
	}
	if _, err := os.Stat(r.buildDir); !os.IsNotExist(err) {
		t.Error("buildDir not cleaned")
	}
	st := r.snapshot()
	for i, s := range st.status {
		if s != statusDone {
			t.Errorf("task %d status = %v", i, s)
		}
	}
	if len(st.status) != 7 {
		t.Errorf("task count = %d", len(st.status))
	}
}

func TestRunnerBuildFailure(t *testing.T) {
	restoreFakes(t)
	opts := testOpts(t)
	r := newRunner(opts, testLogger(t))
	goCmd = fakeToolchain("fakecc", 0, fakeGoBuildFail)
	err := r.runAll(context.Background(), nil)
	if err == nil || err.Error() != "boom" {
		t.Fatalf("err = %v", err)
	}
	st := r.snapshot()
	if st.status[2] != statusFailed || st.status[3] != statusPending {
		t.Errorf("statuses = %v", st.status)
	}
	if r.errDetail != "boom" {
		t.Errorf("errDetail = %q", r.errDetail)
	}
	wantCmd := "CGO_ENABLED=1 go build -o " + r.buildDir + "/sysc-lock ./cmd/sysc-lock"
	if r.cmdLine != wantCmd {
		t.Errorf("cmdLine = %q, want %q", r.cmdLine, wantCmd)
	}
	if _, err := os.Stat(opts.prefix); !os.IsNotExist(err) {
		t.Error("prefix touched")
	}
	data, _ := os.ReadFile(r.log.Path())
	text := string(data)
	if !strings.Contains(text, "Running:") || !strings.Contains(text, "boom") {
		t.Errorf("log missing command/output:\n%s", text)
	}
	if _, err := os.Stat(r.buildDir); !os.IsNotExist(err) {
		t.Error("buildDir not cleaned after failure")
	}
}

func TestRunnerCancelKillsGroup(t *testing.T) {
	restoreFakes(t)
	pidsFile := filepath.Join(t.TempDir(), "pids")
	r := newRunner(testOpts(t), testLogger(t))
	goCmd = fakeGoBuildSleep(pidsFile)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- r.runAll(ctx, nil) }()
	time.Sleep(300 * time.Millisecond)
	cancel()
	if err := <-done; !errors.Is(err, errCancelled) {
		t.Fatalf("err = %v", err)
	}
	data, err := os.ReadFile(pidsFile)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for _, line := range strings.Fields(string(data)) {
		for {
			if _, err := os.Stat("/proc/" + line); os.IsNotExist(err) {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("pid %s still alive after cancel", line)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
}

func TestToolchainGoMissing(t *testing.T) {
	restoreFakes(t)
	lookPath = func(string) (string, error) { return "", errors.New("not found") }
	goCmd = fakeGoBuildFail
	r := newRunner(testOpts(t), testLogger(t))
	err := r.runAll(context.Background(), nil)
	want := "go not found in PATH: install Go (go.mod needs 1.26) or pass --candidate PATH"
	if err == nil || err.Error() != want {
		t.Fatalf("err = %v", err)
	}
}

func TestToolchainPamMissing(t *testing.T) {
	restoreFakes(t)
	goCmd = fakeToolchain("fakecc", 1, fakeGoBuildOK)
	r := newRunner(testOpts(t), testLogger(t))
	err := r.runAll(context.Background(), nil)
	want := "libpam headers missing (security/pam_appl.h): install them " +
		"(Arch: pam, Debian/Ubuntu: libpam0g-dev, Fedora: pam-devel)"
	if err == nil || err.Error() != want {
		t.Fatalf("err = %v", err)
	}
	if st := r.snapshot(); st.status[2] != statusPending {
		t.Errorf("build should not run: %v", st.status)
	}
}

func TestToolchainPassesThenBuilds(t *testing.T) {
	restoreFakes(t)
	goCmd = fakeToolchain("fakecc", 0, fakeGoBuildOK)
	r := newRunner(testOpts(t), testLogger(t))
	if err := r.runAll(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	st := r.snapshot()
	if st.status[1] != statusDone || st.status[2] != statusDone {
		t.Errorf("statuses = %v", st.status)
	}
}

func TestCandidateSkipsToolchainAndBuild(t *testing.T) {
	restoreFakes(t)
	dir := t.TempDir()
	cand := dir + "/candidate"
	if err := os.WriteFile(cand, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	opts := testOpts(t)
	opts.candidate = cand
	r := newRunner(opts, testLogger(t))
	goCmd = fakeGoBuildFail
	if err := r.runAll(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	st := r.snapshot()
	if st.status[1] != statusSkipped || st.status[2] != statusSkipped {
		t.Errorf("statuses = %v", st.status)
	}
	if st.skips[1] != "using --candidate" || st.skips[2] != "using --candidate" {
		t.Errorf("skips = %v", st.skips)
	}
	bin, err := os.ReadFile(opts.prefix + "/bin/sysc-lock")
	if err != nil || string(bin) != "#!/bin/sh\n" {
		t.Errorf("binary = %q, %v", bin, err)
	}
}

func uninstallOpts(t *testing.T) options {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	return options{prefix: t.TempDir() + "/.local", uninstall: true}
}

func seedInstall(t *testing.T, prefix string) {
	t.Helper()
	if err := installDirs(prefix); err != nil {
		t.Fatal(err)
	}
	for path, data := range map[string]string{
		prefix + "/share/systemd/user/sysc-lock-session.service": "[Service]\n",
		prefix + "/bin/sysc-lock":                                "#!/bin/sh\n",
		prefix + "/bin/sysc-lock.new":                            "stale\n",
	} {
		if err := os.WriteFile(path, []byte(data), 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

func TestUninstallRemovesUnitBinaryAndStagedFile(t *testing.T) {
	opts := uninstallOpts(t)
	seedInstall(t, opts.prefix)
	r := newRunner(opts, testLogger(t))
	if err := r.runAll(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"/bin/sysc-lock", "/bin/sysc-lock.new", "/share/systemd/user/sysc-lock-session.service"} {
		if _, err := os.Stat(opts.prefix + p); !os.IsNotExist(err) {
			t.Errorf("%s survived removal", p)
		}
	}
	if fi, err := os.Stat(opts.prefix + "/bin"); err != nil || !fi.IsDir() {
		t.Error("directories must survive the uninstall")
	}
	st := r.snapshot()
	if len(st.status) != 4 {
		t.Fatalf("task count = %d", len(st.status))
	}
	for i, s := range st.status {
		if s != statusDone {
			t.Errorf("task %d status = %v", i, s)
		}
	}
}

func TestUninstallEmptyPrefixSkipsRemovals(t *testing.T) {
	opts := uninstallOpts(t)
	r := newRunner(opts, testLogger(t))
	if err := r.runAll(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	st := r.snapshot()
	if st.status[2] != statusSkipped || st.skips[2] != "unit not installed" {
		t.Errorf("unit task = %v %q", st.status[2], st.skips[2])
	}
	if st.status[3] != statusSkipped || st.skips[3] != "nothing installed" {
		t.Errorf("binary task = %v %q", st.status[3], st.skips[3])
	}
}

func TestUninstallRefusesWhileRunning(t *testing.T) {
	opts := uninstallOpts(t)
	seedInstall(t, opts.prefix)
	sleepBin, err := exec.LookPath("sleep")
	if err != nil {
		t.Skip("no sleep binary")
	}
	data, err := os.ReadFile(sleepBin)
	if err != nil {
		t.Fatal(err)
	}
	live := opts.prefix + "/bin/sysc-lock"
	if err := os.WriteFile(live, data, 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(live, "60")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })

	r := newRunner(opts, testLogger(t))
	err = r.runAll(context.Background(), nil)
	want := fmt.Sprintf("sysc-lock is running (pid %d) from %s: "+
		"stop sysc-lock-session.service in a coordinated Niri session first", cmd.Process.Pid, live)
	if err == nil || err.Error() != want {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Stat(opts.prefix + "/share/systemd/user/sysc-lock-session.service"); err != nil {
		t.Error("unit removed despite the refusal")
	}
}

func TestUninstallRefusesWhenUnitEnabled(t *testing.T) {
	opts := uninstallOpts(t)
	seedInstall(t, opts.prefix)
	wants := os.Getenv("XDG_CONFIG_HOME") + "/systemd/user/graphical-session.target.wants"
	if err := os.MkdirAll(wants, 0o755); err != nil {
		t.Fatal(err)
	}
	link := wants + "/sysc-lock-session.service"
	if err := os.Symlink(opts.prefix+"/share/systemd/user/sysc-lock-session.service", link); err != nil {
		t.Fatal(err)
	}
	r := newRunner(opts, testLogger(t))
	err := r.runAll(context.Background(), nil)
	want := "sysc-lock-session.service is enabled (" + link + "): disable it first"
	if err == nil || err.Error() != want {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Stat(opts.prefix + "/bin/sysc-lock"); err != nil {
		t.Error("binary removed despite the refusal")
	}
}

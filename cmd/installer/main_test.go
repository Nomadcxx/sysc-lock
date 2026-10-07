package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeCheckout builds a checkout that findRepoRoot accepts.
func fakeCheckout(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(root+"/contrib/systemd", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(root+"/go.mod", []byte("module github.com/Nomadcxx/sysc-lock\n\ngo 1.26.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	unit := "[Unit]\nDescription=sysc-lock session locker\nExecStart=%h/.local/bin/sysc-lock --session\n"
	if err := os.WriteFile(root+"/contrib/systemd/sysc-lock-session.service", []byte(unit), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func fakeCandidate(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "sysc-lock")
	if err := os.WriteFile(p, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// nonRoot keeps the EUID-0 rule out of the tests that are not about it.
func nonRoot(t *testing.T) {
	t.Helper()
	old := geteuid
	t.Cleanup(func() { geteuid = old })
	geteuid = func() int { return 1000 }
}

func noTTY(t *testing.T) {
	t.Helper()
	old := ttyOK
	t.Cleanup(func() { ttyOK = old })
	ttyOK = func() bool { return false }
}

func TestRunHelpExitsZeroOnStdout(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"--help"}, &out, &errb); code != exitOK {
		t.Fatalf("code=%d stderr=%q", code, errb.String())
	}
	if !strings.Contains(out.String(), "usage: sysc-lock-installer") {
		t.Fatalf("stdout=%q", out.String())
	}
	if errb.Len() != 0 {
		t.Fatalf("--help wrote to stderr: %q", errb.String())
	}
}

func TestRunUnknownFlagIsUsageError(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"--bogus"}, &out, &errb); code != exitUsage {
		t.Fatalf("code=%d", code)
	}
	if !strings.Contains(errb.String(), "usage: sysc-lock-installer") {
		t.Fatalf("stderr=%q", errb.String())
	}
}

func TestRunPositionalArgIsUsageError(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"extra"}, &out, &errb); code != exitUsage {
		t.Fatalf("code=%d", code)
	}
	if !strings.Contains(errb.String(), "usage: sysc-lock-installer") {
		t.Fatalf("stderr=%q", errb.String())
	}
}

func TestRunEUIDZeroNeedsExplicitPrefix(t *testing.T) {
	old := geteuid
	t.Cleanup(func() { geteuid = old })
	geteuid = func() int { return 0 }
	var out, errb bytes.Buffer
	if code := run(nil, &out, &errb); code != exitUsage {
		t.Fatalf("code=%d", code)
	}
	want := "running as root: pass --prefix explicitly (sysc-lock installs into a user prefix)"
	if strings.TrimSpace(errb.String()) != want {
		t.Fatalf("stderr=%q want %q", errb.String(), want)
	}
}

func TestRunOutsideCheckoutIsPreflightError(t *testing.T) {
	nonRoot(t)
	t.Chdir(t.TempDir())
	var out, errb bytes.Buffer
	if code := run([]string{"--yes"}, &out, &errb); code != exitUsage {
		t.Fatalf("code=%d", code)
	}
	want := "run the installer from inside a sysc-lock checkout"
	if strings.TrimSpace(errb.String()) != want {
		t.Fatalf("stderr=%q want %q", errb.String(), want)
	}
}

func TestRunWithoutTerminalNeedsYes(t *testing.T) {
	nonRoot(t)
	noTTY(t)
	t.Chdir(fakeCheckout(t))
	var out, errb bytes.Buffer
	if code := run([]string{"--prefix", t.TempDir() + "/.local"}, &out, &errb); code != exitUsage {
		t.Fatalf("code=%d", code)
	}
	if strings.TrimSpace(errb.String()) != "no terminal: rerun with --yes" {
		t.Fatalf("stderr=%q", errb.String())
	}
}

func TestRunYesInstallsCandidateAndPrintsSummary(t *testing.T) {
	nonRoot(t)
	noTTY(t)
	restoreFakes(t)
	t.Chdir(fakeCheckout(t))
	prefix := t.TempDir() + "/.local"
	var out, errb bytes.Buffer
	code := run([]string{
		"--yes", "--candidate", fakeCandidate(t), "--prefix", prefix,
		"--log", filepath.Join(t.TempDir(), "installer.log"),
	}, &out, &errb)
	if code != exitOK {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, out.String(), errb.String())
	}
	for _, want := range []string{
		"[OK]   Check prefix",
		"[SKIP] Check toolchain (using --candidate)",
		"[SKIP] Build sysc-lock (using --candidate)",
		"[OK]   Install user unit",
		"sysc-lock installer: install complete: " + prefix + "/bin/sysc-lock, " +
			prefix + "/share/systemd/user/sysc-lock-session.service",
		"Log: ",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("stdout missing %q\n%s", want, out.String())
		}
	}
	for _, path := range []string{
		prefix + "/bin/sysc-lock",
		prefix + "/share/systemd/user/sysc-lock-session.service",
	} {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("%s: %v", path, err)
		}
	}
}

func TestRunYesUninstallRemovesInstalledFiles(t *testing.T) {
	nonRoot(t)
	noTTY(t)
	t.Chdir(fakeCheckout(t))
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	prefix := t.TempDir() + "/.local"
	seedInstall(t, prefix)
	var out, errb bytes.Buffer
	code := run([]string{
		"--yes", "--uninstall", "--prefix", prefix,
		"--log", filepath.Join(t.TempDir(), "installer.log"),
	}, &out, &errb)
	if code != exitOK {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, out.String(), errb.String())
	}
	if !strings.Contains(out.String(), "sysc-lock installer: uninstall complete") {
		t.Fatalf("stdout=%q", out.String())
	}
	for _, path := range []string{
		prefix + "/bin/sysc-lock",
		prefix + "/bin/sysc-lock.new",
		prefix + "/share/systemd/user/sysc-lock-session.service",
	} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("%s still present (%v)", path, err)
		}
	}
}

func TestRunYesBuildFailureExitsOne(t *testing.T) {
	nonRoot(t)
	noTTY(t)
	restoreFakes(t)
	goCmd = fakeToolchain("cc", 0, fakeGoBuildFail)
	t.Chdir(fakeCheckout(t))
	var out, errb bytes.Buffer
	code := run([]string{
		"--yes", "--prefix", t.TempDir() + "/.local",
		"--log", filepath.Join(t.TempDir(), "installer.log"),
	}, &out, &errb)
	if code != exitFailed {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, out.String(), errb.String())
	}
	if !strings.Contains(out.String(), "[FAIL] Build sysc-lock") {
		t.Fatalf("stdout=%q", out.String())
	}
	if !strings.Contains(errb.String(), `install failed at "Build sysc-lock"`) {
		t.Fatalf("stderr=%q", errb.String())
	}
}

func TestRunPlainCancelledContextIs130(t *testing.T) {
	opts := testOpts(t)
	log := testLogger(t)
	r := newRunner(opts, log)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var out bytes.Buffer
	code, err := runPlain(ctx, r, &out)
	if code != exitCancelled {
		t.Fatalf("code=%d stdout=%q", code, out.String())
	}
	if !strings.Contains(out.String(), "cancelled") {
		t.Fatalf("stdout=%q", out.String())
	}
	if err == nil {
		t.Fatal("expected the cancellation error")
	}
}

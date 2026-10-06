package main

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
)

func TestValidatePrefixMatchesScript(t *testing.T) {
	cases := []struct {
		prefix string
		errMsg string
	}{
		{"/home/test/.local", ""},
		{"/tmp/a-b_c.d/e", ""},
		{"/", "use a user prefix"},
		{"", "use a user prefix"},
		{"relative/x", "prefix must be absolute"},
		{"ünï/x", "prefix must be absolute"},
		{"/ünï", "prefix contains unsupported characters"},
		{"/tmp/a b", "prefix contains unsupported characters"},
		{"/tmp/a$b", "prefix contains unsupported characters"},
	}
	for _, c := range cases {
		err := validatePrefix(c.prefix)
		got := ""
		if err != nil {
			got = err.Error()
		}
		if got != c.errMsg {
			t.Errorf("validatePrefix(%q) = %q, want %q", c.prefix, got, c.errMsg)
		}
	}
}

func TestCheckCandidateMatchesScript(t *testing.T) {
	dir := t.TempDir()
	ok := dir + "/ok"
	if err := os.WriteFile(ok, []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatal(err)
	}
	noExec := dir + "/noexec"
	if err := os.WriteFile(noExec, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	link := dir + "/link"
	if err := os.Symlink(ok, link); err != nil {
		t.Fatal(err)
	}
	broken := dir + "/broken"
	if err := os.Symlink(dir+"/gone", broken); err != nil {
		t.Fatal(err)
	}
	sub := dir + "/sub"
	if err := os.Mkdir(sub, 0755); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{ok, link} {
		if err := checkCandidate(p); err != nil {
			t.Errorf("%s: want nil, got %v", p, err)
		}
	}
	for _, p := range []string{noExec, broken, sub, dir + "/missing"} {
		if err := checkCandidate(p); err == nil || err.Error() != "candidate must be an executable regular file" {
			t.Errorf("%s: got %v", p, err)
		}
	}
}

type treeEntry struct {
	dir     bool
	perm    os.FileMode
	content []byte
}

// collectTree replaces the tree's own root path inside file contents, so two
// trees installed under different prefixes compare equal apart from that path.
func collectTree(t *testing.T, root string) map[string]treeEntry {
	t.Helper()
	out := map[string]treeEntry{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		e := treeEntry{dir: d.IsDir(), perm: fi.Mode().Perm()}
		if !d.IsDir() {
			e.content, err = os.ReadFile(p)
			if err != nil {
				return err
			}
			e.content = bytes.ReplaceAll(e.content, []byte(root), []byte("«root»"))
		}
		out[rel] = e
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func equalTrees(t *testing.T, scriptOut, goOut string) {
	t.Helper()
	ta := collectTree(t, scriptOut)
	tb := collectTree(t, goOut)
	for k, ea := range ta {
		eb, ok := tb[k]
		if !ok {
			t.Errorf("%s: missing in go tree", k)
			continue
		}
		if ea.dir != eb.dir || ea.perm != eb.perm {
			t.Errorf("%s: type/perm mismatch script(%v,%04o) go(%v,%04o)", k, ea.dir, ea.perm, eb.dir, eb.perm)
		}
		if !ea.dir && !bytes.Equal(ea.content, eb.content) {
			t.Errorf("%s: content differs", k)
		}
	}
	for k := range tb {
		if _, ok := ta[k]; !ok {
			t.Errorf("%s: extra in go tree", k)
		}
	}
}

func TestGoInstallMatchesScript(t *testing.T) {
	old := syscall.Umask(0o022)
	defer syscall.Umask(old)

	repo := t.TempDir()
	for _, rel := range []string{"scripts/install", "contrib/systemd/sysc-lock-session.service"} {
		data, err := os.ReadFile("../../" + rel)
		if err != nil {
			t.Fatal(err)
		}
		dst := repo + "/" + rel
		if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dst, data, 0644); err != nil {
			t.Fatal(err)
		}
	}
	candidate := repo + "/candidate"
	if err := os.WriteFile(candidate, []byte("#!/bin/sh\necho candidate\n"), 0755); err != nil {
		t.Fatal(err)
	}

	base := t.TempDir()
	for i, suffix := range []string{"", "/"} {
		sp := fmt.Sprintf("%s/p%d%s", base, i, suffix)
		gp := fmt.Sprintf("%s/g%d%s", base, i, suffix)
		out, err := exec.Command("sh", repo+"/scripts/install", candidate, sp).CombinedOutput()
		if err != nil {
			t.Fatalf("script: %v\n%s", err, out)
		}
		if err := installDirs(gp); err != nil {
			t.Fatal(err)
		}
		if err := installBinary(gp, candidate); err != nil {
			t.Fatal(err)
		}
		if err := installUnit(repo, gp); err != nil {
			t.Fatal(err)
		}
		equalTrees(t, sp, gp)
	}
}

func TestRunningLockOwners(t *testing.T) {
	proc := t.TempDir()
	old := procPath
	procPath = proc
	defer func() { procPath = old }()
	bin := "/home/test/.local/bin/sysc-lock"
	mk := func(pid, target string) {
		if err := os.Mkdir(proc+"/"+pid, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, proc+"/"+pid+"/exe"); err != nil {
			t.Fatal(err)
		}
	}
	mk("42", bin)
	mk("99", bin+" (deleted)")
	mk("7", "/usr/bin/sleep")
	mk("6", "/other/.local/bin/sysc-lock")
	if err := os.Mkdir(proc+"/abc", 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(proc+"/5", 0755); err != nil {
		t.Fatal(err)
	}
	got := runningLockOwners(bin)
	if len(got) != 2 {
		t.Fatalf("want 2 owners, got %v", got)
	}
	byPID := map[int]string{}
	for _, o := range got {
		byPID[o.pid] = o.exe
	}
	if byPID[42] != bin || byPID[99] != bin+" (deleted)" {
		t.Errorf("got %v", byPID)
	}
}

func TestEnabledUnitLinks(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	links, err := enabledUnitLinks()
	if err != nil || len(links) != 0 {
		t.Fatalf("want empty, got %v, %v", links, err)
	}
	wants := cfg + "/systemd/user/graphical-session.target.wants"
	if err := os.MkdirAll(wants, 0755); err != nil {
		t.Fatal(err)
	}
	link := wants + "/sysc-lock-session.service"
	if err := os.Symlink("/home/test/.local/share/systemd/user/sysc-lock-session.service", link); err != nil {
		t.Fatal(err)
	}
	links, err = enabledUnitLinks()
	if err != nil || len(links) != 1 || links[0] != link {
		t.Fatalf("want [%s], got %v, %v", link, links, err)
	}
}

func TestRemoveIfExists(t *testing.T) {
	dir := t.TempDir()
	removed, err := removeIfExists(dir + "/gone")
	if err != nil || removed {
		t.Fatalf("missing: %v %v", removed, err)
	}
	if err := os.WriteFile(dir+"/present", []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	removed, err = removeIfExists(dir + "/present")
	if err != nil || !removed {
		t.Fatalf("present: %v %v", removed, err)
	}
	if _, err := os.Stat(dir + "/present"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("still there: %v", err)
	}
}

package main

import (
	"os"
	"strings"
	"testing"
)

func TestLogFallbackFromUnsafePath(t *testing.T) {
	dir := t.TempDir()
	bad := dir + "/log"
	if err := os.Symlink(dir+"/elsewhere", bad); err != nil {
		t.Fatal(err)
	}
	l, err := openLog(bad)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if l.Path() == bad {
		t.Fatal("symlink path must not be used")
	}
	if !strings.HasPrefix(l.Path(), os.TempDir()) {
		t.Fatalf("fallback %q", l.Path())
	}
}

func TestLogHeaderMatchesSpec(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/installer.log"
	l, err := openLog(path)
	if err != nil {
		t.Fatal(err)
	}
	logHeader(l, "install", "/home/u/.local", "/home/u/.local/bin/sysc-lock", "/src/sysc-lock")
	l.Printf("[Check prefix] Started")
	l.Close()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{
		"=== sysc-lock Installer Log ===",
		"Mode: install",
		"Prefix: /home/u/.local",
		"Source: /home/u/.local/bin/sysc-lock",
		"Repo: /src/sysc-lock",
		"EUID:",
		"[Check prefix] Started",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("log missing %q\n%s", want, text)
		}
	}
	if fi, err := os.Stat(path); err != nil || fi.Mode().Perm() != 0600 {
		t.Errorf("mode = %v, %v", fi.Mode(), err)
	}
}

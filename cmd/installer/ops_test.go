package main

import (
	"os"
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

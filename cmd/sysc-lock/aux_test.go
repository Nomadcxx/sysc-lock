package main

import (
	"bytes"
	"github.com/Nomadcxx/sysc-lock/internal/input"
	"github.com/Nomadcxx/sysc-lock/internal/lockd"
	"os"
	"os/user"
	"strconv"
	"testing"
)

func TestEmitLockedHandshakeWritesTheShellLine(t *testing.T) {
	var buf bytes.Buffer
	emitLockedHandshake(&buf)
	if got := buf.String(); got != "sysc-lock: locked\n" {
		t.Fatalf("handshake = %q, want the stdout line the shell greps", got)
	}
}

func TestAuthIdentityIgnoresUserEnvironment(t *testing.T) {
	t.Setenv("USER", "somebody-else")
	want, err := user.LookupId(strconv.Itoa(os.Getuid()))
	if err != nil {
		t.Fatal(err)
	}
	got, err := currentUser()
	if err != nil || got != want.Username {
		t.Fatal(got, err)
	}
}

func TestBusyEntryCannotChange(t *testing.T) {
	m := &input.Model{}
	m.Append("secret")
	g := &enterGate{}
	if submit, err := g.handle(m, lockd.Key{Enter: true}); !submit || err != nil {
		t.Fatal(err)
	}
	for _, k := range []lockd.Key{{Text: "x"}, {Backspace: true}, {Escape: true}, {Enter: true}} {
		if submit, err := g.handle(m, k); submit || err != nil {
			t.Fatal("busy submitted", err)
		}
		if m.Password() != "secret" {
			t.Fatal("busy entry edited")
		}
	}
}

func TestLateAuthResultIgnored(t *testing.T) {
	g := &enterGate{}
	g.try("one")
	old := g.generation
	g.release()
	g.try("two")
	if g.accept(old, lockd.Locked) {
		t.Fatal("accepted stale generation")
	}
	if g.accept(g.generation, lockd.Terminated) {
		t.Fatal("accepted after teardown")
	}
	if !g.accept(g.generation, lockd.Locked) {
		t.Fatal("rejected current attempt")
	}
}

func TestEnterGateRejectsEmptyAndOverlap(t *testing.T) {
	var g enterGate
	if g.try("") {
		t.Fatal("empty password must not start auth")
	}
	if !g.try("secret") {
		t.Fatal("first Enter must start auth")
	}
	if g.try("secret") {
		t.Fatal("overlapping Enter must be ignored")
	}
	g.release()
	if !g.try("secret") {
		t.Fatal("after release, Enter must start auth again")
	}
}

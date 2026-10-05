package main

import (
	"bytes"
	"github.com/Nomadcxx/sysc-lock/internal/input"
	"github.com/Nomadcxx/sysc-lock/internal/lockd"
	"os"
	"os/user"
	"strconv"
	"testing"
	"time"
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
	g.try(true)
	old := g.generation
	g.release()
	g.try(true)
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
	if g.try(false) {
		t.Fatal("empty password must not start auth")
	}
	if !g.try(true) {
		t.Fatal("first Enter must start auth")
	}
	if g.try(true) {
		t.Fatal("overlapping Enter must be ignored")
	}
	g.release()
	if !g.try(true) {
		t.Fatal("after release, Enter must start auth again")
	}
}

func TestHiddenEntryOnlyReveals(t *testing.T) {
	m := &input.Model{}
	var r input.Reveal
	g := &enterGate{}
	now := time.Unix(100, 0)
	for _, k := range []lockd.Key{{Text: "x"}, {Enter: true}, {Backspace: true}, {}} {
		r = input.Reveal{}
		submit, err := g.press(m, &r, k, now)
		if submit || err != nil || len(m.Pass) != 0 {
			t.Fatalf("hidden entry accepted %+v", k)
		}
		if !r.Tick(now, false) {
			t.Fatalf("%+v did not reveal", k)
		}
	}
	if submit, err := g.press(m, &r, lockd.Key{Text: "p"}, now.Add(time.Second)); submit || err != nil || m.Password() != "p" {
		t.Fatal("the second key must type")
	}
}

func TestEscapeWipesAndHides(t *testing.T) {
	m := &input.Model{}
	var r input.Reveal
	g := &enterGate{}
	now := time.Unix(100, 0)
	r.Show(now)
	g.press(m, &r, lockd.Key{Text: "secret"}, now)
	if m.Password() != "secret" {
		t.Fatal("setup")
	}
	g.press(m, &r, lockd.Key{Escape: true}, now.Add(time.Second))
	if len(m.Pass) != 0 || r.Tick(now.Add(time.Second), true) {
		t.Fatal("Esc must clear the field and hide the entry")
	}
	for _, c := range m.Pass[:cap(m.Pass)] {
		if c != 0 {
			t.Fatal("Esc left password runes in the buffer")
		}
	}
}

func TestEscapeDuringVerificationKeepsEntry(t *testing.T) {
	m := &input.Model{}
	m.Append("secret")
	var r input.Reveal
	g := &enterGate{}
	now := time.Unix(100, 0)
	r.Show(now)
	if submit, _ := g.press(m, &r, lockd.Key{Enter: true}, now); !submit {
		t.Fatal("setup")
	}
	g.press(m, &r, lockd.Key{Escape: true}, now.Add(time.Second))
	if m.Password() != "secret" || !r.Tick(now.Add(time.Second), true) {
		t.Fatal("verification must not be interrupted by Esc")
	}
}

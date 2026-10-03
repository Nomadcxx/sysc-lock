package main

import (
	"bytes"
	"testing"
)

func TestEmitLockedHandshakeWritesTheShellLine(t *testing.T) {
	var buf bytes.Buffer
	emitLockedHandshake(&buf)
	if got := buf.String(); got != "sysc-lock: locked\n" {
		t.Fatalf("handshake = %q, want the stdout line the shell greps", got)
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

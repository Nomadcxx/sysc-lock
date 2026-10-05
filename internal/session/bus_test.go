package session

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func TestProtocolHasNoUnlockMethod(t *testing.T) {
	typ := reflect.TypeOf(&busAPI{})
	for i := 0; i < typ.NumMethod(); i++ {
		name := typ.Method(i).Name
		if name != "Lock" && name != "GetState" {
			t.Fatalf("unexpected bus method %s", name)
		}
	}
}
func TestNotifyUsesUnixSocket(t *testing.T) {
	if err := Notify(""); err != nil {
		t.Fatal(err)
	}
}
func TestSessionRejectsMissingStartupContext(t *testing.T) {
	t.Setenv("XDG_SESSION_ID", "")
	if _, err := ValidateNiri(nil); err == nil {
		t.Fatal("accepted missing registration")
	}
}
func TestMarkerRejectsSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	m := marker{Identity: Identity{UID: uint32(os.Getuid()), Session: "a", Compositor: "b"}}
	if err := writeMarker(filepath.Join(dir, "private", "state"), m); err != nil {
		t.Fatal(err)
	}
	original := filepath.Join(dir, "private", "state")
	if err := os.Rename(original, target); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, original); err != nil {
		t.Fatal(err)
	}
	if _, err := readMarker(original); err == nil {
		t.Fatal("followed state symlink")
	}
}

func TestSessionRejectsForeignWaylandDisplay(t *testing.T) {
	t.Setenv("XDG_SESSION_ID", "test")
	t.Setenv("XDG_RUNTIME_DIR", filepath.Join("/run/user", strconv.Itoa(os.Getuid())))
	t.Setenv("NIRI_SOCKET", filepath.Join(os.Getenv("XDG_RUNTIME_DIR"), "missing-niri.sock"))
	t.Setenv("WAYLAND_DISPLAY", "/foreign/wayland-1")
	if _, err := ValidateNiri(nil); err == nil || !strings.Contains(err.Error(), "Wayland") {
		t.Fatalf("expected Wayland boundary rejection, got %v", err)
	}
}

func TestNativeUserUnitStartupRegistration(t *testing.T) {
	// A native user unit need not belong to a logind session scope. Its startup
	// session registration is still compared with the unique graphical session.
	if err := validateStartupEnvironment([]byte("INVOCATION_ID=unit-instance\x00XDG_SESSION_ID=c7\x00"), "c7"); err != nil {
		t.Fatal(err)
	}
	for _, environment := range [][]byte{
		[]byte("XDG_SESSION_ID=stale\x00"),
		[]byte("INVOCATION_ID=unit-instance\x00"),
		[]byte("XDG_SESSION_ID=c7\x00XDG_SESSION_ID=c7\x00"),
		bytes.Repeat([]byte{'x'}, maxStartupEnvironment+1),
	} {
		if err := validateStartupEnvironment(environment, "c7"); err == nil {
			t.Fatal("accepted missing/stale/ambiguous/oversize startup registration")
		}
	}
}

func TestWaylandPeerMatchesNiriPeer(t *testing.T) {
	ipc := &unix.Ucred{Uid: uint32(os.Getuid()), Pid: 123}
	if err := sameCompositorPeer(ipc, &unix.Ucred{Uid: ipc.Uid, Pid: ipc.Pid}); err != nil {
		t.Fatal(err)
	}
	for _, display := range []*unix.Ucred{nil, {Uid: ipc.Uid, Pid: 456}, {Uid: ipc.Uid + 1, Pid: ipc.Pid}, {Uid: ipc.Uid, Pid: 0}} {
		if err := sameCompositorPeer(ipc, display); err == nil {
			t.Fatal("accepted unrelated Wayland peer")
		}
	}
}

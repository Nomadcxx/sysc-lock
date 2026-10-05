package ambient

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestWriteThenLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ambient.json")
	pct := 82
	in := Snapshot{AsOf: time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC), BatteryPct: &pct, Link: LinkWifi}
	if err := Write(path, in); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("mode %v %v", info, err)
	}
	got, err := Load(path, in.AsOf)
	if err != nil || got.Link != LinkWifi || got.BatteryPct == nil || *got.BatteryPct != 82 {
		t.Fatalf("got %+v %v", got, err)
	}
}

func TestLoadRejectsStaleMissingAndJunk(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ambient.json")
	now := time.Date(2026, 10, 6, 0, 0, 6, 0, time.UTC)
	if _, err := Load(path, now); err == nil {
		t.Fatal("missing must fail")
	}
	if err := os.WriteFile(path, []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path, now); err == nil {
		t.Fatal("junk must fail")
	}
	pct := 1
	if err := Write(path, Snapshot{AsOf: now.Add(-6 * time.Second), BatteryPct: &pct}); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path, now); err == nil {
		t.Fatal("stale must fail")
	}
}

func TestWriteRejectsOversizedDecode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ambient.json")
	big := make([]byte, MaxBytes+1)
	for i := range big {
		big[i] = 'x'
	}
	if err := os.WriteFile(path, big, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path, time.Now()); err == nil {
		t.Fatal("oversized must fail")
	}
}

func TestLoadFIFODoesNotBlock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ambient.json")
	if err := syscall.Mkfifo(path, 0600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := Load(path, time.Now()); done <- err }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("accepted a FIFO snapshot")
		}
	case <-time.After(200 * time.Millisecond):
		writer, err := os.OpenFile(path, os.O_WRONLY|syscall.O_NONBLOCK, 0)
		if err == nil {
			writer.Close()
			<-done
		}
		t.Fatal("FIFO snapshot blocked the owner")
	}
}

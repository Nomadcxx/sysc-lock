package ambient

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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
	if err := Write(path, Snapshot{AsOf: now.Add(-6 * time.Second), BatteryPct: &pct, Media: Playing, Title: "Old track", Artist: "Old artist"}); err != nil {
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

func TestWriteAndLoadBoundMetadata(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ambient.json")
	now := time.Now()
	raw := map[string]any{"as_of": now, "media": "playing", "title": "  A\n B\u202e\u200d\x00  ", "artist": strings.Repeat("é", 140)}
	buf, _ := json.Marshal(raw)
	if err := os.WriteFile(path, buf, 0600); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path, now)
	if err != nil {
		t.Fatal(err)
	}
	// Inspect both boundaries; user-owned snapshots bypass collection.
	out, _ := json.Marshal(loaded)
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	if got["title"] != "A B" || got["artist"] != strings.Repeat("é", 128) {
		t.Fatalf("unsafe or unbounded loaded metadata")
	}
	var input Snapshot
	if err := json.Unmarshal(buf, &input); err != nil {
		t.Fatal(err)
	}
	if err := Write(path, input); err != nil {
		t.Fatal(err)
	}
	out, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	if got["title"] != "A B" || got["artist"] != strings.Repeat("é", 128) {
		t.Fatal("writer did not bound metadata")
	}
}

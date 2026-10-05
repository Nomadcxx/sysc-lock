package session

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMarkerReadRequiresPrivateDirectory(t *testing.T) {
	s := newSession(t)
	if _, err := s.Lock(); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Dir(s.path), 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := readMarker(s.path); err == nil {
		t.Fatal("read marker from public directory")
	}
}

func TestMarkerRejectsDirectorySymlink(t *testing.T) {
	s := newSession(t)
	if _, err := s.Lock(); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(filepath.Dir(s.path), alias); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(alias, "state.json")
	if _, err := readMarker(path); err == nil {
		t.Fatal("read through directory symlink")
	}
	if err := writeMarker(path, s.marker); err == nil {
		t.Fatal("wrote through directory symlink")
	}
}

func TestMarkerRejectsIntentBehindGeneration(t *testing.T) {
	s := newSession(t)
	forged := marker{Identity: s.identity, Generation: 2, Intent: 1}
	if err := writeMarker(s.path, forged); err != nil {
		t.Fatal(err)
	}
	if _, err := New(s.identity, s.path); err == nil {
		t.Fatal("accepted recovery intent behind published generation")
	}
}

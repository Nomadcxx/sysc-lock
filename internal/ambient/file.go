package ambient

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

const (
	// MaxBytes is the decode cap; the snapshot never approaches it.
	MaxBytes = 4096
	// Stale is how old a snapshot may be before the owner ignores it.
	Stale = 5 * time.Second
)

// Path is the snapshot under the user's runtime directory, or "" when
// XDG_RUNTIME_DIR is unset.
func Path() string {
	dir := os.Getenv("XDG_RUNTIME_DIR")
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, "sysc-lock", "ambient.json")
}

// Write replaces the snapshot atomically: mode-0700 directory, 0600 tmp file,
// rename. A partial read is impossible.
func Write(path string, s Snapshot) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	s.Title, s.Artist = cleanMetadata(s.Title), cleanMetadata(s.Artist)
	buf, err := json.Marshal(s)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return err
	}
	if _, err := f.Write(buf); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}

// Load reads the snapshot, rejecting a missing, oversized, junk, or stale
// file. Any error means the owner draws no row.
func Load(path string, now time.Time) (Snapshot, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return Snapshot{}, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return Snapshot{}, err
	}
	if !info.Mode().IsRegular() || info.Size() > MaxBytes {
		return Snapshot{}, fmt.Errorf("snapshot is not a regular file")
	}
	buf := make([]byte, MaxBytes+1)
	n, err := f.Read(buf)
	if err != nil {
		return Snapshot{}, err
	}
	if n > MaxBytes {
		return Snapshot{}, fmt.Errorf("snapshot larger than %d bytes", MaxBytes)
	}
	var s Snapshot
	if err := json.NewDecoder(bytes.NewReader(buf[:n])).Decode(&s); err != nil {
		return Snapshot{}, err
	}
	if now.Sub(s.AsOf) > Stale {
		return Snapshot{}, fmt.Errorf("snapshot is older than %s", Stale)
	}
	s.Title, s.Artist = cleanMetadata(s.Title), cleanMetadata(s.Artist)
	return s, nil
}

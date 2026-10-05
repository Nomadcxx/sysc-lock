package session

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/unix"
)

type Identity struct {
	UID        uint32
	Session    string
	Compositor string
}
type marker struct {
	Identity        Identity
	Generation      uint64
	Intent          uint64
	ConfirmedUnlock uint64
}

// openMarkerDirectory pins the private directory before descriptor-relative I/O.
func openMarkerDirectory(path string) (*os.File, error) {
	fd, err := unix.Open(filepath.Dir(path), unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	dir := os.NewFile(uintptr(fd), filepath.Dir(path))
	info, err := dir.Stat()
	if err != nil {
		dir.Close()
		return nil, err
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || st.Uid != uint32(os.Getuid()) || info.Mode().Perm() != 0700 {
		dir.Close()
		return nil, fmt.Errorf("recovery directory must be owned and private")
	}
	return dir, nil
}

func readMarker(path string) (marker, error) {
	var m marker
	dir, err := openMarkerDirectory(path)
	if err != nil {
		return m, err
	}
	defer dir.Close()
	fd, err := unix.Openat(int(dir.Fd()), filepath.Base(path), unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return m, err
	}
	f := os.NewFile(uintptr(fd), path)
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return m, err
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || st.Uid != uint32(os.Getuid()) || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Size() > 4096 {
		return m, fmt.Errorf("invalid recovery state file")
	}
	data, err := io.ReadAll(io.LimitReader(f, 4097))
	if err != nil {
		return m, err
	}
	if len(data) > 4096 {
		return m, fmt.Errorf("recovery state exceeds limit")
	}
	if err = json.Unmarshal(data, &m); err != nil {
		return m, err
	}
	if m.Identity.UID != uint32(os.Getuid()) || m.Identity.Session == "" || m.Identity.Compositor == "" || (m.Intent != 0 && m.Intent != m.Generation) || m.ConfirmedUnlock > m.Generation || (m.Intent != 0 && m.Intent <= m.ConfirmedUnlock) {
		return m, fmt.Errorf("invalid recovery state")
	}
	return m, nil
}

func writeMarker(path string, m marker) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	dir, err := openMarkerDirectory(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	name := ".state-" + rand.Text()
	fd, err := unix.Openat(int(dir.Fd()), name, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0600)
	if err != nil {
		return err
	}
	defer unix.Unlinkat(int(dir.Fd()), name, 0)
	f := os.NewFile(uintptr(fd), name)
	defer f.Close()
	if _, err = f.Write(data); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = unix.Renameat(int(dir.Fd()), name, int(dir.Fd()), filepath.Base(path)); err != nil {
		return err
	}
	return dir.Sync()
}

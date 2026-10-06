package main

import (
	"fmt"
	"os"
	"sync"
	"syscall"
	"time"
)

var geteuid = os.Geteuid

type logger struct {
	mu   sync.Mutex
	path string
	f    *os.File
}

// openLog creates or appends the log as a private regular file (0600,
// O_NOFOLLOW); an unsafe desired path falls back to a temp file so the real
// path is what every screen and summary shows.
func openLog(desired string) (*logger, error) {
	fd, err := syscall.Open(desired,
		syscall.O_WRONLY|syscall.O_CREAT|syscall.O_APPEND|syscall.O_NOFOLLOW, 0o600)
	if err == nil {
		return &logger{path: desired, f: os.NewFile(uintptr(fd), desired)}, nil
	}
	f, terr := os.CreateTemp("", "sysc-lock-installer-*.log")
	if terr != nil {
		return nil, terr
	}
	return &logger{path: f.Name(), f: f}, nil
}

func (l *logger) Printf(format string, v ...any) {
	if l == nil || l.f == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	fmt.Fprintf(l.f, "[%s] %s\n", time.Now().Format("15:04:05"), fmt.Sprintf(format, v...))
	l.f.Sync()
}

func (l *logger) Path() string {
	if l == nil {
		return ""
	}
	return l.path
}

func (l *logger) Close() error {
	if l == nil || l.f == nil {
		return nil
	}
	return l.f.Close()
}

func logHeader(l *logger, mode, prefix, source, repo string) {
	if l == nil || l.f == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	fmt.Fprintf(l.f, "\n=== sysc-lock Installer Log ===\n")
	fmt.Fprintf(l.f, "Started: %s\n", time.Now().Format("2006-01-02 15:04:05"))
	fmt.Fprintf(l.f, "Mode: %s\n", mode)
	fmt.Fprintf(l.f, "Prefix: %s\n", prefix)
	fmt.Fprintf(l.f, "Source: %s\n", source)
	fmt.Fprintf(l.f, "Repo: %s\n", repo)
	fmt.Fprintf(l.f, "EUID: %d\n\n", geteuid())
}

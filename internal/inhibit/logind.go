package inhibit

import (
	"fmt"
	"syscall"

	"github.com/godbus/dbus/v5"
)

// Logind implements Backend with systemd-logind Inhibit("sleep", ..., "block").
// logind's reply is a UNIX fd: while it stays open the sleep is blocked;
// closing it releases. No release method call exists — fd lifetime is the API.
type Logind struct {
	conn *dbus.Conn
	fd   int
}

func NewLogind() (*Logind, error) {
	conn, err := dbus.SessionBus()
	if err != nil {
		return nil, fmt.Errorf("session bus: %w", err)
	}
	obj := conn.Object("org.freedesktop.login1", "/org/freedesktop/login1")
	var fd dbus.UnixFD
	if err := obj.Call("org.freedesktop.login1.Manager.Inhibit", 0,
		"sleep", "sysc-lock", "locking session", "block").Store(&fd); err != nil {
		conn.Close()
		return nil, fmt.Errorf("inhibit: %w", err)
	}
	return &Logind{conn: conn, fd: int(fd)}, nil
}

// Release drops the inhibitor (and the bus connection). Called after the
// compositor confirms "locked"; process exit also drops it via fd close.
func (l *Logind) Release() {
	if l == nil {
		return
	}
	if l.fd >= 0 {
		_ = syscall.Close(l.fd)
		l.fd = -1
	}
	if l.conn != nil {
		l.conn.Close()
		l.conn = nil
	}
}

package inhibit

import (
	"fmt"
	"syscall"

	"github.com/godbus/dbus/v5"
)

// Logind implements Backend with systemd-logind. Inhibit("sleep", ..., "block")
// replies with a UNIX fd: while it stays open sleep is blocked; closing it
// releases. No release method exists — fd lifetime is the API.
type Logind struct {
	conn *dbus.Conn
	fd   int
}

// NewLogind connects to the session bus (dial failure ⇒ caller exits 4:
// refusing to lock without a sleep guard).
func NewLogind() (*Logind, error) {
	conn, err := dbus.SessionBusPrivate()
	if err != nil {
		return nil, fmt.Errorf("session bus: %w", err)
	}
	if err := conn.Hello(); err != nil {
		conn.Close()
		return nil, fmt.Errorf("hello: %w", err)
	}
	return &Logind{conn: conn, fd: -1}, nil
}

func (l *Logind) Inhibit(what, who, why, mode string) error {
	obj := l.conn.Object("org.freedesktop.login1", "/org/freedesktop/login1")
	var fd dbus.UnixFD
	if err := obj.Call("org.freedesktop.login1.Manager.Inhibit", 0,
		what, who, why, mode).Store(&fd); err != nil {
		return fmt.Errorf("inhibit: %w", err)
	}
	l.fd = int(fd)
	return nil
}

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

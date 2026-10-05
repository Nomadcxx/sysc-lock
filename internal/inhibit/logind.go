// Package inhibit owns the persistent logind connection and finite sleep delays.
package inhibit

import (
	"context"
	"fmt"
	"sync"
	"syscall"
	"time"

	"github.com/godbus/dbus/v5"
)

// Logind keeps its connection across individual delay descriptor lifetimes.
type Logind struct{ conn *dbus.Conn }

// NewLogind connects and authenticates before issuing logind calls.
func NewLogind() (*Logind, error) {
	conn, err := dbus.SystemBusPrivate()
	if err != nil {
		return nil, fmt.Errorf("system bus: %w", err)
	}
	// godbus private connections are unauthenticated until Auth runs; calling
	// Hello first hangs forever waiting for a reply the bus won't accept.
	if err := conn.Auth(nil); err != nil {
		conn.Close()
		return nil, fmt.Errorf("auth: %w", err)
	}
	if err := conn.Hello(); err != nil {
		conn.Close()
		return nil, fmt.Errorf("hello: %w", err)
	}
	return &Logind{conn: conn}, nil
}

func (l *Logind) Release() {
	if l == nil {
		return
	}
	if l.conn != nil {
		l.conn.Close()
		l.conn = nil
	}
}

// Connection stays open when individual delay descriptors are released.
func (l *Logind) Connection() *dbus.Conn { return l.conn }
func (l *Logind) Delay() (func(), error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var fd dbus.UnixFD
	if err := l.conn.Object("org.freedesktop.login1", "/org/freedesktop/login1").CallWithContext(ctx, "org.freedesktop.login1.Manager.Inhibit", 0, "sleep", "sysc-lock", "lock before sleep", "delay").Store(&fd); err != nil {
		return nil, err
	}
	var once sync.Once
	return func() { once.Do(func() { _ = syscall.Close(int(fd)) }) }, nil
}
func (l *Logind) SleepDelayLimit() (time.Duration, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var value dbus.Variant
	if err := l.conn.Object("org.freedesktop.login1", "/org/freedesktop/login1").CallWithContext(ctx, "org.freedesktop.DBus.Properties.Get", 0, "org.freedesktop.login1.Manager", "InhibitDelayMaxUS").Store(&value); err != nil {
		return 0, err
	}
	us, ok := value.Value().(uint64)
	if !ok {
		return 0, fmt.Errorf("invalid logind delay type")
	}
	return delayLimit(us)
}
func delayLimit(us uint64) (time.Duration, error) {
	if us == 0 || us > uint64(time.Minute/time.Microsecond) {
		return 0, fmt.Errorf("invalid logind delay limit")
	}
	return time.Duration(us) * time.Microsecond, nil
}
func (l *Logind) SetLocked(path dbus.ObjectPath, locked bool) error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	return l.conn.Object("org.freedesktop.login1", path).CallWithContext(ctx, "org.freedesktop.login1.Session.SetLockedHint", 0, locked).Err
}

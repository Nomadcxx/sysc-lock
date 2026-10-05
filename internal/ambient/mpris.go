package ambient

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/godbus/dbus/v5"
)

const mprisPrefix = "org.mpris.MediaPlayer2."

// Bus is the slice of D-Bus the collector needs; tests feed a fake.
type Bus interface {
	Names() []string
	PlaybackStatus(dest string) (string, error)
}

// ReadMedia returns Playing if any MPRIS player is playing, else the first
// Paused or Stopped status seen, else "". Names that fail to answer are
// skipped so one broken player cannot hide a playing one.
func ReadMedia(b Bus) string {
	fallback := ""
	for _, name := range b.Names() {
		if !strings.HasPrefix(name, mprisPrefix) {
			continue
		}
		status, err := b.PlaybackStatus(name)
		if err != nil {
			continue
		}
		switch status {
		case "Playing":
			return Playing
		case "Paused":
			if fallback == "" {
				fallback = Paused
			}
		case "Stopped":
			if fallback == "" {
				fallback = Stopped
			}
		}
	}
	return fallback
}

// dbusBus is Bus over a private session-bus connection.
type dbusBus struct{ conn *dbus.Conn }

func (b dbusBus) Names() []string {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var names []string
	if err := b.conn.BusObject().CallWithContext(ctx, "org.freedesktop.DBus.ListNames", 0).Store(&names); err != nil {
		return nil
	}
	return names
}

func (b dbusBus) PlaybackStatus(dest string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var v dbus.Variant
	err := b.conn.Object(dest, dbus.ObjectPath("/org/mpris/MediaPlayer2")).CallWithContext(
		ctx, "org.freedesktop.DBus.Properties.Get", 0,
		"org.mpris.MediaPlayer2.Player", "PlaybackStatus").Store(&v)
	if err != nil {
		return "", err
	}
	s, ok := v.Value().(string)
	if !ok {
		return "", fmt.Errorf("PlaybackStatus not a string: %T", v.Value())
	}
	return s, nil
}

// readMedia polls the session bus once. A fresh private connection per tick
// keeps no state across polls and dies with the child process.
func readMedia() string {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return ""
	}
	defer conn.Close()
	return ReadMedia(dbusBus{conn})
}

package ambient

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/godbus/dbus/v5"
)

const mprisPrefix = "org.mpris.MediaPlayer2."

// Bus is the slice of D-Bus the collector needs; tests feed a fake.
type Bus interface {
	Names() []string
	PlaybackStatus(dest string) (string, error)
	Metadata(dest string) (map[string]dbus.Variant, error)
}

// ReadMedia preserves the status-only collector entry point.
func ReadMedia(b Bus) string { return ReadNowPlaying(b).Media }

// ReadNowPlaying selects the first playing player in sorted bus-name order.
// Status survives missing/malformed metadata; no fields come from another player.
func ReadNowPlaying(b Bus) Snapshot {
	result := Snapshot{}
	names := slices.Clone(b.Names())
	slices.Sort(names)
	for _, name := range names {
		if !strings.HasPrefix(name, mprisPrefix) {
			continue
		}
		status, err := b.PlaybackStatus(name)
		if err != nil {
			continue
		}
		switch status {
		case "Playing":
			result.Media = Playing
			metadata, err := b.Metadata(name)
			if err != nil {
				return result
			}
			if title, ok := metadata["xesam:title"].Value().(string); ok {
				result.Title = cleanMetadata(title)
			}
			if artists, ok := metadata["xesam:artist"].Value().([]string); ok {
				for _, artist := range artists {
					artist = cleanMetadata(artist)
					if artist == "" {
						continue
					}
					if result.Artist != "" {
						result.Artist += ", "
					}
					result.Artist = cleanMetadata(result.Artist + artist)
					if len([]rune(result.Artist)) == 128 {
						break
					}
				}
			}
			return result
		case "Paused":
			if result.Media == "" {
				result.Media = Paused
			}
		case "Stopped":
			if result.Media == "" {
				result.Media = Stopped
			}
		}
	}
	return result
}

// dbusBus is Bus over a private session-bus connection.
type dbusBus struct {
	conn *dbus.Conn
	ctx  context.Context
}

func (b dbusBus) Names() []string {
	var names []string
	if err := b.conn.BusObject().CallWithContext(b.ctx, "org.freedesktop.DBus.ListNames", 0).Store(&names); err != nil {
		return nil
	}
	return names
}

func (b dbusBus) PlaybackStatus(dest string) (string, error) {
	var v dbus.Variant
	err := b.conn.Object(dest, dbus.ObjectPath("/org/mpris/MediaPlayer2")).CallWithContext(
		b.ctx, "org.freedesktop.DBus.Properties.Get", 0,
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

func (b dbusBus) Metadata(dest string) (map[string]dbus.Variant, error) {
	var v dbus.Variant
	err := b.conn.Object(dest, dbus.ObjectPath("/org/mpris/MediaPlayer2")).CallWithContext(
		b.ctx, "org.freedesktop.DBus.Properties.Get", 0,
		"org.mpris.MediaPlayer2.Player", "Metadata").Store(&v)
	if err != nil {
		return nil, err
	}
	metadata, ok := v.Value().(map[string]dbus.Variant)
	if !ok {
		return nil, fmt.Errorf("Metadata not a property map: %T", v.Value())
	}
	return metadata, nil
}

// readMedia polls the session bus once. A fresh private connection per tick
// keeps no state across polls and dies with the child process.
func readMedia() Snapshot {
	// Leave room for battery/link publication within the one-second cadence.
	ctx, cancel := context.WithTimeout(context.Background(), 750*time.Millisecond)
	defer cancel()
	conn, err := dbus.ConnectSessionBus(dbus.WithContext(ctx))
	if err != nil {
		return Snapshot{}
	}
	defer conn.Close()
	return ReadNowPlaying(dbusBus{conn, ctx})
}

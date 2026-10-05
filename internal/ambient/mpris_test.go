package ambient

import (
	"errors"
	"testing"
)

type fakeBus struct {
	names []string
	props map[string]string
	errs  map[string]error
}

func (f fakeBus) Names() []string { return f.names }

func (f fakeBus) PlaybackStatus(dest string) (string, error) {
	if err := f.errs[dest]; err != nil {
		return "", err
	}
	return f.props[dest], nil
}

func TestReadMediaPlaying(t *testing.T) {
	b := fakeBus{
		names: []string{"org.mpris.MediaPlayer2.foo"},
		props: map[string]string{"org.mpris.MediaPlayer2.foo": "Playing"},
	}
	if got := ReadMedia(b); got != Playing {
		t.Fatalf("got %q", got)
	}
}

func TestReadMediaPaused(t *testing.T) {
	b := fakeBus{
		names: []string{"org.mpris.MediaPlayer2.foo"},
		props: map[string]string{"org.mpris.MediaPlayer2.foo": "Paused"},
	}
	if got := ReadMedia(b); got != Paused {
		t.Fatalf("got %q", got)
	}
}

func TestReadMediaNoPlayers(t *testing.T) {
	b := fakeBus{names: []string{"org.freedesktop.Notifications"}}
	if got := ReadMedia(b); got != "" {
		t.Fatalf("got %q", got)
	}
}

func TestReadMediaErrorSkippedPlayingWins(t *testing.T) {
	b := fakeBus{
		names: []string{"org.mpris.MediaPlayer2.broken", "org.mpris.MediaPlayer2.good"},
		props: map[string]string{"org.mpris.MediaPlayer2.good": "Playing"},
		errs:  map[string]error{"org.mpris.MediaPlayer2.broken": errors.New("no reply")},
	}
	if got := ReadMedia(b); got != Playing {
		t.Fatalf("got %q", got)
	}
}

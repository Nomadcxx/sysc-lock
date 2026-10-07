package ambient

import (
	"bufio"
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
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

func TestReadMediaStableFallback(t *testing.T) {
	b := fakeBus{
		names: []string{"org.mpris.MediaPlayer2.z", "org.mpris.MediaPlayer2.a"},
		props: map[string]string{"org.mpris.MediaPlayer2.z": "Stopped", "org.mpris.MediaPlayer2.a": "Paused"},
	}
	if got := ReadMedia(b); got != Paused {
		t.Fatalf("sorted fallback = %q, want paused", got)
	}
	if b.names[0] != "org.mpris.MediaPlayer2.z" {
		t.Fatal("changed bus-owned name order")
	}
}

type metadataBus struct {
	fakeBus
	metadata       map[string]map[string]dbus.Variant
	metadataErrors map[string]error
	queried        []string
}

func (b *metadataBus) Metadata(dest string) (map[string]dbus.Variant, error) {
	b.queried = append(b.queried, dest)
	return b.metadata[dest], b.metadataErrors[dest]
}
func TestReadNowPlayingFetchesChosenPlayersMetadata(t *testing.T) {
	const a = "org.mpris.MediaPlayer2.a"
	const z = "org.mpris.MediaPlayer2.z"
	b := &metadataBus{fakeBus: fakeBus{names: []string{z, "org.mpris.MediaPlayer2.0broken", a}, props: map[string]string{z: "Playing", a: "Playing"}, errs: map[string]error{"org.mpris.MediaPlayer2.0broken": errors.New("gone")}}, metadata: map[string]map[string]dbus.Variant{
		a: {"xesam:title": dbus.MakeVariant("A title"), "xesam:artist": dbus.MakeVariant([]string{"A artist"})},
		z: {"xesam:title": dbus.MakeVariant("Z title"), "xesam:artist": dbus.MakeVariant([]string{"Z artist"})},
	}}
	if got := ReadNowPlaying(b); got.Media != Playing || got.Title != "A title" || got.Artist != "A artist" {
		t.Fatal("metadata was not from the selected playing player")
	}
	if len(b.queried) != 1 || b.queried[0] != a {
		t.Fatalf("metadata query = %v, want only sorted playing player", b.queried)
	}
}
func (f fakeBus) Metadata(string) (map[string]dbus.Variant, error) { return nil, nil }

func TestReadNowPlayingMetadata(t *testing.T) {
	const player = "org.mpris.MediaPlayer2.a"
	cases := []struct {
		name          string
		metadata      map[string]dbus.Variant
		err           error
		title, artist string
	}{
		{"text", map[string]dbus.Variant{"xesam:title": dbus.MakeVariant(" Title\n Here\u202e\x00 "), "xesam:artist": dbus.MakeVariant([]string{" First\t Artist ", "", "Second\u200d"})}, nil, "Title Here", "First Artist, Second"},
		{"missing", nil, nil, "", ""},
		{"gone", nil, errors.New("gone"), "", ""},
		{"malformed title", map[string]dbus.Variant{"xesam:title": dbus.MakeVariant(int32(42)), "xesam:artist": dbus.MakeVariant([]string{"Artist"})}, nil, "", "Artist"},
		{"malformed artist", map[string]dbus.Variant{"xesam:title": dbus.MakeVariant("Title"), "xesam:artist": dbus.MakeVariant("Artist")}, nil, "Title", ""},
		{"bound", map[string]dbus.Variant{"xesam:title": dbus.MakeVariant(strings.Repeat("é", 140)), "xesam:artist": dbus.MakeVariant([]string{strings.Repeat("界", 130), "Later"})}, nil, strings.Repeat("é", 128), strings.Repeat("界", 128)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := &metadataBus{fakeBus: fakeBus{names: []string{player}, props: map[string]string{player: "Playing"}}, metadata: map[string]map[string]dbus.Variant{player: tc.metadata}, metadataErrors: map[string]error{player: tc.err}}
			got := ReadNowPlaying(b)
			if got.Media != Playing || got.Title != tc.title || got.Artist != tc.artist {
				t.Fatal("status or sanitized metadata differs")
			}
			b.props[player] = "Paused"
			got = ReadNowPlaying(b)
			if got.Media != Paused || got.Title != "" || got.Artist != "" || len(b.queried) != 1 {
				t.Fatal("paused player retained or fetched metadata")
			}
			delete(b.props, player)
			got = ReadNowPlaying(b)
			if got.Media != "" || got.Title != "" || got.Artist != "" {
				t.Fatal("removed player retained metadata")
			}
		})
	}
}

// delayedProperties is a real private-bus player that outlives the poll budget.
type delayedProperties struct{ status string }

func (p delayedProperties) Get(iface, property string) (dbus.Variant, *dbus.Error) {
	if iface != "org.mpris.MediaPlayer2.Player" || property != "PlaybackStatus" {
		return dbus.MakeVariant(""), dbus.NewError("org.freedesktop.DBus.Error.UnknownProperty", nil)
	}
	time.Sleep(500 * time.Millisecond)
	return dbus.MakeVariant(p.status), nil
}
func TestReadMediaSharesOnePollDeadline(t *testing.T) {
	if _, err := exec.LookPath("dbus-daemon"); err != nil {
		t.Skip("dbus-daemon unavailable")
	}
	cmd := exec.Command("dbus-daemon", "--session", "--nofork", "--print-address=1")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	address, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	address = strings.TrimSpace(address)
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", address)
	for _, player := range []struct{ name, status string }{{"a", "Paused"}, {"b", "Playing"}} {
		conn, err := dbus.Connect(address)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		if err := conn.Export(delayedProperties{player.status}, "/org/mpris/MediaPlayer2", "org.freedesktop.DBus.Properties"); err != nil {
			t.Fatal(err)
		}
		reply, err := conn.RequestName(mprisPrefix+player.name, dbus.NameFlagDoNotQueue)
		if err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
			t.Fatalf("request name: %v, %v", reply, err)
		}
	}
	start := time.Now()
	got := readMedia()
	elapsed := time.Since(start)
	if got.Media != Paused || got.Title != "" || got.Artist != "" {
		t.Fatalf("shared deadline did not retain only completed status: %q", got.Media)
	}
	if elapsed > 1500*time.Millisecond {
		t.Fatalf("poll exceeded shared budget: %v", elapsed)
	}
}

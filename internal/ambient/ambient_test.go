package ambient

import "testing"

func TestLineOmitsUnknownsAndPlayingOnly(t *testing.T) {
	pct, temp := 82, 18.4
	s := Snapshot{BatteryPct: &pct, Link: "wifi", Media: "playing", Temp: &temp, Unit: "C"}
	if got := s.Line(40); got != "[████████░░] 82% • Wi-Fi • playing • 18°" {
		t.Fatalf("got %q", got)
	}
	s.Media = "paused"
	if got := s.Line(40); got != "[████████░░] 82% • Wi-Fi • 18°" {
		t.Fatalf("paused must omit, got %q", got)
	}
	s.Media = "stopped"
	if got := s.Line(40); got != "[████████░░] 82% • Wi-Fi • 18°" {
		t.Fatalf("stopped must omit, got %q", got)
	}
	if got := (Snapshot{}).Line(40); got != "" {
		t.Fatalf("empty snapshot is no row, got %q", got)
	}
}

func TestLineDropsFromTheRightUntilItFits(t *testing.T) {
	pct, temp := 82, 18.0
	s := Snapshot{BatteryPct: &pct, Link: "wired", Media: "playing", Temp: &temp}
	if got := s.Line(len([]rune("[████████░░] 82% • Wired • playing"))); got != "[████████░░] 82% • Wired • playing" {
		t.Fatalf("drop weather first, got %q", got)
	}
	if got := s.Line(1); got != "" {
		t.Fatalf("nothing fits: %q", got)
	}
}

func TestLineFitsNowPlaying(t *testing.T) {
	pct, temp := 82, 18.0
	prefix := "[████████░░] 82% • Wi-Fi"
	cases := []struct {
		name     string
		snapshot Snapshot
		width    int
		want     string
	}{
		{"exact", Snapshot{Media: Playing, Title: "Song", Artist: "Artist"}, 13, "Song - Artist"},
		{"drop artist", Snapshot{Media: Playing, Title: "Song", Artist: "Artist"}, 12, "Song"},
		{"title ellipsis", Snapshot{Media: Playing, Title: "Long title", Artist: "Artist"}, 9, "Long t..."},
		{"unicode", Snapshot{Media: Playing, Title: "αβγδεζη"}, 6, "αβγ..."},
		{"too narrow", Snapshot{Media: Playing, Title: "Song"}, 3, ""},
		{"zero", Snapshot{Media: Playing, Title: "Song"}, 0, ""},
		{"negative", Snapshot{Media: Playing, Title: "Song"}, -1, ""},
		{"title only", Snapshot{Media: Playing, Title: "Song"}, 4, "Song"},
		{"fallback", Snapshot{Media: Playing, Artist: "Artist"}, 7, Playing},
		{"paused", Snapshot{Media: Paused, Title: "Song", Artist: "Artist"}, 40, ""},
		{"stopped", Snapshot{Media: Stopped, Title: "Song", Artist: "Artist"}, 40, ""},
		{"priority", Snapshot{Media: Playing, Title: "Very long song", Artist: "Artist", BatteryPct: &pct, Link: LinkWifi, Temp: &temp}, len([]rune(prefix)) + 3 + 8, prefix + " • Very ..."},
		{"no media room", Snapshot{Media: Playing, Title: "Song", BatteryPct: &pct, Link: LinkWifi}, len([]rune(prefix)), prefix},
		{"safe text", Snapshot{Media: Playing, Title: " A\nB\u202e ", Artist: " C\tD "}, 40, "A B - C D"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.snapshot.Line(tc.width)
			if got != tc.want {
				t.Fatalf("line = %q, want %q", got, tc.want)
			}
			if len([]rune(got)) > max(0, tc.width) {
				t.Fatal("line exceeds width")
			}
		})
	}
}

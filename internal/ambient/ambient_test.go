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

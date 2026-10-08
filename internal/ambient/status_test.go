package ambient

import (
	"reflect"
	"testing"
	"unicode/utf8"
)

func latinOnly(r rune) bool { return r < 0x2E80 }

func pctOf(n int) *int { return &n }

func TestStatusCorner(t *testing.T) {
	temp := 18.4
	w := utf8.RuneCountInString
	discharging := Snapshot{BatteryPct: pctOf(63), Power: PowerDischarging, Link: LinkWifi, Temp: &temp}
	charging := Snapshot{BatteryPct: pctOf(63), Power: PowerCharging, Link: LinkWifi}
	cases := []struct {
		name  string
		s     Snapshot
		width int
		want  string
	}{
		{"everything", discharging, 40, "18° • Wi-Fi • [██████░░░░] 63%"},
		{"charging", charging, 40, "Wi-Fi • [██████░░░░] 63% charging"},
		{"full", Snapshot{BatteryPct: pctOf(100), Power: PowerFull, Link: LinkWired}, 40, "Wired • [██████████] 100% full"},
		{"plugged", Snapshot{BatteryPct: pctOf(80), Power: PowerPlugged, Link: LinkWired}, 40, "Wired • [████████░░] 80% plugged"},
		{"low", Snapshot{BatteryPct: pctOf(17), Power: PowerDischarging, Link: LinkWifi}, 40, "Wi-Fi • [██░░░░░░░░] 17% LOW"},
		{"critical", Snapshot{BatteryPct: pctOf(6), Power: PowerDischarging, Link: LinkWifi}, 40, "Wi-Fi • [█░░░░░░░░░] 6% LOW"},
		{"unknown state is never LOW", Snapshot{BatteryPct: pctOf(5), Link: LinkWifi}, 40, "Wi-Fi • [█░░░░░░░░░] 5%"},
		{"offline is stated", Snapshot{BatteryPct: pctOf(63), Power: PowerDischarging}, 40, "Offline • [██████░░░░] 63%"},
		{"desktop", Snapshot{Link: LinkWired}, 40, "Wired"},
		{"clamped", Snapshot{BatteryPct: pctOf(105), Power: PowerFull, Link: LinkWired}, 40, "Wired • [██████████] 100% full"},
		{"temperature drops first", discharging, w("Wi-Fi • [██████░░░░] 63%"), "Wi-Fi • [██████░░░░] 63%"},
		{"five cells", discharging, w("Wi-Fi • [███░░] 63%"), "Wi-Fi • [███░░] 63%"},
		{"no gauge, short word", charging, w("Wi-Fi • 63% +"), "Wi-Fi • 63% +"},
		{"battery alone", charging, w("63% +"), "63% +"},
		{"nothing fits", charging, 2, ""},
		{"zero", charging, 0, ""},
		{"negative", charging, -1, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Plain(tc.s.Status(tc.width, 0, latinOnly).Corner)
			if got != tc.want {
				t.Fatalf("corner = %q, want %q", got, tc.want)
			}
			if w(got) > max(0, tc.width) {
				t.Fatalf("corner %q exceeds %d runes", got, tc.width)
			}
		})
	}
}

func TestStatusCornerTones(t *testing.T) {
	low := Snapshot{BatteryPct: pctOf(17), Power: PowerDischarging}.Status(40, 0, latinOnly).Corner
	want := []Span{{"Offline", ToneWarn}, {Sep, ToneDim}, {"[██░░░░░░░░]", ToneWarn}, {" ", ToneMuted}, {"17%", ToneWarn}, {" LOW", ToneWarn}}
	if !reflect.DeepEqual(low, want) {
		t.Fatalf("low tones:\n got %+v\nwant %+v", low, want)
	}
	crit := Snapshot{BatteryPct: pctOf(6), Power: PowerDischarging, Link: LinkWifi}.Status(40, 0, latinOnly).Corner
	want = []Span{{"Wi-Fi", ToneMuted}, {Sep, ToneDim}, {"[█░░░░░░░░░]", ToneDanger}, {" ", ToneMuted}, {"6%", ToneDanger}, {" LOW", ToneDanger}}
	if !reflect.DeepEqual(crit, want) {
		t.Fatalf("critical tones:\n got %+v\nwant %+v", crit, want)
	}
	chg := Snapshot{BatteryPct: pctOf(63), Power: PowerCharging, Link: LinkWifi}.Status(40, 0, latinOnly).Corner
	want = []Span{{"Wi-Fi", ToneMuted}, {Sep, ToneDim}, {"[██████░░░░]", ToneAccent}, {" ", ToneMuted}, {"63%", ToneInk}, {" charging", ToneAccent}}
	if !reflect.DeepEqual(chg, want) {
		t.Fatalf("charging tones:\n got %+v\nwant %+v", chg, want)
	}
}

func TestStatusAlert(t *testing.T) {
	cases := []struct {
		s    Snapshot
		want string
	}{
		{Snapshot{BatteryPct: pctOf(6), Power: PowerDischarging}, "Battery at 6%. Connect power."},
		{Snapshot{BatteryPct: pctOf(10), Power: PowerDischarging}, "Battery at 10%. Connect power."},
		{Snapshot{BatteryPct: pctOf(11), Power: PowerDischarging}, ""},
		{Snapshot{BatteryPct: pctOf(6), Power: PowerCharging}, ""},
		{Snapshot{BatteryPct: pctOf(6)}, ""},
		{Snapshot{}, ""},
	}
	for _, tc := range cases {
		if got := tc.s.Status(40, 40, latinOnly).Alert; got != tc.want {
			t.Fatalf("%+v: alert %q, want %q", tc.s, got, tc.want)
		}
	}
}

func TestStatusCaption(t *testing.T) {
	w := utf8.RuneCountInString
	city := Snapshot{Media: Playing, Title: "Midnight City", Artist: "M83"}
	cases := []struct {
		name  string
		s     Snapshot
		width int
		want  string
	}{
		{"playing", city, 40, "♪ Midnight City — M83"},
		{"artist drops first", city, w("♪ Midnight City"), "♪ Midnight City"},
		{"title shortened", city, 8, "♪ Midni…"},
		{"too narrow", city, 5, ""},
		{"zero", city, 0, ""},
		{"hyphenated title", Snapshot{Media: Playing, Title: "Svefn-g-englar", Artist: "Sigur Rós"}, 40, "♪ Svefn-g-englar — Sigur Rós"},
		{"uncovered title yields to artist", Snapshot{Media: Playing, Title: "夜に駆ける", Artist: "YOASOBI"}, 40, "♪ YOASOBI"},
		{"mostly covered title", Snapshot{Media: Playing, Title: "Song 🎵", Artist: "A"}, 40, "♪ Song 🎵 — A"},
		{"nothing drawable", Snapshot{Media: Playing, Title: "夜に駆ける", Artist: "ヨアソビ"}, 40, "♪ playing"},
		{"no metadata", Snapshot{Media: Playing}, 40, "♪ playing"},
		{"artist only", Snapshot{Media: Playing, Artist: "M83"}, 40, "♪ M83"},
		{"paused", Snapshot{Media: Paused, Title: "Midnight City", Artist: "M83"}, 40, "paused · Midnight City"},
		{"paused, narrow", Snapshot{Media: Paused, Title: "Midnight City"}, 14, "paused · Midn…"},
		{"paused, no title", Snapshot{Media: Paused}, 40, ""},
		{"stopped", Snapshot{Media: Stopped, Title: "x"}, 40, ""},
		{"absent", Snapshot{}, 40, ""},
		{"safe text", Snapshot{Media: Playing, Title: " A\nB‮ ", Artist: " C\tD "}, 40, "♪ A B — C D"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Plain(tc.s.Status(0, tc.width, latinOnly).Caption)
			if got != tc.want {
				t.Fatalf("caption = %q, want %q", got, tc.want)
			}
			if w(got) > max(0, tc.width) {
				t.Fatalf("caption %q exceeds %d runes", got, tc.width)
			}
		})
	}
}

func TestStatusCaptionTones(t *testing.T) {
	got := Snapshot{Media: Playing, Title: "Midnight City", Artist: "M83"}.Status(0, 40, latinOnly).Caption
	want := []Span{{"♪ ", ToneAccent}, {"Midnight City", ToneInk}, {" — ", ToneDim}, {"M83", ToneMuted}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("playing tones:\n got %+v\nwant %+v", got, want)
	}
	got = Snapshot{Media: Paused, Title: "Midnight City"}.Status(0, 40, latinOnly).Caption
	want = []Span{{"paused · ", ToneDim}, {"Midnight City", ToneDim}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("paused tones:\n got %+v\nwant %+v", got, want)
	}
}

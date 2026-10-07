package art

import (
	"image"
	"reflect"
	"testing"
	"time"
)

func TestStylesHaveEveryClockGlyph(t *testing.T) {
	for _, name := range []string{"kompaktblk", "phm_blocky_reverse", "phmvga", "phm_slanted"} {
		s := Lookup(name)
		if s.Name != name || s.Rows == 0 || s.Plain() {
			t.Fatalf("%s: %+v", name, s)
		}
		for _, r := range "0123456789: APM" {
			g := s.glyphs[r]
			if len(g) != s.Rows {
				t.Fatalf("%s %q has %d rows, want %d", name, r, len(g), s.Rows)
			}
			for _, row := range g {
				for _, c := range row {
					if !Supported(c) {
						t.Fatalf("%s %q uses unsupported %U", name, r, c)
					}
				}
			}
		}
	}
	if Lookup("phmvga").Rows != 2 || Lookup("phm_slanted").Rows != 6 {
		t.Fatal("row counts changed the layout contract")
	}
}

func TestUnknownStyleFallsBackAndPlainHasNoGlyphs(t *testing.T) {
	if got := Lookup("nope").Name; got != DefaultStyle {
		t.Fatal(got)
	}
	if p := Lookup(Plain); !p.Plain() || p.Name != Plain {
		t.Fatal(p)
	}
	if !reflect.DeepEqual(Names(), []string{"kompaktblk", "phm_blocky_reverse", "phmvga", "phm_slanted", "plain"}) {
		t.Fatal(Names())
	}
}

func TestComposeJoinsGlyphsAtEqualWidth(t *testing.T) {
	rows := Lookup("kompaktblk").Compose("1:")
	if len(rows) != 3 {
		t.Fatal(rows)
	}
	for _, r := range rows {
		if len([]rune(r)) != Width(rows) {
			t.Fatalf("ragged rows %q", rows)
		}
	}
	if got := Lookup(Plain).Compose("1:"); got != nil {
		t.Fatal("plain composes no art", got)
	}
}

func TestRectsHalfBlocksAreGapless(t *testing.T) {
	got := Rects([]string{"▀▀", "▄▄"}, image.Pt(10, 20), 8, 16, -1)
	want := []image.Rectangle{
		image.Rect(10, 20, 18, 28), image.Rect(18, 20, 26, 28),
		image.Rect(10, 44, 18, 52), image.Rect(18, 44, 26, 52),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestRectsLimitCountsDrawnCellsInReadingOrder(t *testing.T) {
	rows := []string{"█ █", "█  "}
	if Total(rows) != 3 {
		t.Fatal(Total(rows))
	}
	if n := len(Rects(rows, image.Point{}, 4, 8, 2)); n != 2 {
		t.Fatal(n)
	}
	if n := len(Rects(rows, image.Point{}, 4, 8, 0)); n != 0 {
		t.Fatal(n)
	}
	if n := len(Rects(rows, image.Point{}, 4, 8, -1)); n != 3 {
		t.Fatal(n)
	}
	if got := Rects(nil, image.Point{}, 4, 8, -1); got != nil {
		t.Fatal(got)
	}
}

func TestUnsupportedRuneDrawsNothing(t *testing.T) {
	if Supported('☃') || len(Rects([]string{"☃"}, image.Point{}, 4, 8, -1)) != 0 {
		t.Fatal("unsupported glyph must not draw")
	}
}

func TestPickHonorsConfiguredPlain(t *testing.T) {
	style, rows, cw := Pick(Plain, "12:59:59 PM", 1920, 1080)
	if !style.Plain() || style.Name != Plain || rows != nil || cw != 0 {
		t.Fatal(style, rows, cw)
	}
}

func TestPickFitsWidestClockOrFallsBack(t *testing.T) {
	const widest = "12:59:59 PM"
	style, rows, cw := Pick("kompaktblk", widest, 1920, 1080)
	if style.Name != "kompaktblk" || cw < MinCell || len(rows) != 3 {
		t.Fatal(style, cw)
	}
	if Width(rows)*cw > 1920*9/10 || len(rows)*2*cw > 1080/5 {
		t.Fatal("clock exceeds its budget", Width(rows)*cw, len(rows)*2*cw)
	}
	for _, size := range [][2]int{{320, 240}, {120, 80}, {3440, 1440}} {
		style, rows, cw := Pick("phm_blocky_reverse", widest, size[0], size[1])
		if style.Plain() {
			if rows != nil || cw != 0 {
				t.Fatal(size, rows, cw)
			}
			continue
		}
		if cw < MinCell || Width(rows)*cw > size[0]*9/10 {
			t.Fatal(size, style.Name, cw)
		}
	}
	if style, _, _ := Pick("kompaktblk", widest, 120, 80); !style.Plain() {
		t.Fatal("tiny output must fall back to plain")
	}
}

func TestPrintLimitIsCappedAtOneSecond(t *testing.T) {
	for _, c := range []struct {
		elapsed time.Duration
		total   int
		want    int
	}{
		{-time.Second, 100, 0}, {0, 100, 0}, {500 * time.Millisecond, 100, 50},
		{time.Second, 100, 100}, {time.Hour, 100, 100}, {time.Second, 0, 0},
		{250 * time.Millisecond, 1000, 250},
	} {
		if got := PrintLimit(c.elapsed, c.total); got != c.want {
			t.Fatalf("PrintLimit(%v,%d)=%d want %d", c.elapsed, c.total, got, c.want)
		}
	}
}

func TestJoltShiftsLeftThenRightThenRests(t *testing.T) {
	for _, c := range []struct {
		elapsed time.Duration
		want    int
	}{
		{-1, 0}, {0, -1}, {39 * time.Millisecond, -1}, {40 * time.Millisecond, 1},
		{79 * time.Millisecond, 1}, {80 * time.Millisecond, 0}, {JoltDuration, 0}, {time.Hour, 0},
	} {
		if got := Jolt(c.elapsed); got != c.want {
			t.Fatalf("Jolt(%v)=%d want %d", c.elapsed, got, c.want)
		}
	}
}

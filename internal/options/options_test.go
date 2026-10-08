package options

import "testing"

func TestMenuOpensCyclesAndCloses(t *testing.T) {
	o := New([]string{"none", "rain", "fire"}, []string{"nord", "eldritch"}, "none", "nord")
	if o.Open() {
		t.Fatal("menu must start closed")
	}
	if o.Press(Key{Right: true}) {
		t.Fatal("a closed menu must ignore arrows")
	}
	if o.Press(Key{F1: true}) || !o.Open() {
		t.Fatal("F1 must open the menu without changing the selection")
	}
	if !o.Press(Key{Right: true}) || o.Effect() != "rain" {
		t.Fatalf("Right must advance the effect, got %q", o.Effect())
	}
	o.Press(Key{Down: true})
	if !o.Press(Key{Right: true}) || o.Theme() != "eldritch" {
		t.Fatalf("Right on the theme row must advance the theme, got %q", o.Theme())
	}
	if !o.Press(Key{Left: true}) || o.Theme() != "nord" {
		t.Fatalf("Left must cycle back, got %q", o.Theme())
	}
	if o.Press(Key{Escape: true}) || o.Open() {
		t.Fatal("Escape must close the menu")
	}
	if o.Effect() != "rain" {
		t.Fatalf("closing must keep the selection, got %q", o.Effect())
	}
}

func TestSetFallsBackToFirstChoice(t *testing.T) {
	o := New([]string{"none", "rain"}, []string{"nord"}, "bogus", "bogus")
	if o.Effect() != "none" || o.Theme() != "nord" {
		t.Fatalf("unknown values must select the first choice, got %q/%q", o.Effect(), o.Theme())
	}
}

func TestReleasedKeysDoNothing(t *testing.T) {
	o := New([]string{"none", "rain"}, []string{"nord"}, "none", "nord")
	o.Press(Key{F1: true})
	if o.Press(Key{Released: true, Right: true}) {
		t.Fatal("key releases must not change the selection")
	}
	if o.Effect() != "none" {
		t.Fatalf("release moved the effect to %q", o.Effect())
	}
}

func TestHeaderCyclingAndTextEffectRows(t *testing.T) {
	o := New([]string{"none", "fire"}, []string{"nord"}, "none", "nord")
	o.SetArtwork([]string{"ascii_1", "ascii_2"}, []string{"none", "print", "pour"}, "ascii_1", "none")
	if !o.Press(Key{PageUp: true}) || o.Header() != "ascii_2" || o.Open() {
		t.Fatal("page-up must wrap headers without opening F1")
	}
	if o.Press(Key{PageDown: true, Released: true}) || o.Header() != "ascii_2" {
		t.Fatal("release changed header")
	}
	o.Press(Key{F1: true})
	for range 3 {
		o.Press(Key{Down: true})
	}
	if !o.Press(Key{Right: true}) || o.TextEffect() != "print" {
		t.Fatal("text row does not cycle")
	}
	o.Press(Key{Down: true})
	if o.Selected() != 0 {
		t.Fatal("four rows do not wrap")
	}
}

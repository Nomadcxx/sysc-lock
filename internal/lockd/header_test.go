package lockd

import (
	"bytes"
	"image"
	"testing"
	"time"

	"github.com/Nomadcxx/sysc-lock/internal/art"
	"github.com/Nomadcxx/sysc-lock/internal/config"
	"github.com/Nomadcxx/sysc-lock/internal/input"
	"github.com/Nomadcxx/sysc-lock/internal/render"
	"github.com/Nomadcxx/sysc-lock/internal/theme"
)

func TestHeaderChangesOnlyDecoration(t *testing.T) {
	now := time.Unix(600, 0)
	v := NewView(theme.Default(), "u", "h")
	v.Reduced = true
	s := Layout(1536, 864, 1, v.StyleName, v.clockText(now))
	if s.Header.Empty() || s.Header.Max.Y > s.ClockBox.Min.Y {
		t.Fatal("missing bounded header slot")
	}
	var previous []byte
	for _, h := range art.DefaultHeaders() {
		v.Header = h.Text
		fb := render.New(1536, 864)
		v.Render(fb, now)
		if previous != nil {
			if bytes.Equal(previous, fb.Pix) {
				t.Fatal("header did not change")
			}
			for y := s.Header.Max.Y; y < fb.Height; y++ {
				start := y * fb.Stride
				if !bytes.Equal(previous[start:start+fb.Stride], fb.Pix[start:start+fb.Stride]) {
					t.Fatal("header moved or changed the form")
				}
			}
		}
		previous = append([]byte(nil), fb.Pix...)
	}
}

func TestHeaderAnimationDeadlineAndCredentialFreeze(t *testing.T) {
	now := time.Unix(600, 0)
	v := NewView(theme.Default(), "u", "h")
	v.Header, v.TextEffect, v.TextPalette = art.DefaultHeaders()[0].Text, "blackhole", "eldritch"
	v.Entry = &input.Model{}
	v.Render(render.New(1536, 864), now)
	now = now.Add(2 * time.Second)
	if d := v.NextDeadline(now); d.Sub(now) > 50*time.Millisecond {
		t.Fatal("no header animation deadline")
	}
	for _, state := range []string{"reduced", "busy", "password", "power", "prompt"} {
		v.Reduced, v.Busy = state == "reduced", state == "busy"
		v.Entry.Clear()
		v.Powering, v.Prompt = "", ""
		if state == "password" {
			v.Entry.Pass = []rune("x")
		}
		if state == "power" {
			v.Powering = "Powering off"
		}
		if state == "prompt" {
			v.Prompt = "Response"
		}
		if d := v.NextDeadline(now); d.Sub(now) < 500*time.Millisecond {
			t.Fatalf("%s animates: %v", state, d)
		}
	}
}

func TestEveryHeaderTextEffectProducesVisibleInk(t *testing.T) {
	now := time.Unix(600, 0)
	for _, effect := range config.TextEffects() {
		if effect == "none" {
			continue
		}
		for _, h := range art.DefaultHeaders() {
			t.Run(effect+"/"+h.ID, func(t *testing.T) {
				v := NewView(theme.Default(), "u", "h")
				v.Header, v.TextEffect, v.TextPalette = h.Text, effect, "eldritch"
				visible := false
				for i := 0; i < 80; i++ {
					fb := render.New(520, 96)
					v.drawHeader(fb, fb.Bounds(), now.Add(time.Duration(i)*50*time.Millisecond))
					if v.headerRenderer == nil {
						t.Fatal("animation fell back")
					}
					for j := 0; j+3 < len(fb.Pix); j += 4 {
						if fb.Pix[j]|fb.Pix[j+1]|fb.Pix[j+2] != 0 {
							visible = true
							break
						}
					}
				}
				if !visible {
					t.Fatal("effect never drew artwork")
				}
			})
		}
	}
}

func TestOptionsHidesLockedCaptionAndClosingRestoresIt(t *testing.T) {
	now := time.Unix(600, 0)
	v := NewView(theme.Default(), "u", "h")
	v.Reduced = true
	s := Layout(1536, 864, 1, v.StyleName, v.clockText(now))
	frame := func() []byte {
		fb := render.New(1536, 864)
		v.Render(fb, now)
		var pix []byte
		for y := s.Rule.Min.Y; y < s.Rule.Max.Y; y++ {
			pix = append(pix, fb.Pix[y*fb.Stride+s.Rule.Min.X*4:y*fb.Stride+s.Rule.Max.X*4]...)
		}
		return pix
	}
	closed := frame()
	v.Options = &MenuView{Open: true, Progress: -1}
	if bytes.Equal(closed, frame()) {
		t.Fatal("F1 retained LOCKED caption")
	}
	v.Options.Open = false
	if !bytes.Equal(closed, frame()) {
		t.Fatal("closing F1 did not restore caption")
	}
}

func TestSaverHeaderIsCompactOnDesktopOutputs(t *testing.T) {
	for _, size := range [][2]int{{3440, 1440}, {2560, 1440}, {960, 720}, {320, 240}} {
		s := ScreensaverLayout(size[0], size[1], 1, "kompaktblk", widestClock)
		if s.Header.Dx() > 520 || s.Header.Dy() > 96 || s.Header.Max.Y > s.Banner.Min.Y || s.Banner.Max.Y > s.ClockBox.Min.Y {
			t.Fatalf("%v oversized or overlapping header: %+v", size, s)
		}
	}
}

func TestBlackholePreservesRaggedHeaderFirstFrame(t *testing.T) {
	now := time.Unix(600, 0)
	v := NewView(theme.Default(), "u", "h")
	v.Header = " A\nABC"
	v.TextPalette = "nord"
	a, b := render.New(520, 96), render.New(520, 96)
	v.drawHeader(a, a.Bounds(), now)
	v.TextEffect = "blackhole"
	v.drawHeader(b, b.Bounds(), now)
	if !bytes.Equal(headerInkMask(a.Pix), headerInkMask(b.Pix)) {
		t.Fatal("blackhole first frame moved header rows")
	}
}

func TestPrintHeaderCompletesWithinHalfSecond(t *testing.T) {
	now := time.Unix(600, 0)
	v := NewView(theme.Default(), "u", "h")
	v.Header = art.DefaultHeaders()[0].Text
	v.TextEffect = "print"
	v.TextPalette = "nord"
	for i := 0; i <= 10; i++ {
		v.drawHeader(render.New(520, 96), image.Rect(0, 0, 520, 96), now.Add(time.Duration(i)*50*time.Millisecond))
	}
	fb := render.New(520, 96)
	v.drawHeader(fb, fb.Bounds(), now.Add(550*time.Millisecond))
	v.TextEffect = "none"
	want := render.New(520, 96)
	v.drawHeader(want, want.Bounds(), now)
	if !bytes.Equal(headerInkMask(fb.Pix), headerInkMask(want.Pix)) {
		t.Fatal("print has not revealed the plain header in 500ms")
	}
}

func TestFormCaptionHiddenByOptions(t *testing.T) {
	now := time.Unix(600, 0)
	v := NewView(theme.Default(), "u", "h")
	s := Layout(1536, 864, 1, v.StyleName, v.clockText(now))
	a, b := render.New(1536, 864), render.New(1536, 864)
	v.drawForm(a, s.Frame, s.Backing, s.Rule, s.Scale)
	v.Options = &MenuView{Open: true}
	v.drawForm(b, s.Frame, s.Backing, s.Rule, s.Scale)
	if bytes.Equal(a.Pix, b.Pix) {
		t.Fatal("form still paints LOCKED while F1 is open")
	}
}

func TestHeaderAnimationRetainsPaletteColors(t *testing.T) {
	now := time.Unix(600, 0)
	frame := func(palette string) []byte {
		v := NewView(theme.Default(), "u", "h")
		v.Header = "ABC\nDEF"
		v.TextEffect = "blackhole"
		v.TextPalette = palette
		fb := render.New(520, 96)
		v.drawHeader(fb, fb.Bounds(), now)
		return fb.Pix
	}
	if bytes.Equal(frame("nord"), frame("eldritch")) {
		t.Fatal("animation discarded palette colors")
	}
}

func headerInkMask(pix []byte) []byte {
	out := make([]byte, len(pix)/4)
	for i := range out {
		if pix[i*4]|pix[i*4+1]|pix[i*4+2] != 0 {
			out[i] = 1
		}
	}
	return out
}

type brokenHeaderAnimation struct{}

func (brokenHeaderAnimation) Update()        { panic("decorative failure") }
func (brokenHeaderAnimation) Render() string { panic("decorative failure") }
func (brokenHeaderAnimation) Reset()         {}

func TestHeaderFailurePreservesStaticArtwork(t *testing.T) {
	now := time.Unix(600, 0)
	v := NewView(theme.Default(), "u", "h")
	v.Header, v.TextEffect, v.TextPalette = "ABC", "blackhole", "nord"
	fb := render.New(520, 96)
	v.drawHeader(fb, fb.Bounds(), now)
	v.headerRenderer = brokenHeaderAnimation{}
	v.drawHeader(fb, fb.Bounds(), now.Add(50*time.Millisecond))
	if v.headerRenderer != nil {
		t.Fatal("broken animation retained")
	}
	v.drawHeader(render.New(520, 96), image.Rect(0, 0, 520, 96), now.Add(time.Second))
	if v.headerRenderer != nil {
		t.Fatal("same failed animation retried")
	}
	v.TextEffect = "none"
	want := render.New(520, 96)
	v.drawHeader(want, want.Bounds(), now)
	if !bytes.Equal(headerInkMask(fb.Pix), headerInkMask(want.Pix)) {
		t.Fatal("animation failure lost plain artwork")
	}
}

func TestCompletedHeaderPrintHasNoAnimationDeadline(t *testing.T) {
	now := time.Unix(600, 0)
	v := NewView(theme.Default(), "u", "h")
	v.Header, v.TextEffect, v.TextPalette = art.DefaultHeaders()[0].Text, "print", "nord"
	for i := 0; i <= 10; i++ {
		v.Render(render.New(1536, 864), now.Add(time.Duration(i)*50*time.Millisecond))
	}
	at := now.Add(2 * time.Second)
	if d := v.NextDeadline(at); d.Sub(at) < 500*time.Millisecond {
		t.Fatalf("completed print still ticks: %v", d)
	}
}

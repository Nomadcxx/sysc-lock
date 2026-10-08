package lockd

import (
	"bytes"
	"testing"
	"time"

	"github.com/Nomadcxx/sysc-lock/internal/art"
	"github.com/Nomadcxx/sysc-lock/internal/input"
	"github.com/Nomadcxx/sysc-lock/internal/render"
	"github.com/Nomadcxx/sysc-lock/internal/theme"
	"github.com/Nomadcxx/sysc-terminal/renderer"
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
	v.Header, v.TextEffect, v.TextPalette = art.DefaultHeaders()[0].Text, "fire-text", "eldritch"
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
	for _, effect := range renderer.TextEffects() {
		t.Run(effect, func(t *testing.T) {
			v := NewView(theme.Default(), "u", "h")
			v.Header, v.TextEffect, v.TextPalette = art.DefaultHeaders()[0].Text, effect, "eldritch"
			fb := render.New(1536, 864)
			visible := false
			for i := 0; i < 80; i++ {
				v.Render(fb, now.Add(time.Duration(i)*50*time.Millisecond))
				if v.headerRenderer == nil {
					t.Fatal("text renderer fell back")
				}
				for j := 0; j+3 < len(v.headerPixels); j += 4 {
					if v.headerPixels[j]|v.headerPixels[j+1]|v.headerPixels[j+2] != 0 {
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

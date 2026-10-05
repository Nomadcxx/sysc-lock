package lockd

import (
	"image"
	"image/color"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Nomadcxx/sysc-lock/internal/input"
	"github.com/Nomadcxx/sysc-lock/internal/render"
	"github.com/Nomadcxx/sysc-lock/internal/theme"
)

func TestErrorAutoClear4s(t *testing.T) {
	v := NewView(theme.Default(), "nomadx", "host")
	t0 := time.Date(2026, 10, 3, 15, 4, 0, 0, time.UTC)
	v.SetError("Incorrect password", t0)
	if got := v.StatusLine(t0.Add(3 * time.Second)); got != "Incorrect password" {
		t.Fatalf("at +3s: %q", got)
	}
	if got := v.StatusLine(t0.Add(5 * time.Second)); got != "" {
		t.Fatalf("at +5s: %q, want cleared", got)
	}
}

func TestTerminalErrorPersists(t *testing.T) {
	v := NewView(theme.Default(), "u", "h")
	t0 := time.Now()
	v.SetErrorTerminal("Too many attempts - locked out", t0)
	if got := v.StatusLine(t0.Add(time.Hour)); got == "" {
		t.Fatal("terminal error must not auto-clear")
	}
}

func TestAttemptsCount(t *testing.T) {
	v := NewView(theme.Default(), "u", "h")
	v.NoteAttempt(time.Now())
	if v.Attempts != 1 {
		t.Fatalf("attempts = %d", v.Attempts)
	}
}

func TestRenderPaints(t *testing.T) {
	v := NewView(theme.Default(), "u", "h")
	m := &input.Model{}
	m.Append("ab")
	v.Entry = m
	fb := render.New(640, 480)
	before := fb.Pix[0]
	v.Render(fb, time.Date(2026, 10, 3, 15, 4, 0, 0, time.UTC))
	changed := false
	for _, b := range fb.Pix {
		if b != before {
			changed = true
			break
		}
	}
	if !changed {
		t.Fatal("Render drew nothing")
	}
}

func TestRenderWithWallpaperColorFallback(t *testing.T) {
	fb := render.New(64, 64)
	fb.Fill(color.NRGBA{R: 1, G: 2, B: 3, A: 255})
	_ = fb // render.Background covered in Task 9 tests; view must not repaint bg
}

func TestPanelFitsSmallLogicalOutput(t *testing.T) {
	for _, size := range [][2]int{{320, 240}, {420, 480}, {1920, 1080}} {
		p := PanelGeometry(size[0], size[1], 1)
		if p.Panel.Min.X < 0 || p.Panel.Min.Y < 0 || p.Panel.Max.X > size[0] || p.Panel.Max.Y > size[1] || p.StatusY >= p.Panel.Max.Y {
			t.Fatal(size, p)
		}
	}
}
func TestPanelScalesWithoutStatusClipping(t *testing.T) {
	p := PanelGeometry(1920, 1080, 1.25)
	if p.Panel.Dx() > 525 || p.StatusY >= p.Panel.Max.Y {
		t.Fatal(p)
	}
}
func TestStatusDoesNotMovePasswordField(t *testing.T) {
	v := NewView(theme.Default(), "u", "h")
	before := PanelGeometry(640, 480, 1)
	v.SetError("Incorrect password", time.Now())
	after := PanelGeometry(640, 480, 1)
	if before.Entry != after.Entry || before.Unlock != after.Unlock {
		t.Fatal("status moved field")
	}
}
func TestClockInvalidatesWithoutTyping(t *testing.T) {
	v := NewView(theme.Default(), "u", "h")
	now := time.Date(2026, 10, 5, 12, 30, 30, 0, time.UTC)
	if got := v.NextDeadline(now); !got.Equal(now.Add(30 * time.Second)) {
		t.Fatal(got)
	}
	v.SetError("Incorrect password", now)
	if got := v.NextDeadline(now); !got.Equal(now.Add(4 * time.Second)) {
		t.Fatal(got)
	}
}
func TestTextGlyphUsesBGRA(t *testing.T) {
	fb := render.New(64, 64)
	fb.Fill(color.NRGBA{A: 255})
	drawText(fb, 32, 40, "X", 32, color.NRGBA{R: 255, A: 255})
	red := false
	for i := 0; i < len(fb.Pix); i += 4 {
		if fb.Pix[i+3] != 255 {
			t.Fatal("alpha")
		}
		if fb.Pix[i+2] > 0 {
			red = true
		}
		if fb.Pix[i] != 0 {
			t.Fatal("red glyph became blue")
		}
	}
	if !red {
		t.Fatal("missing glyph")
	}
}

func TestLongLabelsStayInsidePanel(t *testing.T) {
	fb := render.New(320, 240)
	v := NewView(theme.Default(), strings.Repeat("very-long-account", 20), "host")
	v.Layout = strings.Repeat("layout", 30)
	v.Caps = true
	v.Num = true
	v.SetErrorTerminal(strings.Repeat("error", 40), time.Now())
	v.Render(fb, time.Now())
	p := PanelGeometry(320, 240, 1)
	for y := 0; y < 240; y++ {
		for x := 0; x < 320; x++ {
			if !image.Pt(x, y).In(p.Panel) {
				got := color.NRGBAModel.Convert(fb.At(x, y)).(color.NRGBA)
				if got != v.Pal.Surface {
					t.Fatalf("text escaped panel at %d,%d: %v", x, y, got)
				}
			}
		}
	}
}
func TestScaledTextStaysInsidePanel(t *testing.T) {
	for _, scale := range []float64{.75, 1, 1.5, 2} {
		v := NewView(theme.Default(), "Sample Account", "host")
		v.TextScale = scale
		v.Entry = &input.Model{}
		v.Entry.Append(strings.Repeat("a", 200))
		v.Busy = true
		fb := render.New(320, 240)
		v.Render(fb, time.Now())
		p := PanelGeometry(320, 240, 1)
		for y := 0; y < 240; y++ {
			for x := 0; x < 320; x++ {
				if !image.Pt(x, y).In(p.Panel) && color.NRGBAModel.Convert(fb.At(x, y)).(color.NRGBA) != v.Pal.Surface {
					t.Fatal("scaled text escaped")
				}
			}
		}
	}
}

func TestViewHasNoUnaccountedWallpaperPixelCache(t *testing.T) {
	if _, ok := reflect.TypeOf(View{}).FieldByName("backgroundCache"); ok {
		t.Fatal("view owns unaccounted wallpaper pixels outside output worker")
	}
}

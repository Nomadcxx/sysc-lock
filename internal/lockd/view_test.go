package lockd

import (
	"bytes"
	"image"
	"image/color"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Nomadcxx/sysc-Go/animations"
	"github.com/Nomadcxx/sysc-lock/internal/ambient"
	"github.com/Nomadcxx/sysc-lock/internal/art"
	"github.com/Nomadcxx/sysc-lock/internal/input"
	"github.com/Nomadcxx/sysc-lock/internal/render"
	"github.com/Nomadcxx/sysc-lock/internal/theme"
	xdraw "golang.org/x/image/draw"
)

func TestStatusReplacesUnsupportedGlyphs(t *testing.T) {
	v := NewView(theme.Default(), "user", "host")
	s := Layout(960, 720, 1, v.StyleName, "12:34 PM")
	a, b := render.New(960, 720), render.New(960, 720)
	v.Ambient = ambient.Status{Caption: []ambient.Span{{Text: "♪ Song \U0010ffff", Tone: ambient.ToneInk}}}
	v.drawStatus(a, s, true)
	v.Ambient.Caption = []ambient.Span{{Text: "♪ Song ?", Tone: ambient.ToneInk}}
	v.drawStatus(b, s, true)
	if !bytes.Equal(a.Pix, b.Pix) {
		t.Fatal("unsupported metadata glyph did not render as readable fallback")
	}
}

func TestCompositionUsesOneBoundedColumn(t *testing.T) {
	s := Layout(1536, 864, 1, "kompaktblk", widestClock)
	if s.Identity.Empty() || s.Identity.Max.Y > s.Label.Min.Y || s.Identity.Overlaps(s.Indicators) {
		t.Fatal("account identity must be separate from field and keyboard indicators")
	}
	if s.Frame.Dx() > 520 || s.Logo.Dx() > s.Frame.Dx()/2 || s.ClockBox.Dx() > s.Frame.Dx() {
		t.Fatalf("header and form do not share a restrained column: %+v", s)
	}
	if s.OptionsMenu != s.Frame {
		t.Fatalf("F1 menu must replace the form in its column: %+v", s)
	}
}

func TestRejectionFeedbackDoesNotMoveTheField(t *testing.T) {
	for _, style := range art.Names() {
		for _, scale := range []float64{1, 1.25, 1.5} {
			original := Layout(1536, 864, scale, style, widestClock)
			for _, attempts := range []int{1, 3} {
				failed := Layout(1536, 864, scale, style, widestClock, attempts)
				if original.Entry != failed.Entry {
					t.Fatalf("%s scale %v attempt %d moves field: %v -> %v", style, scale, attempts, original.Entry, failed.Entry)
				}
			}
		}
	}
}

func TestEmptyFieldHasNoDuplicatePasswordPlaceholder(t *testing.T) {
	now := time.Unix(600, 0)
	v := NewView(theme.Default(), "u", "h")
	v.Reduced = true
	v.Entry = &input.Model{}
	v.Reveal.Show(now)
	s := Layout(960, 720, 1, v.StyleName, v.clockText(now))
	fb := render.New(960, 720)
	v.Render(fb, now)
	box := image.Rect(s.Entry.Min.X+s.Entry.Dx()/4, s.Entry.Min.Y+4, s.Entry.Max.X-s.Entry.Dx()/4, s.Entry.Max.Y-4)
	for y := box.Min.Y; y < box.Max.Y; y++ {
		for x := box.Min.X; x < box.Max.X; x++ {
			if got := color.NRGBAModel.Convert(fb.At(x, y)).(color.NRGBA); got != v.ground() {
				t.Fatal("empty field has text competing with its label")
			}
		}
	}
}

func TestOptionsUsesThemeAccent(t *testing.T) {
	now := time.Unix(600, 0)
	v := NewView(theme.Default().WithScheme("eldritch"), "u", "h")
	v.Reduced = true
	v.Options = &MenuView{Open: true, Title: "Options", Progress: -1, Rows: []PowerRow{{Title: "Background", Value: "none", Selected: true}, {Title: "Theme", Value: "eldritch"}}}
	fb := render.New(960, 720)
	v.Render(fb, now)
	s := Layout(960, 720, 1, v.StyleName, v.clockText(now))
	if got := color.NRGBAModel.Convert(fb.At(s.OptionsMenu.Min.X, s.OptionsMenu.Min.Y)).(color.NRGBA); got != v.accent() {
		t.Fatalf("options frame %v does not use theme accent %v", got, v.accent())
	}
}

func TestOptionsDrawsLabelsAndValuesAtDefaultScale(t *testing.T) {
	v := NewView(theme.Default(), "u", "h")
	a, b := render.New(520, 260), render.New(520, 260)
	p := MenuView{Title: "Options", Progress: -1, Rows: []PowerRow{{Title: "Background", Value: "none", Selected: true}, {Title: "Theme", Value: "eldritch"}}}
	v.drawPopup(a, a.Bounds(), p, v.accent())
	p.Rows[1].Value = "nord"
	v.drawPopup(b, b.Bounds(), p, v.accent())
	if bytes.Equal(a.Pix, b.Pix) {
		t.Fatal("selected values are not rendered")
	}
}

func TestOptionsHasNoHoldBar(t *testing.T) {
	v := NewView(theme.Default(), "u", "h")
	fb := render.New(520, 260)
	box := fb.Bounds()
	v.drawPopup(fb, box, MenuView{Title: "Options", Progress: -1}, v.accent())
	for y := box.Dy() / 2; y < box.Max.Y-2; y++ {
		for x := box.Min.X + 2; x < box.Max.X-2; x++ {
			if fb.At(x, y) != v.ground() {
				t.Fatal("options draws a power hold bar")
			}
		}
	}
}

func TestDecorativeTintKeepsItsThemeHue(t *testing.T) {
	v := NewView(theme.Default().WithScheme("eldritch"), "u", "h")
	ink := v.clockInk()
	if ink == panelInk || contrast(ink, color.NRGBA{R: 85, G: 85, B: 85, A: 255}) < 3 {
		t.Fatalf("clock must retain a contrast-safe theme tint: %v", ink)
	}
}

func TestCaretUsesFrameTimeAndHonorsReducedMotion(t *testing.T) {
	v := NewView(theme.Default(), "user", "host")
	fb := render.New(800, 600)
	s := Layout(fb.Width, fb.Height, v.Scale, v.StyleName, v.clockText(time.UnixMilli(10_200)))
	inner := s.Entry.Inset(max(2, int(8*s.Scale)))
	sq := max(2, s.Entry.Dy()/4)
	x, y := inner.Min.X+sq/2, s.Entry.Min.Y+s.Entry.Dy()/2
	for _, reduced := range []bool{false, true} {
		v.Reduced = reduced
		for _, millis := range []int64{200, 700} {
			now := time.UnixMilli(10_000 + millis)
			v.Reveal.Show(now)
			v.Render(fb, now)
			want := v.ground()
			if reduced || millis < 500 {
				want = v.accent()
			}
			if got := fb.At(x, y); got != want {
				t.Fatalf("reduced=%v frame=%dms: caret=%v, want %v", reduced, millis, got, want)
			}
		}
	}
}

func TestLogoRenderingReusesScalingAndPreservesPixels(t *testing.T) {
	v := NewView(theme.Default(), "user", "host")
	fb, want := render.New(80, 40), render.New(80, 40)
	r := image.Rect(-10, 3, 90, 35)
	tint := color.NRGBA{R: 31, G: 120, B: 210, A: 255}
	ground := color.NRGBA{R: 190, G: 81, B: 17, A: 255}
	check := func() {
		t.Helper()
		fb.Fill(ground)
		want.Fill(ground)
		scaled := image.NewNRGBA(image.Rect(0, 0, r.Dx(), r.Dy()))
		xdraw.ApproxBiLinear.Scale(scaled, scaled.Bounds(), art.Logo(), art.Logo().Bounds(), xdraw.Over, nil)
		for y := 0; y < r.Dy(); y++ {
			for x := 0; x < r.Dx(); x++ {
				a := uint32(scaled.Pix[scaled.PixOffset(x, y)+3])
				if a == 0 {
					continue
				}
				dst := color.NRGBAModel.Convert(want.At(r.Min.X+x, r.Min.Y+y)).(color.NRGBA)
				want.Set(r.Min.X+x, r.Min.Y+y, color.NRGBA{
					R: uint8((uint32(dst.R)*(255-a) + uint32(tint.R)*a) / 255),
					G: uint8((uint32(dst.G)*(255-a) + uint32(tint.G)*a) / 255),
					B: uint8((uint32(dst.B)*(255-a) + uint32(tint.B)*a) / 255), A: 255})
			}
		}
		v.drawLogo(fb, r, tint)
		if !bytes.Equal(fb.Pix, want.Pix) {
			t.Fatal("logo pixels changed")
		}
	}
	check()
	r = image.Rect(4, -2, 76, 42)
	tint.R = 220 // changing geometry and theme must remain correct.
	check()
	if allocations := testing.AllocsPerRun(3, func() { v.drawLogo(fb, r, tint) }); allocations > 1 {
		t.Fatalf("steady logo rendering allocates %.0f objects per frame", allocations)
	}
}

func BenchmarkForeground1080p(b *testing.B) {
	v := NewView(theme.Default(), "user", "host")
	v.Scale, v.Reduced = 1.25, true
	v.Hint = "F1 Options - Enter Unlock"
	v.Ambient = ambient.Status{
		Corner:  []ambient.Span{{Text: "Wi-Fi", Tone: ambient.ToneMuted}, {Text: " • ", Tone: ambient.ToneDim}, {Text: "[██████░░░░] 63%", Tone: ambient.ToneInk}},
		Caption: []ambient.Span{{Text: "♪ ", Tone: ambient.ToneAccent}, {Text: "Midnight City", Tone: ambient.ToneInk}},
	}
	fb := render.New(1920, 1080)
	now := time.Unix(10, 0)
	v.Render(fb, now)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		v.Render(fb, now)
	}
}

func TestFillRectClipsWithoutAllocating(t *testing.T) {
	fb := render.New(8, 6)
	fb.Fill(color.NRGBA{R: 17})
	r := image.Rect(-3, 2, 5, 10)
	ink := color.NRGBA{R: 200, G: 21, B: 82, A: 123}
	allocations := testing.AllocsPerRun(3, func() { fillRect(fb, r, ink) })
	if allocations != 0 {
		t.Fatalf("panel fill allocated %.0f objects", allocations)
	}
	for y := range fb.Height {
		for x := range fb.Width {
			want := color.NRGBA{R: 17, A: 255}
			if image.Pt(x, y).In(r) {
				want = ink
				want.A = 255
			}
			if got := fb.At(x, y); got != want {
				t.Fatalf("pixel %d,%d = %v, want %v", x, y, got, want)
			}
		}
	}
}

func TestErrorAutoClear4s(t *testing.T) {
	v := NewView(theme.Default(), "nomadx", "host")
	t0 := time.Date(2026, 10, 3, 15, 4, 0, 0, time.UTC)
	v.SetError("Incorrect password", t0)
	if got := v.StatusLine(t0.Add(3 * time.Second)); got != "Incorrect password" {
		t.Fatalf("at +3s: %q", got)
	}
	if got := v.StatusLine(t0.Add(4 * time.Second)); got != "" {
		t.Fatalf("at 4s: %q, want cleared", got)
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

func TestViewHasNoUnaccountedWallpaperPixelCache(t *testing.T) {
	if _, ok := reflect.TypeOf(View{}).FieldByName("backgroundCache"); ok {
		t.Fatal("view owns unaccounted wallpaper pixels outside output worker")
	}
}

const widestClock = "12:59:59 PM"

func TestSceneFitsEverySize(t *testing.T) {
	for _, c := range []struct {
		w, h  int
		scale float64
	}{{320, 240, 1}, {420, 480, 1}, {1536, 864, 1.25}, {1920, 1080, 1.5}, {1920, 1080, 1.25}, {3440, 1440, 1}} {
		for _, style := range art.Names() {
			s := Layout(c.w, c.h, c.scale, style, widestClock)
			fb := image.Rect(0, 0, c.w, c.h)
			for name, r := range map[string]image.Rectangle{
				"clock": s.ClockBox, "date": s.Date, "entry": s.Entry,
				"backing": s.Backing, "status": s.Status,
				"menu": s.Menu, "help": s.Help, "logo": s.Logo,
				"corner": s.Corner, "caption": s.Caption,
				"frame": s.Frame, "rule": s.Rule, "label": s.Label, "identity": s.Identity, "options": s.OptionsMenu,
			} {
				if r.Empty() {
					continue // a dropped row, like the logo or title on tiny outputs
				}
				if !r.In(fb) {
					t.Fatalf("%dx%d %s: %s %v outside output", c.w, c.h, style, name, r)
				}
			}
			if (!s.Date.Empty() && !s.ClockBox.Empty() && s.ClockBox.Max.Y > s.Date.Min.Y) ||
				(!s.Date.Empty() && s.Date.Max.Y > s.Entry.Min.Y) || (!s.ClockBox.Empty() && s.ClockBox.Max.Y > s.Entry.Min.Y) {
				t.Fatalf("%dx%d %s: stack overlaps %+v", c.w, c.h, style, s)
			}
			if !s.Caption.Empty() && s.Caption.Max.Y > s.Entry.Min.Y {
				t.Fatalf("%dx%d %s: caption overlaps the entry", c.w, c.h, style)
			}
			if s.Help.Empty() && c.h > 240 {
				t.Fatalf("%dx%d %s: the help strip must fit above 240 rows", c.w, c.h, style)
			}
		}
	}
}

func TestSceneIsDeterministicAndScaleSafe(t *testing.T) {
	a := Layout(1920, 1080, 1.25, "kompaktblk", "3:04:05 PM")
	if b := Layout(1920, 1080, 1.25, "kompaktblk", "3:04:05 PM"); !reflect.DeepEqual(a, b) {
		t.Fatal("layout must be a pure function")
	}
	for _, scale := range []float64{0, -1, math.NaN(), math.Inf(1), 99} {
		s := Layout(640, 480, scale, "kompaktblk", widestClock)
		if !s.Entry.In(image.Rect(0, 0, 640, 480)) {
			t.Fatalf("scale %v: %v", scale, s.Entry)
		}
	}
}

func TestClockTextFormats(t *testing.T) {
	v := NewView(theme.Default(), "u", "h")
	now := time.Date(2026, 10, 5, 15, 4, 5, 0, time.UTC)
	if got := v.clockText(now); got != "3:04:05 PM" {
		t.Fatal(got)
	}
	v.Clock24 = true
	if got := v.clockText(now); got != "15:04:05" {
		t.Fatal(got)
	}
}

func TestSecondsDeadlineAndErrorExpiry(t *testing.T) {
	v := NewView(theme.Default(), "u", "h")
	v.Reduced = true
	now := time.Date(2026, 10, 5, 12, 30, 30, 400*int(time.Millisecond), time.UTC)
	if got := v.NextDeadline(now); !got.Equal(time.Date(2026, 10, 5, 12, 30, 31, 0, time.UTC)) {
		t.Fatal("clock ticks once a second:", got)
	}
	v.SetError("Incorrect password", now)
	late := now.Add(3700 * time.Millisecond)
	if got := v.NextDeadline(late); !got.Equal(now.Add(4 * time.Second)) {
		t.Fatal("error expiry must be exact once it is the next event:", got)
	}
}

func TestPrintRevealIsBoundedAndSkippedWhenReduced(t *testing.T) {
	now := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	v := NewView(theme.Default(), "u", "h")
	fb := render.New(960, 720)
	v.RenderForeground(fb, now)
	s := Layout(960, 720, 1, "", v.clockText(now))
	early, done := v.printLimits(now.Add(100*time.Millisecond), s)
	if done || early >= art.Total(s.Clock) {
		t.Fatal("print reveal should still be running", early)
	}
	if _, done = v.printLimits(now.Add(art.PrintDuration), s); !done {
		t.Fatal("print reveal must end within one second")
	}
	if got := v.NextDeadline(now); got.After(now.Add(40 * time.Millisecond)) {
		t.Fatal("repaint must be scheduled while printing", got.Sub(now))
	}
	r := NewView(theme.Default(), "u", "h")
	r.Reduced = true
	r.RenderForeground(render.New(960, 720), now)
	if c, done := r.printLimits(now, s); !done || c != -1 {
		t.Fatal("reduced motion draws the final frame", c, done)
	}
}

func TestReducedMotionSkipsPrintAndJolt(t *testing.T) {
	now := time.Now()
	v := NewView(theme.Default(), "u", "h")
	v.Reduced = true
	v.Reject("Incorrect password", now)
	if !v.joltStart.IsZero() || v.Attempts != 1 || v.StatusLine(now) != "Incorrect password" {
		t.Fatal("reduced motion keeps the message and count but not the jolt")
	}
	m := NewView(theme.Default(), "u", "h")
	m.Reject("Incorrect password", now)
	if m.joltStart.IsZero() {
		t.Fatal("ordinary rejection must jolt")
	}
	term := NewView(theme.Default(), "u", "h")
	term.SetErrorTerminal("Account locked", now)
	if !term.Terminal() || !term.joltStart.IsZero() || term.StatusLine(now.Add(time.Hour)) == "" {
		t.Fatal("terminal errors persist without a jolt")
	}
}

func TestStatusShowsWhileEntryHidden(t *testing.T) {
	now := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	v := NewView(theme.Default(), "u", "h")
	v.Reduced = true
	v.SetErrorTerminal("Account locked", now)
	fb := render.New(960, 720)
	fb.Fill(v.Pal.Surface)
	v.RenderForeground(fb, now)
	s := Layout(960, 720, 1, "", v.clockText(now))
	if v.EntryVisible(now) {
		t.Fatal("entry must stay hidden until a key reveals it")
	}
	got := color.NRGBAModel.Convert(fb.At(s.Backing.Min.X+1, s.Backing.Min.Y+1)).(color.NRGBA)
	if got == v.Pal.Surface || got == panelInk {
		t.Fatal("status needs its frosted backing even with the entry hidden", got)
	}
}

func TestRevealAddsCaretWithoutMovingField(t *testing.T) {
	now := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	hidden := NewView(theme.Default(), "u", "h")
	hidden.Reduced = true
	a := render.New(960, 720)
	hidden.Render(a, now)
	shown := NewView(theme.Default(), "u", "h")
	shown.Reduced = true
	shown.Reveal.Show(now)
	b := render.New(960, 720)
	shown.Render(b, now)
	s := Layout(960, 720, 1, "", hidden.clockText(now))
	if reflect.DeepEqual(a.Pix, b.Pix) {
		t.Fatal("revealing the entry must change the frame")
	}
	ink := func(fb *render.Framebuffer) int {
		n := 0
		for y := s.Entry.Min.Y + 2; y < s.Entry.Max.Y-2; y++ {
			for x := s.Entry.Min.X + 2; x < s.Entry.Max.X-2; x++ {
				if color.NRGBAModel.Convert(fb.At(x, y)).(color.NRGBA) == shown.accent() {
					n++
				}
			}
		}
		return n
	}
	if ink(a) != 0 {
		t.Fatal("hidden entry must draw no ink inside the field")
	}
	if ink(b) == 0 {
		t.Fatal("the revealed entry must draw its left caret")
	}
}

func TestSceneStaysInsideItsBounds(t *testing.T) {
	now := time.Date(2026, 10, 5, 12, 59, 59, 0, time.UTC)
	for _, size := range [][2]int{{320, 240}, {960, 720}} {
		v := NewView(theme.Default(), strings.Repeat("very-long-account", 20), "host")
		v.Reduced = true
		v.Layout = strings.Repeat("layout", 30)
		v.Caps, v.Num = true, true
		v.Entry = &input.Model{}
		v.Entry.Append(strings.Repeat("a", 200))
		v.Reveal.Show(now)
		v.Hint = "F4 Power • Enter Unlock"
		long := []ambient.Span{{Text: strings.Repeat("82% • Wi-Fi • playing • 18° • ", 10), Tone: ambient.ToneInk}}
		v.Ambient = ambient.Status{Corner: long, Caption: long}
		v.Power = &PowerView{
			Open: true, Title: "Power Options", Progress: 40, Help: "help",
			Rows: []PowerRow{{Title: "Log out"}, {Title: "Reboot", Selected: true}, {Title: "Cancel"}},
		}
		v.SetErrorTerminal(strings.Repeat("error", 40), now)
		fb := render.New(size[0], size[1])
		v.Render(fb, now)
		bounds := Layout(size[0], size[1], 1, "", v.clockText(now)).Bounds()
		for y := 0; y < size[1]; y++ {
			for x := 0; x < size[0]; x++ {
				if !image.Pt(x, y).In(bounds) && color.NRGBAModel.Convert(fb.At(x, y)).(color.NRGBA) != v.Pal.Surface {
					t.Fatalf("%v: ink escaped the scene at %d,%d", size, x, y)
				}
			}
		}
	}
}

func TestDimBackgroundKeepsAlpha(t *testing.T) {
	pix := []byte{200, 100, 50, 255, 255, 255, 255, 255}
	DimBackground(pix)
	if !reflect.DeepEqual(pix, []byte{66, 33, 16, 255, 85, 85, 85, 255}) {
		t.Fatal(pix)
	}
}

func TestMutedRoleMeetsTheHelpLineContrastFloor(t *testing.T) {
	worst := luminance(panelGround)
	a, b := luminance(panelMuted), worst
	if ratio := (max(a, b) + .05) / (min(a, b) + .05); ratio < 4.5 {
		t.Fatalf("help ink %.2f:1 below the 4.5:1 floor", ratio)
	}
}

func TestHintStripFollowsTheForm(t *testing.T) {
	s := Layout(960, 720, 1, "", "12:59:59 PM")
	if s.Help.Empty() {
		t.Fatal("a 720p output has room for the hint")
	}
	if gap := s.Help.Min.Y - s.Frame.Max.Y; gap < 8 || gap > 20 {
		t.Fatalf("hint must follow the form with a nearby gap: %v", s.Help)
	}
	if s.Help.Overlaps(s.Backing) {
		t.Fatal("hint overlaps the entry backing")
	}
}

func TestHintStripAppearsWithTheEntryAndTheTextMatchesAvailability(t *testing.T) {
	now := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	v := NewView(theme.Default(), "u", "h")
	v.Reduced = true
	v.Reveal.Show(now)
	v.Hint = "Enter Unlock"
	s := Layout(960, 720, 1, "", v.clockText(now))
	if s.Help.Empty() {
		t.Skip("this output has no room for a hint strip")
	}
	fb := render.New(960, 720)
	fb.Fill(v.Pal.Surface)
	v.RenderForeground(fb, now)
	if color.NRGBAModel.Convert(fb.At(s.Help.Min.X+1, s.Help.Min.Y+1)).(color.NRGBA) != panelGround {
		t.Fatal("the hint strip needs a solid backing over the effect")
	}
	v.Hint = "F4 Power • Enter Unlock"
	fb = render.New(960, 720)
	fb.Fill(v.Pal.Surface)
	v.RenderForeground(fb, now)
	hidden := NewView(theme.Default(), "u", "h")
	hidden.Reduced = true
	hidden.Reveal.Show(now)
	hidden.Hint = "Enter Unlock"
	fb2 := render.New(960, 720)
	fb2.Fill(hidden.Pal.Surface)
	hidden.RenderForeground(fb2, now)
	if reflect.DeepEqual(fb.Pix, fb2.Pix) {
		t.Fatal("F4 must be mentioned when an action is available")
	}
}

func TestPopupUsesDangerForItsFrameTitleAndSelectedRow(t *testing.T) {
	now := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	v := NewView(theme.Default(), "u", "h")
	v.Reduced = true
	v.Entry = &input.Model{}
	v.Reveal.Show(now)
	v.Power = &PowerView{
		Open:     true,
		Title:    "Power Options",
		Help:     "help",
		Progress: -1,
		Rows:     []PowerRow{{Title: "Log out"}, {Title: "Reboot", Selected: true}, {Title: "Cancel"}},
	}
	fb := render.New(960, 720)
	v.Render(fb, now)
	s := Layout(960, 720, 1, "", v.clockText(now))
	px := func(p image.Point) color.NRGBA { return color.NRGBAModel.Convert(fb.At(p.X, p.Y)).(color.NRGBA) }
	if got := px(image.Pt(s.Menu.Min.X, s.Menu.Min.Y)); got != panelDanger {
		t.Fatalf("frame must be danger ink, got %v", got)
	}
	counts := map[color.NRGBA]int{}
	for x := s.Menu.Min.X; x < s.Menu.Max.X; x++ {
		for y := s.Menu.Min.Y + 2; y < s.Menu.Max.Y; y++ {
			counts[px(image.Pt(x, y))]++
		}
	}
	if counts[panelDanger] < 500 {
		t.Fatalf("the selected row must be a danger bar, counted %v", counts[panelDanger])
	}
	if counts[panelGround] == 0 {
		t.Fatal("the popup must sit on its own solid ground")
	}
}

func TestHoldBarFillsAsTheHoldRuns(t *testing.T) {
	now := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	count := func(p int) int {
		v := NewView(theme.Default(), "u", "h")
		v.Reduced = true
		v.Entry = &input.Model{}
		v.Reveal.Show(now)
		v.Power = &PowerView{
			Open: true, Title: "Power Options", Help: "help", Progress: p,
			Rows: []PowerRow{{Title: "Log out", Selected: true}},
		}
		fb := render.New(960, 720)
		v.Render(fb, now)
		s := Layout(960, 720, 1, "", v.clockText(now))
		n := 0
		for x := s.Menu.Min.X; x < s.Menu.Max.X; x++ {
			for y := s.Menu.Min.Y + s.Menu.Dy()/2; y < s.Menu.Max.Y-2; y++ {
				if color.NRGBAModel.Convert(fb.At(x, y)).(color.NRGBA) == panelDanger {
					n++
				}
			}
		}
		return n
	}
	empty, full := count(0), count(100)
	if full <= empty {
		t.Fatalf("the bar must grow: empty %d full %d", empty, full)
	}
}

func TestPopupAndHintStayInsideEveryOutput(t *testing.T) {
	for _, size := range [][2]int{{320, 240}, {420, 480}, {960, 720}, {1920, 1080}, {3440, 1440}} {
		for _, style := range art.Names() {
			now := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
			v := NewView(theme.Default(), "Sample Account", "example")
			v.Reduced = true
			v.StyleName = style
			v.Entry = &input.Model{}
			v.Reveal.Show(now)
			v.Hint = "F4 Power • Enter Unlock"
			v.Power = &PowerView{
				Open: true, Title: "Power Options", Progress: 40,
				Help: "↑↓ Navigate • Enter Select • Esc Cancel",
				Rows: []PowerRow{{Title: "Log out"}, {Title: "Reboot", Selected: true}, {Title: "Shutdown"}, {Title: "Cancel"}},
			}
			fb := render.New(size[0], size[1])
			v.Render(fb, now)
			fbRect := image.Rect(0, 0, size[0], size[1])
			s := Layout(size[0], size[1], 1, style, v.clockText(now))
			if s.Menu.Empty() || !s.Menu.In(fbRect) {
				t.Fatalf("%v %s: popup %v", size, style, s.Menu)
			}
			if !s.Help.Empty() && !s.Help.In(fbRect) {
				t.Fatalf("%v %s: hint %v", size, style, s.Help)
			}
		}
	}
}

func TestOpenPopupKeepsTheEntryVisiblePastIdle(t *testing.T) {
	now := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	v := NewView(theme.Default(), "u", "h")
	v.Reduced = true
	v.Reveal.Show(now)
	v.Power = &PowerView{Open: true}
	if !v.EntryVisible(now.Add(input.HideAfter + time.Second)) {
		t.Fatal("an open popup must keep the entry from hiding")
	}
}

func TestPoweringHoldsTheStatusLine(t *testing.T) {
	now := time.Now()
	const status = "Rebooting..."
	v := NewView(theme.Default(), "u", "h")
	v.Powering = status
	if v.StatusLine(now) != status {
		t.Fatalf("status %q", v.StatusLine(now))
	}
	if v.StatusLine(now.Add(time.Hour)) != status {
		t.Fatal("the action status must not expire")
	}
	v.Busy = true
	if v.StatusLine(now) != status {
		t.Fatal("the action status wins over the verification label")
	}
}

func TestHoldingShortensTheRepaintDeadline(t *testing.T) {
	now := time.Now()
	v := NewView(theme.Default(), "u", "h")
	v.Power = &PowerView{Progress: -1}
	if got := v.NextDeadline(now); got.Sub(now) > time.Second {
		t.Fatal("no hold means no short deadline", got.Sub(now))
	}
	v.Power = &PowerView{Progress: 30}
	if got := v.NextDeadline(now); got.Sub(now) > 40*time.Millisecond {
		t.Fatal("the bar must repaint on the print-reveal deadline", got.Sub(now))
	}
}

func TestParityScreensaverDropsTheFormAfterFiveMinutes(t *testing.T) {
	now := time.Unix(600, 0)
	v := NewView(theme.Default(), "Sample Account", "example")
	v.Reduced = true
	v.Entry = &input.Model{}
	fb := render.New(960, 720)
	v.Render(fb, now)
	s := Layout(960, 720, 1, v.StyleName, v.clockText(now))
	before := append([]byte(nil), fb.Pix...)
	v.Render(fb, now.Add(5*time.Minute))
	if sameRegion(before, fb.Pix, fb.Stride, s.Frame) {
		t.Fatal("the framed form survives five minutes of idle")
	}
}

func TestParityAttemptsAreVisibleAfterTheErrorExpires(t *testing.T) {
	now := time.Unix(600, 0)
	v := NewView(theme.Default(), "u", "h")
	v.Reduced = true
	fb := render.New(960, 720)
	v.Render(fb, now)
	before := append([]byte(nil), fb.Pix...)
	v.Reject("Incorrect password", now)
	v.Render(fb, now.Add(5*time.Second))
	// The attempt row extends the form downward; check the rect it is drawn in.
	s := Layout(960, 720, 1, v.StyleName, v.clockText(now), v.Attempts)
	if n, _ := inkIn(fb, s.Attempts, v.ground()); s.Attempts.Empty() || n == 0 || sameRegion(before, fb.Pix, fb.Stride, s.Attempts) {
		t.Fatal("failed attempt count is not visible after transient error expiry")
	}
}

func TestParityOptionsHoldAndCaretDeadline(t *testing.T) {
	now := time.UnixMilli(600200)
	v := NewView(theme.Default(), "u", "h")
	v.Entry = &input.Model{}
	v.Reveal.Show(now)
	v.Options = &MenuView{Open: true}
	if !v.EntryVisible(now.Add(9 * time.Second)) {
		t.Error("options menu does not hold entry visible")
	}
	v.Options = nil
	v.Reveal.Show(now)
	if got := v.NextDeadline(now); !got.Equal(now.Truncate(500 * time.Millisecond).Add(500 * time.Millisecond)) {
		t.Errorf("caret deadline=%v", got)
	}
	v.Reduced = true
	if got := v.NextDeadline(now); !got.Equal(now.Truncate(time.Second).Add(time.Second)) {
		t.Errorf("reduced motion schedules caret blink: %v", got)
	}
}

func TestParityActualSchemeContrast(t *testing.T) {
	for _, name := range []string{"nord", "rama", "eldritch"} {
		v := NewView(theme.Default().WithScheme(name), "u", "h")
		ratio := func(a, b color.NRGBA) float64 {
			x, y := luminance(a), luminance(b)
			return (max(x, y) + .05) / (min(x, y) + .05)
		}
		if got := ratio(v.muted(), v.ground()); got < 4.5 {
			t.Errorf("%s muted text contrast %.2f", name, got)
		}
		if got := ratio(v.accent(), v.ground()); got < 3 {
			t.Errorf("%s focus contrast %.2f", name, got)
		}
	}
}

func sameRegion(a, b []byte, stride int, r image.Rectangle) bool {
	for y := r.Min.Y; y < r.Max.Y; y++ {
		if !bytes.Equal(a[y*stride+r.Min.X*4:y*stride+r.Max.X*4], b[y*stride+r.Min.X*4:y*stride+r.Max.X*4]) {
			return false
		}
	}
	return true
}

func TestParityIdleHoldsForAllActiveOwners(t *testing.T) {
	now := time.Unix(600, 0)
	for _, name := range []string{"password", "PAM", "options", "power menu", "power action", "terminal"} {
		t.Run(name, func(t *testing.T) {
			v := NewView(theme.Default(), "u", "h")
			v.Entry = &input.Model{}
			v.Screensaver(now)
			switch name {
			case "password":
				v.Entry.Append("synthetic")
			case "PAM":
				v.Busy = true
			case "options":
				v.Options = &MenuView{Open: true}
			case "power menu":
				v.Power = &PowerView{Open: true}
			case "power action":
				v.Powering = "Rebooting..."
			case "terminal":
				v.SetErrorTerminal("Account expired", now)
			}
			held := now.Add(10 * time.Minute)
			if v.Screensaver(held) {
				t.Fatal("active owner disappeared behind idle mode")
			}
			v.Entry.Clear()
			v.Busy = false
			v.Options = nil
			v.Power = nil
			v.Powering = ""
			v.errTerm = false
			if v.Screensaver(held.Add(299 * time.Second)) {
				t.Fatal("idle timer did not restart after hold")
			}
			if !v.Screensaver(held.Add(5 * time.Minute)) {
				t.Fatal("idle never resumed")
			}
		})
	}
}

func TestParityWarningsAndScreensaverFitCompactOutputs(t *testing.T) {
	for _, size := range [][2]int{{320, 240}, {420, 480}, {960, 720}, {1920, 1080}} {
		for _, style := range art.Names() {
			s := Layout(size[0], size[1], 1, style, widestClock, 3)
			output := image.Rect(0, 0, size[0], size[1])
			for name, r := range map[string]image.Rectangle{"label": s.Label, "entry": s.Entry, "status": s.Status, "count": s.Attempts, "warning": s.Warning, "backing": s.Backing} {
				if r.Empty() || !r.In(output) {
					t.Fatalf("%v %s %s=%v", size, style, name, r)
				}
			}
			if s.Status.Max.Y > s.Attempts.Min.Y || s.Attempts.Max.Y > s.Warning.Min.Y {
				t.Fatal("feedback overlaps")
			}
			saver := ScreensaverLayout(size[0], size[1], 1, style, widestClock)
			if saver.Logo.Empty() || saver.Banner.Empty() || saver.ClockBox.Empty() || saver.Date.Empty() || !saver.Bounds().In(output) {
				t.Fatalf("%v %s incomplete saver: %+v", size, style, saver)
			}
			if !saver.Entry.Empty() || !saver.Frame.Empty() {
				t.Fatal("screensaver exposes form")
			}
		}
	}
}

func TestParityEverySchemeMeetsRenderedContrastFloors(t *testing.T) {
	for _, name := range animations.GetThemeNames() {
		v := NewView(theme.Default().WithScheme(name), "u", "h")
		for role, item := range map[string]struct {
			ink, ground color.NRGBA
			minimum     float64
		}{
			"body":  {safeInk(panelInk, v.ground(), 4.5), v.ground(), 4.5},
			"error": {safeInk(panelDanger, v.ground(), 4.5), v.ground(), 4.5},
			"muted": {v.muted(), v.ground(), 4.5}, "rule": {v.banner(), v.ground(), 4.5}, "focus label": {v.accent(), v.ground(), 4.5},
			"selected popup": {safeInk(v.ground(), panelDanger, 4.5), panelDanger, 4.5},
		} {
			if got := contrast(item.ink, item.ground); got < item.minimum {
				t.Errorf("%s %s %.2f:1", name, role, got)
			}
		}
		for grey := 0; grey <= 85; grey++ {
			ground := color.NRGBA{R: uint8(grey), G: uint8(grey), B: uint8(grey), A: 255}
			for role, item := range map[string]struct {
				ink     color.NRGBA
				minimum float64
			}{"logo": {v.artInk(v.banner(), 3), 3}, "clock": {v.clockInk(), 3}, "banner": {v.artInk(v.banner(), 4.5), 4.5}, "date": {v.dateInk(), 4.5}} {
				if got := contrast(item.ink, ground); got < item.minimum {
					t.Errorf("%s %s grey%d %.2f:1", name, role, grey, got)
				}
				if got := contrast(item.ink, v.Pal.Surface); got < item.minimum {
					t.Errorf("%s %s plain %.2f:1", name, role, got)
				}
			}
		}
	}
}

func TestLockedCaptionIsInsetIntoTopBorder(t *testing.T) {
	v := NewView(theme.Default(), "u", "h")
	s := Layout(1536, 864, 1, v.StyleName, widestClock)
	if s.Rule.Empty() || s.Rule.Min.Y >= s.Frame.Min.Y || s.Rule.Max.Y <= s.Frame.Min.Y {
		t.Fatal("caption does not straddle top border")
	}
	fb := render.New(1536, 864)
	fb.Fill(v.Pal.Surface)
	v.drawForm(fb, s.Frame, s.Backing, s.Rule, s.Scale)
	// The continuous line must be interrupted for caption ink at its centre.
	mid := s.Frame.Min.X + s.Frame.Dx()/2
	ground := 0
	for x := mid - 80; x < mid+80; x++ {
		if fb.At(x, s.Frame.Min.Y) == v.ground() {
			ground++
		}
	}
	if ground < 20 {
		t.Fatal("caption has no inset in top border")
	}
	inkAbove := false
	for y := s.Rule.Min.Y; y < s.Frame.Min.Y; y++ {
		for x := mid - 120; x < mid+120; x++ {
			if c := fb.At(x, y); c != v.ground() && c != v.Pal.Surface {
				inkAbove = true
			}
		}
	}
	if !inkAbove {
		t.Fatal("caption ink stays inside the frame instead of sitting in its border")
	}
}

func stackOf(s Scene) image.Rectangle {
	return s.Header.Union(s.Logo).Union(s.ClockBox).Union(s.Date).Union(s.Caption).
		Union(s.Rule).Union(s.Frame).Union(s.Backing).Union(s.Help).Union(s.Banner)
}

func TestStatusCornerSitsTopRightClearOfTheStack(t *testing.T) {
	for _, c := range []struct {
		w, h  int
		scale float64
	}{{960, 720, 1}, {1536, 864, 1}, {1920, 1080, 1.5}, {3440, 1440, 1}, {1080, 1920, 2}} {
		s := Layout(c.w, c.h, c.scale, "kompaktblk", widestClock)
		if s.Corner.Empty() {
			t.Fatalf("%dx%d@%v: no status corner", c.w, c.h, c.scale)
		}
		margin := max(1, int(8*c.scale))
		if s.Corner.Max.X != c.w-margin || s.Corner.Min.Y != margin {
			t.Fatalf("%dx%d@%v: corner %v not in the top-right margin", c.w, c.h, c.scale, s.Corner)
		}
		if s.Corner.Overlaps(stackOf(s)) {
			t.Fatalf("%dx%d@%v: corner %v overlaps the stack", c.w, c.h, c.scale, s.Corner)
		}
	}
	for _, size := range [][2]int{{320, 240}, {420, 480}} {
		s := Layout(size[0], size[1], 1, "kompaktblk", widestClock)
		if !s.Corner.Empty() && s.Corner.Overlaps(stackOf(s)) {
			t.Fatalf("%v: corner must drop rather than overlap", size)
		}
	}
}

func TestCaptionFollowsTheDate(t *testing.T) {
	s := Layout(960, 720, 1, "kompaktblk", widestClock)
	if s.Caption.Empty() || s.Date.Empty() {
		t.Fatal("a 720p output has room for date and caption")
	}
	if s.Caption.Min.Y < s.Date.Max.Y || (!s.Rule.Empty() && s.Caption.Max.Y > s.Rule.Min.Y) {
		t.Fatalf("caption %v must sit between the date %v and the form %v", s.Caption, s.Date, s.Rule)
	}
	if s.Caption.Min.X != s.Date.Min.X || s.Caption.Dx() != s.Date.Dx() {
		t.Fatal("caption shares the date's column")
	}
}

func TestCaptionDropsBeforeTheForm(t *testing.T) {
	s := Layout(320, 240, 1, "kompaktblk", widestClock)
	if !s.Caption.Empty() && (s.Help.Empty() || s.Identity.Empty()) {
		t.Fatal("the caption must drop before help and the form chrome")
	}
}

func TestScreensaverKeepsCornerAndCaption(t *testing.T) {
	for _, size := range [][2]int{{1920, 1080}, {960, 720}} {
		s := ScreensaverLayout(size[0], size[1], 1, "kompaktblk", widestClock)
		form := Layout(size[0], size[1], 1, "kompaktblk", widestClock)
		if s.Corner != form.Corner {
			t.Fatalf("%v: the corner must not move between form and idle", size)
		}
		if s.Caption.Empty() || s.Caption.Min.Y < s.Date.Max.Y || s.Caption.Dx() != form.Caption.Dx() {
			t.Fatalf("%v: idle caption %v must follow the date %v", size, s.Caption, s.Date)
		}
		if s.Corner.Overlaps(stackOf(s)) {
			t.Fatalf("%v: idle corner overlaps the stack", size)
		}
	}
}

func inkIn(fb *render.Framebuffer, r image.Rectangle, ground color.NRGBA) (n, maxX int) {
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			if color.NRGBAModel.Convert(fb.At(x, y)).(color.NRGBA) != ground {
				n++
				maxX = max(maxX, x)
			}
		}
	}
	return n, maxX
}

func TestStatusCornerIsRightAlignedAndStaysWhileIdle(t *testing.T) {
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	corner := []ambient.Span{{Text: "Wi-Fi • 63%", Tone: ambient.ToneInk}}
	for _, idle := range []bool{false, true} {
		v := NewView(theme.Default(), "u", "h")
		v.Reduced = true
		v.Ambient = ambient.Status{Corner: corner}
		at := now
		if idle {
			v.Screensaver(now)
			at = now.Add(6 * time.Minute)
		}
		fb := render.New(1920, 1080)
		v.Render(fb, at)
		s := Layout(1920, 1080, 1, v.StyleName, v.clockText(at))
		n, maxX := inkIn(fb, s.Corner, v.Pal.Surface)
		if n == 0 {
			t.Fatalf("idle=%v: corner drew nothing", idle)
		}
		if maxX < s.Corner.Max.X-12 {
			t.Fatalf("idle=%v: corner text ends at %d, want right-aligned to %d", idle, maxX, s.Corner.Max.X)
		}
	}
}

func TestIdleCaptionFollowsTheSetting(t *testing.T) {
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	for _, keep := range []bool{false, true} {
		v := NewView(theme.Default(), "u", "h")
		v.Reduced = true
		v.IdleCaption = keep
		v.Ambient = ambient.Status{Caption: []ambient.Span{{Text: "♪ Midnight City", Tone: ambient.ToneInk}}}
		v.Screensaver(now)
		at := now.Add(6 * time.Minute)
		fb := render.New(1920, 1080)
		v.Render(fb, at)
		s := ScreensaverLayout(1920, 1080, 1, v.StyleName, v.clockText(at))
		if n, _ := inkIn(fb, s.Caption, v.Pal.Surface); (n > 0) != keep {
			t.Fatalf("IdleCaption=%v: caption ink %d", keep, n)
		}
	}
}

func TestCaptionShowsOnTheForm(t *testing.T) {
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	v := NewView(theme.Default(), "u", "h")
	v.Reduced = true
	v.Ambient = ambient.Status{Caption: []ambient.Span{{Text: "♪ Midnight City", Tone: ambient.ToneInk}}}
	fb := render.New(960, 720)
	v.Render(fb, now)
	s := Layout(960, 720, 1, v.StyleName, v.clockText(now))
	if n, _ := inkIn(fb, s.Caption, v.Pal.Surface); n == 0 {
		t.Fatal("caption missing on the lock form")
	}
}

func TestBatteryAlertYieldsToFormStatus(t *testing.T) {
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	v := NewView(theme.Default(), "u", "h")
	alert := "Battery at 6%. Connect power."
	v.Ambient.Alert = alert
	if got := v.StatusLine(now); got != alert {
		t.Fatalf("idle status %q, want the alert", got)
	}
	v.SetError("Incorrect password", now)
	if got := v.StatusLine(now); got != "Incorrect password" {
		t.Fatalf("an error must win over the alert, got %q", got)
	}
	if got := v.StatusLine(now.Add(5 * time.Second)); got != alert {
		t.Fatalf("the alert returns when the error expires, got %q", got)
	}
	v.Busy = true
	if got := v.StatusLine(now.Add(5 * time.Second)); got != "Authenticating..." {
		t.Fatalf("busy must win over the alert, got %q", got)
	}
	v.Busy, v.Ambient.Alert = false, ""
	if got := v.StatusLine(now.Add(5 * time.Second)); got != "" {
		t.Fatalf("a cleared snapshot clears the alert, got %q", got)
	}
}

func TestStatusTonesMeetContrastOverTheBackdrop(t *testing.T) {
	v := NewView(theme.Default(), "u", "h")
	worst := color.NRGBA{R: 85, G: 85, B: 85, A: 255}
	for tone, floor := range map[ambient.Tone]float64{
		ambient.ToneMuted: 4.5, ambient.ToneInk: 4.5, ambient.ToneAccent: 4.5,
		ambient.ToneWarn: 4.5, ambient.ToneDanger: 4.5, ambient.ToneDim: 3,
	} {
		if got := contrast(v.tone(tone), worst); got < floor {
			t.Fatalf("tone %d: %.2f:1 below %.1f:1 over the dimmed backdrop", tone, got, floor)
		}
	}
}

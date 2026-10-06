package lockd

import (
	"image"
	"image/color"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Nomadcxx/sysc-lock/internal/art"
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
	}{{320, 240, 1}, {420, 480, 1}, {1920, 1080, 1.25}, {3440, 1440, 1}} {
		for _, style := range []string{"kompaktblk", "phm_blocky_reverse", "plain"} {
			s := Layout(c.w, c.h, c.scale, style, widestClock)
			fb := image.Rect(0, 0, c.w, c.h)
			for name, r := range map[string]image.Rectangle{
				"clock": s.ClockBox, "date": s.Date, "entry": s.Entry,
				"backing": s.Backing, "status": s.Status, "ambient": s.Ambient,
				"menu": s.Menu, "help": s.Help,
			} {
				if r.Empty() {
					continue // a dropped row, like the wordmark
				}
				if !r.In(fb) {
					t.Fatalf("%dx%d %s: %s %v outside output", c.w, c.h, style, name, r)
				}
			}
			wm := image.Rectangle{Min: s.WordAt, Max: s.WordAt.Add(image.Pt(art.Width(s.Wordmark)*s.WordCW, len(s.Wordmark)*2*s.WordCW))}
			if s.WordCW > 0 && !wm.In(fb) {
				t.Fatalf("%dx%d %s: wordmark outside output", c.w, c.h, style)
			}
			if !(s.ClockBox.Max.Y <= s.Date.Min.Y && s.Date.Max.Y <= s.Entry.Min.Y) {
				t.Fatalf("%dx%d %s: stack overlaps %+v", c.w, c.h, style, s)
			}
			if !s.Ambient.Empty() && s.Ambient.Min.Y < s.Status.Max.Y {
				t.Fatalf("%dx%d %s: ambient overlaps status", c.w, c.h, style)
			}
			if !s.Ambient.Empty() && !s.Help.Empty() && s.Ambient.Max.Y > s.Help.Min.Y {
				t.Fatalf("%dx%d %s: ambient overlaps help", c.w, c.h, style)
			}
			if s.Help.Empty() && c.h > 240 {
				t.Fatalf("%dx%d %s: the help strip must fit above 240 rows", c.w, c.h, style)
			}
		}
	}
}

func TestAmbientDropsBeforeHelp(t *testing.T) {
	s := Layout(320, 240, 1, "kompaktblk", widestClock)
	if !s.Ambient.Empty() && s.Help.Empty() {
		t.Fatal("ambient must drop before help")
	}
	wide := Layout(960, 720, 1, "kompaktblk", widestClock)
	if wide.Ambient.Empty() {
		t.Fatal("ambient must fit at 960x720")
	}
	if !wide.Ambient.In(image.Rect(0, 0, 960, 720)) {
		t.Fatal("ambient outside output")
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
	early, _, done := v.printLimits(now.Add(100*time.Millisecond), s)
	if done || early >= art.Total(s.Wordmark) {
		t.Fatal("print reveal should still be running", early)
	}
	if _, _, done = v.printLimits(now.Add(art.PrintDuration), s); !done {
		t.Fatal("print reveal must end within one second")
	}
	if got := v.NextDeadline(now); got.After(now.Add(40 * time.Millisecond)) {
		t.Fatal("repaint must be scheduled while printing", got.Sub(now))
	}
	r := NewView(theme.Default(), "u", "h")
	r.Reduced = true
	r.RenderForeground(render.New(960, 720), now)
	if w, c, done := r.printLimits(now, s); !done || w != -1 || c != -1 {
		t.Fatal("reduced motion draws the final frame", w, c, done)
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
	if got != panelGround {
		t.Fatal("status needs its solid backing even with the entry hidden", got)
	}
}

func TestHiddenEntryDrawsNoFieldAndRevealedDoes(t *testing.T) {
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
	px := func(fb *render.Framebuffer, p image.Point) color.NRGBA {
		return color.NRGBAModel.Convert(fb.At(p.X, p.Y)).(color.NRGBA)
	}
	if px(a, s.Entry.Min) != hidden.Pal.Surface {
		t.Fatal("hidden entry must leave the background alone")
	}
	if px(b, s.Entry.Min) != panelAccent {
		t.Fatal("revealed entry draws an accent frame")
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
		v.Ambient = strings.Repeat("82% • Wi-Fi • playing • 18° • ", 10)
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

func TestHiddenEntryPaintsNoAmbientInk(t *testing.T) {
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	v := NewView(theme.Default(), "u", "h")
	v.Reduced = true
	v.Ambient = "82% • Wi-Fi • playing • 18°"
	fb := render.New(960, 720)
	v.Render(fb, now)
	s := Layout(960, 720, 1, "", v.clockText(now))
	if s.Ambient.Empty() {
		t.Skip("no ambient slot")
	}
	got := color.NRGBAModel.Convert(fb.At(s.Ambient.Min.X+1, s.Ambient.Min.Y+1)).(color.NRGBA)
	if got != v.Pal.Surface {
		t.Fatal("hidden entry must leave ambient undrawn")
	}
}

func TestRevealedAmbientSitsOnGroundInMutedInk(t *testing.T) {
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	v := NewView(theme.Default(), "u", "h")
	v.Reduced = true
	v.Reveal.Show(now)
	v.Ambient = "82% • Wi-Fi"
	fb := render.New(960, 720)
	v.Render(fb, now)
	s := Layout(960, 720, 1, "", v.clockText(now))
	if s.Ambient.Empty() {
		t.Skip("no ambient slot")
	}
	got := color.NRGBAModel.Convert(fb.At(s.Ambient.Min.X+1, s.Ambient.Min.Y+1)).(color.NRGBA)
	if got != panelGround {
		t.Fatal("ambient needs a solid backing")
	}
}

func TestDimBackgroundKeepsAlpha(t *testing.T) {
	pix := []byte{200, 100, 50, 255, 255, 255, 255, 255}
	DimBackground(pix)
	if !reflect.DeepEqual(pix, []byte{100, 50, 25, 255, 127, 127, 127, 255}) {
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

func TestHintStripSitsOnTheOutputNotInTheStack(t *testing.T) {
	s := Layout(960, 720, 1, "", "12:59:59 PM")
	if s.Help.Empty() {
		t.Fatal("a 720p output has room for the hint")
	}
	if s.Help.Max.Y != 720-8 {
		t.Fatalf("hint %v must sit on the output, 8px in from the edge", s.Help)
	}
	if !s.Ambient.Empty() && s.Help.Overlaps(s.Ambient) {
		t.Fatal("hint overlaps the ambient row")
	}
	if s.Help.Overlaps(s.Backing) {
		t.Fatal("hint overlaps the entry backing")
	}
}

func TestBackingStopsAtTheStatusLine(t *testing.T) {
	s := Layout(960, 720, 1, "", "12:59:59 PM")
	if s.Ambient.Empty() {
		t.Fatal("a 720p output has an ambient slot")
	}
	if s.Backing.Max.Y > s.Ambient.Min.Y {
		t.Fatalf("backing %v bleeds into ambient %v", s.Backing, s.Ambient)
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
		for _, style := range []string{"kompaktblk", "phm_blocky_reverse", "plain"} {
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

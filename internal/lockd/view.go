package lockd

import (
	"image"
	"image/color"
	"math"
	"strconv"
	"time"

	"github.com/Nomadcxx/sysc-lock/internal/input"
	"github.com/Nomadcxx/sysc-lock/internal/render"
	"github.com/Nomadcxx/sysc-lock/internal/theme"
)

// View composes the lock screen: static background (wallpaper file or palette
// surface color), centered clock, user@host, masked entry, status line with
// DMS-parity 4s auto-clear, attempts count, keyboard layout label.
type View struct {
	Pal        theme.Palette
	User, Host string
	Layout     string // e.g. "us"; "" hides the indicator
	Caps       bool
	Num        bool
	Attempts   int
	Busy       bool
	Scale      float64
	TextScale  float64
	Entry      *input.Model

	errMsg   string
	errUntil time.Time
	errTerm  bool
}

func NewView(pal theme.Palette, user, host string) *View {
	return &View{Pal: pal, User: user, Host: host, TextScale: 1}
}

func (v *View) SetError(msg string, now time.Time) {
	v.errMsg, v.errUntil, v.errTerm = msg, now.Add(4*time.Second), false
}

// SetErrorTerminal shows a message that does not auto-clear (lockout).
func (v *View) SetErrorTerminal(msg string, _ time.Time) {
	v.errMsg, v.errTerm = msg, true
}

func (v *View) NoteAttempt(now time.Time) { v.Attempts++ }

// StatusLine is the visible error text at now (empty after the 4s window).
func (v *View) StatusLine(now time.Time) string {
	if v.Busy {
		return "Checking…"
	}
	if v.errMsg == "" {
		return ""
	}
	if !v.errTerm && now.After(v.errUntil) {
		v.errMsg = ""
	}
	return v.errMsg
}

// Render paints one full frame into fb at pixel size (fb dims). now drives the
// clock (minute resolution) and error auto-clear.
type PanelLayout struct {
	Panel, Entry, Unlock                             image.Rectangle
	ArtY, ClockY, DateY, UserY, IndicatorsY, StatusY int
	Scale                                            float64
	Compact                                          bool
}

func PanelGeometry(width, height int, scale float64) PanelLayout {
	if scale <= 0 || math.IsNaN(scale) || math.IsInf(scale, 0) {
		scale = 1
	}
	scale = min(scale, 4)
	w, h := int(float64(width)/scale), int(float64(height)/scale)
	compact := h < 480
	panelW := min(420, max(1, w-32))
	panelH := 420
	if compact {
		panelH = 206
	}
	panelH = min(panelH, max(1, h-16))
	x, y := (w-panelW)/2, (h-panelH)/2
	p := PanelLayout{Scale: scale, Compact: compact}
	rect := func(x, y, w, h int) image.Rectangle {
		return image.Rect(int(float64(x)*scale), int(float64(y)*scale), int(float64(x+w)*scale), int(float64(y+h)*scale))
	}
	line := func(n int) int { return int(float64(y+n) * scale) }
	p.Panel = rect(x, y, panelW, panelH)
	if compact {
		p.ClockY = line(30)
		p.DateY = line(48)
		p.UserY = line(72)
		p.Entry = rect(x+12, y+80, max(1, panelW-24), 32)
		p.Unlock = rect(x+12, y+120, max(1, panelW-24), 32)
		p.IndicatorsY = line(174)
		p.StatusY = line(196)
	} else {
		p.ArtY = line(64)
		p.ClockY = line(120)
		p.DateY = line(152)
		p.UserY = line(196)
		p.Entry = rect(x+20, y+220, max(1, panelW-40), 48)
		p.Unlock = rect(x+20, y+284, max(1, panelW-40), 44)
		p.IndicatorsY = line(360)
		p.StatusY = line(394)
	}
	return p
}
func (v *View) NextDeadline(now time.Time) time.Time {
	next := now.Truncate(time.Minute).Add(time.Minute)
	if !v.errTerm && v.errMsg != "" && v.errUntil.After(now) && v.errUntil.Before(next) {
		next = v.errUntil
	}
	return next
}
func (v *View) Render(fb *render.Framebuffer, now time.Time) {
	fb.Fill(v.Pal.Surface)
	v.RenderForeground(fb, now)
}

func fillRect(fb *render.Framebuffer, r image.Rectangle, c color.NRGBA) {
	r = r.Intersect(fb.Bounds())
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			fb.Set(x, y, c)
		}
	}
}
func border(fb *render.Framebuffer, r image.Rectangle, c color.NRGBA, n int) {
	fillRect(fb, image.Rect(r.Min.X, r.Min.Y, r.Max.X, r.Min.Y+n), c)
	fillRect(fb, image.Rect(r.Min.X, r.Max.Y-n, r.Max.X, r.Max.Y), c)
	fillRect(fb, image.Rect(r.Min.X, r.Min.Y, r.Min.X+n, r.Max.Y), c)
	fillRect(fb, image.Rect(r.Max.X-n, r.Min.Y, r.Max.X, r.Max.Y), c)
}
func (v *View) RenderForeground(fb *render.Framebuffer, now time.Time) {
	p := PanelGeometry(fb.Width, fb.Height, v.Scale)
	// Fixed dark panel roles retain readable contrast over every effect palette.
	fillRect(fb, p.Panel, panelGround)
	border(fb, p.Panel, panelAccent, max(1, int(p.Scale)))
	border(fb, p.Panel.Inset(max(3, int(4*p.Scale))), panelAccent, 1)
	px := func(n int) int { return max(1, int(float64(n)*p.Scale)) }
	textScale := v.TextScale
	if textScale <= 0 || math.IsNaN(textScale) || math.IsInf(textScale, 0) {
		textScale = 1
	}
	textScale = max(.75, min(2, textScale))
	text := func(baseline int, s string, size, top, bottom int, box image.Rectangle, col color.NRGBA) {
		box = box.Intersect(image.Rect(p.Panel.Min.X+px(12), top, p.Panel.Max.X-px(12), bottom))
		size = min(int(float64(px(size))*textScale), max(1, int(float64(box.Dy())*.84)))
		f := face(size)
		ascent := f.Metrics().Ascent.Ceil()
		descent := f.Metrics().Descent.Ceil()
		baseline = max(box.Min.Y+ascent, min(baseline, box.Max.Y-descent))
		drawTextBox(fb, box, baseline, s, size, col)
	}
	label := func(y int, s string, size, height int, col color.NRGBA) {
		text(y, s, size, y-px(height), y+px(4), p.Panel, col)
	}
	if !p.Compact {
		label(p.ArtY, "SYSC", 38, 52, panelAccent)
	}
	clockSize := 42
	if p.Compact {
		clockSize = 24
	}
	clockTop := p.ArtY + px(8)
	if p.Compact {
		clockTop = p.Panel.Min.Y + px(4)
	}
	text(p.ClockY, now.Format("15:04"), clockSize, clockTop, p.DateY-px(8), p.Panel, panelInk)
	text(p.DateY, now.Format("Monday, 2 January"), 12, p.ClockY+px(6), p.UserY-px(8), p.Panel, panelInk)
	text(p.UserY, "/ "+v.User+" /", 18, p.DateY+px(6), p.Entry.Min.Y-px(2), p.Panel, panelInk)
	border(fb, p.Entry, panelAccent, max(2, px(2)))
	mask := "Password"
	if v.Entry != nil && len(v.Entry.Pass) > 0 {
		// ponytail: show the trailing 24 mask glyphs; the complete credential stays in the owner.
		dots := v.Entry.Mask()
		runes := []rune(dots)
		mask = string(runes[max(0, len(runes)-24):])
	}
	text(p.Entry.Min.Y+p.Entry.Dy()/2+px(7), mask, 22, p.Entry.Min.Y+px(3), p.Entry.Max.Y-px(3), p.Entry.Inset(px(8)), panelInk)
	border(fb, p.Unlock, panelAccent, max(1, px(1)))
	button := "Unlock →"
	if v.Busy {
		button = "Checking…"
	}
	text(p.Unlock.Min.Y+p.Unlock.Dy()/2+px(6), button, 18, p.Unlock.Min.Y+px(3), p.Unlock.Max.Y-px(3), p.Unlock.Inset(px(8)), panelInk)
	indicator := v.Layout
	if v.Caps {
		if indicator != "" {
			indicator += " · "
		}
		indicator += "Caps Lock"
	}
	if v.Num {
		if indicator != "" {
			indicator += " · "
		}
		indicator += "Num Lock"
	}
	text(p.IndicatorsY, indicator, 12, p.Unlock.Max.Y+px(6), p.StatusY-px(18), p.Panel, panelInk)
	status := v.StatusLine(now)
	statusInk := panelDanger
	if v.Busy {
		statusInk = panelInk
	}
	text(p.StatusY, status, 14, p.IndicatorsY+px(5), p.Panel.Max.Y-px(6), p.Panel, statusInk)
}

func itoa(n int) string { return strconv.Itoa(n) }

func (v *View) Terminal() bool { return v.errTerm }

// Fixed opaque roles maintain contrast independently of decoration palettes.
var (
	panelGround = color.NRGBA{R: 16, G: 20, B: 28, A: 255}
	panelInk    = color.NRGBA{R: 240, G: 244, B: 250, A: 255}
	panelAccent = color.NRGBA{R: 147, G: 197, B: 253, A: 255}
	panelDanger = color.NRGBA{R: 255, G: 180, B: 180, A: 255}
)

package lockd

import (
	"image"
	"image/color"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/Nomadcxx/sysc-lock/internal/art"
	"github.com/Nomadcxx/sysc-lock/internal/input"
	"github.com/Nomadcxx/sysc-lock/internal/render"
	"github.com/Nomadcxx/sysc-lock/internal/theme"
)

// View composes the lock screen over the background: wordmark, block-digit
// clock, date, and an entry that appears when a key reveals it. Errors keep the
// 4s auto-clear; terminal PAM errors persist.
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
	Reveal     input.Reveal
	StyleName  string // clock style; unknown names fall back in art.Pick
	Clock24    bool
	Reduced    bool // no print reveal and no jolt
	// Power is nil when no action is available. Hint is the strip text,
	// handed in by the owner so this package does not import power. Ambient
	// is the one-line status text the owner reads from the collector's
	// snapshot file; it draws only while the entry is visible.
	Power   *PowerView
	Hint    string
	Ambient string
	// Powering is the status shown once an action is under way. While it is
	// set, keys are ignored.
	Powering string

	errMsg     string
	errUntil   time.Time
	errTerm    bool
	printStart time.Time
	joltStart  time.Time
}

func NewView(pal theme.Palette, user, host string) *View {
	return &View{Pal: pal, User: user, Host: host, TextScale: 1, StyleName: art.DefaultStyle}
}

func (v *View) SetError(msg string, now time.Time) {
	v.errMsg, v.errUntil, v.errTerm = msg, now.Add(4*time.Second), false
}

// SetErrorTerminal shows a message that does not auto-clear (lockout).
func (v *View) SetErrorTerminal(msg string, _ time.Time) {
	v.errMsg, v.errTerm = msg, true
}

func (v *View) NoteAttempt(now time.Time) { v.Attempts++ }

// Reject records an ordinary failed attempt: the message, the count and, unless
// motion is reduced, the entry jolt.
func (v *View) Reject(msg string, now time.Time) {
	v.SetError(msg, now)
	v.NoteAttempt(now)
	if !v.Reduced {
		v.joltStart = now
	}
}

// EntryVisible reports whether the entry is shown at now. Text in the field or
// a running verification keeps it up.
func (v *View) EntryVisible(now time.Time) bool {
	open := v.Power != nil && v.Power.Open
	return v.Reveal.Tick(now, v.Busy || v.Powering != "" || open || (v.Entry != nil && len(v.Entry.Pass) > 0))
}

// StatusLine is the visible error text at now (empty after the 4s window).
func (v *View) StatusLine(now time.Time) string {
	if v.Powering != "" {
		return v.Powering
	}
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

func (v *View) clockText(now time.Time) string {
	if v.Clock24 {
		return now.Format("15:04:05")
	}
	return now.Format("3:04:05 PM")
}

// NextDeadline is the next time the foreground must repaint without input:
// the next second, an error expiry, the entry hiding, and frequent steps while
// the print reveal or the jolt is running.
func (v *View) NextDeadline(now time.Time) time.Time {
	next := now.Truncate(time.Second).Add(time.Second)
	consider := func(t time.Time) {
		if t.After(now) && t.Before(next) {
			next = t
		}
	}
	if !v.errTerm && v.errMsg != "" {
		consider(v.errUntil)
	}
	consider(v.Reveal.Deadline())
	if !v.Reduced {
		if !v.printStart.IsZero() && now.Sub(v.printStart) < art.PrintDuration {
			consider(now.Add(33 * time.Millisecond))
		}
		if !v.joltStart.IsZero() && now.Sub(v.joltStart) < art.JoltDuration {
			consider(now.Add(40 * time.Millisecond))
		}
	}
	if v.Power != nil && v.Power.Progress >= 0 {
		consider(now.Add(33 * time.Millisecond))
	}
	return next
}

// Render paints one full frame into fb over the opaque surface colour.
func (v *View) Render(fb *render.Framebuffer, now time.Time) {
	fb.Fill(v.Pal.Surface)
	v.RenderForeground(fb, now)
}

// DimBackground halves red, green and blue so the art ink keeps 3:1 contrast
// over the brightest effect pixel. Alpha is untouched.
func DimBackground(pix []byte) {
	for i := 0; i+3 < len(pix); i += 4 {
		pix[i] /= 2
		pix[i+1] /= 2
		pix[i+2] /= 2
	}
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

// printLimits reports how many wordmark and clock cells the print reveal has
// drawn at now (-1: all). done is true once the reveal has finished or motion
// is reduced; the date appears then.
func (v *View) printLimits(now time.Time, s Scene) (word, clock int, done bool) {
	if v.Reduced || now.Sub(v.printStart) >= art.PrintDuration {
		return -1, -1, true
	}
	wt := art.Total(s.Wordmark)
	n := art.PrintLimit(now.Sub(v.printStart), wt+art.Total(s.Clock))
	return min(n, wt), max(0, n-wt), false
}

func (v *View) textPx(n int, box image.Rectangle) int {
	t := v.TextScale
	if t <= 0 || math.IsNaN(t) || math.IsInf(t, 0) {
		t = 1
	}
	t = max(.75, min(2, t))
	return max(1, min(int(float64(n)*t), max(1, box.Dy()*84/100)))
}

func (v *View) RenderForeground(fb *render.Framebuffer, now time.Time) {
	if v.printStart.IsZero() {
		v.printStart = now // the first foreground frame starts the print reveal
	}
	text := v.clockText(now)
	s := Layout(fb.Width, fb.Height, v.Scale, v.StyleName, text)
	wordLimit, clockLimit, done := v.printLimits(now, s)
	for _, r := range art.Rects(s.Wordmark, s.WordAt, s.WordCW, 2*s.WordCW, wordLimit) {
		fillRect(fb, r, panelInk)
	}
	if s.ClockCW > 0 {
		for _, r := range art.Rects(s.Clock, s.ClockBox.Min, s.ClockCW, 2*s.ClockCW, clockLimit) {
			fillRect(fb, r, panelInk)
		}
	} else if clockLimit != 0 {
		size := max(1, s.ClockBox.Dy()*7/10)
		drawTextBox(fb, s.ClockBox, s.ClockBox.Min.Y+s.ClockBox.Dy()*4/5, text, size, panelInk)
	}
	if done {
		drawTextBox(fb, s.Date, s.Date.Min.Y+s.DateSize, strings.ToUpper(now.Format("Monday, January 2")), v.textPx(s.DateSize, s.Date), panelInk)
	}
	visible := v.EntryVisible(now)
	status := v.StatusLine(now)
	if !visible && status == "" {
		return
	}
	dx := art.Jolt(now.Sub(v.joltStart)) * s.Cell
	shift := func(r image.Rectangle) image.Rectangle { return r.Add(image.Pt(dx, 0)) }
	fillRect(fb, shift(s.Backing), panelGround)
	if visible {
		v.drawEntry(fb, shift(s.Entry), shift(s.Indicators), s.Scale)
	}
	ink := panelDanger
	if v.Busy {
		ink = panelInk
	}
	line := shift(s.Status)
	drawTextBox(fb, line, line.Min.Y+line.Dy()*3/4, status, v.textPx(line.Dy()*3/5, line), ink)
	if visible {
		v.drawAmbient(fb, s)
		v.drawHint(fb, s)
	}
	if v.Power != nil && v.Power.Open {
		v.drawPopup(fb, s, *v.Power)
	}
}

func (v *View) drawEntry(fb *render.Framebuffer, entry, indicators image.Rectangle, scale float64) {
	border(fb, entry, panelAccent, max(2, int(2*scale)))
	inner := entry.Inset(max(2, int(8*scale)))
	if v.Entry == nil || len(v.Entry.Pass) == 0 {
		drawTextBox(fb, inner, entry.Min.Y+entry.Dy()*2/3, "PASSWORD", v.textPx(entry.Dy()/2, inner), panelInk)
	} else {
		sq := max(2, entry.Dy()/4)
		step := sq * 3 / 2
		n := min(len(v.Entry.Pass), max(1, inner.Dx()/step))
		y := entry.Min.Y + (entry.Dy()-sq)/2
		for i := 0; i < n; i++ {
			fillRect(fb, image.Rect(inner.Min.X+i*step, y, inner.Min.X+i*step+sq, y+sq), panelInk)
		}
	}
	parts := []string{v.User}
	if v.Layout != "" {
		parts = append(parts, v.Layout)
	}
	if v.Caps {
		parts = append(parts, "Caps Lock")
	}
	if v.Num {
		parts = append(parts, "Num Lock")
	}
	drawTextBox(fb, indicators, indicators.Min.Y+indicators.Dy()*3/4, strings.Join(parts, " • "), v.textPx(indicators.Dy()*3/5, indicators), panelInk)
}

func (v *View) drawHint(fb *render.Framebuffer, s Scene) {
	if s.Help.Empty() {
		return
	}
	fillRect(fb, s.Help, panelGround)
	if v.Hint == "" {
		return
	}
	box := s.Help.Inset(max(1, s.Help.Dy()/6))
	drawTextBox(fb, box, box.Min.Y+box.Dy()*3/5, v.Hint, v.textPx(14, box), panelMuted)
}

func (v *View) drawAmbient(fb *render.Framebuffer, s Scene) {
	if s.Ambient.Empty() || v.Ambient == "" {
		return
	}
	fillRect(fb, s.Ambient, panelGround)
	box := s.Ambient.Inset(max(1, s.Ambient.Dy()/6))
	drawTextBox(fb, box, box.Min.Y+box.Dy()*3/5, v.Ambient, v.textPx(14, box), panelMuted)
}

func (v *View) drawPopup(fb *render.Framebuffer, s Scene, p PowerView) {
	box := s.Menu
	if box.Empty() {
		return
	}
	fillRect(fb, box, panelGround)
	n := max(1, int(v.Scale))
	border(fb, box, panelDanger, n)
	lineH := max(n*3, box.Dy()/8)
	pad := max(n*2, lineH/3)
	inner := box.Inset(pad + n)
	if inner.Dy() <= 0 || inner.Dx() <= 0 {
		return
	}
	px := v.textPx(18, inner)
	y := inner.Min.Y
	drawTextBox(fb, inner, y+lineH*3/5, p.Title, px, panelDanger)
	y += lineH
	for _, row := range p.Rows {
		if y+lineH > inner.Max.Y {
			break
		}
		r := image.Rect(inner.Min.X, y, inner.Max.X, y+lineH)
		ink := panelMuted
		if row.Selected {
			fillRect(fb, r, panelDanger)
			ink = panelGround
		}
		drawTextBox(fb, r.Inset(px/2), y+lineH*3/5, row.Title, px, ink)
		y += lineH
	}
	barH := max(2, lineH/4)
	helpGap := lineH / 3
	helpH := 0
	if p.Help != "" {
		helpH = lineH
	}
	barY := inner.Max.Y - barH - helpH - helpGap
	if barY < y {
		barY = y
	}
	bar := image.Rect(inner.Min.X, barY, inner.Max.X, barY+barH)
	border(fb, bar, panelMuted, max(1, barH/3))
	if p.Progress > 0 && bar.Dy() > 0 {
		fillRect(fb, image.Rect(bar.Min.X, bar.Min.Y, bar.Min.X+bar.Dx()*min(100, p.Progress)/100, bar.Max.Y), panelDanger)
	}
	y = barY + barH + helpGap
	if p.Help != "" && y < inner.Max.Y {
		drawTextBox(fb, inner, y+lineH/2, p.Help, v.textPx(12, inner), panelMuted)
	}
}

func itoa(n int) string { return strconv.Itoa(n) }

func (v *View) Terminal() bool { return v.errTerm }

// Fixed opaque roles maintain contrast independently of decoration palettes.
// The entry and status sit on panelGround; the art sits on the dimmed effect.
var (
	panelGround = color.NRGBA{R: 16, G: 20, B: 28, A: 255}
	panelInk    = color.NRGBA{R: 240, G: 244, B: 250, A: 255}
	panelAccent = color.NRGBA{R: 147, G: 197, B: 253, A: 255}
	panelDanger = color.NRGBA{R: 255, G: 180, B: 180, A: 255}
	panelMuted  = color.NRGBA{R: 130, G: 138, B: 150, A: 255} // 5.3:1 on the ground
)

type PowerView struct {
	Open     bool
	Title    string
	Rows     []PowerRow
	Help     string
	Progress int // -1 when no hold is running
}

type PowerRow struct {
	Title    string
	Selected bool
}

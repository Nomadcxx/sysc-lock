package lockd

import (
	"image"
	"image/color"
	"math"
	"strconv"
	"strings"
	"time"

	xdraw "golang.org/x/image/draw"

	"github.com/Nomadcxx/sysc-lock/internal/art"
	"github.com/Nomadcxx/sysc-lock/internal/input"
	"github.com/Nomadcxx/sysc-lock/internal/render"
	"github.com/Nomadcxx/sysc-lock/internal/theme"
)

// View composes the lock screen over the background: SYSC header, a greet-style
// framed form with the LOCKED rule and solid panel ground, the block-digit clock,
// date, and an entry that appears when a key reveals it. Errors keep the 4s
// auto-clear; terminal PAM errors persist.
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
	Power *PowerView
	// Options is the F1 effects/theme menu; same popup surface as Power.
	Options    *MenuView
	Hint       string
	Prompt     string // sanitized PAM prompt text, replaces the hint while set
	PromptEcho bool
	Ambient    string
	// Powering is the status shown once an action is under way. While it is
	// set, keys are ignored.
	Powering string

	errMsg     string
	errUntil   time.Time
	errTerm    bool
	printStart time.Time
	joltStart  time.Time
	logoScaled *image.NRGBA
	idleSince  time.Time
	idleMode   bool
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
	open := (v.Power != nil && v.Power.Open) || (v.Options != nil && v.Options.Open)
	return v.Reveal.Tick(now, v.Busy || v.Powering != "" || open || (v.Entry != nil && len(v.Entry.Pass) > 0))
}

// Screensaver reports the five-minute idle mode. The foreground owner calls it;
// credentials, active dialogs and terminal failures remain at the prompt.
func (v *View) Screensaver(now time.Time) bool {
	if v.idleSince.IsZero() || v.Busy || v.Powering != "" || v.Terminal() ||
		(v.Power != nil && v.Power.Open) || (v.Options != nil && v.Options.Open) ||
		(v.Entry != nil && len(v.Entry.Pass) > 0) {
		v.idleSince = now
	}
	idle := !now.Before(v.idleSince.Add(5 * time.Minute))
	if idle && !v.idleMode {
		v.printStart = now
	}
	v.idleMode = idle
	return idle
}

// Activity consumes the wake press before any key can edit, paste or submit.
// Releases are filtered by the input owner and never reach this method.
func (v *View) Activity(now time.Time) bool {
	wake := v.Screensaver(now)
	v.idleSince = now
	v.idleMode = false
	if wake {
		v.Reveal.Show(now)
		v.printStart = now
	}
	return wake
}

// StatusLine is the visible error text at now (empty after the 4s window).
func (v *View) StatusLine(now time.Time) string {
	if v.Powering != "" {
		return v.Powering
	}
	if v.Busy {
		return "Authenticating..."
	}
	if v.errMsg == "" {
		return ""
	}
	if !v.errTerm && !now.Before(v.errUntil) {
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
	screensaver := v.Screensaver(now)
	if !screensaver {
		consider(v.idleSince.Add(5 * time.Minute))
		consider(v.Reveal.Deadline())
	}
	if !screensaver && !v.Reduced && v.EntryVisible(now) && !v.Busy && v.Powering == "" {
		consider(now.Truncate(500 * time.Millisecond).Add(500 * time.Millisecond))
	}
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

// DimBackground caps effect luminance so contrast-safe theme ink remains legible.
// Alpha is untouched.
func DimBackground(pix []byte) {
	for i := 0; i+3 < len(pix); i += 4 {
		pix[i] /= 3
		pix[i+1] /= 3
		pix[i+2] /= 3
	}
}

func fillRect(fb *render.Framebuffer, r image.Rectangle, c color.NRGBA) {
	r = r.Intersect(fb.Bounds())
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			i := y*fb.Stride + x*4
			fb.Pix[i], fb.Pix[i+1], fb.Pix[i+2], fb.Pix[i+3] = c.B, c.G, c.R, 0xFF
		}
	}
}
func border(fb *render.Framebuffer, r image.Rectangle, c color.NRGBA, n int) {
	fillRect(fb, image.Rect(r.Min.X, r.Min.Y, r.Max.X, r.Min.Y+n), c)
	fillRect(fb, image.Rect(r.Min.X, r.Max.Y-n, r.Max.X, r.Max.Y), c)
	fillRect(fb, image.Rect(r.Min.X, r.Min.Y, r.Min.X+n, r.Max.Y), c)
	fillRect(fb, image.Rect(r.Max.X-n, r.Min.Y, r.Max.X, r.Max.Y), c)
}

// printLimits reports how many clock cells the print reveal has drawn at now
// (-1: all). done is true once the reveal has finished or motion is reduced;
// the date appears then. The header and rule show immediately.
func (v *View) printLimits(now time.Time, s Scene) (clock int, done bool) {
	if v.Reduced || now.Sub(v.printStart) >= art.PrintDuration {
		return -1, true
	}
	n := art.PrintLimit(now.Sub(v.printStart), art.Total(s.Clock))
	return min(n, art.Total(s.Clock)), false
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
	screensaver := v.Screensaver(now)
	s := Layout(fb.Width, fb.Height, v.Scale, v.StyleName, text, v.Attempts)
	if screensaver {
		s = ScreensaverLayout(fb.Width, fb.Height, v.Scale, v.StyleName, text)
	}
	clockLimit, done := v.printLimits(now, s)
	v.drawLogo(fb, s.Logo, v.artInk(v.banner(), 3))
	if screensaver {
		drawTextBox(fb, s.Banner, s.Banner.Min.Y+s.Banner.Dy()*3/4, "// SEE YOU SPACE COWBOY //", v.textPx(s.Banner.Dy()*3/5, s.Banner), v.artInk(v.banner(), 4.5))
	}
	if s.ClockCW > 0 {
		for _, r := range art.Rects(s.Clock, s.ClockBox.Min, s.ClockCW, 2*s.ClockCW, clockLimit) {
			fillRect(fb, r, v.clockInk())
		}
	} else if clockLimit != 0 {
		size := max(1, s.ClockBox.Dy()*7/10)
		drawTextBox(fb, s.ClockBox, s.ClockBox.Min.Y+s.ClockBox.Dy()*4/5, text, size, v.clockInk())
	}
	if done {
		px := v.textPx(s.DateSize, s.Date)
		date := strings.ToUpper(now.Format("Monday, January 2, 2006"))
		if textWidth(px, date) > s.Date.Dx() {
			date = strings.ToUpper(now.Format("Mon, 02 Jan 2006"))
		}
		drawTextBox(fb, s.Date, s.Date.Min.Y+s.DateSize, date, px, v.dateInk())
	}
	if screensaver {
		return
	}
	visible := v.EntryVisible(now)
	status := v.StatusLine(now)
	dx := art.Jolt(now.Sub(v.joltStart)) * s.Cell
	shift := func(r image.Rectangle) image.Rectangle { return r.Add(image.Pt(dx, 0)) }
	v.drawForm(fb, shift(s.Frame), shift(s.Backing), shift(s.Rule), s.Scale)
	if visible {
		v.drawLabel(fb, shift(s.Label))
		v.drawEntry(fb, shift(s.Entry), shift(s.Indicators), s.Scale, now)
	}
	if status != "" {
		ink := safeInk(panelDanger, v.ground(), 4.5)
		if v.Busy {
			ink = safeInk(panelInk, v.ground(), 4.5)
		}
		line := shift(s.Status)
		drawTextBox(fb, line, line.Min.Y+line.Dy()*3/4, status, v.textPx(line.Dy()*3/5, line), ink)
	}
	if !s.Attempts.Empty() {
		attempts := shift(s.Attempts)
		drawTextBox(fb, attempts, attempts.Min.Y+attempts.Dy()*3/4, "Failed attempts: "+itoa(v.Attempts), v.textPx(s.Attempts.Dy()*3/5, s.Attempts), v.muted())
	}
	if !s.Warning.Empty() {
		for i, text := range []string{"WARNING: Failures may", "lock your account"} {
			warning := shift(s.Warning)
			box := image.Rect(warning.Min.X, warning.Min.Y+i*warning.Dy()/2, warning.Max.X, warning.Min.Y+(i+1)*warning.Dy()/2)
			drawTextBox(fb, box, box.Min.Y+box.Dy()*3/4, text, v.textPx(min(12, int(12*s.Scale)), box), safeInk(panelDanger, v.ground(), 4.5))
		}
	}
	// The guidance and status rows stay on screen at all times, greet-style.
	v.drawAmbient(fb, s)
	v.drawHint(fb, s)
	if v.Power != nil && v.Power.Open {
		v.drawPopup(fb, s, *v.Power)
	}
	if v.Options != nil && v.Options.Open {
		v.drawPopup(fb, s, *v.Options)
	}
}

// drawLabel is greet's input-row label: left-aligned field name in the
// focus colour above the entry field.
func (v *View) drawLabel(fb *render.Framebuffer, r image.Rectangle) {
	if r.Empty() {
		return
	}
	text := strings.TrimSpace(v.Prompt)
	if text == "" {
		text = "Password:"
	}
	drawTextBoxLeft(fb, r, r.Min.Y+r.Dy()*3/4, text, v.textPx(r.Dy()*3/5, r), v.accent())
}

// drawEntry draws the entry field inside the framed form, which owns the
// border and the frost; the field itself stays frameless (greet minimal style).
func (v *View) drawEntry(fb *render.Framebuffer, entry, indicators image.Rectangle, scale float64, now time.Time) {
	inner := entry.Inset(max(2, int(8*scale)))
	sq := max(2, entry.Dy()/4)
	cy := entry.Min.Y + (entry.Dy()-sq)/2
	cursorX := inner.Min.X
	if v.Entry == nil || len(v.Entry.Pass) == 0 {
		drawTextBox(fb, inner, entry.Min.Y+entry.Dy()*2/3, "PASSWORD", v.textPx(entry.Dy()/2, inner), safeInk(panelInk, v.ground(), 4.5))
	} else if v.PromptEcho {
		text := string(v.Entry.Pass)
		px := v.textPx(entry.Dy()/2, inner)
		drawTextBoxLeft(fb, inner, entry.Min.Y+entry.Dy()*2/3, text, px, safeInk(panelInk, v.ground(), 4.5))
		cursorX = inner.Min.X + min(textWidth(px, text), max(0, inner.Dx()-sq))
	} else {
		step := sq * 3 / 2
		n := min(len(v.Entry.Pass), max(1, inner.Dx()/step))
		for i := 0; i < n; i++ {
			fillRect(fb, image.Rect(inner.Min.X+i*step, cy, inner.Min.X+i*step+sq, cy+sq), safeInk(panelInk, v.ground(), 4.5))
		}
		cursorX = inner.Min.X + n*step
	}
	// The greet input carries a blinking block cursor at the typing point.
	if (v.Reduced || (now.UnixMilli()/500)%2 == 0) && cursorX+sq <= inner.Max.X {
		fillRect(fb, image.Rect(cursorX, cy, cursorX+sq, cy+sq), v.accent())
	}
	parts := []string{}
	if v.Caps {
		parts = append(parts, "CAPS LOCK ON")
	}
	if v.Num {
		parts = append(parts, "Num Lock")
	}
	parts = append(parts, v.User)
	if v.Layout != "" {
		parts = append(parts, v.Layout)
	}
	drawTextBox(fb, indicators, indicators.Min.Y+indicators.Dy()*3/4, strings.Join(parts, " • "), v.textPx(indicators.Dy()*3/5, indicators), safeInk(panelInk, v.ground(), 4.5))
}

func (v *View) drawHint(fb *render.Framebuffer, s Scene) {
	if s.Help.Empty() {
		return
	}
	fillRect(fb, s.Help, v.ground())
	if v.Hint == "" {
		return
	}
	box := s.Help.Inset(max(1, s.Help.Dy()/6))
	drawTextBox(fb, box, box.Min.Y+box.Dy()*3/5, v.Hint, v.textPx(14, box), v.muted())
}

func (v *View) drawAmbient(fb *render.Framebuffer, s Scene) {
	if s.Ambient.Empty() || v.Ambient == "" {
		return
	}
	// The row is a compact ASCII chip: bordered ground box, block battery.
	fillRect(fb, s.Ambient, v.ground())
	border(fb, s.Ambient, v.accent(), max(1, int(v.Scale)))
	box := s.Ambient.Inset(max(2, s.Ambient.Dy()/6))
	if box.Empty() {
		return
	}
	px := v.textPx(14, box)
	f := face(px)
	text := strings.Map(func(r rune) rune {
		if _, ok := f.GlyphAdvance(r); !ok {
			return '?'
		}
		return r
	}, v.Ambient)
	drawTextBox(fb, box, box.Min.Y+box.Dy()*3/5, text, px, v.muted())
}

func (v *View) drawPopup(fb *render.Framebuffer, s Scene, p MenuView) {
	box := s.Menu
	if box.Empty() {
		return
	}
	fillRect(fb, box, v.ground())
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
		ink := v.muted()
		if row.Selected {
			fillRect(fb, r, panelDanger)
			ink = safeInk(v.ground(), panelDanger, 4.5)
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
		drawTextBox(fb, inner, y+lineH/2, p.Help, v.textPx(12, inner), v.muted())
	}
}

// drawForm renders the greet-style framed form: theme-primary border, the
// LOCKED rule along the top row, over a solid ground panel.
func (v *View) drawForm(fb *render.Framebuffer, frame, backing, rule image.Rectangle, scale float64) {
	if !backing.Empty() {
		fillRect(fb, backing, v.ground())
	}
	if frame.Empty() {
		return
	}
	border(fb, frame, v.banner(), max(2, int(2*scale)))
	if !rule.Empty() {
		drawTextBox(fb, rule, rule.Min.Y+rule.Dy()*3/4, "────///////LOCKED///////────", v.textPx(rule.Dy()*3/5, rule), v.banner())
	}
}

// drawLogo paints the SYSC wordmark tinted with the banner ink: the asset is
// white on transparent, so its alpha is the mix factor.
func (v *View) drawLogo(fb *render.Framebuffer, r image.Rectangle, tint color.NRGBA) {
	src := art.Logo()
	if src == nil || r.Empty() {
		return
	}
	w, h := r.Dx(), r.Dy()
	if w < 1 || h < 1 {
		return
	}
	// ponytail: retain one size; differing outputs rescale. Move the cache
	// per output if a measured multi-output workload needs multiple sizes.
	if v.logoScaled == nil || v.logoScaled.Bounds().Size() != r.Size() {
		v.logoScaled = image.NewNRGBA(image.Rect(0, 0, w, h))
		xdraw.ApproxBiLinear.Scale(v.logoScaled, v.logoScaled.Bounds(), src, src.Bounds(), xdraw.Over, nil)
	}
	clipped := r.Intersect(fb.Bounds())
	for y := clipped.Min.Y; y < clipped.Max.Y; y++ {
		for x := clipped.Min.X; x < clipped.Max.X; x++ {
			a := uint32(v.logoScaled.Pix[v.logoScaled.PixOffset(x-r.Min.X, y-r.Min.Y)+3])
			if a == 0 {
				continue
			}
			i := y*fb.Stride + x*4
			fb.Pix[i] = uint8((uint32(fb.Pix[i])*(255-a) + uint32(tint.B)*a) / 255)
			fb.Pix[i+1] = uint8((uint32(fb.Pix[i+1])*(255-a) + uint32(tint.G)*a) / 255)
			fb.Pix[i+2] = uint8((uint32(fb.Pix[i+2])*(255-a) + uint32(tint.R)*a) / 255)
			fb.Pix[i+3] = 0xFF
		}
	}
}

// Role inks fall back to the fixed floors when a role is unset (zero palette).
func role(c, fallback color.NRGBA) color.NRGBA {
	if c.A == 0 {
		return fallback
	}
	return c
}
func (v *View) ground() color.NRGBA   { return role(v.Pal.Ground, panelGround) }
func (v *View) banner() color.NRGBA   { return safeInk(role(v.Pal.Banner, panelAccent), v.ground(), 4.5) }
func (v *View) accent() color.NRGBA   { return safeInk(role(v.Pal.Accent, panelAccent), v.ground(), 4.5) }
func (v *View) clockInk() color.NRGBA { return v.artInk(role(v.Pal.ClockInk, panelInk), 3) }
func (v *View) dateInk() color.NRGBA  { return v.artInk(role(v.Pal.DateInk, panelInk), 4.5) }

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

type MenuView struct {
	Open     bool
	Title    string
	Rows     []PowerRow
	Help     string
	Progress int // -1 when no hold is running
}

// PowerView is the old name for the shared popup surface.
type PowerView = MenuView

type PowerRow struct {
	Title    string
	Selected bool
}

// safeInk retains theme colors that meet the actual background contrast floor.
func safeInk(ink, ground color.NRGBA, minimum float64) color.NRGBA {
	if contrast(ink, ground) >= minimum {
		return ink
	}
	white, black := panelInk, color.NRGBA{A: 255}
	if contrast(white, ground) >= contrast(black, ground) {
		return white
	}
	return black
}
func contrast(a, b color.NRGBA) float64 {
	x, y := relativeLuminance(a), relativeLuminance(b)
	return (max(x, y) + .05) / (min(x, y) + .05)
}
func relativeLuminance(c color.NRGBA) float64 {
	linear := func(v uint8) float64 {
		x := float64(v) / 255
		if x <= .04045 {
			return x / 12.92
		}
		return math.Pow((x+.055)/1.055, 2.4)
	}
	return .2126*linear(c.R) + .7152*linear(c.G) + .0722*linear(c.B)
}
func (v *View) muted() color.NRGBA { return safeInk(panelMuted, v.ground(), 4.5) }
func (v *View) artInk(c color.NRGBA, minimum float64) color.NRGBA {
	// Effects/blur are dimmed to <=85 per channel. Use the brighter of that
	// ceiling and the plain theme background, covering every supported pixel.
	worst := color.NRGBA{R: 85, G: 85, B: 85, A: 255}
	if relativeLuminance(v.Pal.Surface) > relativeLuminance(worst) {
		worst = v.Pal.Surface
	}
	if relativeLuminance(c) <= relativeLuminance(worst) || contrast(c, worst) < minimum {
		return panelInk
	}
	return c
}

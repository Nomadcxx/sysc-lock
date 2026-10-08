package lockd

import (
	"image"
	"math"

	"github.com/Nomadcxx/sysc-lock/internal/art"
)

// Scene is the pixel layout of the lock screen for one output: a centred
// stack of SYSC header, clock, date and the framed form with its entry,
// indicator and status lines. Later sub-projects add rows to this stack;
// nothing here is a widget toolkit.
type Scene struct {
	Scale float64
	Cell  int // clock cell width in pixels; also the jolt step
	// Logo and clock share the form's bounded column. Frame and Rule are
	// decorative; compact outputs retain credentials and failure feedback.
	Logo, Header image.Rectangle
	Frame, Rule  image.Rectangle
	// Label is the left-aligned field name above the entry row, greet-style;
	// it survives the decorative frame when the output is compact.
	Label, Identity image.Rectangle
	Clock           []string
	ClockCW         int // 0: the plain style draws text inside ClockBox
	ClockBox        image.Rectangle
	Date            image.Rectangle
	DateSize        int
	// Entry, Indicators and Status are laid out even while the entry is hidden,
	// so revealing it never moves anything.
	Entry, Indicators, Status image.Rectangle
	Attempts, Warning         image.Rectangle
	Banner                    image.Rectangle
	Backing                   image.Rectangle
	// OptionsMenu replaces the form; Menu is the independent power popup.
	// Help follows the form and Ambient belongs to its footer. Ambient, help,
	// header, ambient, frame/identity, date and clock drop before help on short outputs.
	Menu, OptionsMenu image.Rectangle
	Help              image.Rectangle
	Ambient           image.Rectangle
}

// Bounds is the union of everything the scene can draw, jolt excluded.
func (s Scene) Bounds() image.Rectangle {
	return s.Header.Union(s.Logo).Union(s.Frame).Union(s.Label).Union(s.ClockBox).Union(s.Date).Union(s.Backing).Union(s.Menu).Union(s.Help).Union(s.Ambient).Union(s.Banner).Union(s.OptionsMenu)
}

// Layout computes the scene for a width by height pixel output. It is a pure
// function of its arguments. The style steps down to a narrower one, then to
// plain, rather than overflow; decoration drops when the output is
// too short; header decoration drops before credentials and help.
func Layout(width, height int, scale float64, styleName, clockText string, attempts ...int) Scene {
	if scale <= 0 || math.IsNaN(scale) || math.IsInf(scale, 0) {
		scale = 1
	}
	scale = min(scale, 4)
	px := func(n int) int { return max(1, int(float64(n)*scale)) }
	margin := px(8)
	columnW := min(px(520), max(1, width-2*margin))
	style, rows, cw := art.Pick(styleName, clockText, columnW*9/10, height)
	s := Scene{Scale: scale, Clock: rows, ClockCW: cw, Cell: max(px(6), cw)}
	clockH, clockW := len(rows)*2*cw, art.Width(rows)*cw
	if style.Plain() {
		clockH, clockW = px(36), columnW
	}
	logoW := columnW / 2
	logoH := px(96)
	logoGap, clockGap, formGap := px(18), px(10), px(24)
	dateSize, dateH := px(16), px(24)
	lineH, entryH := px(20), px(44)
	padX := min(px(28), max(0, (columnW-px(260))/2))
	padY, ruleH, innerGap := px(12), lineH, px(12)
	identityH, identityGap := lineH, px(8)
	labelH, labelGap := lineH, px(4)
	ambientH, ambientGap := lineH, px(8)
	helpH, helpGap := lineH, px(12)
	attemptH, warningH := 0, 0
	if len(attempts) > 0 && attempts[0] > 0 {
		attemptH = lineH
	}
	if len(attempts) > 0 && attempts[0] >= 3 {
		warningH = 2 * lineH
	}
	headerH := func() int {
		h := clockH + dateH
		if clockH > 0 && dateH > 0 {
			h += clockGap
		}
		if logoH > 0 {
			h += logoH + logoGap
		}
		if h > 0 {
			h += formGap
		}
		return h
	}
	formH := func() int {
		return 2*padY + ruleH + innerGap + identityH + identityGap + labelH + labelGap + entryH + 2*lineH + attemptH + warningH + ambientGap + ambientH
	}
	total := func() int { return headerH() + formH() + helpGap + helpH }
	// Reserve failure feedback before dropping decoration so rejection never
	// changes which rows fit or moves the password field.
	fits := func() bool { return total()+3*lineH-attemptH-warningH <= height-2*margin }
	if !fits() {
		logoH, logoW = 0, 0
	}
	if !fits() {
		ambientH, ambientGap = 0, 0
	}
	if !fits() {
		ruleH, innerGap, padX, padY, identityH, identityGap = 0, 0, 0, 0, 0, 0
	}
	if !fits() {
		dateH, clockGap = 0, 0
	}
	if !fits() {
		clockH, clockW, formGap = 0, 0, 0
		s.Clock, s.ClockCW = nil, 0
	}
	if !fits() {
		helpH, helpGap = 0, 0
	}
	// Extra rejection feedback extends downward without moving the field,
	// unless a compact output needs the space to keep feedback on screen.
	y := max(margin, min((height-total()+attemptH+warningH)/2, height-margin-(total()-attemptH-warningH+3*lineH)))
	if logoH > 0 {
		s.Header = image.Rect((width-columnW)/2, y, (width+columnW)/2, y+logoH)
		legacyH := logoW * art.LogoH / art.LogoW
		s.Logo = image.Rect((width-logoW)/2, y+(logoH-legacyH)/2, (width+logoW)/2, y+(logoH+legacyH)/2)
		y += logoH + logoGap
	}
	if clockH > 0 {
		s.ClockBox = image.Rect((width-clockW)/2, y, (width+clockW)/2, y+clockH)
		y += clockH
	}
	if clockH > 0 && dateH > 0 {
		y += clockGap
	}
	if dateH > 0 {
		s.Date = image.Rect((width-columnW)/2, y, (width+columnW)/2, y+dateH)
		y += dateH
	}
	s.DateSize = dateSize
	if headerH() > 0 {
		y += formGap
	}
	formTop := y
	entryW := columnW - 2*padX
	x := (width - entryW) / 2
	y += padY
	if ruleH > 0 {
		s.Rule = image.Rect(x, y, x+entryW, y+ruleH)
		y += ruleH + innerGap
	}
	if identityH > 0 {
		s.Identity = image.Rect(x, y, x+entryW, y+identityH)
		y += identityH + identityGap
	}
	s.Label = image.Rect(x, y, x+entryW, y+labelH)
	y += labelH + labelGap
	s.Entry = image.Rect(x, y, x+entryW, y+entryH)
	y += entryH
	s.Indicators = image.Rect(x, y, x+entryW, y+lineH)
	y += lineH
	s.Status = image.Rect(x, y, x+entryW, y+lineH)
	y += lineH
	if attemptH > 0 {
		s.Attempts = image.Rect(x, y, x+entryW, y+attemptH)
		y += attemptH
	}
	if warningH > 0 {
		s.Warning = image.Rect(x, y, x+entryW, y+warningH)
		y += warningH
	}
	if ambientH > 0 {
		y += ambientGap
		s.Ambient = image.Rect(x, y, x+entryW, y+ambientH)
		y += ambientH
	}
	s.Backing = image.Rect(x, formTop+padY, x+entryW, y)
	y += padY
	if ruleH > 0 {
		s.Frame = image.Rect(x-padX, formTop, x+entryW+padX, y)
	}
	if helpH > 0 {
		s.Help = image.Rect(x, y+helpGap, x+entryW, y+helpGap+helpH)
	}
	s.OptionsMenu = image.Rect((width-columnW)/2, formTop, (width+columnW)/2, y)
	menuH := min(px(260), max(1, height-2*margin))
	menuY := max(margin, min(formTop, height-margin-menuH))
	s.Menu = image.Rect((width-columnW)/2, menuY, (width+columnW)/2, menuY+menuH)
	return s
}

// ScreensaverLayout retains the larger idle clock and wordmark without the form.
func ScreensaverLayout(width, height int, scale float64, style, text string) Scene {
	s := Layout(width, height, scale, style, text)
	gap := max(4, int(8*s.Scale))
	bannerH := max(16, int(22*s.Scale))
	logoW, logoH := logoSize(width, height, s.Scale)
	chosen, rows, cw := art.Pick(style, text, width, height)
	if !chosen.Plain() {
		cw = max(art.MinCell, cw*3/4)
	}
	s.Clock, s.ClockCW = rows, cw
	clockH, clockW := len(rows)*2*cw, art.Width(rows)*cw
	if chosen.Plain() {
		clockH, clockW = min(max(int(48*s.Scale), height/8), max(1, height/4)), width*4/5
	}
	s.DateSize = max(24, int(24*s.Scale))
	dateH := s.DateSize * 3 / 2
	total := logoH + clockH + dateH + bannerH + 3*gap
	if total > height-2*gap {
		logoH, logoW = 0, 0
		total = clockH + dateH + bannerH + 2*gap
	}
	if total > height-2*gap {
		dateH = 0
		total = clockH + bannerH + gap
	}
	s = Scene{Scale: s.Scale, Cell: s.Cell, Clock: s.Clock, ClockCW: s.ClockCW, DateSize: s.DateSize}
	y := max(gap, (height-total)/2)
	if logoH > 0 {
		s.Logo = image.Rect((width-logoW)/2, y, (width+logoW)/2, y+logoH)
		s.Header = s.Logo
		y += logoH + gap
	}
	s.Banner = image.Rect(gap, y, width-gap, y+bannerH)
	y += bannerH + gap
	s.ClockBox = image.Rect((width-clockW)/2, y, (width+clockW)/2, y+clockH)
	y += clockH + gap
	s.Date = image.Rectangle{}
	if dateH > 0 {
		s.Date = image.Rect(gap, y, width-gap, y+dateH)
	}
	return s
}

func logoSize(width, height int, scale float64) (int, int) {
	px := func(n int) int { return max(1, int(float64(n)*scale)) }
	margin := px(8)
	logoW := min(width*9/10, max(1, width-2*margin))
	logoH := logoW * art.LogoH / art.LogoW
	if capH := max(1, height/5); logoH > capH {
		logoH = capH
		logoW = logoH * art.LogoW / art.LogoH
	}
	if logoH < px(16) || logoW < px(64) {
		logoH, logoW = 0, 0
	}
	return logoW, logoH
}

package lockd

import (
	"image"
	"math"

	"github.com/Nomadcxx/sysc-lock/internal/art"
)

// Scene is the pixel layout of the lock screen for one output: a centred
// stack of wordmark, clock, date and the on-demand entry with its indicator
// and status lines. Later sub-projects add rows to this stack; nothing here is
// a widget toolkit.
type Scene struct {
	Scale    float64
	Cell     int // clock cell width in pixels; also the jolt step
	Wordmark []string
	WordCW   int // 0 when the wordmark does not fit
	WordAt   image.Point
	Clock    []string
	ClockCW  int // 0: the plain style draws text inside ClockBox
	ClockBox image.Rectangle
	Date     image.Rectangle
	DateSize int
	// Entry, Indicators and Status are laid out even while the entry is hidden,
	// so revealing it never moves anything.
	Entry, Indicators, Status image.Rectangle
	Backing                   image.Rectangle
	// Menu is the Power Options popup, centred over the stack, and Help is the
	// bottom hint strip. Both are laid out whether or not they are drawn, so
	// opening the popup never moves anything. Menu is empty only when the
	// output is too small for it; Help is dropped, like the wordmark, when the
	// stack would not fit. Ambient is the one-line status row under Status; it
	// drops first when the stack would not fit, then Help, then the wordmark.
	Menu    image.Rectangle
	Help    image.Rectangle
	Ambient image.Rectangle
}

// Bounds is the union of everything the scene can draw, jolt excluded.
func (s Scene) Bounds() image.Rectangle {
	wm := image.Rectangle{Min: s.WordAt, Max: s.WordAt.Add(image.Pt(art.Width(s.Wordmark)*s.WordCW, len(s.Wordmark)*2*s.WordCW))}
	return wm.Union(s.ClockBox).Union(s.Date).Union(s.Backing).Union(s.Menu).Union(s.Help).Union(s.Ambient)
}

// Layout computes the scene for a width by height pixel output. It is a pure
// function of its arguments. The style steps down to a narrower one, then to
// plain, rather than overflow; the wordmark is dropped before anything else
// when the output is too short.
func Layout(width, height int, scale float64, styleName, clockText string) Scene {
	if scale <= 0 || math.IsNaN(scale) || math.IsInf(scale, 0) {
		scale = 1
	}
	scale = min(scale, 4)
	px := func(n int) int { return max(1, int(float64(n)*scale)) }
	style, rows, cw := art.Pick(styleName, clockText, width, height)
	s := Scene{Scale: scale, Clock: rows, ClockCW: cw}
	clockH, clockW, unit := len(rows)*2*cw, art.Width(rows)*cw, cw
	if style.Plain() {
		clockH = min(max(px(48), height/8), max(1, height/4))
		clockW = width * 4 / 5
		unit = max(px(8), height/60)
	}
	s.Cell = max(unit, px(6))
	wm := art.Wordmark()
	wcw := min(max(art.MinCell, unit/2), width*9/10/max(1, art.Width(wm)))
	wmH := len(wm) * 2 * wcw
	if wcw < art.MinCell {
		wm, wmH, wcw = nil, 0, 0
	}
	gap := 2 * unit
	dateSize := max(px(24), unit*3/2)
	dateH := dateSize * 3 / 2
	entryH := max(px(40), unit*3)
	lineH := px(22)
	margin := px(8)
	helpH := lineH + unit // gap above the strip plus the strip itself
	ambientH := lineH
	total := func() int {
		t := clockH + gap + dateH + 2*gap + entryH + 2*lineH + ambientH + helpH
		if wmH > 0 {
			t += wmH + gap
		}
		return t
	}
	if total() > height-2*margin && ambientH > 0 {
		ambientH = 0 // the ambient row drops before anything else
	}
	if total() > height-2*margin && helpH > 0 {
		helpH = 0 // greet chrome drops before the wordmark
	}
	if total() > height-2*margin && wmH > 0 {
		wm, wmH, wcw = nil, 0, 0
	}
	y := max(margin, (height-total())*2/5)
	s.Wordmark, s.WordCW = wm, wcw
	if wmH > 0 {
		s.WordAt = image.Pt((width-art.Width(wm)*wcw)/2, y)
		y += wmH + gap
	}
	s.ClockBox = image.Rect((width-clockW)/2, y, (width+clockW)/2, y+clockH)
	y += clockH + gap
	s.Date = image.Rect(0, y, width, y+dateH)
	s.DateSize = dateSize
	y += dateH + 2*gap
	entryW := min(max(1, width-2*px(16)), max(px(260), clockW/2))
	x := (width - entryW) / 2
	s.Entry = image.Rect(x, y, x+entryW, y+entryH)
	y += entryH
	s.Indicators = image.Rect(x, y, x+entryW, y+lineH)
	s.Status = image.Rect(x, y+lineH, x+entryW, y+2*lineH)
	s.Backing = image.Rect(x, s.Entry.Min.Y, x+entryW, s.Status.Max.Y).Inset(-px(6))
	if ambientH > 0 {
		s.Ambient = image.Rect(x, y+2*lineH, x+entryW, y+3*lineH)
	}
	if helpH > 0 {
		y += 2*lineH + ambientH + unit
		s.Help = image.Rect(x, y, x+entryW, y+lineH)
	}
	menuW := min(max(px(320), entryW), max(1, width-2*margin))
	menuH := min(px(180), max(1, height-2*margin))
	mx := (width - menuW) / 2
	my := min(s.Entry.Min.Y-(menuH-entryH)/2, height-margin-menuH)
	s.Menu = image.Rect(mx, max(margin, my), mx+menuW, max(margin, my)+menuH)
	return s
}

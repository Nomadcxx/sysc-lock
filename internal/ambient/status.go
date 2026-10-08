package ambient

import (
	"fmt"
	"math"
	"strings"
	"unicode/utf8"
)

// Tone is the semantic ink of a status span. The view maps it to a palette
// colour; the formatter never sees colours.
type Tone uint8

const (
	ToneMuted Tone = iota
	ToneInk
	ToneDim
	ToneAccent
	ToneWarn
	ToneDanger
)

// Span is one run of status text in a single tone.
type Span struct {
	Text string
	Tone Tone
}

// Status is the formatted ambient display: the corner status line, the
// now-playing caption under the date, and a battery alert for the form's
// status row. Empty fields draw nothing.
type Status struct {
	Corner, Caption []Span
	Alert           string
}

// Plain joins span text. Callers measure with it and tests compare with it.
func Plain(spans []Span) string {
	var b strings.Builder
	for _, s := range spans {
		b.WriteString(s.Text)
	}
	return b.String()
}

const (
	lowPct   = 20
	critPct  = 10
	minTitle = 4 // shortest title worth showing, ellipsis included
)

// Status formats the snapshot for the corner and caption budgets, in runes.
// covered reports whether the lock font draws a rune.
func (s Snapshot) Status(cornerRunes, captionRunes int, covered func(rune) bool) Status {
	st := Status{Corner: s.corner(cornerRunes), Caption: s.caption(captionRunes, covered)}
	if s.BatteryPct != nil && s.Power == PowerDischarging && *s.BatteryPct <= critPct {
		st.Alert = fmt.Sprintf("Battery at %d%%. Connect power.", max(0, *s.BatteryPct))
	}
	return st
}

// corner returns the widest variant that fits: temperature, link and a
// ten-cell gauge; then link and ten cells; five cells; no gauge with short
// words; the battery alone. Link is always stated, so no route reads
// "Offline" instead of disappearing.
func (s Snapshot) corner(width int) []Span {
	if width <= 0 {
		return nil
	}
	link := Span{"Offline", ToneWarn}
	switch s.Link {
	case LinkWifi:
		link = Span{"Wi-Fi", ToneMuted}
	case LinkWired:
		link = Span{"Wired", ToneMuted}
	}
	var temp []Span
	if s.Temp != nil {
		temp = []Span{{fmt.Sprintf("%.0f°", math.Round(*s.Temp)), ToneMuted}}
	}
	for _, groups := range [][][]Span{
		{temp, {link}, s.battery(10, false)},
		{{link}, s.battery(10, false)},
		{{link}, s.battery(5, false)},
		{{link}, s.battery(0, true)},
		{s.battery(0, true)},
	} {
		if line := join(groups); len(line) > 0 && utf8.RuneCountInString(Plain(line)) <= width {
			return line
		}
	}
	return nil
}

// battery is the gauge, percentage and state word. cells 0 drops the gauge;
// short writes charging as "+" and drops the full and plugged words.
func (s Snapshot) battery(cells int, short bool) []Span {
	if s.BatteryPct == nil {
		return nil
	}
	pct := max(0, min(100, *s.BatteryPct))
	tone, word := ToneMuted, Span{}
	switch {
	case s.Power == PowerDischarging && pct <= critPct:
		tone, word = ToneDanger, Span{" LOW", ToneDanger}
	case s.Power == PowerDischarging && pct <= lowPct:
		tone, word = ToneWarn, Span{" LOW", ToneWarn}
	case s.Power == PowerCharging:
		tone, word = ToneAccent, Span{" charging", ToneAccent}
		if short {
			word.Text = " +"
		}
	case s.Power == PowerFull && !short:
		word = Span{" full", ToneAccent}
	case s.Power == PowerPlugged && !short:
		word = Span{" plugged", ToneMuted}
	}
	pctTone := ToneInk
	if tone == ToneWarn || tone == ToneDanger {
		pctTone = tone
	}
	var out []Span
	if cells > 0 {
		out = append(out, Span{gauge(pct, cells), tone}, Span{" ", ToneMuted})
	}
	out = append(out, Span{fmt.Sprintf("%d%%", pct), pctTone})
	if word.Text != "" {
		out = append(out, word)
	}
	return out
}

// gauge renders a block gauge such as [████░░░░░░]; each cell rounds.
func gauge(pct, cells int) string {
	filled := max(0, min(cells, (pct*cells+50)/100))
	return "[" + strings.Repeat("█", filled) + strings.Repeat("░", cells-filled) + "]"
}

func join(groups [][]Span) []Span {
	var out []Span
	for _, g := range groups {
		if len(g) == 0 {
			continue
		}
		if len(out) > 0 {
			out = append(out, Span{Sep, ToneDim})
		}
		out = append(out, g...)
	}
	return out
}

// caption is "♪ Title — Artist" while playing and "paused · Title" while
// paused. The artist drops before the title shortens; a title the font
// cannot draw yields to the artist.
func (s Snapshot) caption(width int, covered func(rune) bool) []Span {
	if width <= 0 {
		return nil
	}
	title, artist := cleanMetadata(s.Title), cleanMetadata(s.Artist)
	if !drawable(artist, covered) {
		artist = ""
	}
	if !drawable(title, covered) {
		title = ""
	}
	if title == "" {
		title, artist = artist, ""
	}
	switch s.Media {
	case Playing:
		if title == "" {
			title = Playing
		}
		note := Span{"♪ ", ToneAccent}
		if artist != "" && 2+utf8.RuneCountInString(title)+3+utf8.RuneCountInString(artist) <= width {
			return []Span{note, {title, ToneInk}, {" — ", ToneDim}, {artist, ToneMuted}}
		}
		if width < 2+minTitle && utf8.RuneCountInString(title) > width-2 {
			return nil
		}
		return []Span{note, {fit(title, width-2), ToneInk}}
	case Paused:
		const lead = "paused · "
		n := utf8.RuneCountInString(lead)
		if title == "" || width < n+minTitle && utf8.RuneCountInString(title) > width-n {
			return nil
		}
		return []Span{{lead, ToneDim}, {fit(title, width-n), ToneDim}}
	}
	return nil
}

// drawable reports whether at least half of text's runes are in the lock
// font. The view still swaps an odd missing glyph for '?'.
func drawable(text string, covered func(rune) bool) bool {
	total, missing := 0, 0
	for _, r := range text {
		total++
		if covered != nil && !covered(r) {
			missing++
		}
	}
	return total > 0 && missing*2 <= total
}

func fit(text string, width int) string {
	r := []rune(text)
	if len(r) <= width {
		return text
	}
	if width < 2 {
		return ""
	}
	return string(r[:width-1]) + "…"
}

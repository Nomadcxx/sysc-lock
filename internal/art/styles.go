package art

import (
	_ "embed"
	"strings"
)

const (
	DefaultStyle = "kompaktblk"
	Plain        = "plain"
)

//go:embed wordmark.txt
var wordmark string

// Wordmark returns the sysc-lock wordmark rows.
func Wordmark() []string { return strings.Split(strings.TrimRight(wordmark, "\n"), "\n") }

// Style is one selectable clock style. plain has no glyph table.
type Style struct {
	Name   string
	Rows   int
	glyphs map[rune][]string
}

// Names lists the selectable styles.
func Names() []string {
	return []string{"kompaktblk", "phm_blocky_reverse", "phmvga", "phm_slanted", Plain}
}

// Lookup returns the named style, or the default when the name is unknown.
func Lookup(name string) Style {
	if name == Plain {
		return Style{Name: Plain}
	}
	if g, ok := tables[name]; ok {
		return Style{Name: name, Rows: len(g['0']), glyphs: g}
	}
	return Lookup(DefaultStyle)
}

func (s Style) Plain() bool { return s.glyphs == nil }

// Compose joins the glyph rows of text. A rune without a glyph is a blank
// cell column. Glyphs are padded to their own widest row.
func (s Style) Compose(text string) []string {
	if s.Plain() {
		return nil
	}
	rows := make([]string, s.Rows)
	for _, r := range text {
		g, ok := s.glyphs[r]
		if !ok {
			g = s.glyphs[' ']
		}
		w := Width(g)
		for i := range rows {
			line := ""
			if i < len(g) {
				line = g[i]
			}
			rows[i] += line + strings.Repeat(" ", w-len([]rune(line)))
		}
	}
	return rows
}

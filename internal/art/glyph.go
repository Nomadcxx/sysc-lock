// Package art is the pure geometry behind the lock screen's block-ASCII look:
// glyph tables, fitting and reveal timing. It returns pixel rectangles and
// draws nothing, so it needs no Wayland, PAM or framebuffer code.
package art

import "image"

// Block is a filled rectangle inside one cell, in eighths of the cell.
type Block struct{ X0, Y0, X1, Y1 int }

var blocks = map[rune]Block{
	'█': {0, 0, 8, 8},
	'▀': {0, 0, 8, 4},
	'▄': {0, 4, 8, 8},
	'▌': {0, 0, 4, 8},
	'▐': {4, 0, 8, 8},
	'▘': {0, 0, 4, 4},
	'▖': {0, 4, 4, 8},
	'■': {2, 2, 6, 6},
}

// Supported reports whether r is a space or a block this package can draw.
func Supported(r rune) bool {
	if r == ' ' {
		return true
	}
	_, ok := blocks[r]
	return ok
}

// Width is the widest row of rows, in cells.
func Width(rows []string) int {
	w := 0
	for _, r := range rows {
		w = max(w, len([]rune(r)))
	}
	return w
}

// Total counts the drawn (non-blank, supported) cells of rows.
func Total(rows []string) int {
	n := 0
	for _, r := range rows {
		for _, c := range r {
			if _, ok := blocks[c]; ok {
				n++
			}
		}
	}
	return n
}

// Rects returns the filled pixel rectangles for rows. Each cell is cw by ch
// pixels and rows start at origin. Edges are computed from the cell origin so
// neighbouring cells share an exact pixel edge. limit caps the number of
// drawn cells in reading order; a negative limit draws everything.
func Rects(rows []string, origin image.Point, cw, ch, limit int) []image.Rectangle {
	var out []image.Rectangle
	drawn := 0
	for y, row := range rows {
		for x, c := range []rune(row) {
			b, ok := blocks[c]
			if !ok {
				continue
			}
			if limit >= 0 && drawn >= limit {
				return out
			}
			drawn++
			cx, cy := origin.X+x*cw, origin.Y+y*ch
			out = append(out, image.Rect(cx+b.X0*cw/8, cy+b.Y0*ch/8, cx+b.X1*cw/8, cy+b.Y1*ch/8))
		}
	}
	return out
}

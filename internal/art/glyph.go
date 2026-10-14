// Package art is the pure geometry behind the lock screen's block-ASCII look:
// glyph tables, fitting and reveal timing. It returns pixel rectangles and
// draws nothing, so it needs no Wayland, PAM or framebuffer code.
package art

import (
	"image"
	"math"
)

// Block is a filled rectangle inside one cell, in eighths of the cell.
type Block struct{ X0, Y0, X1, Y1 int }

var blocks = map[rune][]Block{
	'█': {{0, 0, 8, 8}},
	'▀': {{0, 0, 8, 4}},
	'▄': {{0, 4, 8, 8}},
	'▌': {{0, 0, 4, 8}},
	'▐': {{4, 0, 8, 8}},
	'▘': {{0, 0, 4, 4}},
	'▖': {{0, 4, 4, 8}},
	'■': {{2, 2, 6, 6}},
	// Octants and quarters follow Unicode 17 names, numbered left-to-right,
	// top-to-bottom. Coordinates are eighths of the cell, y down.
	'𜴅': {{0, 0, 4, 2}, {4, 0, 8, 2}, {4, 2, 8, 4}},
	'𜴈': {{4, 0, 8, 2}, {0, 2, 4, 4}, {4, 2, 8, 4}},
	'𜴉': {{0, 4, 4, 6}},
	'𜴊': {{0, 0, 4, 2}, {0, 4, 4, 6}},
	'𜴍': {{0, 0, 4, 2}, {0, 2, 4, 4}, {0, 4, 4, 6}},
	'𜴖': {{4, 0, 8, 2}, {0, 2, 4, 4}, {4, 2, 8, 4}, {0, 4, 4, 6}},
	'𜴘': {{4, 4, 8, 6}},
	'𜴛': {{0, 0, 4, 2}, {4, 0, 8, 2}, {4, 4, 8, 6}},
	'𜴡': {{4, 0, 8, 2}, {4, 2, 8, 4}, {4, 4, 8, 6}},
	'𜴣': {{0, 2, 4, 4}, {4, 2, 8, 4}, {4, 4, 8, 6}},
	'𜴦': {{0, 0, 4, 2}, {4, 0, 8, 2}, {0, 2, 4, 4}, {4, 2, 8, 4}, {4, 4, 8, 6}},
	'𜴧': {{0, 4, 4, 6}, {4, 4, 8, 6}},
	'𜴫': {{0, 2, 4, 4}, {0, 4, 4, 6}, {4, 4, 8, 6}},
	'𜴬': {{0, 0, 4, 2}, {0, 2, 4, 4}, {0, 4, 4, 6}, {4, 4, 8, 6}},
	'𜴮': {{0, 0, 4, 2}, {4, 0, 8, 2}, {0, 2, 4, 4}, {0, 4, 4, 6}, {4, 4, 8, 6}},
	'𜴯': {{4, 2, 8, 4}, {0, 4, 4, 6}, {4, 4, 8, 6}},
	'𜴱': {{4, 0, 8, 2}, {4, 2, 8, 4}, {0, 4, 4, 6}, {4, 4, 8, 6}},
	'𜵈': {{0, 2, 4, 4}, {0, 4, 4, 6}, {0, 6, 4, 8}},
	'𜵬': {{0, 0, 4, 2}, {4, 0, 8, 2}, {4, 2, 8, 4}, {0, 4, 4, 6}, {4, 4, 8, 6}, {0, 6, 4, 8}},
	'𜵳': {{0, 0, 4, 2}, {4, 0, 8, 2}, {4, 6, 8, 8}},
	'𜵷': {{0, 0, 4, 2}, {4, 0, 8, 2}, {0, 2, 4, 4}, {4, 6, 8, 8}},
	'𜶘': {{0, 0, 4, 2}, {4, 0, 8, 2}, {4, 2, 8, 4}, {4, 4, 8, 6}, {4, 6, 8, 8}},
	'𜶚': {{0, 0, 4, 2}, {0, 2, 4, 4}, {4, 2, 8, 4}, {4, 4, 8, 6}, {4, 6, 8, 8}},
	'𜶛': {{4, 0, 8, 2}, {0, 2, 4, 4}, {4, 2, 8, 4}, {4, 4, 8, 6}, {4, 6, 8, 8}},
	'𜶪': {{4, 0, 8, 2}, {0, 2, 4, 4}, {4, 2, 8, 4}, {0, 4, 4, 6}, {4, 4, 8, 6}, {4, 6, 8, 8}},
	'𜶬': {{0, 0, 4, 2}, {0, 6, 4, 8}, {4, 6, 8, 8}},
	'𜶮': {{0, 0, 4, 2}, {4, 0, 8, 2}, {0, 6, 4, 8}, {4, 6, 8, 8}},
	'𜶲': {{0, 0, 4, 2}, {4, 0, 8, 2}, {0, 2, 4, 4}, {0, 6, 4, 8}, {4, 6, 8, 8}},
	'𜷂': {{0, 0, 4, 2}, {4, 0, 8, 2}, {0, 2, 4, 4}, {0, 4, 4, 6}, {0, 6, 4, 8}, {4, 6, 8, 8}},
	'𜷋': {{4, 4, 8, 6}, {0, 6, 4, 8}, {4, 6, 8, 8}},
	'𜷖': {{0, 0, 4, 2}, {4, 0, 8, 2}, {4, 2, 8, 4}, {4, 4, 8, 6}, {0, 6, 4, 8}, {4, 6, 8, 8}},
	'𜷘': {{0, 0, 4, 2}, {0, 2, 4, 4}, {4, 2, 8, 4}, {4, 4, 8, 6}, {0, 6, 4, 8}, {4, 6, 8, 8}},
	'𜷞': {{0, 2, 4, 4}, {0, 4, 4, 6}, {4, 4, 8, 6}, {0, 6, 4, 8}, {4, 6, 8, 8}},
	'𜷡': {{4, 2, 8, 4}, {0, 4, 4, 6}, {4, 4, 8, 6}, {0, 6, 4, 8}, {4, 6, 8, 8}},
	'𜷥': {{4, 0, 8, 2}, {0, 2, 4, 4}, {4, 2, 8, 4}, {0, 4, 4, 6}, {4, 4, 8, 6}, {0, 6, 4, 8}, {4, 6, 8, 8}},
	'𜺨': {{0, 0, 4, 2}},
	'𜺫': {{4, 0, 8, 2}},
	'🮂': {{0, 0, 8, 2}},
	'🮅': {{0, 0, 8, 6}},
	'🯦': {{0, 2, 4, 4}},
	'🯧': {{4, 2, 8, 4}},
}

// Diagonal endpoints follow Unicode 17 Symbols for Legacy Computing names.
// Scan conversion uses pixel centres, preserving complementary cuts at odd sizes.
type diagonal struct {
	x0, y0, x1, y1 int
	left           bool
}

var diagonals = map[rune]diagonal{
	'🬾': {0, 2, 4, 8, true},
	'🭀': {0, 0, 4, 8, true},
	'🭁': {4, 0, 0, 2, false},
	'🭂': {8, 0, 0, 2, false},
	'🭃': {4, 0, 0, 6, false},
	'🭄': {8, 0, 0, 6, false},
	'🭅': {4, 0, 0, 8, false},
	'🭆': {8, 2, 0, 6, false},
	'🭇': {8, 6, 4, 8, false},
	'🭈': {8, 6, 0, 8, false},
	'🭉': {8, 2, 4, 8, false},
	'🭊': {8, 2, 0, 8, false},
	'🭋': {8, 0, 4, 8, false},
	'🭍': {0, 0, 8, 2, true},
	'🭐': {4, 0, 8, 8, true},
	'🭓': {0, 6, 8, 8, false},
	'🭔': {0, 2, 4, 8, false},
	'🭗': {4, 0, 0, 2, true},
	'🭘': {8, 0, 0, 2, true},
	'🭙': {4, 0, 0, 6, true},
	'🭚': {8, 0, 0, 6, true},
	'🭛': {4, 0, 0, 8, true},
	'🭜': {8, 2, 0, 6, true},
	'🭝': {8, 6, 4, 8, true},
	'🭞': {8, 6, 0, 8, true},
	'🭟': {8, 2, 4, 8, true},
	'🭠': {8, 2, 0, 8, true},
	'🭡': {8, 0, 4, 8, true},
	'🭣': {0, 0, 8, 2, false},
	'🭤': {4, 0, 8, 6, false},
}

// Supported reports whether r is a space or a block this package can draw.
func Supported(r rune) bool {
	if r == ' ' {
		return true
	}
	_, block := blocks[r]
	_, wedge := diagonals[r]
	return block || wedge || r == '🭪'
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
			if c != ' ' && Supported(c) {
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
			bs := blocks[c]
			if c == ' ' || !Supported(c) {
				continue
			}
			if limit >= 0 && drawn >= limit {
				return out
			}
			drawn++
			cx, cy := origin.X+x*cw, origin.Y+y*ch
			if d, ok := diagonals[c]; ok || c == '🭪' {
				for row := 0; row < ch; row++ {
					var edge float64
					if c == '🭪' {
						edge = float64(cw) * (0.5 + math.Abs((float64(row)+0.5)/float64(ch)-0.5))
						d.left = true
					} else {
						edge = float64(cw) / 8 * (float64(d.x0) + ((float64(row)+0.5)*8/float64(ch)-float64(d.y0))*float64(d.x1-d.x0)/float64(d.y1-d.y0))
					}
					cut := max(0, min(cw, int(math.Ceil(edge-0.5))))
					lo, hi := cut, cw
					if d.left {
						lo, hi = 0, cut
					}
					if lo < hi {
						out = append(out, image.Rect(cx+lo, cy+row, cx+hi, cy+row+1))
					}
				}
				continue
			}
			for _, b := range bs {
				out = append(out, image.Rect(cx+b.X0*cw/8, cy+b.Y0*ch/8, cx+b.X1*cw/8, cy+b.Y1*ch/8))
			}
		}
	}
	return out
}

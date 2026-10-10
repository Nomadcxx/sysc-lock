package lockd

import (
	"image"
	"image/color"

	"github.com/Nomadcxx/sysc-lock/internal/render"
	"github.com/mattn/go-runewidth"
	"golang.org/x/image/font"
	"golang.org/x/image/math/fixed"
)

// Header artwork is drawn the way greet's kitty draws it: on an integer cell
// grid from the font's own line metrics, with block elements and light box
// lines filled geometrically so adjacent cells tile without seams.

// cellSize is the terminal cell for font size px: the rounded advance of 'M'
// by ascent plus descent, with the baseline at the ascent.
func cellSize(px int) (w, h, base int) {
	f := face(px)
	adv, _ := f.GlyphAdvance('M')
	m := f.Metrics()
	return max(1, adv.Round()), max(1, (m.Ascent + m.Descent).Round()), m.Ascent.Round()
}

// drawCells draws rows from at, one terminal cell per column, clipped to clip.
func drawCells(fb *render.Framebuffer, clip image.Rectangle, at image.Point, rows []string, px int, ink color.NRGBA) {
	cw, ch, base := cellSize(px)
	d := font.Drawer{Dst: clippedText{fb, clip.Intersect(fb.Bounds())}, Src: image.NewUniform(ink), Face: face(px)}
	for i, row := range rows {
		x, y := at.X, at.Y+i*ch
		for _, r := range row {
			cell := image.Rect(x, y, x+cw, y+ch)
			if r != ' ' && !drawBlockCell(fb, cell.Intersect(clip), cell, r, ink) {
				d.Dot = fixed.P(x, y+base)
				d.DrawString(string(r))
			}
			x += cw * runewidth.RuneWidth(r)
		}
	}
}

// Light box-drawing arms.
const (
	armL = 1 << iota
	armR
	armU
	armD
)

var lightBox = map[rune]uint8{
	'─': armL | armR, '│': armU | armD,
	'┌': armR | armD, '┐': armL | armD, '└': armU | armR, '┘': armU | armL,
	'├': armU | armD | armR, '┤': armU | armD | armL,
	'┬': armL | armR | armD, '┴': armL | armR | armU, '┼': armL | armR | armU | armD,
	'╴': armL, '╵': armU, '╶': armR, '╷': armD,
}

// Quadrant blocks U+2596..U+259F as upper-left, upper-right, lower-left,
// lower-right bits.
var quadrants = [...]uint8{4, 8, 1, 1 | 4 | 8, 1 | 8, 1 | 2 | 4, 1 | 2 | 8, 2, 2 | 4, 2 | 4 | 8}

// drawBlockCell fills r's cell geometry for block elements and light box
// lines, reporting false for runes the font should draw.
func drawBlockCell(fb *render.Framebuffer, clip, cell image.Rectangle, r rune, ink color.NRGBA) bool {
	w, h := cell.Dx(), cell.Dy()
	eighths := func(x0, y0, x1, y1 int) image.Rectangle {
		return image.Rect(cell.Min.X+w*x0/8, cell.Min.Y+h*y0/8, cell.Min.X+w*x1/8, cell.Min.Y+h*y1/8).Intersect(clip)
	}
	switch {
	case r == '▀':
		fillRect(fb, eighths(0, 0, 8, 4), ink)
	case r >= '▁' && r <= '█':
		fillRect(fb, eighths(0, 8-int(r-'▀'), 8, 8), ink)
	case r >= '▉' && r <= '▏':
		fillRect(fb, eighths(0, 0, int('▐'-r), 8), ink)
	case r == '▐':
		fillRect(fb, eighths(4, 0, 8, 8), ink)
	case r >= '░' && r <= '▓':
		blendRect(fb, cell.Intersect(clip), ink, uint8(64*int(r-'▐')-1))
	case r == '▔':
		fillRect(fb, eighths(0, 0, 8, 1), ink)
	case r == '▕':
		fillRect(fb, eighths(7, 0, 8, 8), ink)
	case r >= '▖' && r <= '▟':
		q := quadrants[r-'▖']
		for bit, sub := range [4]image.Rectangle{eighths(0, 0, 4, 4), eighths(4, 0, 8, 4), eighths(0, 4, 4, 8), eighths(4, 4, 8, 8)} {
			if q&(1<<bit) != 0 {
				fillRect(fb, sub, ink)
			}
		}
	default:
		arms, ok := lightBox[r]
		if !ok {
			return false
		}
		t := max(1, h/16)
		cx, cy := cell.Min.X+(w-t)/2, cell.Min.Y+(h-t)/2
		for bit, arm := range [4]image.Rectangle{
			image.Rect(cell.Min.X, cy, cx+t, cy+t), image.Rect(cx, cy, cell.Max.X, cy+t),
			image.Rect(cx, cell.Min.Y, cx+t, cy+t), image.Rect(cx, cy, cx+t, cell.Max.Y),
		} {
			if arms&(1<<bit) != 0 {
				fillRect(fb, arm.Intersect(clip), ink)
			}
		}
	}
	return true
}

// blendRect composites c over r at alpha a, for the shade blocks.
func blendRect(fb *render.Framebuffer, r image.Rectangle, c color.NRGBA, a uint8) {
	r = r.Intersect(fb.Bounds())
	mix := func(d, s uint8) uint8 { return uint8((int(d)*(255-int(a)) + int(s)*int(a)) / 255) }
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			i := y*fb.Stride + x*4
			fb.Pix[i], fb.Pix[i+1], fb.Pix[i+2], fb.Pix[i+3] = mix(fb.Pix[i], c.B), mix(fb.Pix[i+1], c.G), mix(fb.Pix[i+2], c.R), 0xFF
		}
	}
}

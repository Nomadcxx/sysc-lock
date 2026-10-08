package lockd

import (
	"context"
	"github.com/godbus/dbus/v5"
	"image"
	"image/color"
	"math"
	"os"
	"strings"
	"time"

	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gomono"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"

	"github.com/Nomadcxx/sysc-lock/internal/ambient"
	"github.com/Nomadcxx/sysc-lock/internal/render"
)

// ponytail: gofont + opentype face per size (cached) instead of the full
// go-text/typesetting shaping pipeline. ASCII-first password prompts don't
// need complex shaping; add typesetting when non-Latin usernames appear.

var (
	parsed   *opentype.Font
	faceCach = map[int]font.Face{}
)

// fontPaths is where Fira Code lands on common layouts; greet renders in the
// terminal, and the shipping kitty config is Fira Code, so the lock screen
// matches the greeter. Missing system font falls back to the embedded Go Mono.
// ponytail: fixed path list; consult kitty.conf/fontconfig when a user's
// custom terminal font needs to match too.
var fontPaths = []string{
	"/usr/share/fonts/TTF/FiraCode-Regular.ttf",
	"/usr/share/fonts/truetype/firacode/FiraCode-Regular.ttf",
	"/usr/share/fonts/opentype/firacode/FiraCode-Regular.ttf",
	"/usr/local/share/fonts/FiraCode-Regular.ttf",
}

func fontBytes() []byte {
	for _, p := range fontPaths {
		if b, err := os.ReadFile(p); err == nil {
			return b
		}
	}
	return gomono.TTF
}

func face(px int) font.Face {
	if f, ok := faceCach[px]; ok {
		return f
	}
	if parsed == nil {
		f, err := opentype.Parse(fontBytes())
		if err != nil {
			panic("gofont: " + err.Error())
		}
		parsed = f
	}
	f, err := opentype.NewFace(parsed, &opentype.FaceOptions{Size: float64(px), DPI: 72, Hinting: font.HintingFull})
	if err != nil {
		panic("opentype face: " + err.Error())
	}
	// ponytail: retain 32 font sizes; a larger cache needs measured output-scale demand.
	if len(faceCach) >= 32 {
		for size, old := range faceCach {
			_ = old.Close()
			delete(faceCach, size)
			break
		}
	}
	faceCach[px] = f
	return f
}

// drawText draws s with its baseline at y and horizontal anchor cx: centered
// normally; when cx == 8 it is left-aligned; when cx == fb.Width-8 it is
// right-aligned (callers in view.go follow that convention).
func drawText(fb *render.Framebuffer, cx, y int, s string, px int, col color.NRGBA) {
	if s == "" {
		return
	}
	f := face(px)
	w := font.MeasureString(f, s)
	ww := w.Ceil()
	px0 := cx - ww/2
	if cx == 8 {
		px0 = 8
	} else if cx == fb.Width-8 {
		px0 = fb.Width - 8 - ww
	}
	d := &font.Drawer{
		Dst:  fb,
		Src:  image.NewUniform(col),
		Face: f,
		Dot:  fixed.P(px0, y),
	}
	d.DrawString(s)
}

// SystemTextScale reads the desktop's existing setting once, before acquisition.
// A missing portal/setting uses the built-in scale, never a session-lock dependency.
func SystemTextScale() float64 {
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return 1
	}
	defer conn.Close()
	var value dbus.Variant
	err = conn.Object("org.freedesktop.portal.Desktop", "/org/freedesktop/portal/desktop").CallWithContext(ctx, "org.freedesktop.portal.Settings.Read", 0, "org.gnome.desktop.interface", "text-scaling-factor").Store(&value)
	if err != nil {
		return 1
	}
	if nested, ok := value.Value().(dbus.Variant); ok {
		value = nested
	}
	scale, ok := value.Value().(float64)
	if !ok || math.IsNaN(scale) || math.IsInf(scale, 0) || scale <= 0 {
		return 1
	}
	return max(.75, min(2, scale))
}

type clippedText struct {
	*render.Framebuffer
	clip image.Rectangle
}

func (dst clippedText) Bounds() image.Rectangle { return dst.clip }
func (dst clippedText) Set(x, y int, col color.Color) {
	if image.Pt(x, y).In(dst.clip) {
		dst.Framebuffer.Set(x, y, col)
	}
}

// drawTextBox clips glyph ink as well as advance widths, including overhangs.
func drawTextBox(fb *render.Framebuffer, box image.Rectangle, baseline int, text string, px int, col color.NRGBA) {
	drawTextBoxAlign(fb, box, baseline, text, px, col, false)
}

// drawTextBoxLeft draws like drawTextBox but anchors the text to the box's
// left edge, which is how greet labels its input rows.
func drawTextBoxLeft(fb *render.Framebuffer, box image.Rectangle, baseline int, text string, px int, col color.NRGBA) {
	drawTextBoxAlign(fb, box, baseline, text, px, col, true)
}

// textWidth is the pixel width of s at font size px.
func textWidth(px int, s string) int {
	return font.MeasureString(face(px), s).Ceil()
}

func drawTextBoxAlign(fb *render.Framebuffer, box image.Rectangle, baseline int, text string, px int, col color.NRGBA, left bool) {
	box = box.Intersect(fb.Bounds())
	if box.Empty() || text == "" {
		return
	}
	f := face(px)
	// ponytail: text fitting is bounded by 256 visible glyphs; add shaping for complex scripts.
	runes := make([]rune, 0, 64)
	width := fixed.Int26_6(0)
	reserve := font.MeasureString(f, "…")
	for _, r := range text {
		advance, _ := f.GlyphAdvance(r)
		if len(runes) > 0 {
			advance += f.Kern(runes[len(runes)-1], r)
		}
		if len(runes) >= 256 || (width+advance+reserve).Ceil() > box.Dx() {
			runes = append(runes, '…')
			break
		}
		width += advance
		runes = append(runes, r)
	}
	text = string(runes)
	width = font.MeasureString(f, text)
	x := box.Min.X + (box.Dx()-width.Ceil())/2
	if left {
		x = box.Min.X
	}
	d := font.Drawer{Dst: clippedText{fb, box}, Src: image.NewUniform(col), Face: f, Dot: fixed.P(x, baseline)}
	d.DrawString(text)
}

// Covered reports whether the lock font draws r. The owner hands it to the
// ambient formatter, so metadata the font cannot show is replaced rather than
// drawn as '?'.
func Covered(r rune) bool {
	_, ok := face(14).GlyphAdvance(r)
	return ok
}

// drawRuns draws styled spans on one baseline inside box, right-aligned when
// right is set and centred otherwise. Glyphs the face lacks become '?'; ink
// is clipped to box like drawTextBox.
func drawRuns(fb *render.Framebuffer, box image.Rectangle, baseline int, spans []ambient.Span, px int, right bool, ink func(ambient.Tone) color.NRGBA) {
	box = box.Intersect(fb.Bounds())
	if box.Empty() || len(spans) == 0 {
		return
	}
	f := face(px)
	texts := make([]string, len(spans))
	width := fixed.Int26_6(0)
	for i, sp := range spans {
		texts[i] = strings.Map(func(r rune) rune {
			if _, ok := f.GlyphAdvance(r); !ok {
				return '?'
			}
			return r
		}, sp.Text)
		width += font.MeasureString(f, texts[i])
	}
	x := box.Min.X + (box.Dx()-width.Ceil())/2
	if right {
		x = box.Max.X - width.Ceil()
	}
	d := font.Drawer{Dst: clippedText{fb, box}, Face: f, Dot: fixed.P(max(x, box.Min.X), baseline)}
	for i, sp := range spans {
		d.Src = image.NewUniform(ink(sp.Tone))
		d.DrawString(texts[i])
	}
}

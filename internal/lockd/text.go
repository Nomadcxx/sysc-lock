package lockd

import (
	"context"
	"github.com/godbus/dbus/v5"
	"image"
	"image/color"
	"math"
	"time"

	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gomono"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"

	"github.com/Nomadcxx/sysc-lock/internal/render"
)

// ponytail: gofont + opentype face per size (cached) instead of the full
// go-text/typesetting shaping pipeline. ASCII-first password prompts don't
// need complex shaping; add typesetting when non-Latin usernames appear.

var (
	parsed   *opentype.Font
	faceCach = map[int]font.Face{}
)

func face(px int) font.Face {
	if f, ok := faceCach[px]; ok {
		return f
	}
	if parsed == nil {
		f, err := opentype.Parse(gomono.TTF)
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
	d := font.Drawer{Dst: clippedText{fb, box}, Src: image.NewUniform(col), Face: f, Dot: fixed.P(box.Min.X+(box.Dx()-width.Ceil())/2, baseline)}
	d.DrawString(text)
}

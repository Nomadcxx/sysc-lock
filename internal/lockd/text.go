package lockd

import (
	"image"
	"image/color"

	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/goregular"
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
		f, err := opentype.Parse(goregular.TTF)
		if err != nil {
			panic("gofont: " + err.Error())
		}
		parsed = f
	}
	f, err := opentype.NewFace(parsed, &opentype.FaceOptions{Size: float64(px), DPI: 72, Hinting: font.HintingFull})
	if err != nil {
		panic("opentype face: " + err.Error())
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
		Dst:  &image.RGBA{Pix: fb.Pix, Stride: fb.Stride, Rect: image.Rect(0, 0, fb.Width, fb.Height)},
		Src:  image.NewUniform(col),
		Face: f,
		Dot:  fixed.P(px0, y),
	}
	d.DrawString(s)
}

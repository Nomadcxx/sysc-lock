// Package render provides the CPU RGBA framebuffer the locker attaches to its
// lock surfaces: solid fills, centered text is drawn by the UI layer, and
// static wallpaper backgrounds scaled-to-cover with center crop.
package render

import (
	"image"
	"image/color"
	_ "image/jpeg" // background decode
	"image/png"
	"math"
	"os"

	xdraw "golang.org/x/image/draw"
)

// Framebuffer is a wl_shm-ready RGBA buffer (XRGB8888 little-endian).
type Framebuffer struct {
	Width, Height int
	Stride        int
	Pix           []byte
}

func New(w, h int) *Framebuffer {
	if w < 1 {
		w = 1
	}
	if h < 1 {
		h = 1
	}
	return &Framebuffer{Width: w, Height: h, Stride: w * 4, Pix: make([]byte, w*4*h)}
}

// Fill paints the whole buffer with c (opaque assumed; alpha forced).
func (fb *Framebuffer) Fill(c color.NRGBA) {
	for i := 0; i < len(fb.Pix); i += 4 {
		fb.Pix[i], fb.Pix[i+1], fb.Pix[i+2], fb.Pix[i+3] = c.R, c.G, c.B, 0xFF
	}
}

// Set writes one pixel (bounds-checked no-op outside the frame).
func (fb *Framebuffer) Set(x, y int, c color.NRGBA) {
	if x < 0 || y < 0 || x >= fb.Width || y >= fb.Height {
		return
	}
	i := y*fb.Stride + x*4
	fb.Pix[i], fb.Pix[i+1], fb.Pix[i+2], fb.Pix[i+3] = c.R, c.G, c.B, 0xFF
}

// Background fills with wallpaper path (PNG or JPEG) scaled-to-cover with
// center crop; unreadable/missing file falls back to solid col (no error).
func (fb *Framebuffer) Background(path string, col color.NRGBA) error {
	img, err := decodeFile(path)
	if err != nil {
		fb.Fill(col)
		return nil // ponytail: a missing wallpaper is a theme color, not a lock failure
	}
	fb.cover(img)
	return nil
}

func decodeFile(path string) (image.Image, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if _, name := splitExt(path); name == "png" {
		return png.Decode(f)
	}
	img, _, err := image.Decode(f)
	return img, err
}

func splitExt(p string) (string, string) {
	for i := len(p) - 1; i >= 0 && i > len(p)-6; i-- {
		if p[i] == '.' {
			e := p[i+1:]
			for j := 0; j < len(e); j++ {
				if e[j] >= 'A' && e[j] <= 'Z' {
					e = e[:j] + string(e[j]-'A'+'a') + e[j+1:]
				}
			}
			return p[:i], e
		}
	}
	return p, ""
}

// cover scales src to the smallest size that covers fb, then center-crops.
func (fb *Framebuffer) cover(src image.Image) {
	b := src.Bounds()
	sw, sh := b.Dx(), b.Dy()
	if sw <= 0 || sh <= 0 {
		fb.Fill(color.NRGBA{A: 255})
		return
	}
	// ponytail: single-buffer lock UI is fine; frames only change on keystroke/clock tick.
	scale := math.Max(float64(fb.Width)/float64(sw), float64(fb.Height)/float64(sh))
	nw, nh := int(float64(sw)*scale+0.5), int(float64(sh)*scale+0.5)
	big := image.NewNRGBA(image.Rect(0, 0, nw, nh))
	xdraw.CatmullRom.Scale(big, big.Bounds(), src, b, xdraw.Src, nil)
	fb.BlitCrop(big, (nw-fb.Width)/2, (nh-fb.Height)/2)
}

// Blit copies the whole src into fb (clipped at origin).
func (fb *Framebuffer) Blit(src *image.NRGBA) {
	fb.BlitCrop(src, 0, 0)
}

// BlitCrop copies src at offset (ox,oy) into fb.
func (fb *Framebuffer) BlitCrop(src *image.NRGBA, ox, oy int) {
	for y := 0; y < fb.Height; y++ {
		sy := y + oy
		if sy < 0 || sy >= src.Bounds().Dy() {
			continue
		}
		for x := 0; x < fb.Width; x++ {
			sx := x + ox
			if sx < 0 || sx >= src.Bounds().Dx() {
				continue
			}
			i := y*fb.Stride + x*4
			j := sy*src.Stride + sx*4
			fb.Pix[i], fb.Pix[i+1], fb.Pix[i+2], fb.Pix[i+3] = src.Pix[j], src.Pix[j+1], src.Pix[j+2], 0xFF
		}
	}
}

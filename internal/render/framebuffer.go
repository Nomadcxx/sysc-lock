// Package render provides the CPU BGRA framebuffer the locker attaches to its
// lock surfaces: solid fills, centered text is drawn by the UI layer, and
// static wallpaper backgrounds scaled-to-cover with center crop.
package render

import (
	"bytes"
	"fmt"
	"golang.org/x/image/math/f64"
	"golang.org/x/sys/unix"
	"image"
	"image/color"
	_ "image/jpeg" // background decode
	_ "image/png"
	"io"
	"math"
	"os"

	xdraw "golang.org/x/image/draw"
)

// Wallpaper limits include a possible 8-byte RGBA64 pixel representation.
const MaxWallpaperPixels = 4_000_000
const MaxWallpaperFileBytes = 16 << 20

// Framebuffer is a wl_shm-ready opaque BGRA buffer (ARGB8888 little-endian).
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
		fb.Pix[i], fb.Pix[i+1], fb.Pix[i+2], fb.Pix[i+3] = c.B, c.G, c.R, 0xFF
	}
}

// Set writes one pixel (bounds-checked no-op outside the frame).
func (fb *Framebuffer) Set(x, y int, col color.Color) {
	c := color.NRGBAModel.Convert(col).(color.NRGBA)
	if x < 0 || y < 0 || x >= fb.Width || y >= fb.Height {
		return
	}
	i := y*fb.Stride + x*4
	fb.Pix[i], fb.Pix[i+1], fb.Pix[i+2], fb.Pix[i+3] = c.B, c.G, c.R, 0xFF
}

// Background fills with wallpaper path (PNG or JPEG) scaled-to-cover with
// center crop; unreadable/missing files fill solid col and return an error.
func (fb *Framebuffer) Background(path string, col color.NRGBA) error {
	img, err := LoadWallpaper(path)
	if err != nil {
		fb.Fill(col)
		return err // caller caches the fallback rather than decoding on each key
	}
	fb.Cover(img)
	return nil
}

func LoadWallpaper(path string) (image.Image, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), path)
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() < 0 || info.Size() > MaxWallpaperFileBytes {
		return nil, fmt.Errorf("wallpaper exceeds file budget")
	}
	// Read one bounded encoded snapshot so a concurrent writer cannot change
	// geometry between DecodeConfig and Decode.
	data := make([]byte, int(info.Size())+1)
	n, readErr := io.ReadFull(f, data)
	if readErr != nil && readErr != io.EOF && readErr != io.ErrUnexpectedEOF {
		return nil, readErr
	}
	if n == len(data) {
		return nil, fmt.Errorf("wallpaper grew beyond encoded snapshot budget")
	}
	data = data[:n]
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	if cfg.Width < 1 || cfg.Height < 1 || cfg.Width > MaxWallpaperPixels/cfg.Height {
		return nil, fmt.Errorf("wallpaper exceeds pixel budget")
	}
	img, _, err := image.Decode(bytes.NewReader(data))
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

// Cover scales src to the smallest size that covers fb, then center-crops.
func (fb *Framebuffer) Cover(src image.Image) {
	b := src.Bounds()
	sw, sh := b.Dx(), b.Dy()
	if sw <= 0 || sh <= 0 {
		fb.Fill(color.NRGBA{A: 255})
		return
	}
	// Scale directly into the bounded destination; extreme aspect ratios never allocate a huge intermediate.
	scale := math.Max(float64(fb.Width)/float64(sw), float64(fb.Height)/float64(sh))
	tx := (float64(fb.Width)-float64(sw)*scale)/2 - float64(b.Min.X)*scale
	ty := (float64(fb.Height)-float64(sh)*scale)/2 - float64(b.Min.Y)*scale
	xdraw.CatmullRom.Transform(fb, f64.Aff3{scale, 0, tx, 0, scale, ty}, src, b, xdraw.Src, nil)
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
			fb.Pix[i], fb.Pix[i+1], fb.Pix[i+2], fb.Pix[i+3] = src.Pix[j+2], src.Pix[j+1], src.Pix[j], 0xFF
		}
	}
}

func (fb *Framebuffer) ColorModel() color.Model { return color.NRGBAModel }
func (fb *Framebuffer) Bounds() image.Rectangle { return image.Rect(0, 0, fb.Width, fb.Height) }
func (fb *Framebuffer) At(x, y int) color.Color {
	if !image.Pt(x, y).In(fb.Bounds()) {
		return color.NRGBA{}
	}
	i := y*fb.Stride + x*4
	return color.NRGBA{R: fb.Pix[i+2], G: fb.Pix[i+1], B: fb.Pix[i], A: 255}
}

package render

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func TestExactSizeAndFill(t *testing.T) {
	fb := New(3, 2)
	if fb.Width != 3 || fb.Height != 2 || len(fb.Pix) != 3*2*4 {
		t.Fatalf("size: %dx%d pix=%d", fb.Width, fb.Height, len(fb.Pix))
	}
	fb.Fill(color.NRGBA{R: 0x11, G: 0x22, B: 0x33, A: 0xFF})
	for _, off := range []int{0, (1*3 + 2) * 4} {
		if got := fb.Pix[off : off+4]; got[0] != 0x11 || got[3] != 0xFF {
			t.Fatalf("pixel at %d = %v", off, got)
		}
	}
}

func writePNG(t *testing.T, dir, name string, w, h int) string {
	t.Helper()
	p := filepath.Join(dir, name)
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := color.NRGBA{R: 0, G: 0, B: 0, A: 255}
			if x < w/2 && y < h/2 {
				c = color.NRGBA{R: 255, G: 0, B: 0, A: 255}
			}
			i := y*img.Stride + x*4
			img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = c.R, c.G, c.B, c.A
		}
	}
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if name[len(name)-4:] == ".jpg" {
		if err := jpeg.Encode(f, img, nil); err != nil {
			t.Fatal(err)
		}
	} else if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestBackgroundCoverCenterCrop(t *testing.T) {
	dir := t.TempDir()
	// 20x10 red-top-left image; target 5x5 → scale-to-cover 2x then center crop.
	p := writePNG(t, dir, "red.png", 20, 10)
	fb := New(5, 5)
	if err := fb.Background(p, color.NRGBA{A: 255}); err != nil {
		t.Fatal(err)
	}
	// Cover = 10x5 (scale 0.5); crop window x[2..7): left pixels red, x=4 → big x=6 → src right half → black.
	if got := fb.Pix[0:4]; !bytes.Equal(got, []byte{255, 0, 0, 255}) {
		t.Fatalf("top-left = %v, want red", got)
	}
	if got := fb.Pix[4*4 : 4*4+4]; !bytes.Equal(got, []byte{0, 0, 0, 255}) {
		t.Fatalf("x=4 = %v, want black", got)
	}
	// JPEG path: real jpeg file bytes must decode (Background falls back on error,
	// so prove decode worked via an all-red 8x8 jpeg → 4x4 fb is red not black.
	jp := filepath.Join(dir, "allred.jpg")
	src := image.NewNRGBA(image.Rect(0, 0, 8, 8))
	for k := range src.Pix {
		if k%4 == 0 {
			src.Pix[k] = 200
		} else if k%4 == 3 {
			src.Pix[k] = 255
		}
	}
	jf, _ := os.Create(jp)
	if err := jpeg.Encode(jf, src, nil); err != nil {
		t.Fatal(err)
	}
	jf.Close()
	fb2 := New(4, 4)
	if err := fb2.Background(jp, color.NRGBA{A: 255}); err != nil {
		t.Fatal(err)
	}
	if fb2.Pix[0] < 150 {
		t.Fatalf("jpeg decode failed, pix[0]=%d", fb2.Pix[0])
	}
}

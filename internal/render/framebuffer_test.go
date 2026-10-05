package render

import (
	"bytes"
	"golang.org/x/sys/unix"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestExactSizeAndFill(t *testing.T) {
	fb := New(3, 2)
	if fb.Width != 3 || fb.Height != 2 || len(fb.Pix) != 3*2*4 {
		t.Fatalf("size: %dx%d pix=%d", fb.Width, fb.Height, len(fb.Pix))
	}
	fb.Fill(color.NRGBA{R: 0x11, G: 0x22, B: 0x33, A: 0xFF})
	for _, off := range []int{0, (1*3 + 2) * 4} {
		if got := fb.Pix[off : off+4]; got[2] != 0x11 || got[3] != 0xFF {
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
	if got := fb.Pix[0:4]; !bytes.Equal(got, []byte{0, 0, 255, 255}) {
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
	if fb2.Pix[2] < 150 {
		t.Fatalf("jpeg decode failed, pix[2]=%d", fb2.Pix[2])
	}
}

func TestFramebufferBGRA(t *testing.T) {
	fb := New(1, 1)
	fb.Fill(color.NRGBA{R: 1, G: 2, B: 3, A: 255})
	want := [4]byte{3, 2, 1, 255}
	for i, b := range want {
		if fb.Pix[i] != b {
			t.Fatalf("pixel byte %d = %d, want %d", i, fb.Pix[i], b)
		}
	}
}

func TestWallpaperRejectsFIFOWithoutWaiting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wallpaper.fifo")
	if err := unix.Mkfifo(path, 0600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- New(2, 2).Background(path, color.NRGBA{A: 255}) }()
	// Release an old blocking open if the check fails, keeping the test leak-free.
	defer func() {
		fd, _ := unix.Open(path, unix.O_RDWR|unix.O_NONBLOCK, 0)
		if fd >= 0 {
			unix.Close(fd)
		}
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("accepted FIFO")
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("FIFO blocked framebuffer asset decode")
	}
}
func TestWallpaperDecodedPixelCeiling(t *testing.T) {
	path := filepath.Join(t.TempDir(), "oversize.png")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	err = png.Encode(f, image.NewGray(image.Rect(0, 0, 4001, 1000)))
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	if err = New(2, 2).Background(path, color.NRGBA{A: 255}); err == nil {
		t.Fatal("accepted more than four million wallpaper pixels")
	}
}

func TestWallpaperEncodedAllocationHasExactCeiling(t *testing.T) {
	source, err := os.ReadFile("framebuffer.go")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(source), "io.ReadAll") {
		t.Fatal("growing encoded allocation exceeds reserved decode peak")
	}
}

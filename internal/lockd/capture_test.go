package lockd

import (
	"testing"

	"github.com/Nomadcxx/sysc-lock/internal/render"
	"github.com/Nomadcxx/sysc-wayland/client"
)

func TestCaptureBufferFits(t *testing.T) {
	argb := uint32(client.ShmFormatArgb8888)
	xrgb := uint32(client.ShmFormatXrgb8888)
	if !captureBufferFits(argb, 8, 4, 32) || !captureBufferFits(xrgb, 8, 4, 32) {
		t.Fatal("32-bit formats must be accepted")
	}
	if captureBufferFits(0x38415258, 8, 4, 32) {
		t.Fatal("foreign fourcc accepted")
	}
	if captureBufferFits(argb, 8, 4, 40) {
		t.Fatal("padded stride accepted")
	}
	if captureBufferFits(argb, 0, 4, 16) {
		t.Fatal("zero width accepted")
	}
}

func TestNormaliseCaptureInvertsAndSealsAlpha(t *testing.T) {
	// Two rows of two pixels at stride 12; the trailing junk must be skipped.
	src := []byte{
		1, 2, 3, 0, 4, 5, 6, 0, 9, 9, 9, 9,
		11, 12, 13, 14, 21, 22, 23, 24, 9, 9, 9, 9,
	}
	dst := make([]byte, 16)
	normaliseCapture(dst, src, 2, 2, 12, true, true)
	want := []byte{11, 12, 13, 0xff, 21, 22, 23, 0xff, 1, 2, 3, 0xff, 4, 5, 6, 0xff}
	for i := range want {
		if dst[i] != want[i] {
			t.Fatalf("y-invert/alpha wrong: got %v want %v", dst, want)
		}
	}
	normaliseCapture(dst, src[:23], 2, 2, 12, false, false)
	if dst[0] != 11 {
		t.Fatal("short source rewrote dst")
	}
}

func TestUpscaleBackdropNearestNeighbour(t *testing.T) {
	src := render.New(2, 2)
	src.Pix[0], src.Pix[4], src.Pix[8], src.Pix[12] = 10, 20, 30, 40
	out := &lockOut{w: 4, h: 4, backdrop: src}
	dst := make([]byte, 4*4*4)
	out.upscaleBackdrop(dst)
	at := func(x, y int) byte { return dst[(y*4+x)*4] }
	if at(0, 0) != 10 || at(1, 0) != 10 || at(2, 0) != 20 || at(3, 3) != 40 || at(0, 2) != 30 {
		t.Fatalf("nearest stretch wrong: %v %v %v %v %v", at(0, 0), at(1, 0), at(2, 0), at(3, 3), at(0, 2))
	}
}

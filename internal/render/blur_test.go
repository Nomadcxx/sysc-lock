package render

import (
	"bytes"
	"testing"
)

func TestBlurNilAndEmptySources(t *testing.T) {
	if Blur(nil, 4, 24) != nil {
		t.Fatal("nil source blurred")
	}
	if Blur(&Framebuffer{}, 4, 24) != nil {
		t.Fatal("empty source blurred")
	}
}

func TestBlurDownsamplesAndKeepsFlatColor(t *testing.T) {
	src := New(8, 4)
	ink := [3]byte{10, 120, 200}
	for i := range src.Pix {
		src.Pix[i] = 0
	}
	for y := 0; y < 4; y++ {
		for x := 0; x < 8; x++ {
			i := y*src.Stride + x*4
			src.Pix[i], src.Pix[i+1], src.Pix[i+2], src.Pix[i+3] = ink[0], ink[1], ink[2], 0xff
		}
	}
	out := Blur(src, 2, 1)
	if out.Width != 4 || out.Height != 2 {
		t.Fatalf("downsample geometry %dx%d", out.Width, out.Height)
	}
	for i := 0; i < len(out.Pix); i += 4 {
		if out.Pix[i] != ink[0] || out.Pix[i+1] != ink[1] || out.Pix[i+2] != ink[2] || out.Pix[i+3] != 0xff {
			t.Fatalf("flat color shifted at %d: %v", i, out.Pix[i:i+4])
		}
	}
}

func TestBlurRadiusBelowFactorClampsToOneTap(t *testing.T) {
	src := New(8, 8)
	for i := range src.Pix {
		src.Pix[i] = byte(i * 7)
	}
	out := Blur(src, 4, 3)
	want := Blur(src, 4, 4)
	if out.Width != want.Width || !bytes.Equal(out.Pix, want.Pix) {
		t.Fatal("radius/factor should clamp to a one-tap box")
	}
	if bytes.Equal(out.Pix, Blur(src, 4, 0).Pix) {
		t.Fatal("clamped box pass did not smooth")
	}
}

func TestBlurSmoothsEdgesWithoutWrapping(t *testing.T) {
	src := New(4, 1)
	// one bright pixel at the left edge, dark elsewhere
	copy(src.Pix, []byte{0xff, 0xff, 0xff, 0xff, 0, 0, 0, 0xff, 0, 0, 0, 0xff, 0, 0, 0, 0xff})
	out := Blur(src, 1, 1)
	if out.Pix[12] > out.Pix[4] {
		t.Fatalf("wrap-around leak: far pixel %d > near pixel %d", out.Pix[12], out.Pix[4])
	}
	if out.Pix[0] == 0 || out.Pix[0] == 0xff {
		t.Fatalf("edge pixel untouched or erased: %d", out.Pix[0])
	}
}

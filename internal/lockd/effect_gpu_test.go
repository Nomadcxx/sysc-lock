package lockd

import (
	"bytes"
	"math"
	"os"
	"testing"
)

func newTestGpuBackend(t *testing.T, w, h int) *gpuBackend {
	t.Helper()
	if _, err := os.Stat("/dev/dri"); err != nil {
		t.Skip("no render node")
	}
	b, err := newGpuBackend("rain", "nord", w, h)
	if err != nil {
		t.Skip("no usable EGL device:", err)
	}
	t.Cleanup(func() { b.Close() })
	gb, ok := b.(*gpuBackend)
	if !ok {
		t.Fatalf("factory returned %T", b)
	}
	return gb
}

func TestGpuRedRoundTrip(t *testing.T) {
	b := newTestGpuBackend(t, 2, 2)
	dst := make([]byte, 2*2*4)
	if err := b.Draw(dst, 2*4); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < len(dst); i += 4 {
		if got := dst[i : i+4]; !bytes.Equal(got, []byte{0, 0, 255, 255}) {
			t.Fatalf("pixel %d: want {0,0,255,255} BGRA, got %v", i/4, got)
		}
	}
	if n := b.glErrors(); n != 0 {
		t.Fatalf("%d GL errors after the frame", n)
	}
}

// The worker hands rows wider than the image; only the first w pixels of each
// row may be touched, and padding must keep whatever the caller left there.
func TestGpuDrawHonorsStride(t *testing.T) {
	const w, h, stride = 2, 3, 16
	b := newTestGpuBackend(t, w, h)
	const sentinel = 0x77
	dst := bytes.Repeat([]byte{sentinel}, h*stride)
	if err := b.Draw(dst, stride); err != nil {
		t.Fatal(err)
	}
	want := func(x, y int) byte {
		if x < w*4 {
			return []byte{0, 0, 255, 255}[x%4]
		}
		return sentinel
	}
	for y := 0; y < h; y++ {
		for x := 0; x < stride; x++ {
			if dst[y*stride+x] != want(x, y) {
				t.Fatalf("byte (%d,%d) = 0x%02x, want 0x%02x", x, y, dst[y*stride+x], want(x, y))
			}
		}
	}
}

// The plan's step-1 sketch says "len(got) == 6" but then states the real rule:
// always exactly 8 vec3 stops, fewer inputs repeat the last, more truncate to 8.
// The stated rule wins; 8 stops is what the `uniform vec3 uPalette[8]` contract needs.
func TestParseHexColors(t *testing.T) {
	got, err := parseHexColors([]string{"#ff8000", "#00ff00"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 24 {
		t.Fatalf("want 8 vec3 stops (24 floats), got %d", len(got))
	}
	want := []float32{1, 0x80 / 255.0, 0, 0, 1, 0}
	for i, w := range want {
		if math.Abs(float64(got[i]-w)) > 1e-6 {
			t.Fatalf("float %d: want %v, got %v", i, w, got[i])
		}
	}
	for i := 6; i < len(got); i++ { // padding repeats the last stop, vec3 by vec3
		wantLast := want[3+(i%3)]
		if math.Abs(float64(got[i]-wantLast)) > 1e-6 {
			t.Fatalf("padded float %d: want %v, got %v", i, wantLast, got[i])
		}
	}
	if _, err := parseHexColors([]string{"not-a-color"}); err == nil {
		t.Fatal("bad hex must error")
	}
	if _, err := parseHexColors(nil); err == nil {
		t.Fatal("no stops must error, the shader needs a palette")
	}
	long := make([]string, 40)
	for i := range long {
		long[i] = "#010203"
	}
	if got, err := parseHexColors(long); err != nil || len(got) != 24 {
		t.Fatalf("40 stops must truncate to 24 floats, got %d (%v)", len(got), err)
	}
}

func TestPaletteStopsCoverEveryEffect(t *testing.T) {
	// Every non-text effect the renderer accepts must resolve a palette, or the
	// GPU backend would silently refuse work the CPU backend performs.
	for _, effect := range cpuEffectIDs {
		if stops := paletteStops(effect, "nord"); len(stops) == 0 {
			t.Errorf("effect %q resolved no stops for theme nord", effect)
		}
	}
	if stops := paletteStops("nonsense", "nord"); stops != nil {
		t.Errorf("unknown effect resolved %d stops", len(stops))
	}
}

func TestGpuBackendInit(t *testing.T) {
	if _, err := os.Stat("/dev/dri"); err != nil {
		t.Skip("no render node")
	}
	b, err := newGpuBackend("rain", "nord", 64, 64)
	if err != nil {
		t.Skip("no usable EGL device:", err) // headless or driver-less boxes
	}
	defer b.Close()
	if err := b.Step(); err != nil {
		t.Fatal(err)
	}
}

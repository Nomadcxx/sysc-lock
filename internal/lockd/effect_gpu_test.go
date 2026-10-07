package lockd

import (
	"bytes"
	"math"
	"os"
	"testing"
)

func newTestGpuBackend(t *testing.T, effect string, w, h int) *gpuBackend {
	t.Helper()
	if _, err := os.Stat("/dev/dri"); err != nil {
		t.Skip("no render node")
	}
	b, err := newGpuBackend(effect, "nord", w, h)
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

// gpuFrame renders one test frame of an effect: fresh backend, 20 steps,
// draw at w x h.
func gpuFrame(t *testing.T, effect string, w, h int) []byte {
	t.Helper()
	b := newTestGpuBackend(t, effect, w, h)
	dst := make([]byte, w*h*4)
	for i := 0; i < 20; i++ {
		if err := b.Step(); err != nil {
			t.Fatal(effect, err)
		}
	}
	if err := b.Draw(dst, w*4); err != nil {
		t.Fatal(effect, err)
	}
	if n := b.glErrors(); n != 0 {
		t.Fatalf("%s: %d GL errors after the frame", effect, n)
	}
	return dst
}

// gpuEffectFrame asserts an effect paints an opaque frame with at least one
// lit pixel.
func gpuEffectFrame(t *testing.T, effect string) {
	dst := gpuFrame(t, effect, 16, 16)
	lit := 0
	for i := 0; i < len(dst); i += 4 {
		if dst[i+3] != 0xff {
			t.Fatalf("%s pixel %d: alpha must be opaque, got 0x%02x", effect, i/4, dst[i+3])
		}
		if dst[i]|dst[i+1]|dst[i+2] != 0 {
			lit++
		}
	}
	if lit == 0 {
		t.Fatalf("%s: 20 ticks produced an all-black frame", effect)
	}
}

// gpuEffectDeterministic asserts two fresh backends with identical seeds
// draw byte-identical frames, one alive at a time like the worker does.
// CPU animations cannot serve as the reference: they draw from the global
// math/rand source.
func gpuEffectDeterministic(t *testing.T, effect string) {
	const w, h = 16, 16
	a := gpuFrameSerial(t, effect, w, h)
	b := gpuFrameSerial(t, effect, w, h)
	if !bytes.Equal(a, b) {
		t.Fatalf("%s: two fresh backends with identical seeds drew different frames", effect)
	}
}

func gpuFrameSerial(t *testing.T, effect string, w, h int) []byte {
	t.Helper()
	// Close explicitly before returning so the next frame gets a fresh
	// context on this thread (the EGL context is current per thread).
	b := newTestGpuBackend(t, effect, w, h)
	dst := make([]byte, w*h*4)
	for i := 0; i < 20; i++ {
		if err := b.Step(); err != nil {
			t.Fatal(effect, err)
		}
	}
	if err := b.Draw(dst, w*4); err != nil {
		t.Fatal(effect, err)
	}
	if err := b.Close(); err != nil {
		t.Fatal(effect, err)
	}
	return dst
}

func TestGpuRedRoundTrip(t *testing.T) {
	b := newTestGpuBackend(t, "rain", 2, 2)
	dst := make([]byte, 2*2*4)
	if err := b.paintSolid([4]float32{1, 0, 0, 1}, dst, 2*4); err != nil {
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

func TestGpuOverlappingBackendsSurviveClose(t *testing.T) {
	first := newTestGpuBackend(t, "rain", 2, 2)
	second := newTestGpuBackend(t, "fire", 4, 4)
	draw := func(b *gpuBackend, col [4]float32, want []byte) {
		t.Helper()
		pixels := make([]byte, b.w*b.h*4)
		if err := b.paintSolid(col, pixels, b.w*4); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < len(pixels); i += 4 {
			if !bytes.Equal(pixels[i:i+4], want) {
				t.Fatalf("backend %dx%d pixel %d: %v, want %v", b.w, b.h, i/4, pixels[i:i+4], want)
			}
		}
	}
	draw(first, [4]float32{1, 0, 0, 1}, []byte{0, 0, 255, 255})
	draw(second, [4]float32{0, 0, 1, 1}, []byte{255, 0, 0, 255})
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
	if err := first.Resize(3, 3); err != nil {
		t.Fatal("closing another backend invalidated the survivor:", err)
	}
	draw(first, [4]float32{1, 0, 0, 1}, []byte{0, 0, 255, 255})
}

// The worker hands rows wider than the image; only the first w pixels of each
// row may be touched, and padding must keep whatever the caller left there.
func TestGpuDrawHonorsStride(t *testing.T) {
	const w, h, stride = 2, 3, 16
	b := newTestGpuBackend(t, "rain", w, h)
	const sentinel = 0x77
	dst := bytes.Repeat([]byte{sentinel}, h*stride)
	if err := b.paintSolid([4]float32{1, 0, 0, 1}, dst, stride); err != nil {
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

// TestGpuPlatformReportsRenderer pins the diagnostic contract: with a render
// node present the backend must come up on the GBM platform, and it always
// records what actually renders so a live journal shows llvmpipe instead of
// silently accepting software for gpu.
func TestGpuPlatformReportsRenderer(t *testing.T) {
	b := newTestGpuBackend(t, "rain", 8, 8)
	if b.platform == "" || b.renderer == "" {
		t.Fatalf("platform=%q renderer=%q, want both non-empty", b.platform, b.renderer)
	}
	t.Logf("platform=%s renderer=%q vendor=%q", b.platform, b.renderer, b.vendor)
	if _, err := os.Stat("/dev/dri/renderD128"); err == nil && b.platform != "gbm" {
		t.Errorf("renderD128 present but platform=%q, want gbm hardware path", b.platform)
	}
}

func TestGpuRainFrame(t *testing.T) { gpuEffectFrame(t, "rain") }

// GPU-vs-GPU determinism is the gate the CPU side can never offer: the
// animations effects draw from the global math/rand, so two renderer
// instances can't be byte-compared at all. Same seed, same step count, same
// driver => identical bytes.
//
// ponytail: identical only on one driver; cross-vendor float hashing may
// differ. If a Mesa mediump flake shows up, relax to coverage-only.
func TestGpuRainDeterministic(t *testing.T) { gpuEffectDeterministic(t, "rain") }

func TestGpuMatrixFrame(t *testing.T) {
	gpuEffectFrame(t, "matrix")
	gpuEffectDeterministic(t, "matrix")
}

func TestGpuFireFrame(t *testing.T) {
	gpuEffectFrame(t, "fire")
	gpuEffectDeterministic(t, "fire")
}

func TestGpuFireworksFrame(t *testing.T) {
	gpuEffectFrame(t, "fireworks")
	gpuEffectDeterministic(t, "fireworks")
}

func TestGpuBeamsFrame(t *testing.T) {
	gpuEffectFrame(t, "beams")
	gpuEffectDeterministic(t, "beams")
}

func TestGpuAquariumFrame(t *testing.T) {
	gpuEffectFrame(t, "aquarium")
	gpuEffectDeterministic(t, "aquarium")
}

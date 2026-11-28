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
	b := newTestGpuBackend(t, "fire", 2, 2)
	dst := make([]byte, 2*2*4)
	if err := b.paintSolid([4]float32{1, 0, 0, 1}, dst, 2*4); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < len(dst); i += 4 {
		if got := dst[i : i+4]; !bytes.Equal(got, []byte{0, 0, 85, 255}) {
			t.Fatalf("pixel %d: want {0,0,85,255} dimmed BGRA, got %v", i/4, got)
		}
	}
	if n := b.glErrors(); n != 0 {
		t.Fatalf("%d GL errors after the frame", n)
	}
}

func TestGpuOverlappingBackendsSurviveClose(t *testing.T) {
	first := newTestGpuBackend(t, "fire", 2, 2)
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
	draw(first, [4]float32{1, 0, 0, 1}, []byte{0, 0, 85, 255})
	draw(second, [4]float32{0, 0, 1, 1}, []byte{85, 0, 0, 255})
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
	if err := first.Resize(3, 3); err != nil {
		t.Fatal("closing another backend invalidated the survivor:", err)
	}
	draw(first, [4]float32{1, 0, 0, 1}, []byte{0, 0, 85, 255})
}

// The worker hands rows wider than the image; only the first w pixels of each
// row may be touched, and padding must keep whatever the caller left there.
func TestGpuDrawHonorsStride(t *testing.T) {
	const w, h, stride = 2, 3, 16
	b := newTestGpuBackend(t, "fire", w, h)
	const sentinel = 0x77
	dst := bytes.Repeat([]byte{sentinel}, h*stride)
	if err := b.paintSolid([4]float32{1, 0, 0, 1}, dst, stride); err != nil {
		t.Fatal(err)
	}
	want := func(x, y int) byte {
		if x < w*4 {
			return []byte{0, 0, 85, 255}[x%4]
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

func TestGpuTightReadbackUsesRowStorage(t *testing.T) {
	b := newTestGpuBackend(t, "fire", 8, 8)
	pixels := make([]byte, 8*8*4)
	if err := b.paintSolid([4]float32{1, 0, 0, 1}, pixels, 8*4); err != nil {
		t.Fatal(err)
	}
	if len(b.scratch) > 8*4 {
		t.Fatalf("tight readback retains %d bytes, want one row", len(b.scratch))
	}
}

// The draw pass lands rows top-down in BGRA order, so a tight caller buffer is
// read straight into and no CPU row storage is needed to repair the frame.
func TestGpuTightDrawNeedsNoScratch(t *testing.T) {
	b := newTestGpuBackend(t, "fire", 8, 8)
	for i := 0; i < 5; i++ {
		b.Step()
	}
	pixels := make([]byte, 8*8*4)
	if err := b.Draw(pixels, 8*4); err != nil {
		t.Fatal(err)
	}
	if len(b.scratch) != 0 {
		t.Fatalf("tight draw readback retains %d bytes of CPU row storage, want none", len(b.scratch))
	}
}

// installDrawShader swaps the draw pass for a test fragment shader, so the
// vertex stage, the colour helper and the readback can be checked without an
// effect's own maths in the way.
func installDrawShader(t *testing.T, b *gpuBackend, fs string) {
	t.Helper()
	glMu.Lock()
	defer glMu.Unlock()
	if err := b.makeCurrent(); err != nil {
		t.Fatal(err)
	}
	prog, err := buildProgram(drawVS, fs)
	if err != nil {
		t.Fatal(err)
	}
	b.drawProg = prog
	b.drawU = resolveUniforms(prog)
}

// A frame is read back top row first and in blue, green, red, alpha byte order
// straight from the draw pass. The shader paints a quadrant per channel
// pattern: red grows to the right, green grows toward the top.
func TestGpuDrawPassOrientationAndByteOrder(t *testing.T) {
	const w, h = 4, 4
	for _, stride := range []int{w * 4, w*4 + 8} {
		b := newTestGpuBackend(t, "fire", w, h)
		installDrawShader(t, b, effectFSHead+`
void main() {
	gl_FragColor = sealed(vec3(step(0.5, vUv.x), step(0.5, vUv.y), 0.0));
}
`)
		b.Step()
		const sentinel = 0x77
		pixels := bytes.Repeat([]byte{sentinel}, stride*h)
		if err := b.Draw(pixels, stride); err != nil {
			t.Fatal(err)
		}
		if n := b.glErrors(); n != 0 {
			t.Fatalf("%d GL errors after the frame", n)
		}
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				var want []byte
				switch {
				case y < h/2 && x < w/2: // top left: green only
					want = []byte{0, 85, 0, 255}
				case y < h/2: // top right: green and red
					want = []byte{0, 85, 85, 255}
				case x < w/2: // bottom left: black
					want = []byte{0, 0, 0, 255}
				default: // bottom right: red only
					want = []byte{0, 0, 85, 255}
				}
				if got := pixels[y*stride+x*4 : y*stride+x*4+4]; !bytes.Equal(got, want) {
					t.Fatalf("stride %d pixel (%d,%d) = %v, want %v", stride, x, y, got, want)
				}
			}
			for x := w * 4; x < stride; x++ {
				if pixels[y*stride+x] != sentinel {
					t.Fatalf("stride %d row %d padding byte %d was overwritten", stride, y, x)
				}
			}
		}
	}
}

// The helper dims every channel to a third of its 8-bit level with integer
// division, exactly as the frame conversion on the CPU did, for all 256 levels.
func TestGpuSealedColourDimsEveryLevelLikeIntegerDivision(t *testing.T) {
	const w = 256
	b := newTestGpuBackend(t, "fire", w, 1)
	installDrawShader(t, b, effectFSHead+`
void main() {
	float i = floor(gl_FragCoord.x);
	gl_FragColor = sealed(vec3(i / 255.0, (255.0 - i) / 255.0, mod(i * 7.0, 256.0) / 255.0));
}
`)
	b.Step()
	pixels := make([]byte, w*4)
	if err := b.Draw(pixels, w*4); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < w; i++ {
		r, g, bl := byte(i), byte(255-i), byte(i*7%256)
		want := []byte{bl / 3, g / 3, r / 3, 255}
		if got := pixels[i*4 : i*4+4]; !bytes.Equal(got, want) {
			t.Fatalf("level %d: got BGRA %v, want %v", i, got, want)
		}
	}
}

// Values past one are clamped before the dim, as the effect shaders clamp.
func TestGpuSealedColourClampsAndSealsAlpha(t *testing.T) {
	b := newTestGpuBackend(t, "fire", 2, 2)
	installDrawShader(t, b, effectFSHead+`
void main() {
	gl_FragColor = sealed(vec3(4.0, -1.0, 0.5));
}
`)
	b.Step()
	pixels := make([]byte, 2*2*4)
	if err := b.Draw(pixels, 2*4); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < len(pixels); i += 4 {
		// 0.5 is 128 as a byte, a third of which is 42.
		if got := pixels[i : i+4]; !bytes.Equal(got, []byte{42, 0, 85, 255}) {
			t.Fatalf("pixel %d = %v, want {42,0,85,255}", i/4, got)
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
	// Each retained GPU effect must resolve its authoritative palette.
	for _, effect := range gpuEffectIDs {
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
	b, err := newGpuBackend("fire", "nord", 64, 64)
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
	b := newTestGpuBackend(t, "fire", 8, 8)
	if b.platform == "" || b.renderer == "" {
		t.Fatalf("platform=%q renderer=%q, want both non-empty", b.platform, b.renderer)
	}
	t.Logf("platform=%s renderer=%q vendor=%q", b.platform, b.renderer, b.vendor)
	if _, err := os.Stat("/dev/dri/renderD128"); err == nil && b.platform != "gbm" {
		t.Errorf("renderD128 present but platform=%q, want gbm hardware path", b.platform)
	}
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

func TestGpuRefusesSimplifiedTerminalEffects(t *testing.T) {
	for _, effect := range []string{"matrix", "rain", "aquarium"} {
		b, err := newGpuBackend(effect, "nord", 64, 64)
		if err == nil {
			b.Close()
			t.Errorf("%s still uses simplified shader", effect)
		}
	}
}

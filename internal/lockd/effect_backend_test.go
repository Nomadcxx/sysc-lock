package lockd

import (
	"bytes"
	"testing"

	"github.com/Nomadcxx/sysc-terminal/renderer"
)

// cpuBackend must be a pure pass-through: same renderer, same pixels, same
// stride. The effects read the global rand source, so two independent
// renderers can never be compared byte for byte; instead both draws replay the
// same previous frame over the same prior buffer content.
func TestCpuBackendMatchesRendererDirect(t *testing.T) {
	r, err := renderer.New(renderer.Config{Effect: "rain", Palette: "nord", Width: 120, Height: 60, PixelSize: 12})
	if err != nil {
		t.Fatal(err)
	}
	a := &cpuBackend{r: r}
	pa := make([]byte, 120*4*60)
	pb := make([]byte, 120*4*60)
	for i := 0; i < 3; i++ {
		if err := a.Step(); err != nil {
			t.Fatal(err)
		}
		if err := a.Draw(pa, 120*4); err != nil {
			t.Fatal(err)
		}
		copy(pb, pa)
		if _, err := r.Draw(pb, 120*4, a.frame); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(pa, pb) {
			t.Fatalf("frame %d: cpuBackend drifted from the renderer", i)
		}
	}
}

// The worker alternates two pixel buffers. Both hold the same grid at the same
// step, so a swap that keeps the wrong buffer history shows up as a diff.
func TestCpuBackendSwapsDestination(t *testing.T) {
	a, err := newCpuBackend("rain", "nord", 120, 60)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	pa := make([]byte, 120*4*60)
	pb := make([]byte, 120*4*60)
	if err := a.Step(); err != nil {
		t.Fatal(err)
	}
	if err := a.Draw(pa, 120*4); err != nil {
		t.Fatal(err)
	}
	if err := a.Step(); err != nil {
		t.Fatal(err)
	}
	if err := a.Draw(pb, 120*4); err != nil {
		t.Fatal(err)
	}
	if err := a.Draw(pa, 120*4); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(pa, pb) {
		t.Fatal("swapped destination rendered from another buffer history")
	}
}

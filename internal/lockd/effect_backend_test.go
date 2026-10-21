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
	a := &cpuBackend{r: r, width: 120, height: 60}
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
		if _, err := r.Draw(pb, 120*4, historyForBuffer(a, pa).frame); err != nil {
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

func TestCpuBackendKeepsHistoryForAlternatingAndFrozenBuffers(t *testing.T) {
	const width, height = 120, 60
	const stride = width * 4
	for _, effect := range []string{"ak-47", "matrix", "rain"} {
		t.Run(effect, func(t *testing.T) {
			backend, err := newCpuBackend(effect, "dark", width, height)
			if err != nil {
				t.Fatal(err)
			}
			defer backend.Close()
			cpu := backend.(*cpuBackend)
			buffers := [2][]byte{make([]byte, height*stride), make([]byte, height*stride)}
			draw := func(dst []byte) {
				t.Helper()
				if err := cpu.Step(); err != nil {
					t.Fatal(err)
				}
				if err := cpu.Draw(dst, stride); err != nil {
					t.Fatal(err)
				}
				want := make([]byte, len(dst))
				if _, err := cpu.r.Draw(want, stride, nil); err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(dst, want) {
					t.Fatal("incremental frame differs from a full redraw")
				}
			}

			for i := 0; i < 20; i++ {
				dst := buffers[i%len(buffers)]
				draw(dst)
				if i >= 1 {
					for _, buffer := range buffers {
						if historyForBuffer(cpu, buffer) == nil {
							t.Fatal("alternating draw discarded a reusable buffer history")
						}
					}
				}
			}

			// While motion is frozen the worker retains its spare frame. On
			// resume it draws into that same buffer before swapping again.
			draw(buffers[1])
			for _, buffer := range buffers {
				if historyForBuffer(cpu, buffer) == nil {
					t.Fatal("frozen spare draw discarded the other buffer history")
				}
			}
		})
	}
}

func TestCpuBackendHistoryTracksStrideAndResize(t *testing.T) {
	const width, height = 120, 60
	strideA, strideB := width*4, width*4+8
	backend, err := newCpuBackend("rain", "dark", width, height)
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()
	cpu := backend.(*cpuBackend)
	pixels := make([]byte, height*strideB)
	for _, stride := range []int{strideA, strideB, strideA} {
		if err := cpu.Step(); err != nil {
			t.Fatal(err)
		}
		if err := cpu.Draw(pixels, stride); err != nil {
			t.Fatal(err)
		}
		history := historyForBuffer(cpu, pixels)
		if history == nil || history.stride != stride {
			t.Fatalf("destination history stride = %v, want %d", history, stride)
		}
	}

	if err := cpu.Resize(width+12, height+12); err != nil {
		t.Fatal(err)
	}
	for _, history := range cpu.histories {
		if history.frame != nil {
			t.Fatal("geometry change retained an incompatible frame history")
		}
	}
}

func TestCpuBackendHistoryEvictsLeastRecentlyUsedBuffer(t *testing.T) {
	const width, height = 120, 60
	const stride = width * 4
	backend, err := newCpuBackend("rain", "dark", width, height)
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()
	cpu := backend.(*cpuBackend)
	buffers := [3][]byte{
		make([]byte, height*stride),
		make([]byte, height*stride),
		make([]byte, height*stride),
	}
	draw := func(index int) {
		t.Helper()
		if err := cpu.Step(); err != nil {
			t.Fatal(err)
		}
		if err := cpu.Draw(buffers[index], stride); err != nil {
			t.Fatal(err)
		}
		want := make([]byte, len(buffers[index]))
		if _, err := cpu.r.Draw(want, stride, nil); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(buffers[index], want) {
			t.Fatal("evicted destination differs from a full redraw")
		}
	}

	for _, index := range []int{0, 1, 0, 2} {
		draw(index)
	}
	if historyForBuffer(cpu, buffers[0]) == nil || historyForBuffer(cpu, buffers[2]) == nil {
		t.Fatal("cache did not retain the recently used and newest buffers")
	}
	if historyForBuffer(cpu, buffers[1]) != nil {
		t.Fatal("cache did not evict the least recently used buffer")
	}
	draw(1)
}

func historyForBuffer(backend *cpuBackend, pixels []byte) *cpuFrameHistory {
	if len(pixels) == 0 {
		return nil
	}
	for i := range backend.histories {
		history := &backend.histories[i]
		if len(history.pixels) > 0 && &history.pixels[0] == &pixels[0] {
			return history
		}
	}
	return nil
}

func BenchmarkCPUBackendDraw(b *testing.B) {
	const width, height = 3440, 1440
	for _, effect := range []string{"ak-47", "matrix", "rain"} {
		for _, pattern := range []string{"same", "alternating"} {
			b.Run(pattern+"/"+effect, func(b *testing.B) {
				backend, err := newCpuBackend(effect, "dark", width, height)
				if err != nil {
					b.Fatal(err)
				}
				defer backend.Close()
				buffers := [2][]byte{
					make([]byte, width*height*4),
					make([]byte, width*height*4),
				}
				draw := func(i int) {
					b.Helper()
					if err := backend.Step(); err != nil {
						b.Fatal(err)
					}
					dst := buffers[0]
					if pattern == "alternating" {
						dst = buffers[i%len(buffers)]
					}
					if err := backend.Draw(dst, width*4); err != nil {
						b.Fatal(err)
					}
				}
				for i := 0; i < 50; i++ {
					draw(i)
				}
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					b.StopTimer()
					if err := backend.Step(); err != nil {
						b.Fatal(err)
					}
					b.StartTimer()
					dst := buffers[0]
					if pattern == "alternating" {
						dst = buffers[i%len(buffers)]
					}
					if err := backend.Draw(dst, width*4); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}

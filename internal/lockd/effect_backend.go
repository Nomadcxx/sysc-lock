package lockd

import "github.com/Nomadcxx/sysc-terminal/renderer"

// EffectBackend paints one frame of a background effect into caller memory.
// Draw writes opaque BGRA, exactly like the renderer package. A backend belongs
// to the background worker goroutine; it owns no scheduling or transport.
type EffectBackend interface {
	Resize(width, height int) error
	Step() error
	Draw(pixels []byte, stride int) error
	Close() error
}

// backendFactory builds one backend for a geometry. The worker calls it again on
// geometry change and on demotion, so a failed attempt costs nothing extra.
type backendFactory func(effect, palette string, width, height int) (EffectBackend, error)

type cpuBackend struct {
	r          *renderer.Renderer
	width      int
	height     int
	histories  [2]cpuFrameHistory
	historyUse uint64
}

type cpuFrameHistory struct {
	pixels        []byte
	frame         *renderer.Frame
	width, height int
	stride        int
	lastUsed      uint64
}

func newCpuBackend(effect, palette string, width, height int) (EffectBackend, error) {
	r, err := renderer.New(renderer.Config{Effect: effect, Palette: palette, Width: width, Height: height, PixelSize: 12, DimOutput: true})
	if err != nil {
		return nil, err
	}
	return &cpuBackend{r: r, width: width, height: height}, nil
}

func (b *cpuBackend) Resize(w, h int) error {
	if err := b.r.Resize(w, h); err != nil {
		return err
	}
	if b.width != w || b.height != h {
		b.histories = [2]cpuFrameHistory{}
		b.width, b.height = w, h
	}
	return nil
}

func (b *cpuBackend) Step() error { return b.r.Step() }

// Close is nil: the renderer exposes no destructor and the CPU path never
// blocks, so a lock unlock is never delayed by teardown.
func (b *cpuBackend) Close() error { return nil }

func (b *cpuBackend) Draw(pixels []byte, stride int) error {
	index, previous, hit := b.frameHistory(pixels, stride)
	f, err := b.r.Draw(pixels, stride, previous)
	if err != nil {
		if hit {
			b.histories[index] = cpuFrameHistory{}
		}
		return err
	}
	if index >= 0 {
		b.historyUse++
		b.histories[index] = cpuFrameHistory{
			pixels: pixels, frame: f, width: b.width, height: b.height,
			stride: stride, lastUsed: b.historyUse,
		}
	}
	return nil
}

func (b *cpuBackend) frameHistory(pixels []byte, stride int) (int, *renderer.Frame, bool) {
	if len(pixels) == 0 {
		return -1, nil, false
	}
	for i := range b.histories {
		history := &b.histories[i]
		if len(history.pixels) == 0 || &history.pixels[0] != &pixels[0] {
			continue
		}
		if history.width == b.width && history.height == b.height && history.stride == stride && len(history.pixels) == len(pixels) {
			return i, history.frame, true
		}
		*history = cpuFrameHistory{}
		return i, nil, false
	}
	for i := range b.histories {
		if b.histories[i].frame == nil {
			return i, nil, false
		}
	}
	// ponytail: Two entries cover the worker's cached/spare buffers. Extra
	// destinations stay correct but can evict a hot buffer and redraw it once.
	if b.histories[0].lastUsed <= b.histories[1].lastUsed {
		return 0, nil, false
	}
	return 1, nil, false
}

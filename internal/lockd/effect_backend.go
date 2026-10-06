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
	r     *renderer.Renderer
	frame *renderer.Frame
}

func newCpuBackend(effect, palette string, width, height int) (EffectBackend, error) {
	r, err := renderer.New(renderer.Config{Effect: effect, Palette: palette, Width: width, Height: height, PixelSize: 12})
	if err != nil {
		return nil, err
	}
	return &cpuBackend{r: r}, nil
}

func (b *cpuBackend) Resize(w, h int) error { return b.r.Resize(w, h) }
func (b *cpuBackend) Step() error           { return b.r.Step() }

// Close is nil: the renderer exposes no destructor and the CPU path never
// blocks, so a lock unlock is never delayed by teardown.
func (b *cpuBackend) Close() error { return nil }

func (b *cpuBackend) Draw(pixels []byte, stride int) error {
	f, err := b.r.Draw(pixels, stride, b.frame)
	b.frame = f
	return err
}

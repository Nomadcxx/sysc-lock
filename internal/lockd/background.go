package lockd

import (
	"fmt"
	"github.com/Nomadcxx/sysc-lock/internal/render"
	"image"
	"os"
	"sync"
	"time"
)

type backgroundFrame struct {
	pixels        []byte
	width, height int
	err           error
}
type backgroundJob struct {
	width, height int
	previous      backgroundFrame
}
type backgroundWorker struct {
	jobs          chan backgroundJob
	results       chan backgroundFrame
	stopped       chan struct{}
	done          chan struct{}
	once          sync.Once
	busy, failed  bool            // owner-only
	cached, spare backgroundFrame // owner-only
	jobDeadline   time.Time       // owner-only
	storageBytes  int             // reserved owner pixel accounting, includes one in-flight frame
	interval      time.Duration   // resolved once for this worker, including the battery cap
	demoted       bool            // set by the worker on GPU->CPU drop; readers sync via results
}

func newBackgroundWorker(effect, palette string, wallpaper *wallpaperAsset, wake func(), policy effectPolicy, newBackend backendFactory) *backgroundWorker {
	b := &backgroundWorker{jobs: make(chan backgroundJob, 1), results: make(chan backgroundFrame, 1), stopped: make(chan struct{}), done: make(chan struct{}), interval: policy.Interval}
	go func() {
		defer func() { close(b.done); wake() }()
		defer func() {
			if recover() != nil {
				select {
				case b.results <- backgroundFrame{err: fmt.Errorf("background failed")}:
				default:
				}
				wake()
			}
		}()
		var paint EffectBackend
		factory, gpu, slow := newBackend, policy.GPU, 0
		defer func() {
			if paint != nil {
				paint.Close()
			}
		}()
		// demote drops the GPU backend for good and retries on CPU. It can
		// only run while gpu is set and clears it, so the log line is once
		// per lock.
		demote := func() {
			fmt.Fprintf(os.Stderr, "sysc-lock: gpu effect %q dropped to cpu\n", effect)
			if paint != nil {
				paint.Close()
				paint = nil
			}
			factory, gpu, slow = newCpuBackend, false, 0
			b.demoted = true
		}
		// draw runs one effect frame against the current backend.
		draw := func(job backgroundJob) (backgroundFrame, error) {
			frame := job.previous
			var err error
			if paint == nil {
				paint, err = factory(effect, palette, job.width, job.height)
			} else {
				err = paint.Resize(job.width, job.height)
			}
			if err == nil {
				err = paint.Step()
			}
			if err == nil {
				if frame.width != job.width || frame.height != job.height {
					frame = backgroundFrame{width: job.width, height: job.height, pixels: make([]byte, job.width*job.height*4)}
				}
				err = paint.Draw(frame.pixels, job.width*4)
			}
			return frame, err
		}
		for {
			select {
			case <-b.stopped:
				return
			case job := <-b.jobs:
				var frame backgroundFrame
				var err error

				if wallpaper != nil {
					var img image.Image
					frame = job.previous
					img, err = wallpaper.load()
					if err == nil && (frame.width != job.width || frame.height != job.height || len(frame.pixels) != job.width*job.height*4) {
						frame = backgroundFrame{width: job.width, height: job.height, pixels: make([]byte, job.width*job.height*4)}
						fb := render.Framebuffer{Width: job.width, Height: job.height, Stride: job.width * 4, Pix: frame.pixels}
						fb.Cover(img)
					}
				} else {
					start := time.Now()
					frame, err = draw(job)
					if err == nil && gpu && policy.Interval > 0 && policy.MaxSlow > 0 {
						if time.Since(start) > 2*policy.Interval {
							slow++
						} else {
							slow = 0
						}
						if slow >= policy.MaxSlow {
							demote()
						}
					}
					if err != nil && gpu {
						demote()
						frame, err = draw(job)
					}
				}
				frame.err = err
				select {
				case <-b.stopped:
					return
				case b.results <- frame:
					wake()
				}
				if err != nil {
					return
				}
			}
		}
	}()
	return b
}
func (b *backgroundWorker) stop() {
	if b.stopped != nil {
		b.once.Do(func() { close(b.stopped) })
	}
}
func (out *lockOut) backgroundPixels() []byte {
	if b := out.background; b != nil && !b.failed && b.cached.width == out.w && b.cached.height == out.h {
		return b.cached.pixels
	}
	return nil
}

const (
	defaultEffectEvery = 50 * time.Millisecond
	minEffectFPS       = 10
	maxEffectFPS       = 120
)

func (out *lockOut) effectDue(now time.Time, every time.Duration) bool {
	if out.background != nil {
		every = max(every, out.background.interval)
	}
	return out.lastFrame.IsZero() || now.Sub(out.lastFrame) >= every
}

// SetEffectRate sets effect ticks per second, clamped to 10..120. The interval
// carries a 10% tolerance so a frame callback that lands a little early is not
// skipped, which would otherwise halve the rate.
func (c *Client) SetEffectRate(fps int) {
	if fps <= 0 {
		c.effectEvery = 0
		return
	}
	fps = max(minEffectFPS, min(maxEffectFPS, fps))
	c.effectEvery = time.Second / time.Duration(fps) * 9 / 10
}

func (c *Client) effectInterval() time.Duration {
	if c.effectEvery > 0 {
		return c.effectEvery
	}
	return defaultEffectEvery
}
func (c *Client) EnableBackground(effect, palette string, reduced bool, backend string, powerSave bool) {
	c.effect, c.palette, c.reduced = effect, palette, reduced
	c.effectBackend, c.effectPowerSave = backend, powerSave
}

// ApplyPresentation swaps what the next frames draw. Workers own their effect
// for life, so each running worker is torn down and scheduleBackground builds
// a fresh one; the old worker's memory returns when its goroutine exits.
func (c *Client) ApplyPresentation(effect, palette string, reduced bool, backend string, powerSave bool) {
	c.EnableBackground(effect, palette, reduced, backend, powerSave)
	for _, out := range c.outputs {
		if b := out.background; b != nil {
			b.stop()
			c.releaseStoppedBackground(b)
			out.background = nil
			out.pending = true
		}
	}
	c.repaintOwner()
}
func (c *Client) motionAllowed(now time.Time) bool {
	return !c.frozen && (c.resumeAt.IsZero() || !now.Before(c.resumeAt))
}

// SetMotionFrozen runs on the credential owner. Neither key data nor credentials enter the worker.
func (c *Client) SetMotionFrozen(frozen bool, now time.Time) {
	if c.frozen == frozen {
		return
	}
	c.frozen = frozen
	if frozen {
		c.resumeAt = time.Time{}
	} else {
		c.resumeAt = now.Add(2 * time.Second)
	}
	c.publish()
}
func (c *Client) scheduleBackground(out *lockOut, now time.Time) {
	if (c.effect == "" && c.wallpaper == nil) || out.removed || out.callback != nil || (c.wallpaper == nil && !c.motionAllowed(now)) {
		return
	}
	b := out.background
	if b != nil && (b.busy || b.failed || ((c.reduced || c.wallpaper != nil) && b.cached.width == out.w && b.cached.height == out.h && b.cached.pixels != nil)) {
		return
	}
	if !out.effectDue(now, c.effectInterval()) {
		if err := c.armFrame(out, true); err != nil {
			c.failUI(err)
		}
		return
	}
	size, err := bufferBytes(out.w, out.h)
	if err != nil {
		c.failUI(err)
		return
	}
	if b == nil {
		if 2*size > maxPixelBytes-c.pixelBytes() {
			return
		} // opaque foreground already exists
		policy := resolveEffectPolicy(c.effectBackend, c.effectPowerSave, c.effectInterval(), systemBattery)
		b = newBackgroundWorker(c.effect, c.palette, c.wallpaper, func() { c.Post(func() { c.collectBackground(out) }) }, policy, policy.factory())
		out.background = b
	}
	need := b.jobStorage(out.w, out.h)
	if need > b.storageBytes && need-b.storageBytes > maxPixelBytes-c.pixelBytes() {
		return
	}
	b.storageBytes = need
	b.busy = true
	b.jobDeadline = now.Add(100 * time.Millisecond)
	if b.cached.pixels == nil {
		b.jobDeadline = now.Add(time.Second)
	}
	out.lastFrame = now
	b.jobs <- backgroundJob{width: out.w, height: out.h, previous: b.spare}
	b.spare = backgroundFrame{}
}
func (c *Client) collectBackground(out *lockOut) {
	c.collectRemoved()
	b := out.background
	if b == nil {
		return
	}
	if b.failed {
		before := c.pixelBytes()
		c.releaseStoppedBackground(b)
		c.releaseWallpaperIfStopped()
		if before != c.pixelBytes() {
			c.repaintOwner()
			c.publish()
		}
	}
	select {
	case frame := <-b.results:
		b.busy = false
		b.jobDeadline = time.Time{}
		if out.removed || b.failed {
			c.releaseStoppedBackground(b)
			c.repaintOwner()
			c.publish()
			return
		}
		if frame.err != nil {
			b.cached = backgroundFrame{}
			b.spare = backgroundFrame{}
			b.failed = true
			b.stop()
			out.pending = true
			c.paint(out)
			c.publish()
			return
		}
		if c.wallpaper == nil && !c.motionAllowed(time.Now()) {
			// Retain the frame that was visible when typing/PAM began.
			b.spare = frame
			b.storageBytes = len(b.spare.pixels) + len(b.cached.pixels)
			return
		}
		b.spare, b.cached = b.cached, frame
		b.storageBytes = len(b.spare.pixels) + len(b.cached.pixels)
		c.publish()
		if frame.width == out.w && frame.height == out.h {
			out.pending = true
			c.paint(out)
		} else {
			c.scheduleBackground(out, time.Now())
		}
	default:
	}
}

// WaitBackground runs after the Wayland owner exits, so teardown cannot stall input.
func (c *Client) WaitBackground() error {
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	outputs := append([]*lockOut(nil), c.removed...)
	for _, out := range c.outputs {
		outputs = append(outputs, out)
	}
	for _, out := range outputs {
		if out.background == nil {
			continue
		}
		select {
		case <-out.background.done:
		case <-timer.C:
			return fmt.Errorf("background teardown deadline expired")
		}
	}
	return nil
}

// jobStorage includes old geometry until the worker returns its replacement.
func (b *backgroundWorker) jobStorage(w, h int) int {
	storage := len(b.cached.pixels) + len(b.spare.pixels)
	if b.spare.width != w || b.spare.height != h || len(b.spare.pixels) != w*h*4 {
		storage += w * h * 4
	}
	return storage
}
func (c *Client) releaseStoppedBackground(b *backgroundWorker) {
	if b.done == nil {
		return
	}
	select {
	case <-b.done:
		b.cached, b.spare = backgroundFrame{}, backgroundFrame{}
		b.storageBytes = 0
	default:
	}
}
func (c *Client) backgroundStatus(now time.Time) string {
	if len(c.outputs) == 0 {
		return "fallback"
	}
	backdrop := false
	for _, out := range c.outputs {
		if c.effect == "" && c.wallpaper == nil && out.backdrop != nil && out.blurW == out.w && out.blurH == out.h {
			backdrop = true
			continue
		}
		b := out.background
		if b == nil || b.failed || len(out.backgroundPixels()) == 0 {
			return "fallback"
		}
	}
	if backdrop || c.wallpaper != nil || c.reduced || !c.motionAllowed(now) {
		return "frozen"
	}
	return "animated"
}

// A stalled decoration job keeps its memory reservation until its goroutine exits.
// The owner never waits or starts a replacement during this acquisition.
func (c *Client) expireBackground(now time.Time) {
	for _, out := range c.outputs {
		b := out.background
		if b != nil && b.busy && !b.failed && !b.jobDeadline.IsZero() && !now.Before(b.jobDeadline) {
			b.failed = true
			b.jobDeadline = time.Time{}
			b.stop()
			out.pending = true
			c.paint(out)
			c.publish()
		}
	}
}

// One immutable decoded asset belongs to this acquisition and all its workers.
// The reservation includes RGBA64 pixels and the encoded decode snapshot peak.
const wallpaperReservation = render.MaxWallpaperPixels*8 + render.MaxWallpaperFileBytes + 1

type wallpaperAsset struct {
	path    string
	once    sync.Once
	decoded image.Image
	err     error
}

func (a *wallpaperAsset) load() (image.Image, error) {
	a.once.Do(func() { a.decoded, a.err = render.LoadWallpaper(a.path) })
	return a.decoded, a.err
}
func (c *Client) EnableWallpaper(path string) {
	if path != "" {
		c.wallpaper = &wallpaperAsset{path: path}
		c.effect = ""
	}
}

// Called only after stopping decoration; a hung worker retains the reservation.
func (c *Client) releaseWallpaperIfStopped() {
	if c.wallpaper == nil {
		return
	}
	outputs := append([]*lockOut(nil), c.removed...)
	for _, out := range c.outputs {
		outputs = append(outputs, out)
	}
	for _, out := range outputs {
		if b := out.background; b != nil {
			if b.done == nil {
				return
			}
			select {
			case <-b.done:
			default:
				return
			}
		}
	}
	c.wallpaper = nil
}

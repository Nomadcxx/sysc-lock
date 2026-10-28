package lockd

import (
	"fmt"
	"github.com/Nomadcxx/sysc-lock/internal/render"
	"github.com/Nomadcxx/sysc-wayland/client"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPamVerificationFreezesEffect(t *testing.T) {
	c := &Client{}
	now := time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC)
	c.SetMotionFrozen(true, now)
	if c.motionAllowed(now.Add(time.Hour)) {
		t.Fatal("effect ticked during verification")
	}
	c.SetMotionFrozen(false, now)
	if c.motionAllowed(now.Add(time.Second)) {
		t.Fatal("resumed before quiet period")
	}
	if !c.motionAllowed(now.Add(2 * time.Second)) {
		t.Fatal("did not resume")
	}
}
func TestBackgroundStallKeepsForegroundResponsive(t *testing.T) {
	pixels := make([]byte, 16)
	out := &lockOut{w: 2, h: 2, background: &backgroundWorker{busy: true, cached: backgroundFrame{pixels: pixels, width: 2, height: 2}}}
	if got := out.backgroundPixels(); len(got) != 16 || &got[0] != &pixels[0] {
		t.Fatal("stall hides cached foreground source")
	}
}
func TestFrameCallbackPacesEffect(t *testing.T) {
	now := time.Now()
	out := &lockOut{lastFrame: now}
	if out.effectDue(now.Add(20*time.Millisecond), defaultEffectEvery) {
		t.Fatal("exceeded 20fps")
	}
	if !out.effectDue(now.Add(50*time.Millisecond), defaultEffectEvery) {
		t.Fatal("missed frame deadline")
	}
}

func TestEffectRateSetsTheTickInterval(t *testing.T) {
	c := &Client{}
	if got := c.effectInterval(); got != defaultEffectEvery {
		t.Fatal(got)
	}
	c.SetEffectRate(20)
	if got := c.effectInterval(); got != 45*time.Millisecond {
		t.Fatal("20 fps keeps a 10% tolerance under 50ms:", got)
	}
	c.SetEffectRate(60)
	if got := c.effectInterval(); got != 14999999*time.Nanosecond {
		t.Fatal(got)
	}
	for _, bad := range []int{0, -3, 1, 100000} {
		c.SetEffectRate(bad)
		if got := c.effectInterval(); got < 7*time.Millisecond || got > 90*time.Millisecond {
			t.Fatalf("rate %d produced %v", bad, got)
		}
	}
	out := &lockOut{lastFrame: time.Unix(10, 0)}
	c.SetEffectRate(60)
	if out.effectDue(out.lastFrame.Add(10*time.Millisecond), c.effectInterval()) {
		t.Fatal("due too early")
	}
	if !out.effectDue(out.lastFrame.Add(16*time.Millisecond), c.effectInterval()) {
		t.Fatal("a vsync landing slightly early must not be skipped")
	}
}

func TestBackgroundPolicyPacesFrames(t *testing.T) {
	for _, tc := range []struct {
		name                    string
		fps                     int
		battery, powerSave, due bool
		elapsed                 time.Duration
	}{
		{"battery caps 60fps", 60, true, true, false, 16 * time.Millisecond},
		{"battery reaches capped deadline", 60, true, true, true, time.Second / 30},
		{"AC keeps 60fps", 60, false, true, true, 16 * time.Millisecond},
		{"disabled power saving keeps 60fps", 60, true, false, true, 16 * time.Millisecond},
		{"battery preserves slower rate", 20, true, true, false, 34 * time.Millisecond},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := &Client{}
			c.SetEffectRate(tc.fps)
			policy := resolveEffectPolicy("cpu", tc.powerSave, c.effectInterval(), func() bool { return tc.battery })
			b := newBackgroundWorker("rain", "nord", nil, func() {}, policy, newCpuBackend)
			t.Cleanup(func() { b.stop(); <-b.done })
			out := &lockOut{lastFrame: time.Unix(10, 0), background: b}
			if got := out.effectDue(out.lastFrame.Add(tc.elapsed), c.effectInterval()); got != tc.due {
				t.Fatalf("due=%v, want %v with policy interval %v", got, tc.due, policy.Interval)
			}
		})
	}
}

func TestBackgroundResizeReservesOldAndNewFrames(t *testing.T) {
	b := &backgroundWorker{cached: backgroundFrame{pixels: make([]byte, 16)}, spare: backgroundFrame{pixels: make([]byte, 16), width: 2, height: 2}}
	if got := b.jobStorage(4, 4); got != 96 {
		t.Fatalf("reserved %d, want old cache+spare+new frame=96", got)
	}
}
func TestCompletedRemovedWorkerReleasesAccounting(t *testing.T) {
	done := make(chan struct{})
	close(done)
	c := &Client{removed: []*lockOut{{background: &backgroundWorker{done: done, storageBytes: 64}}}}
	c.collectRemoved()
	if len(c.removed) != 0 || c.pixelBytes() != 0 {
		t.Fatal("retained completed removed worker")
	}
}
func TestBackgroundStateTracksAllOutputsAndResume(t *testing.T) {
	now := time.Now()
	c := &Client{effect: "rain", outputs: map[OutputID]*lockOut{1: {w: 2, h: 2, background: &backgroundWorker{cached: backgroundFrame{pixels: make([]byte, 16), width: 2, height: 2}}}, 2: {w: 2, h: 2}}}
	if got := c.backgroundStatus(now); got != "fallback" {
		t.Fatal("partial decoration reported", got)
	}
	delete(c.outputs, 2)
	c.resumeAt = now.Add(-time.Second)
	if got := c.backgroundStatus(now); got != "animated" {
		t.Fatal("expired delay still frozen", got)
	}
	c.frozen = true
	if got := c.backgroundStatus(now); got != "frozen" {
		t.Fatal(got)
	}
}

func TestMissedWorkerDeadlineFallsBackWithoutWaiting(t *testing.T) {
	now := time.Now()
	b := &backgroundWorker{busy: true, stopped: make(chan struct{}), jobDeadline: now, cached: backgroundFrame{pixels: make([]byte, 16), width: 2, height: 2}, storageBytes: 32}
	out := &lockOut{w: 2, h: 2, background: b}
	c := &Client{state: New(), outputs: map[OutputID]*lockOut{1: out}}
	c.expireBackground(now)
	if !b.failed || !out.pending || out.backgroundPixels() != nil {
		t.Fatal("stalled work did not freeze to fallback")
	}
	if b.storageBytes != 32 {
		t.Fatal("released hung worker accounting")
	}
	select {
	case <-b.stopped:
	default:
		t.Fatal("did not stop future work")
	}
}

func TestFrozenInFlightResultKeepsVisibleCache(t *testing.T) {
	old := make([]byte, 16)
	old[0] = 3
	fresh := make([]byte, 16)
	fresh[0] = 9
	b := &backgroundWorker{busy: true, results: make(chan backgroundFrame, 1), cached: backgroundFrame{pixels: old, width: 2, height: 2}, storageBytes: 32}
	b.results <- backgroundFrame{pixels: fresh, width: 2, height: 2}
	out := &lockOut{w: 2, h: 2, background: b}
	c := &Client{frozen: true, outputs: map[OutputID]*lockOut{1: out}}
	c.collectBackground(out)
	if out.backgroundPixels()[0] != 3 {
		t.Fatal("in-flight frame moved background during typing")
	}
	if b.spare.pixels[0] != 9 || b.storageBytes != 32 {
		t.Fatal("completed frame/accounting lost")
	}
}

func wallpaperTestAsset(t *testing.T) *wallpaperAsset {
	t.Helper()
	path := filepath.Join(t.TempDir(), "wallpaper.png")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	img := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	for y := range 2 {
		for x := range 2 {
			img.Set(x, y, color.NRGBA{R: 200, B: 20, A: 255})
		}
	}
	err = png.Encode(f, img)
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	return &wallpaperAsset{path: path}
}
func takeWallpaperFrame(t *testing.T, b *backgroundWorker, w, h int, previous backgroundFrame) backgroundFrame {
	t.Helper()
	b.jobs <- backgroundJob{width: w, height: h, previous: previous}
	select {
	case frame := <-b.results:
		if frame.err != nil {
			t.Fatal(frame.err)
		}
		return frame
	case <-time.After(time.Second):
		t.Fatal("wallpaper worker stalled")
		return backgroundFrame{}
	}
}
func TestWallpaperSharedDecodeSurvivesFileRemovalAndAlternatingOutputs(t *testing.T) {
	asset := wallpaperTestAsset(t)
	first := newBackgroundWorker("", "", asset, func() {}, effectPolicy{}, newCpuBackend)
	second := newBackgroundWorker("", "", asset, func() {}, effectPolicy{}, newCpuBackend)
	defer first.stop()
	defer second.stop()
	a := takeWallpaperFrame(t, first, 4, 4, backgroundFrame{})
	if err := os.Remove(asset.path); err != nil {
		t.Fatal(err)
	}
	b := takeWallpaperFrame(t, second, 8, 6, backgroundFrame{})
	for range 10 {
		a = takeWallpaperFrame(t, first, 4, 4, a)
		b = takeWallpaperFrame(t, second, 8, 6, b)
		if a.pixels[2] != 66 || b.pixels[2] != 66 {
			t.Fatal("shared dimmed image not preserved")
		}
	}
}
func TestStaticWallpaperReusesSlotAndDoesNotSchedulePerKey(t *testing.T) {
	asset := wallpaperTestAsset(t)
	worker := newBackgroundWorker("", "", asset, func() {}, effectPolicy{}, newCpuBackend)
	defer worker.stop()
	first := takeWallpaperFrame(t, worker, 4, 4, backgroundFrame{})
	again := takeWallpaperFrame(t, worker, 4, 4, first)
	if &first.pixels[0] != &again.pixels[0] {
		t.Fatal("static scaling allocated despite reusable slot")
	}
	worker.cached = again
	worker.busy = false
	out := &lockOut{w: 4, h: 4, background: worker}
	c := &Client{wallpaper: asset, frozen: true}
	for range 20 {
		c.scheduleBackground(out, time.Now())
	}
	if len(worker.jobs) != 0 || worker.busy {
		t.Fatal("static wallpaper scheduled on key repaint")
	}
}
func TestWallpaperReservationIncludesSharedAssetAndRetiredWorker(t *testing.T) {
	done := make(chan struct{})
	worker := &backgroundWorker{done: done, storageBytes: 64}
	c := &Client{wallpaper: &wallpaperAsset{}, removed: []*lockOut{{background: worker}}}
	if got := c.pixelBytes(); got != wallpaperReservation+64 {
		t.Fatal("decoded asset omitted", got)
	}
	c.releaseWallpaperIfStopped()
	if c.wallpaper == nil {
		t.Fatal("released image reachable by hung worker")
	}
	close(done)
	c.collectRemoved()
	c.releaseWallpaperIfStopped()
	if c.wallpaper != nil || c.pixelBytes() != 0 {
		t.Fatal("retained stopped shared asset")
	}
}
func TestWallpaperDecorationEvictedBeforeForegroundAdmission(t *testing.T) {
	c := &Client{wallpaper: &wallpaperAsset{}}
	if err := c.reserveForeground(maxPixelBytes); err != nil {
		t.Fatal(err)
	}
	if c.wallpaper != nil {
		t.Fatal("wallpaper reservation displaced opaque surfaces")
	}
}
func TestBlurBackdropReportsFrozenAndNeverSchedules(t *testing.T) {
	c := &Client{blur: true, blurRadius: 24, outputs: map[OutputID]*lockOut{
		1: {w: 2, h: 2, backdrop: &render.Framebuffer{Width: 1, Height: 1, Stride: 4, Pix: make([]byte, 4)}, blurW: 2, blurH: 2},
	}}
	if got := c.backgroundStatus(time.Now()); got != "frozen" {
		t.Fatal("captured backdrop reported", got)
	}
	c.scheduleBackground(c.outputs[1], time.Now())
	if c.outputs[1].background != nil {
		t.Fatal("effect scheduled under a frozen backdrop")
	}
	c.outputs[1].backdrop, c.outputs[1].blurW, c.outputs[1].blurH = nil, 0, 0
	if got := c.backgroundStatus(time.Now()); got != "fallback" {
		t.Fatal("missed capture reported", got)
	}
}

func TestSelectedEffectOverridesCapturedBackdrop(t *testing.T) {
	now := time.Now()
	b := &backgroundWorker{jobs: make(chan backgroundJob, 1), cached: backgroundFrame{width: 2, height: 2, pixels: make([]byte, 16)}}
	out := &lockOut{w: 2, h: 2, background: b, backdrop: render.New(1, 1), blurW: 2, blurH: 2}
	c := &Client{effect: "fire", outputs: map[OutputID]*lockOut{1: out}}
	c.scheduleBackground(out, now)
	if len(b.jobs) != 1 {
		t.Fatal("captured backdrop prevented the selected effect from stepping")
	}
	if got := c.backgroundStatus(now); got != "animated" {
		t.Fatal("selected effect reported", got)
	}
}

func TestFirePacesAtHalfSpeedAfterPolicy(t *testing.T) {
	for _, tc := range []struct {
		effect  string
		fps     int
		battery bool
		want    time.Duration
	}{
		{"fire", 60, false, 29999998 * time.Nanosecond},
		{"fire", 20, false, 90 * time.Millisecond},
		{"fire", 60, true, 2 * (time.Second / 30)},
		{"fire", 20, true, 90 * time.Millisecond},
		{"rain", 60, false, 14999999 * time.Nanosecond},
		{"rain", 60, true, time.Second / 30},
	} {
		for _, backend := range []string{"cpu", "gpu"} {
			t.Run(fmt.Sprintf("%s/%s/%d/battery=%v", tc.effect, backend, tc.fps, tc.battery), func(t *testing.T) {
				c := &Client{}
				c.SetEffectRate(tc.fps)
				policy := resolveEffectPolicy(backend, true, c.effectInterval(), func() bool { return tc.battery })
				b := newBackgroundWorker(tc.effect, "nord", nil, func() {}, policy, newCpuBackend)
				t.Cleanup(func() { b.stop(); <-b.done })
				out := &lockOut{lastFrame: time.Unix(10, 0), background: b}
				if b.interval != tc.want {
					t.Fatalf("interval=%v want=%v", b.interval, tc.want)
				}
				if out.effectDue(out.lastFrame.Add(tc.want-1), c.effectInterval()) {
					t.Fatal("tick before half-speed deadline")
				}
				if !out.effectDue(out.lastFrame.Add(tc.want), c.effectInterval()) {
					t.Fatal("missed half-speed deadline")
				}
			})
		}
	}
}

func TestScheduleBackgroundDoesNotCommitBeforeEffectDeadline(t *testing.T) {
	now := time.Unix(100, 0)
	out := &lockOut{
		w:          2,
		h:          2,
		lastFrame:  now,
		background: &backgroundWorker{jobs: make(chan backgroundJob, 1)},
	}
	c := &Client{
		effect:  "rain",
		outputs: map[OutputID]*lockOut{1: out},
	}
	c.SetEffectRate(20)

	defer func() {
		if recovered := recover(); recovered != nil {
			t.Fatalf("waiting for the next effect frame touched the Wayland surface: %v", recovered)
		}
	}()
	c.scheduleBackground(out, now.Add(20*time.Millisecond))

	if out.callback != nil {
		t.Fatal("waiting for the next effect frame armed a callback without a paint")
	}
	if want := now.Add(50 * time.Millisecond); !out.effectDeadline.Equal(want) {
		t.Fatalf("effect deadline = %v, want %v", out.effectDeadline, want)
	}
	if out.background.busy || len(out.background.jobs) != 0 {
		t.Fatal("effect started before its frame interval elapsed")
	}
}

func TestScheduleDueBackgroundStartsEffectAtDeadline(t *testing.T) {
	now := time.Unix(100, 0)
	deadline := now.Add(50 * time.Millisecond)
	b := &backgroundWorker{jobs: make(chan backgroundJob, 1)}
	out := &lockOut{w: 2, h: 2, lastFrame: now, effectDeadline: deadline, background: b}
	c := &Client{effect: "rain", outputs: map[OutputID]*lockOut{1: out}}
	c.SetEffectRate(20)

	c.scheduleDueBackgrounds(deadline.Add(-time.Nanosecond))
	if b.busy || len(b.jobs) != 0 {
		t.Fatal("effect started before its deadline")
	}
	c.scheduleDueBackgrounds(deadline)
	if !b.busy || len(b.jobs) != 1 {
		t.Fatal("effect did not start at its deadline")
	}
	if !out.effectDeadline.IsZero() {
		t.Fatal("started effect retained its deadline")
	}
	if job := <-b.jobs; job.width != 2 || job.height != 2 {
		t.Fatalf("job size = %dx%d, want 2x2", job.width, job.height)
	}
}

func TestEffectDeadlineWaitsForFrameCallback(t *testing.T) {
	now := time.Unix(100, 0)
	deadline := now.Add(50 * time.Millisecond)
	b := &backgroundWorker{jobs: make(chan backgroundJob, 1)}
	out := &lockOut{
		w:              2,
		h:              2,
		callback:       &client.Callback{},
		lastFrame:      now,
		effectDeadline: deadline,
		background:     b,
	}
	c := &Client{effect: "rain", outputs: map[OutputID]*lockOut{1: out}}
	c.SetEffectRate(20)

	c.scheduleDueBackgrounds(deadline)
	if b.busy || len(b.jobs) != 0 {
		t.Fatal("effect started while the frame callback was still pending")
	}
	if !out.effectDeadline.IsZero() {
		t.Fatal("expired effect deadline remained scheduled")
	}

	out.callback = nil
	c.scheduleBackground(out, deadline)
	if !b.busy || len(b.jobs) != 1 {
		t.Fatal("effect did not resume after the frame callback fired")
	}
}

func TestFreezingMotionClearsEffectDeadlines(t *testing.T) {
	now := time.Unix(100, 0)
	out := &lockOut{effectDeadline: now.Add(50 * time.Millisecond)}
	c := &Client{outputs: map[OutputID]*lockOut{1: out}}

	c.SetMotionFrozen(true, now)
	if !out.effectDeadline.IsZero() {
		t.Fatal("frozen output retained an effect deadline")
	}
}

func TestApplyPresentationClearsEffectDeadlines(t *testing.T) {
	out := &lockOut{effectDeadline: time.Now().Add(time.Second)}
	c := &Client{outputs: map[OutputID]*lockOut{1: out}}

	c.ApplyPresentation("rain", "nord", false, "cpu", false)
	if !out.effectDeadline.IsZero() {
		t.Fatal("new presentation retained an old effect deadline")
	}
}

func TestEffectCadenceUsesRequestedRate(t *testing.T) {
	c := &Client{}
	for _, fps := range []int{20, 30, 60, 120} {
		c.SetEffectRate(fps)
		if want, got := time.Second/time.Duration(fps), c.effectCadence(); got != want {
			t.Errorf("rate %d cadence = %v, want %v", fps, got, want)
		}
	}
	c.SetEffectRate(0)
	if got := c.effectCadence(); got != defaultEffectEvery {
		t.Fatalf("default cadence = %v, want %v", got, defaultEffectEvery)
	}
}

func TestPresentationRefreshRequantizesEffectDeadline(t *testing.T) {
	start := time.Unix(100, 0)
	callbackAt := start.Add(6 * time.Millisecond)
	out := &lockOut{
		w:              2,
		h:              2,
		lastFrame:      start,
		lastCallbackAt: callbackAt,
		effectDeadline: start.Add(time.Second / 60),
		background:     &backgroundWorker{jobs: make(chan backgroundJob, 1)},
	}
	c := &Client{effect: "rain", outputs: map[OutputID]*lockOut{1: out}}
	c.SetEffectRate(60)

	c.updatePresentationRefresh(out, 6*time.Millisecond, callbackAt)

	want := callbackAt.Add(2 * 6 * time.Millisecond)
	if !out.effectDeadline.Equal(want) {
		t.Fatalf("effect deadline = %v, want presentation-aligned %v", out.effectDeadline, want)
	}
}

func TestEffectCadenceAtRefreshRates(t *testing.T) {
	// Match the callback-sampled effect counts from before removing empty commits.
	want := map[int]map[int]int{
		60:  {20: 20, 30: 30, 60: 60},
		120: {20: 20, 30: 30, 60: 60},
		165: {20: 21, 30: 33, 60: 55},
	}
	for _, hz := range []int{60, 120, 165} {
		for _, fps := range []int{20, 30, 60} {
			t.Run(fmt.Sprintf("%dHz/%dfps", hz, fps), func(t *testing.T) {
				c := &Client{effect: "rain"}
				c.SetEffectRate(fps)
				framePeriod := time.Second / time.Duration(hz)
				b := &backgroundWorker{jobs: make(chan backgroundJob, 1)}
				// A wp_presentation v2 feedback event supplies this refresh estimate.
				out := &lockOut{w: 2, h: 2, background: b, refreshInterval: framePeriod}
				c.outputs = map[OutputID]*lockOut{1: out}
				start := time.Unix(100, 0)
				end := start.Add(time.Second)
				nextFrame := start.Add(framePeriod)
				starts := 0

				// ponytail: finish jobs immediately to isolate timer cadence; live renderer timing is the hardware gate.
				completeJob := func(now time.Time) {
					if !b.busy {
						return
					}
					<-b.jobs
					starts++
					b.busy = false
					out.callback = &client.Callback{}
					c.scheduleBackground(out, now)
				}

				c.scheduleBackground(out, start)
				completeJob(start)
				for {
					next := end
					if nextFrame.Before(next) {
						next = nextFrame
					}
					if !out.effectDeadline.IsZero() && out.effectDeadline.Before(next) {
						next = out.effectDeadline
					}
					if !next.Before(end) {
						break
					}

					if !out.effectDeadline.IsZero() && !next.Before(out.effectDeadline) {
						c.scheduleDueBackgrounds(next)
						completeJob(next)
					}
					if next.Equal(nextFrame) {
						nextFrame = nextFrame.Add(framePeriod)
						if out.callback != nil {
							out.callback = nil
							out.lastCallbackAt = next
							c.scheduleBackground(out, next)
							completeJob(next)
						}
					}
				}

				expected := want[hz][fps]
				if starts < expected-1 || starts > expected+1 {
					t.Fatalf("effect jobs in 1s = %d, want previous cadence %d±1", starts, expected)
				}
			})
		}
	}
}

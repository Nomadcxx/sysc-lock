package lockd

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestTypingAndPamFreezeEffect(t *testing.T) {
	c := &Client{}
	now := time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC)
	c.SetMotionFrozen(true, now)
	if c.motionAllowed(now.Add(time.Hour)) {
		t.Fatal("typing/busy effect ticks")
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
	if out.effectDue(now.Add(20 * time.Millisecond)) {
		t.Fatal("exceeded 20fps")
	}
	if !out.effectDue(now.Add(50 * time.Millisecond)) {
		t.Fatal("missed frame deadline")
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
	first := newBackgroundWorker("", "", asset, func() {})
	second := newBackgroundWorker("", "", asset, func() {})
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
		if a.pixels[2] != 200 || b.pixels[2] != 200 {
			t.Fatal("shared image not preserved")
		}
	}
}
func TestStaticWallpaperReusesSlotAndDoesNotSchedulePerKey(t *testing.T) {
	asset := wallpaperTestAsset(t)
	worker := newBackgroundWorker("", "", asset, func() {})
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

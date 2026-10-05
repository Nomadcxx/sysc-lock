package lockd

import (
	"os"
	"strings"
	"testing"
)

func TestLockBuffersNeverReuseBusySlot(t *testing.T) {
	o := &lockOut{buffers: []*shmBuffer{{w: 2, h: 2, busy: true}, {w: 2, h: 2}}}
	if got := o.availableBuffer(2, 2); got != o.buffers[1] {
		t.Fatal("selected busy slot")
	}
	o.buffers[1].busy = true
	if o.availableBuffer(2, 2) != nil {
		t.Fatal("reused busy slot")
	}
}
func TestResizeRetiresOneGeneration(t *testing.T) {
	o := &lockOut{buffers: []*shmBuffer{{w: 2, h: 2, busy: true}, {w: 2, h: 2, busy: true}}}
	if !o.canResize(4, 4) {
		t.Fatal("first resize refused")
	}
	for _, b := range o.buffers {
		b.retired = true
	}
	o.buffers = append(o.buffers, &shmBuffer{w: 4, h: 4, busy: true}, &shmBuffer{w: 4, h: 4, busy: true})
	if o.canResize(6, 6) {
		t.Fatal("allocated third generation")
	}
}
func TestPixelBudgetIncludesRetiredBuffers(t *testing.T) {
	c := &Client{outputs: map[OutputID]*lockOut{1: {buffers: []*shmBuffer{{data: make([]byte, 32), retired: true}, {data: make([]byte, 32)}}}}}
	if c.pixelBytes() != 64 {
		t.Fatal("retired pixels omitted")
	}
	for _, wh := range [][2]int{{-1, 2}, {1 << 30, 1 << 30}, {0, 2}} {
		if _, err := bufferBytes(wh[0], wh[1]); err == nil {
			t.Fatal("bad geometry accepted", wh)
		}
	}
}

func TestForegroundAdmissionDegradesDecoration(t *testing.T) {
	done := make(chan struct{})
	close(done)
	b := &backgroundWorker{done: done, storageBytes: maxPixelBytes - 16, stopped: make(chan struct{})}
	c := &Client{outputs: map[OutputID]*lockOut{1: {background: b}}}
	if err := c.reserveForeground(32); err != nil {
		t.Fatal(err)
	}
	if !b.failed || b.storageBytes != 0 {
		t.Fatal("decoration retained at foreground budget ceiling")
	}
}

func TestConfigureStormCoalesces(t *testing.T) {
	retired := &shmBuffer{busy: true, retired: true, w: 2, h: 2}
	current := &shmBuffer{busy: true, w: 4, h: 4}
	out := &lockOut{buffers: []*shmBuffer{retired, current}, pending: true}
	c := &Client{}
	for size := 6; size <= 60; size += 2 {
		out.w, out.h = size, size
		ready, err := c.prepareBuffers(out, size, size)
		if ready || err != nil || len(out.buffers) != 2 {
			t.Fatal("configure storm allocated another generation", size, ready, err)
		}
	}
	if out.w != 60 || out.h != 60 || !out.pending {
		t.Fatal("latest configure not retained")
	}
}
func TestPartialAllocationDiscardsNewGeneration(t *testing.T) {
	data, err := os.ReadFile("buffers.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "c.discardNewBuffers(out)") {
		t.Fatal("partial allocation leaves incomplete generation")
	}
}
func TestRemovedWorkerStaysAccountedUntilExit(t *testing.T) {
	done := make(chan struct{})
	c := &Client{removed: []*lockOut{{background: &backgroundWorker{done: done, storageBytes: 64}}}}
	c.collectRemoved()
	if len(c.removed) != 1 || c.pixelBytes() != 64 {
		t.Fatal("freed in-flight removed worker accounting")
	}
	close(done)
	c.collectRemoved()
	if len(c.removed) != 0 || c.pixelBytes() != 0 {
		t.Fatal("retained exited worker")
	}
}

func TestDiscardNewGenerationRetainsBusyRetiredSlot(t *testing.T) {
	old := &shmBuffer{busy: true, retired: true}
	partial := &shmBuffer{}
	out := &lockOut{buffers: []*shmBuffer{old, partial}}
	c := &Client{}
	c.discardNewBuffers(out)
	if len(out.buffers) != 1 || out.buffers[0] != old {
		t.Fatal("partial rollback released old compositor slot")
	}
}
func TestLateReleaseAfterOutputRemoval(t *testing.T) {
	old := &lockOut{id: 1, removed: true}
	slot := &shmBuffer{busy: true, retired: true}
	old.buffers = []*shmBuffer{slot}
	replacement := &lockOut{id: 1}
	c := &Client{state: New(), removed: []*lockOut{old}, outputs: map[OutputID]*lockOut{1: replacement}}
	c.releaseBuffer(old, slot)
	if len(c.removed) != 0 || len(old.buffers) != 0 || c.outputs[1] != replacement {
		t.Fatal("late release retained resource or touched replacement")
	}
	c.releaseBuffer(old, slot) // duplicate cleanup stays idempotent
}

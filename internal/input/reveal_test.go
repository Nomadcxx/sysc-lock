package input

import (
	"testing"
	"time"
)

func TestRevealStartsHiddenAndAutoHidesWhenIdle(t *testing.T) {
	var r Reveal
	t0 := time.Unix(1000, 0)
	if r.Tick(t0, false) || !r.Deadline().IsZero() {
		t.Fatal("zero value must be hidden")
	}
	r.Show(t0)
	if !r.Deadline().Equal(t0.Add(HideAfter)) {
		t.Fatal(r.Deadline())
	}
	if !r.Tick(t0.Add(7*time.Second), false) {
		t.Fatal("hid early")
	}
	if r.Tick(t0.Add(HideAfter), false) {
		t.Fatal("stayed past the idle limit")
	}
	if !r.Deadline().IsZero() {
		t.Fatal("hidden entry has no deadline")
	}
}

func TestRevealStaysUpWhileActiveAndRestartsTheTimer(t *testing.T) {
	var r Reveal
	t0 := time.Unix(1000, 0)
	r.Show(t0)
	if !r.Tick(t0.Add(20*time.Second), true) {
		t.Fatal("active entry must stay up")
	}
	if !r.Tick(t0.Add(27*time.Second), false) {
		t.Fatal("timer must restart from the last activity")
	}
	if r.Tick(t0.Add(28*time.Second), false) {
		t.Fatal("did not hide 8s after the last activity")
	}
}

func TestShowRestartsAndHideIsImmediate(t *testing.T) {
	var r Reveal
	t0 := time.Unix(1000, 0)
	r.Show(t0)
	r.Show(t0.Add(5 * time.Second))
	if !r.Tick(t0.Add(12*time.Second), false) {
		t.Fatal("Show must restart the timer")
	}
	r.Hide()
	if r.Tick(t0.Add(12*time.Second), true) {
		t.Fatal("Hide must win over activity")
	}
}

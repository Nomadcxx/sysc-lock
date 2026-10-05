package input

import "time"

// HideAfter is how long a revealed, empty entry waits before hiding again.
const HideAfter = 8 * time.Second

// Reveal tracks whether the password entry is shown. A hidden entry holds no
// password; the zero value is hidden.
type Reveal struct {
	shown bool
	since time.Time
}

// Show reveals the entry and restarts the idle timer.
func (r *Reveal) Show(now time.Time) { r.shown, r.since = true, now }

// Hide hides the entry at once.
func (r *Reveal) Hide() { r.shown = false }

// Tick reports whether the entry is visible at now. active is true while the
// field holds text or a verification is running; either keeps the entry up and
// restarts the idle timer.
func (r *Reveal) Tick(now time.Time, active bool) bool {
	if !r.shown {
		return false
	}
	if active {
		r.since = now
	}
	if !now.Before(r.since.Add(HideAfter)) {
		r.shown = false
	}
	return r.shown
}

// Deadline is when the entry will hide if left idle; zero when hidden.
func (r *Reveal) Deadline() time.Time {
	if !r.shown {
		return time.Time{}
	}
	return r.since.Add(HideAfter)
}

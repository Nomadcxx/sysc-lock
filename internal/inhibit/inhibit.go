// Package inhibit holds the logind sleep inhibitor that keeps the session
// from suspending between lock request and the compositor-confirmed locked
// state (ext-session-lock spec: "locked" is only sent after every output has
// presented a locked frame).
package inhibit

import "sync"

// Backend is the dbus-facing half (swapped for a fake in tests).
type Backend interface {
	Inhibit(what, who, why, mode string) error
	Release()
}

// Take inhibits sleep and returns an idempotent release function.
func Take(b Backend) (func(), error) {
	if err := b.Inhibit("sleep", "sysc-lock", "locking session", "block"); err != nil {
		return nil, err
	}
	var once sync.Once
	return func() { once.Do(b.Release) }, nil
}

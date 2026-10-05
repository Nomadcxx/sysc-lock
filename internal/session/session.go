// Package session owns acquisition intent and confirmed state across shell lifetimes.
package session

import (
	"errors"
	"fmt"
	"github.com/Nomadcxx/sysc-lock/internal/lockd"
	"os"
	"sync"
	"time"
)

// Snapshot is credential-free. Missing owner/state is never an unlock receipt.
type Snapshot struct {
	Sequence        uint64
	Generation      uint64
	ConfirmedUnlock uint64
	Session         string
	Compositor      string
	Phase           string
	ForegroundReady bool
	Background      string
	SleepProtected  bool
	SleepError      string
}

type Session struct {
	mu               sync.Mutex
	identity         Identity
	path             string
	marker           marker
	snapshot         Snapshot
	recovering       bool
	active           bool
	activeGeneration uint64
	Requests         chan uint64
	Changed          chan struct{}
	release          func()
	sleeping         bool
	deadline         time.Time
}

func New(identity Identity, path string) (*Session, error) {
	if identity.Session == "" || identity.Compositor == "" || identity.UID != uint32(os.Getuid()) {
		return nil, fmt.Errorf("invalid session identity")
	}
	m, err := readMarker(path)
	if errors.Is(err, os.ErrNotExist) {
		m = marker{Identity: identity}
	} else if err != nil {
		return nil, err
	}
	if m.Identity != identity {
		return nil, fmt.Errorf("recovery state belongs to another session/compositor")
	}
	s := &Session{identity: identity, path: path, marker: m, Requests: make(chan uint64, 1), Changed: make(chan struct{}, 1)}
	s.snapshot = Snapshot{Generation: m.Generation, ConfirmedUnlock: m.ConfirmedUnlock, Session: identity.Session, Compositor: identity.Compositor, Phase: "idle", Background: "fallback"}
	if m.Intent != 0 {
		s.recovering = true
		s.active = true
		s.activeGeneration = m.Intent
		s.snapshot.Phase = "recovering"
		s.Requests <- m.Intent
	}
	return s, nil
}
func (s *Session) Snapshot() Snapshot { s.mu.Lock(); defer s.mu.Unlock(); return s.snapshot }
func (s *Session) changed() {
	s.snapshot.Sequence++
	select {
	case s.Changed <- struct{}{}:
	default:
	}
}
func (s *Session) Lock() (Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active && s.marker.Intent == 0 {
		return s.snapshot, fmt.Errorf("lock cleanup in progress")
	}
	if s.active || s.marker.Intent != 0 {
		return s.snapshot, nil
	}
	m := s.marker
	m.Generation++
	m.Intent = m.Generation
	if m.Generation == 0 {
		return s.snapshot, fmt.Errorf("generation exhausted")
	}
	if err := writeMarker(s.path, m); err != nil {
		return s.snapshot, err
	}
	s.marker = m
	s.active = true
	s.activeGeneration = m.Generation
	s.recovering = false
	s.snapshot.Generation = m.Generation
	s.snapshot.Phase = "requesting"
	s.snapshot.ForegroundReady = false
	s.changed()
	s.Requests <- m.Generation
	return s.snapshot, nil
}

// BeginUnlock admits authenticated unlock atomically against pending sleep.
// The Wayland owner calls this before changing its own phase or sending unlock.
func (s *Session) BeginUnlock(generation uint64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if generation != s.activeGeneration || generation != s.marker.Intent || !s.active || s.snapshot.Phase != "sealed" {
		return fmt.Errorf("obsolete or unsealed unlock generation")
	}
	if s.sleeping {
		return lockd.ErrUnlockDeferred
	}
	s.snapshot.Phase = "unlocking"
	s.changed()
	return nil
}

func (s *Session) Report(generation uint64, event lockd.Snapshot) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if generation != s.marker.Intent || !s.active {
		return nil
	}
	s.snapshot.ForegroundReady = event.UIReady
	if event.Background != "" {
		s.snapshot.Background = event.Background
	}
	switch event.Phase {
	case lockd.Locked:
		s.snapshot.Phase = "sealed"
		if s.sleeping {
			s.dropDelay()
		}
	case lockd.Unlocking:
		s.snapshot.Phase = "unlocking"
	case lockd.Done:
		m := s.marker
		m.Intent = 0
		m.ConfirmedUnlock = generation
		if s.sleeping {
			m.Generation++
			if m.Generation == 0 {
				s.snapshot.Phase = "sealed/unknown"
				s.changed()
				return fmt.Errorf("generation exhausted during sleep recapture")
			}
			m.Intent = m.Generation
		}
		if err := writeMarker(s.path, m); err != nil {
			s.snapshot.Phase = "sealed/unknown"
			s.changed()
			return err
		}
		s.marker = m
		s.snapshot.ConfirmedUnlock = generation
		s.snapshot.Phase = "idle"
		if s.sleeping {
			// Persist next intent before publishing the old receipt; the shell stays held.
			s.snapshot.Generation = m.Generation
			s.snapshot.Phase = "requesting"
			s.snapshot.Background = "fallback"
		}
		s.snapshot.ForegroundReady = false
	}
	s.changed()
	return nil
}
func (s *Session) End(generation uint64, phase lockd.Phase, err error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if generation != s.activeGeneration || !s.active {
		return nil
	}
	s.active = false
	if phase == lockd.Done && s.marker.Intent == 0 {
		s.changed()
		return err
	}
	if phase == lockd.Done && s.marker.ConfirmedUnlock == generation && s.marker.Intent > generation && err == nil {
		// Old core cleanup is complete; admit the staged acquisition exactly once.
		s.active = true
		s.activeGeneration = s.marker.Intent
		s.recovering = false
		s.Requests <- s.marker.Intent
		s.changed()
		return nil
	}
	if phase == lockd.Refused && !s.recovering && s.snapshot.Phase == "requesting" && err == nil {
		m := s.marker
		m.Intent = 0
		if e := writeMarker(s.path, m); e != nil {
			return e
		}
		s.marker = m
		s.snapshot.Phase = "failed-before-acquisition"
	} else {
		s.snapshot.Phase = "sealed/unknown"
	}
	s.snapshot.ForegroundReady = false
	s.changed()
	return err
}
func (s *Session) dropDelay() {
	if s.release != nil {
		s.release()
		s.release = nil
	}
	s.snapshot.SleepProtected = false
}

// Arm is called before sd_notify READY, and again after resume or unlock.
func (s *Session) Arm(take func() (func(), error)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.release != nil {
		return
	}
	release, err := take()
	if err != nil {
		s.snapshot.SleepProtected = false
		s.snapshot.SleepError = "Sleep protection unavailable"
	} else {
		s.release = release
		s.snapshot.SleepProtected = true
		s.snapshot.SleepError = ""
	}
	s.changed()
}
func (s *Session) PrepareSleep(now time.Time, limit time.Duration) error {
	s.mu.Lock()
	if !s.sleeping {
		s.sleeping = true
		s.deadline = now.Add(max(time.Millisecond, limit-100*time.Millisecond))
	}
	// Unlock is confirmed, but the old core may still own cleanup. Stage the
	// next durable intent now; End queues it only after that cleanup completes.
	if s.active && s.marker.Intent == 0 {
		m := s.marker
		m.Generation++
		m.Intent = m.Generation
		var err error
		if m.Generation == 0 {
			err = fmt.Errorf("generation exhausted during sleep recapture")
		} else {
			err = writeMarker(s.path, m)
		}
		if err != nil {
			s.snapshot.Phase = "sealed/unknown"
			s.snapshot.SleepError = "Lock before sleep unavailable"
		} else {
			s.marker = m
			s.snapshot.Generation = m.Generation
			s.snapshot.Phase = "requesting"
			s.snapshot.ForegroundReady = false
			s.snapshot.Background = "fallback"
		}
		s.changed()
		s.mu.Unlock()
		return err
	}
	sealed := s.snapshot.Phase == "sealed"
	if sealed {
		s.dropDelay()
		s.changed()
	}
	s.mu.Unlock()
	if sealed {
		return nil
	}
	_, err := s.Lock()
	return err
}
func (s *Session) CheckSleepDeadline(now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sleeping && s.release != nil && !now.Before(s.deadline) {
		s.dropDelay()
		s.snapshot.SleepError = "Lock before sleep deadline expired"
		s.changed()
	}
}
func (s *Session) Resume(take func() (func(), error)) error {
	s.mu.Lock()
	retry := s.sleeping && s.snapshot.Phase != "sealed" && s.snapshot.Phase != "unlocking"
	s.sleeping = false
	s.deadline = time.Time{}
	s.mu.Unlock()
	s.Arm(take)
	if retry {
		_, err := s.Lock()
		if err != nil {
			s.mu.Lock()
			s.snapshot.SleepError = "Lock after resume failed"
			s.changed()
			s.mu.Unlock()
		}
		return err
	}
	return nil
}
func (s *Session) Close() { s.mu.Lock(); defer s.mu.Unlock(); s.dropDelay() }

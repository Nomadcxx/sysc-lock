// Package lockd holds the locker's session-lock state machine: pure logic,
// no Wayland types. Wire glue consumes it (see internal/client).
package lockd

import (
	"errors"
	"sync"
)

type OutputID uint32

type Phase int

const (
	Idle Phase = iota
	Requesting
	Locked
	Unlocking
	Done
	Refused
)

var (
	ErrInvalidTransition = errors.New("lockd: invalid phase transition")
	ErrInvalidUnlock     = errors.New("lockd: unlock before locked")
	ErrDuplicateOutput   = errors.New("lockd: output already registered")
	ErrWrongSize         = errors.New("lockd: committed size differs from configure")
	ErrNotAcked          = errors.New("lockd: commit before ack-configure")
	ErrUnknownOutput     = errors.New("lockd: unknown output")
)

type Output struct {
	Width, Height uint32
	PendingSerial uint32
	AckedSerial   uint32
	Acked         bool
	Ready         bool
}

type State struct {
	mu      sync.Mutex
	phase   Phase
	outputs map[OutputID]*Output
}

func New() *State {
	return &State{outputs: make(map[OutputID]*Output)}
}

func (s *State) Phase() Phase {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.phase
}

// HandshakeReady reports whether the "locked" stdout handshake line may be
// emitted: only while Locked (invariant: never signal before the compositor
// says locked, i.e. after first frames on every output).
func (s *State) HandshakeReady() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.phase == Locked
}

func (s *State) AddOutput(id OutputID, w, h uint32) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, dup := s.outputs[id]; dup {
		return ErrDuplicateOutput
	}
	s.outputs[id] = &Output{Width: w, Height: h}
	return nil
}

func (s *State) Configure(id OutputID, serial, w, h uint32) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	o, ok := s.outputs[id]
	if !ok {
		return ErrUnknownOutput
	}
	o.PendingSerial, o.Width, o.Height = serial, w, h
	o.Acked, o.Ready = false, false
	return nil
}

func (s *State) Ack(id OutputID) (uint32, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	o, ok := s.outputs[id]
	if !ok {
		return 0, ErrUnknownOutput
	}
	o.AckedSerial, o.Acked = o.PendingSerial, true
	return o.AckedSerial, nil
}

func (s *State) Commit(id OutputID, w, h uint32) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	o, ok := s.outputs[id]
	if !ok {
		return ErrUnknownOutput
	}
	if !o.Acked {
		return ErrNotAcked
	}
	if w != o.Width || h != o.Height {
		return ErrWrongSize
	}
	o.Ready = true
	return nil
}

func (s *State) transition(from, to Phase) error {
	if s.phase != from {
		return ErrInvalidTransition
	}
	s.phase = to
	return nil
}

func (s *State) LockRequested() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.transition(Idle, Requesting)
}

func (s *State) Locked() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.transition(Requesting, Locked)
}

func (s *State) Finished() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.phase == Refused {
		return nil // finished is sent at most once; idempotent terminal
	}
	return s.transition(Requesting, Refused)
}

func (s *State) Unlock() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.phase != Locked {
		return ErrInvalidUnlock
	}
	s.phase = Unlocking
	return nil
}

func (s *State) Done() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.transition(Unlocking, Done)
}

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
	// Terminated is the compositor-initiated end of the lock after it was
	// granted (finished received while Locked/Unlocking). Terminal.
	Terminated
)

var (
	ErrInvalidTransition = errors.New("lockd: invalid phase transition")
	ErrInvalidUnlock     = errors.New("lockd: unlock before locked")
	ErrDuplicateOutput   = errors.New("lockd: output already registered")
	ErrWrongSize         = errors.New("lockd: committed size differs from configure")
	ErrNotAcked          = errors.New("lockd: commit before ack-configure")
	ErrUnknownOutput     = errors.New("lockd: unknown output")
	ErrNotConfigured     = errors.New("lockd: ack before configure")
)

type Output struct {
	Width, Height uint32
	PendingSerial uint32
	AckedSerial   uint32
	Configured    bool
	Acked         bool
	Ready         bool
}

type State struct {
	mu       sync.Mutex
	phase    Phase
	outputs  map[OutputID]*Output
	sequence uint64
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
	s.sequence++
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
	o.Configured, o.Acked, o.Ready = true, false, false
	s.sequence++
	return nil
}

func (s *State) Ack(id OutputID) (uint32, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	o, ok := s.outputs[id]
	if !ok {
		return 0, ErrUnknownOutput
	}
	if !o.Configured {
		return 0, ErrNotConfigured
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
	s.sequence++
	return nil
}

func (s *State) transition(from, to Phase) error {
	if s.phase != from {
		return ErrInvalidTransition
	}
	s.phase = to
	s.sequence++
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
	switch s.phase {
	case Refused, Terminated:
		return nil // finished is sent at most once; idempotent terminal
	case Requesting:
		s.sequence++
		s.phase = Refused // lock refused: locked was never sent
		return nil
	case Locked, Unlocking:
		s.sequence++
		s.phase = Terminated // compositor ended the granted lock
		return nil
	default:
		return ErrInvalidTransition
	}
}

func (s *State) Unlock() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.phase != Locked {
		return ErrInvalidUnlock
	}
	s.phase = Unlocking
	s.sequence++
	return nil
}

func (s *State) Done() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.transition(Unlocking, Done)
}

// FinishedDestructor is the protocol request to send after Finished():
// destroy if locked was never sent, unlock_and_destroy if it was.
func (s *State) FinishedDestructor() string {
	switch s.Phase() {
	case Refused:
		return "destroy"
	case Terminated:
		return "unlock_and_destroy"
	default:
		return ""
	}
}

// Snapshot contains no owner-mutated maps or protocol objects.
type Snapshot struct {
	Sequence uint64
	Phase    Phase
	UIReady  bool
	UIError  string
}

func (s *State) Snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	v := Snapshot{Sequence: s.sequence, Phase: s.phase, UIReady: len(s.outputs) > 0}
	for _, o := range s.outputs {
		v.UIReady = v.UIReady && o.Ready
	}
	return v
}

func (s *State) RemoveOutput(id OutputID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.outputs, id)
	s.sequence++
}

// CompleteUnlock confirms transport completion before publishing Done.
func (s *State) CompleteUnlock(request, sync func() error) error {
	if err := s.Unlock(); err != nil {
		return err
	}
	if err := request(); err != nil {
		return err
	}
	if err := sync(); err != nil {
		return err
	}
	return s.Done()
}

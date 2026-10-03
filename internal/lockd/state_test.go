package lockd

import (
	"errors"
	"sync"
	"testing"
)

func newReadyState(t *testing.T) *State {
	t.Helper()
	s := New()
	if err := s.AddOutput(1, 1920, 1080); err != nil {
		t.Fatal(err)
	}
	if err := s.Configure(1, 7, 1920, 1080); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Ack(1); err != nil {
		t.Fatal(err)
	}
	if err := s.Commit(1, 1920, 1080); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestPhaseTransitionMatrix(t *testing.T) {
	tests := []struct {
		name string
		fn   func(*State) error
		want Phase
	}{
		{"lock-request", func(s *State) error { return s.LockRequested() }, Requesting},
		{"locked", func(s *State) error {
			if err := s.LockRequested(); err != nil {
				return err
			}
			return s.Locked()
		}, Locked},
		{"refused", func(s *State) error {
			if err := s.LockRequested(); err != nil {
				return err
			}
			return s.Finished()
		}, Refused},
		{"unlock", func(s *State) error {
			if err := s.LockRequested(); err != nil {
				return err
			}
			if err := s.Locked(); err != nil {
				return err
			}
			return s.Unlock()
		}, Unlocking},
		{"done", func(s *State) error {
			if err := s.LockRequested(); err != nil {
				return err
			}
			if err := s.Locked(); err != nil {
				return err
			}
			if err := s.Unlock(); err != nil {
				return err
			}
			return s.Done()
		}, Done},
	}
	for _, tc := range tests {
		s := New()
		if err := tc.fn(s); err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if got := s.Phase(); got != tc.want {
			t.Fatalf("%s: phase=%v want %v", tc.name, got, tc.want)
		}
	}
}

func TestLockedOnlyFromRequesting(t *testing.T) {
	s := newReadyState(t)
	if err := s.Locked(); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("Locked from Requesting-without-lock: got %v", err)
	}
	if err := s.LockRequested(); err != nil {
		t.Fatal(err)
	}
	if err := s.Locked(); err != nil {
		t.Fatalf("Locked from Requesting: %v", err)
	}
	if err := s.Locked(); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("double Locked: got %v", err)
	}
}

func TestRefusedTerminal(t *testing.T) {
	s := New()
	if err := s.LockRequested(); err != nil {
		t.Fatal(err)
	}
	if err := s.Finished(); err != nil {
		t.Fatal(err)
	}
	if err := s.Locked(); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("Locked after Refused: %v", err)
	}
	if err := s.Unlock(); !errors.Is(err, ErrInvalidUnlock) {
		t.Fatalf("Unlock after Refused: %v", err)
	}
}

func TestUnlockBeforeLocked(t *testing.T) {
	s := New()
	if err := s.Unlock(); !errors.Is(err, ErrInvalidUnlock) {
		t.Fatalf("got %v want ErrInvalidUnlock", err)
	}
}

func TestConfigureAckRoundtrip(t *testing.T) {
	s := New()
	if err := s.AddOutput(2, 800, 600); err != nil {
		t.Fatal(err)
	}
	if err := s.Configure(2, 3, 800, 600); err != nil {
		t.Fatal(err)
	}
	if err := s.Configure(2, 9, 800, 600); err != nil {
		t.Fatal(err)
	}
	if got, err := s.Ack(2); err != nil || got != 9 {
		t.Fatalf("Ack newest serial: got (%v,%v) want (9,nil)", got, err)
	}
	if _, err := s.Ack(99); !errors.Is(err, ErrUnknownOutput) {
		t.Fatalf("Ack unknown: %v", err)
	}
	if err := s.Commit(2, 800, 601); !errors.Is(err, ErrWrongSize) {
		t.Fatalf("Commit mismatched size: %v", err)
	}
	if err := s.Commit(2, 800, 600); err != nil {
		t.Fatal(err)
	}
}

func TestCommitNeedsAck(t *testing.T) {
	s := New()
	if err := s.AddOutput(1, 1920, 1080); err != nil {
		t.Fatal(err)
	}
	if err := s.Configure(1, 5, 1920, 1080); err != nil {
		t.Fatal(err)
	}
	if err := s.Commit(1, 1920, 1080); !errors.Is(err, ErrNotAcked) {
		t.Fatalf("got %v want ErrNotAcked", err)
	}
}

func TestDuplicateOutput(t *testing.T) {
	s := newReadyState(t)
	if err := s.AddOutput(1, 640, 480); !errors.Is(err, ErrDuplicateOutput) {
		t.Fatalf("got %v want ErrDuplicateOutput", err)
	}
}

func TestHandshakeFalseUntilLocked(t *testing.T) {
	check := func(s *State) {
		if s.HandshakeReady() {
			t.Fatalf("handshake ready in phase %v", s.Phase())
		}
	}
	s := New()
	check(s)
	if err := s.LockRequested(); err != nil {
		t.Fatal(err)
	}
	check(s)
	if err := s.Finished(); err != nil {
		t.Fatal(err)
	}
	check(s)
	s2 := newReadyState(t)
	check(s2)
	if err := s2.LockRequested(); err != nil {
		t.Fatal(err)
	}
	if err := s2.Locked(); err != nil {
		t.Fatal(err)
	}
	if !s2.HandshakeReady() {
		t.Fatal("handshake not ready in Locked")
	}
	if err := s2.Unlock(); err != nil {
		t.Fatal(err)
	}
	check(s2)
}

func TestConcurrentAccess(t *testing.T) {
	s := newReadyState(t)
	if err := s.AddOutput(2, 800, 600); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(2)
		go func() { defer wg.Done(); _ = s.Configure(2, uint32(i), 800, 600) }()
		go func() { defer wg.Done(); _ = s.Phase() }()
	}
	wg.Wait()
}

func TestFinishedDestructor(t *testing.T) {
	refused := New()
	if err := refused.LockRequested(); err != nil {
		t.Fatal(err)
	}
	if err := refused.Finished(); err != nil {
		t.Fatal(err)
	}
	if got := refused.FinishedDestructor(); got != "destroy" {
		t.Fatalf("refused destructor = %q, want destroy (locked was never sent)", got)
	}

	ended := New()
	if err := ended.LockRequested(); err != nil {
		t.Fatal(err)
	}
	if err := ended.Locked(); err != nil {
		t.Fatal(err)
	}
	if err := ended.Finished(); err != nil {
		t.Fatal(err)
	}
	if got := ended.FinishedDestructor(); got != "unlock_and_destroy" {
		t.Fatalf("terminated destructor = %q, want unlock_and_destroy", got)
	}

	if New().FinishedDestructor() != "" {
		t.Fatal("idle has no destructor")
	}
}

func TestFinishedFromLockedTerminates(t *testing.T) {
	s := New()
	if err := s.LockRequested(); err != nil {
		t.Fatal(err)
	}
	if err := s.Locked(); err != nil {
		t.Fatal(err)
	}
	if err := s.Finished(); err != nil {
		t.Fatalf("compositor-initiated finish while Locked: %v", err)
	}
	if got := s.Phase(); got != Terminated {
		t.Fatalf("phase=%v want Terminated", got)
	}
	if err := s.Unlock(); !errors.Is(err, ErrInvalidUnlock) {
		t.Fatalf("Unlock after Terminated: %v", err)
	}
	if s.HandshakeReady() {
		t.Fatal("handshake ready after Terminated")
	}
	if err := s.Finished(); err != nil {
		t.Fatalf("finished is at-most-once; repeat must be idempotent: %v", err)
	}
}

func TestFinishedFromUnlockingAndIdle(t *testing.T) {
	s := New()
	if err := s.Finished(); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("finished from Idle: %v", err)
	}
	s2 := New()
	if err := s2.LockRequested(); err != nil {
		t.Fatal(err)
	}
	if err := s2.Locked(); err != nil {
		t.Fatal(err)
	}
	if err := s2.Unlock(); err != nil {
		t.Fatal(err)
	}
	if err := s2.Finished(); err != nil {
		t.Fatalf("finished from Unlocking: %v", err)
	}
	if s2.Phase() != Terminated {
		t.Fatalf("phase=%v want Terminated", s2.Phase())
	}
}

func TestAckNeedsConfigure(t *testing.T) {
	s := New()
	if err := s.AddOutput(1, 1920, 1080); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Ack(1); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("Ack before configure: got %v want ErrNotConfigured", err)
	}
	if err := s.Configure(1, 4, 1920, 1080); err != nil {
		t.Fatal(err)
	}
	if got, err := s.Ack(1); err != nil || got != 4 {
		t.Fatalf("Ack after configure: got (%v,%v)", got, err)
	}
}

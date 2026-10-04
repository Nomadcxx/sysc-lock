package lockd

import (
	"errors"
	"testing"
)

func TestSealedDoesNotWaitForUI(t *testing.T) {
	s := New()
	_ = s.AddOutput(1, 0, 0)
	_ = s.LockRequested()
	_ = s.Locked()
	c := &Client{state: s, outputs: map[OutputID]*lockOut{1: {}}}
	if !c.HandshakeReady() {
		t.Fatal("sealed must not depend on UI")
	}
	if s.Snapshot().UIReady {
		t.Fatal("unconfigured UI is not ready")
	}
}

func TestLockSnapshotUsesOwnerEvents(t *testing.T) {
	s := New()
	c := &Client{state: s}
	var got []Snapshot
	c.OnEvent = func(v Snapshot) { got = append(got, v) }
	_ = s.LockRequested()
	c.publish()
	_ = s.Locked()
	c.publish()
	if len(got) != 2 || got[1].Phase != Locked || got[1].Sequence <= got[0].Sequence {
		t.Fatal(got)
	}
	_ = s.Finished()
	if got[1].Phase != Locked {
		t.Fatal("published snapshot changed")
	}
}

func TestUnlockFailureCannotComplete(t *testing.T) {
	for _, failRequest := range []bool{false, true} {
		s := New()
		_ = s.LockRequested()
		_ = s.Locked()
		failure := errors.New("transport failed")
		calls := 0
		err := s.CompleteUnlock(func() error {
			if failRequest {
				return failure
			}
			return nil
		}, func() error { calls++; return failure })
		if !errors.Is(err, failure) || s.Phase() == Done {
			t.Fatal("failed unlock reported done", err)
		}
		if failRequest && calls != 0 {
			t.Fatal("synced failed request")
		}
	}
}

func TestRemoveOutputDropsState(t *testing.T) {
	s := newReadyState(t)
	s.RemoveOutput(1)
	if err := s.Configure(1, 9, 1, 1); !errors.Is(err, ErrUnknownOutput) {
		t.Fatal(err)
	}
	if s.Snapshot().UIReady {
		t.Fatal("no output UI")
	}
}

func TestLateRemovedOutputCallback(t *testing.T) {
	c := &Client{state: New(), outputs: make(map[OutputID]*lockOut)}
	old := &lockOut{id: 1}
	c.outputs[1] = old
	_ = c.state.AddOutput(1, 0, 0)
	c.removeOutput(1)
	replacement := &lockOut{id: 1}
	c.outputs[1] = replacement
	c.freeBuffer(old, &shmBuffer{})
	if c.outputs[1] != replacement {
		t.Fatal("late release touched replacement")
	}
}

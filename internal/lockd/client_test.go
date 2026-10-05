package lockd

import (
	"errors"
	"github.com/Nomadcxx/sysc-lock/internal/lockd/viewporter"
	"os"
	"strings"
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
		}, func() error { calls++; return failure }, nil)
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

func TestConfigureHandlerPrecedesOptionalScaling(t *testing.T) {
	data, err := os.ReadFile("client.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	start := strings.Index(s, "func (c *Client) createLockSurface")
	s = s[start : strings.Index(s[start:], "func (c *Client) configure")+start]
	if strings.Index(s, "SetConfigureHandler") > strings.Index(s, "GetViewport") {
		t.Fatal("optional feature failure leaves no configure handler")
	}
}
func TestIntegerScaleUntilFractionalPreference(t *testing.T) {
	out := &lockOut{scale: 2, logicalW: 10, logicalH: 20, viewport: &viewporter.WpViewport{}}
	w, h, scale, err := out.geometry()
	if err != nil || w != 20 || h != 40 || scale != 2 {
		t.Fatal(w, h, scale, err)
	}
	out.scale120 = 150
	w, h, scale, err = out.geometry()
	if err != nil || w != 13 || h != 25 || scale != 1.25 {
		t.Fatal(w, h, scale, err)
	}
}
func TestOwnerSuppressesUnchangedPresentationEvents(t *testing.T) {
	s := newReadyState(t)
	c := &Client{state: s}
	count := 0
	c.OnEvent = func(Snapshot) { count++ }
	c.publish()
	for range 20 {
		_ = s.Commit(1, 1920, 1080)
		c.publish()
	}
	if count != 1 {
		t.Fatalf("sent %d unchanged presentation events", count)
	}
}

func TestUnlockTransportPublishesBegin(t *testing.T) {
	data, err := os.ReadFile("client.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "CompleteUnlock(c.lock.UnlockAndDestroy, c.display.Roundtrip, c.publish)") {
		t.Fatal("unlock transport has no owner notification at BeginUnlock")
	}
}

func TestBeginUnlockEventPrecedesRequestAndSync(t *testing.T) {
	s := New()
	_ = s.LockRequested()
	_ = s.Locked()
	calls := []string{}
	err := s.CompleteUnlock(func() error {
		if len(calls) != 1 || calls[0] != "unlocking" || s.Phase() != Unlocking {
			t.Fatal("request before owner event", calls)
		}
		calls = append(calls, "request")
		return nil
	}, func() error { calls = append(calls, "sync"); return nil }, func() {
		if s.Phase() != Unlocking {
			t.Fatal("wrong begin phase")
		}
		calls = append(calls, "unlocking")
	})
	if err != nil || s.Phase() != Done || strings.Join(calls, ",") != "unlocking,request,sync" {
		t.Fatal(err, s.Phase(), calls)
	}
}

func TestSleepUnlockGuardRunsBeforeTransport(t *testing.T) {
	data, err := os.ReadFile("client.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	start := strings.Index(s, "func (c *Client) UnlockAndQuit")
	s = s[start : strings.Index(s[start:], "func (c *Client) publish")+start]
	guard := strings.Index(s, "c.BeforeUnlock()")
	if guard < 0 || guard > strings.Index(s, "SetReadDeadline") {
		t.Fatal("sleep admission guard missing before transport")
	}
}

func TestSleepUnlockRefusalRetainsSealedPhase(t *testing.T) {
	s := New()
	_ = s.LockRequested()
	_ = s.Locked()
	c := &Client{state: s, BeforeUnlock: func() error { return ErrUnlockDeferred }}
	if err := c.UnlockAndQuit(); !errors.Is(err, ErrUnlockDeferred) || s.Phase() != Locked || c.fatal != nil {
		t.Fatal(err, s.Phase(), c.fatal)
	}
	// Nil display proves this refusal did not touch transport.
}

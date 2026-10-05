package session

import (
	"errors"
	"fmt"
	"github.com/Nomadcxx/sysc-lock/internal/lockd"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func newSession(t *testing.T) *Session {
	t.Helper()
	s, err := New(Identity{UID: uint32(os.Getuid()), Session: "test-session", Compositor: "test-niri"}, filepath.Join(t.TempDir(), "private", "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func seal(t *testing.T, s *Session) uint64 {
	t.Helper()
	v, err := s.Lock()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Report(v.Generation, lockd.Snapshot{Phase: lockd.Locked}); err != nil {
		t.Fatal(err)
	}
	return v.Generation
}
func TestSessionSingleFlight(t *testing.T) {
	s := newSession(t)
	a, _ := s.Lock()
	b, _ := s.Lock()
	if a.Generation != b.Generation || len(s.Requests) != 1 {
		t.Fatal("duplicate acquisition")
	}
}
func TestSessionUnknownDoesNotUnlock(t *testing.T) {
	s := newSession(t)
	g := seal(t, s)
	s.End(g, lockd.Locked, errors.New("connection lost"))
	v := s.Snapshot()
	if v.Phase != "sealed/unknown" || v.ConfirmedUnlock != 0 {
		t.Fatal(v)
	}
}
func TestMarkerRecoverySameSessionOnly(t *testing.T) {
	s := newSession(t)
	v, _ := s.Lock()
	recovered, err := New(s.identity, s.path)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.Snapshot().Phase != "recovering" || recovered.Snapshot().Generation != v.Generation {
		t.Fatal(recovered.Snapshot())
	}
	other := s.identity
	other.Compositor = "other-niri"
	if _, err := New(other, s.path); err == nil {
		t.Fatal("accepted stale compositor marker")
	}
	info, _ := os.Stat(s.path)
	if info.Mode().Perm() != 0600 {
		t.Fatal(info.Mode())
	}
	info, _ = os.Stat(filepath.Dir(s.path))
	if info.Mode().Perm() != 0700 {
		t.Fatal(info.Mode())
	}
}
func TestRefusalDuringRecoveryRetainsMarker(t *testing.T) {
	s := newSession(t)
	v, _ := s.Lock()
	r, err := New(s.identity, s.path)
	if err != nil {
		t.Fatal(err)
	}
	r.End(v.Generation, lockd.Refused, nil)
	if r.Snapshot().Phase != "sealed/unknown" {
		t.Fatal(r.Snapshot())
	}
	again, err := New(s.identity, s.path)
	if err != nil || again.marker.Intent == 0 {
		t.Fatal(err)
	}
}
func TestCleanUnlockClearsAcquisitionIntent(t *testing.T) {
	s := newSession(t)
	g := seal(t, s)
	if err := s.Report(g, lockd.Snapshot{Phase: lockd.Done}); err != nil {
		t.Fatal(err)
	}
	r, err := New(s.identity, s.path)
	if err != nil {
		t.Fatal(err)
	}
	if r.marker.Intent != 0 || r.Snapshot().ConfirmedUnlock != g || r.Snapshot().Phase != "idle" {
		t.Fatal(r.Snapshot())
	}
}
func TestInitialRefusalClearsIntent(t *testing.T) {
	s := newSession(t)
	v, _ := s.Lock()
	s.End(v.Generation, lockd.Refused, nil)
	r, err := New(s.identity, s.path)
	if err != nil {
		t.Fatal(err)
	}
	if r.marker.Intent != 0 || r.Snapshot().ConfirmedUnlock != 0 {
		t.Fatal(r.Snapshot())
	}
}
func TestSleepWaitsForSealedNotUI(t *testing.T) {
	s := newSession(t)
	released := 0
	s.Arm(func() (func(), error) { return func() { released++ }, nil })
	if !s.Snapshot().SleepProtected {
		t.Fatal("delay not held before ready")
	}
	if err := s.PrepareSleep(time.Now(), time.Second); err != nil {
		t.Fatal(err)
	}
	if err := s.PrepareSleep(time.Now(), time.Second); err != nil {
		t.Fatal(err)
	}
	if len(s.Requests) != 1 || released != 0 {
		t.Fatal("released before sealed")
	}
	g := s.Snapshot().Generation
	if err := s.Report(g, lockd.Snapshot{Phase: lockd.Locked, UIReady: false}); err != nil {
		t.Fatal(err)
	}
	if released != 1 {
		t.Fatal("UI delayed sleep release")
	}
}
func TestResumeRearmsDelay(t *testing.T) {
	s := newSession(t)
	calls := 0
	take := func() (func(), error) { calls++; return func() {}, nil }
	s.Arm(take)
	_ = s.PrepareSleep(time.Now(), time.Second)
	_ = s.Report(s.Snapshot().Generation, lockd.Snapshot{Phase: lockd.Locked})
	s.Resume(take)
	if calls != 2 || !s.Snapshot().SleepProtected || s.Snapshot().Phase != "sealed" {
		t.Fatal(s.Snapshot(), calls)
	}
}
func TestDelayFailureReportsUnprotected(t *testing.T) {
	s := newSession(t)
	s.Arm(func() (func(), error) { return nil, errors.New("no logind") })
	if s.Snapshot().SleepProtected {
		t.Fatal("claimed protection")
	}
	if _, err := s.Lock(); err != nil {
		t.Fatal("manual lock depends on logind", err)
	}
}
func TestSleepDeadlineDoesNotInventProtection(t *testing.T) {
	s := newSession(t)
	released := 0
	s.Arm(func() (func(), error) { return func() { released++ }, nil })
	now := time.Now()
	_ = s.PrepareSleep(now, time.Second)
	s.CheckSleepDeadline(now.Add(time.Second))
	if released != 1 || s.Snapshot().SleepProtected || s.Snapshot().SleepError == "" {
		t.Fatal(s.Snapshot())
	}
}

func TestResumeRetriesRefusedSleepAcquisition(t *testing.T) {
	s := newSession(t)
	take := func() (func(), error) { return func() {}, nil }
	s.Arm(take)
	if err := s.PrepareSleep(time.Now(), time.Second); err != nil {
		t.Fatal(err)
	}
	generation := <-s.Requests
	if err := s.End(generation, lockd.Refused, nil); err != nil {
		t.Fatal(err)
	}
	s.Resume(take)
	if s.Snapshot().Phase != "requesting" || s.Snapshot().Generation != generation+1 || len(s.Requests) != 1 {
		t.Fatalf("resume did not retry permitted acquisition: %+v", s.Snapshot())
	}
}

func TestUnlockReceiptSurvivesServiceRestart(t *testing.T) {
	s := newSession(t)
	g := seal(t, s)
	<-s.Requests
	if err := s.Report(g, lockd.Snapshot{Phase: lockd.Done}); err != nil {
		t.Fatal(err)
	}
	if err := s.End(g, lockd.Done, nil); err != nil {
		t.Fatal(err)
	}
	r, err := New(s.identity, s.path)
	if err != nil {
		t.Fatal(err)
	}
	if r.Snapshot().ConfirmedUnlock != g || r.Snapshot().Phase != "idle" || len(r.Requests) != 0 {
		t.Fatal(r.Snapshot())
	}
	v, err := r.Lock()
	if err != nil || v.Generation != g+1 || v.ConfirmedUnlock != g {
		t.Fatal(v, err)
	}
}

func TestRestartAfterConfirmedUnlockMayRelockConservatively(t *testing.T) {
	s := newSession(t)
	g := seal(t, s)
	if err := s.Report(g, lockd.Snapshot{Phase: lockd.Done}); err != nil {
		t.Fatal(err)
	}
	// Simulate a restart with the pre-unlock disk image (e.g. failed durability).
	if err := writeMarker(s.path, marker{Identity: s.identity, Generation: g, Intent: g}); err != nil {
		t.Fatal(err)
	}
	r, err := New(s.identity, s.path)
	if err != nil {
		t.Fatal(err)
	}
	if r.Snapshot().Phase != "recovering" || r.Snapshot().ConfirmedUnlock != 0 || <-r.Requests != g {
		t.Fatal(r.Snapshot())
	}
}

func TestUnlockRearmsDelay(t *testing.T) {
	s := newSession(t)
	calls := 0
	take := func() (func(), error) { calls++; return func() {}, nil }
	s.Arm(take)
	if err := s.PrepareSleep(time.Now(), time.Second); err != nil {
		t.Fatal(err)
	}
	g := seal(t, s)
	<-s.Requests
	if err := s.Report(g, lockd.Snapshot{Phase: lockd.Done}); err != nil {
		t.Fatal(err)
	}
	if err := s.End(g, lockd.Done, nil); err != nil {
		t.Fatal(err)
	}
	s.Arm(take)
	if calls != 2 || !s.Snapshot().SleepProtected || s.Snapshot().ConfirmedUnlock != g {
		t.Fatal(s.Snapshot(), calls)
	}
}

func TestSleepDuringUnlockRecapturesBeforeDelayRelease(t *testing.T) {
	s := newSession(t)
	releases := 0
	s.Arm(func() (func(), error) { return func() { releases++ }, nil })
	g := seal(t, s)
	<-s.Requests // The old core is running.
	if err := s.BeginUnlock(g); err != nil {
		t.Fatal(err)
	}
	if err := s.PrepareSleep(time.Now(), time.Second); err != nil {
		t.Fatal(err)
	}
	if releases != 0 {
		t.Fatal("released delay during unlock")
	}
	if err := s.Report(g, lockd.Snapshot{Phase: lockd.Done}); err != nil {
		t.Fatal(err)
	}
	next := s.Snapshot()
	if next.Phase != "requesting" || next.Generation != g+1 || next.ConfirmedUnlock != g || !next.SleepProtected || releases != 0 {
		t.Fatalf("published unprotected idle while sleep pending: %+v", next)
	}
	if len(s.Requests) != 0 {
		t.Fatal("new core queued before old cleanup")
	}
	recovered, err := New(s.identity, s.path)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.Snapshot().Phase != "recovering" || recovered.marker.Intent != g+1 || recovered.marker.ConfirmedUnlock != g {
		t.Fatal("recapture intent not durable", recovered.Snapshot())
	}
	if err := s.End(g, lockd.Done, nil); err != nil {
		t.Fatal(err)
	}
	if len(s.Requests) != 1 || <-s.Requests != g+1 {
		t.Fatal("no recapture after cleanup")
	}
	if err := s.Report(g+1, lockd.Snapshot{Phase: lockd.Locked}); err != nil {
		t.Fatal(err)
	}
	if releases != 1 || s.Snapshot().Phase != "sealed" {
		t.Fatal("delay not released exactly once after recapture", releases, s.Snapshot())
	}
}

func TestSleepUnlockCleanupFailureRetainsNextIntent(t *testing.T) {
	s := newSession(t)
	g := seal(t, s)
	<-s.Requests
	if err := s.BeginUnlock(g); err != nil {
		t.Fatal(err)
	}
	if err := s.PrepareSleep(time.Now(), time.Second); err != nil {
		t.Fatal(err)
	}
	if err := s.Report(g, lockd.Snapshot{Phase: lockd.Done}); err != nil {
		t.Fatal(err)
	}
	failure := errors.New("worker cleanup timeout")
	if err := s.End(g, lockd.Done, failure); !errors.Is(err, failure) {
		t.Fatal("cleanup failure hidden", err)
	}
	if len(s.Requests) != 0 || s.Snapshot().Phase != "sealed/unknown" || s.marker.Intent != g+1 {
		t.Fatal("cleanup failure lost recapture intent", s.Snapshot())
	}
}

func TestUnlockAdmissionIsAtomicWithSleep(t *testing.T) {
	t.Run("sleep first", func(t *testing.T) {
		s := newSession(t)
		g := seal(t, s)
		<-s.Requests
		if err := s.PrepareSleep(time.Now(), time.Second); err != nil {
			t.Fatal(err)
		}
		if err := s.BeginUnlock(g); !errors.Is(err, lockd.ErrUnlockDeferred) {
			t.Fatal("admitted unlock during pending sleep", err)
		}
		if s.Snapshot().Phase != "sealed" {
			t.Fatal(s.Snapshot())
		}
		if err := s.Resume(func() (func(), error) { return func() {}, nil }); err != nil {
			t.Fatal(err)
		}
		if err := s.BeginUnlock(g); err != nil {
			t.Fatal("unlock not admitted after resume", err)
		}
	})
	t.Run("unlock first", func(t *testing.T) {
		s := newSession(t)
		g := seal(t, s)
		<-s.Requests
		releases := 0
		s.Arm(func() (func(), error) { return func() { releases++ }, nil })
		if err := s.BeginUnlock(g); err != nil {
			t.Fatal(err)
		}
		if s.Snapshot().Phase != "unlocking" {
			t.Fatal("unlock phase not published before protocol admission")
		}
		if err := s.PrepareSleep(time.Now(), time.Second); err != nil {
			t.Fatal(err)
		}
		if releases != 0 {
			t.Fatal("released inhibitor after unlock admission")
		}
	})
}

func TestRepeatedSleepCoalescesLock(t *testing.T) {
	s := newSession(t)
	s.Arm(func() (func(), error) { return func() {}, nil })
	now := time.Now()
	if err := s.PrepareSleep(now, time.Second); err != nil {
		t.Fatal(err)
	}
	first := s.Snapshot()
	if err := s.PrepareSleep(now.Add(500*time.Millisecond), time.Second); err != nil {
		t.Fatal(err)
	}
	if s.Snapshot().Generation != first.Generation || len(s.Requests) != 1 || s.deadline != now.Add(900*time.Millisecond) {
		t.Fatal("duplicate sleep changed acquisition or extended deadline")
	}
}

func TestSleepAfterUnlockBeforeCleanupStagesRecapture(t *testing.T) {
	for _, cleanupFails := range []bool{false, true} {
		t.Run(fmt.Sprintf("cleanup-fails-%t", cleanupFails), func(t *testing.T) {
			s := newSession(t)
			releases := 0
			s.Arm(func() (func(), error) { return func() { releases++ }, nil })
			g := seal(t, s)
			<-s.Requests
			if err := s.BeginUnlock(g); err != nil {
				t.Fatal(err)
			}
			if err := s.Report(g, lockd.Snapshot{Phase: lockd.Done}); err != nil {
				t.Fatal(err)
			}
			if err := s.PrepareSleep(time.Now(), time.Second); err != nil {
				t.Fatal("sleep during completed-unlock cleanup could not stage recapture", err)
			}
			v := s.Snapshot()
			if v.Phase != "requesting" || v.Generation != g+1 || v.ConfirmedUnlock != g || !v.SleepProtected || releases != 0 || len(s.Requests) != 0 {
				t.Fatal("sleep admitted without staged protection", v, releases)
			}
			recovered, err := New(s.identity, s.path)
			if err != nil {
				t.Fatal(err)
			}
			if recovered.Snapshot().Phase != "recovering" || recovered.marker.Intent != g+1 || recovered.marker.ConfirmedUnlock != g {
				t.Fatal("recapture intent not durable before sleep admission", recovered.Snapshot())
			}
			var failure error
			if cleanupFails {
				failure = errors.New("cleanup failed")
			}
			if err := s.End(g, lockd.Done, failure); !errors.Is(err, failure) {
				t.Fatal(err)
			}
			if cleanupFails {
				if s.Snapshot().Phase != "sealed/unknown" || s.marker.Intent != g+1 || len(s.Requests) != 0 || releases != 0 {
					t.Fatal("failed cleanup lost staged intent/protection", s.Snapshot())
				}
				return
			}
			if len(s.Requests) != 1 || <-s.Requests != g+1 {
				t.Fatal("cleanup did not hand off recapture")
			}
			if err := s.Report(g+1, lockd.Snapshot{Phase: lockd.Locked}); err != nil {
				t.Fatal(err)
			}
			if releases != 1 || s.Snapshot().Phase != "sealed" {
				t.Fatal("delay not held until next seal", releases, s.Snapshot())
			}
		})
	}
}

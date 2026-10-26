package main

import (
	"os"
	"path/filepath"
	"testing"
	"testing/synctest"
	"time"

	"github.com/Nomadcxx/sysc-lock/internal/lockd"
	"github.com/Nomadcxx/sysc-lock/internal/session"
)

func TestCLISnapshotRequiresRegisteredIdentity(t *testing.T) {
	id := session.Identity{Session: "registered", Compositor: "niri-instance"}
	good := session.Snapshot{Generation: 1, Session: id.Session, Compositor: id.Compositor, Phase: "sealed"}
	if err := validateRequestSnapshot(good, id); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*session.Snapshot){
		func(v *session.Snapshot) { v.Session = "foreign" },
		func(v *session.Snapshot) { v.Compositor = "old-niri" },
		func(v *session.Snapshot) { v.Generation = 0 },
		func(v *session.Snapshot) { v.ConfirmedUnlock = 2 },
		func(v *session.Snapshot) { v.Phase = "invented" },
	} {
		invalid := good
		mutate(&invalid)
		if err := validateRequestSnapshot(invalid, id); err == nil {
			t.Fatalf("accepted %+v", invalid)
		}
	}
	if err := validateRequestSnapshot(good, session.Identity{}); err == nil {
		t.Fatal("accepted missing startup identity")
	}
}

func TestCLIValidatesIdleOwnerBeforeLock(t *testing.T) {
	id := session.Identity{Session: "registered", Compositor: "niri-instance"}
	value := session.Snapshot{Session: id.Session, Compositor: id.Compositor, Phase: "idle"}
	if err := validateRequestSnapshot(value, id); err != nil {
		t.Fatal("new idle owner cannot be checked", err)
	}
	value.Session = "other"
	if err := validateRequestSnapshot(value, id); err == nil {
		t.Fatal("accepted foreign idle owner")
	}
}

func TestCLIRejectsSupersededOwnerAndRegressingReceipt(t *testing.T) {
	if err := validateRequestOwner(":1.1", ":1.1"); err != nil {
		t.Fatal(err)
	}
	for _, owner := range []string{"", ":1.2"} {
		if err := validateRequestOwner(":1.1", owner); err == nil {
			t.Fatal("accepted superseded owner")
		}
	}
	if err := validateRequestOwner("", ""); err == nil {
		t.Fatal("accepted missing owner")
	}
	previous := session.Snapshot{Sequence: 3, Generation: 4, ConfirmedUnlock: 2}
	for _, next := range []session.Snapshot{
		{Sequence: 2, Generation: 4, ConfirmedUnlock: 2},
		{Sequence: 4, Generation: 3, ConfirmedUnlock: 2},
		{Sequence: 4, Generation: 4, ConfirmedUnlock: 1},
	} {
		if err := validateRequestProgress(previous, next); err == nil {
			t.Fatalf("accepted regressing snapshot %+v", next)
		}
	}
	if err := validateRequestProgress(previous, session.Snapshot{Sequence: 4, Generation: 5, ConfirmedUnlock: 4}); err != nil {
		t.Fatal(err)
	}
}

func TestCLIDecodeDoesNotReuseMissingFields(t *testing.T) {
	id := session.Identity{Session: "registered", Compositor: "niri-instance"}
	v := session.Snapshot{Generation: 1, Session: id.Session, Compositor: id.Compositor, Phase: "sealed"}
	if err := decodeRequestSnapshot(`{"Phase":"sealed"}`, id, &v); err == nil {
		t.Fatal("accepted identity left over from an earlier snapshot")
	}
}

func TestSleepDeadlineTimerIsOneShot(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		owner := newTimerTestSession(t)
		releases := 0
		take := func() (func(), error) { return func() { releases++ }, nil }
		owner.Arm(take)
		timer := time.NewTimer(time.Hour)
		timer.Stop()
		defer timer.Stop()

		resetSleepDeadlineTimer(timer, owner)
		time.Sleep(10 * time.Second)
		assertSleepTimerIdle(t, timer)

		if err := owner.PrepareSleep(time.Now(), time.Second); err != nil {
			t.Fatal(err)
		}
		resetSleepDeadlineTimer(timer, owner)
		time.Sleep(500 * time.Millisecond)
		latestPrepare := time.Now()
		if err := owner.PrepareSleep(latestPrepare, time.Second); err != nil {
			t.Fatal(err)
		}
		resetSleepDeadlineTimer(timer, owner)
		deadline, ok := owner.SleepDeadline()
		if want := latestPrepare.Add(900 * time.Millisecond); !ok || !deadline.Equal(want) {
			t.Fatalf("SleepDeadline() = (%v, %t), want (%v, true)", deadline, ok, want)
		}

		time.Sleep(400 * time.Millisecond)
		assertSleepTimerIdle(t, timer)
		time.Sleep(500 * time.Millisecond)
		var fired time.Time
		select {
		case fired = <-timer.C:
		default:
			t.Fatal("deadline timer did not fire at the latest deadline")
		}
		owner.CheckSleepDeadline(fired)
		resetSleepDeadlineTimer(timer, owner)
		if releases != 1 || owner.Snapshot().SleepProtected || owner.Snapshot().SleepError == "" {
			t.Fatalf("deadline handling released %d times or left protection active: %+v", releases, owner.Snapshot())
		}
		time.Sleep(10 * time.Second)
		assertSleepTimerIdle(t, timer)
	})
}

func TestSleepDeadlineTimerStopsAfterEarlyRelease(t *testing.T) {
	for _, stopAt := range []string{"resume", "sealed"} {
		t.Run(stopAt, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				owner := newTimerTestSession(t)
				take := func() (func(), error) { return func() {}, nil }
				owner.Arm(take)
				timer := time.NewTimer(time.Hour)
				timer.Stop()
				defer timer.Stop()
				if err := owner.PrepareSleep(time.Now(), time.Second); err != nil {
					t.Fatal(err)
				}
				resetSleepDeadlineTimer(timer, owner)
				if stopAt == "resume" {
					if err := owner.Resume(take); err != nil {
						t.Fatal(err)
					}
				} else if err := owner.Report(owner.Snapshot().Generation, lockd.Snapshot{Phase: lockd.Locked}); err != nil {
					t.Fatal(err)
				}
				resetSleepDeadlineTimer(timer, owner)
				time.Sleep(10 * time.Second)
				assertSleepTimerIdle(t, timer)
			})
		})
	}
}

func newTimerTestSession(t *testing.T) *session.Session {
	t.Helper()
	owner, err := session.New(session.Identity{
		UID:        uint32(os.Getuid()),
		Session:    "timer-test-session",
		Compositor: "timer-test-compositor",
	}, filepath.Join(t.TempDir(), "private", "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	return owner
}

func assertSleepTimerIdle(t *testing.T, timer *time.Timer) {
	t.Helper()
	select {
	case fired := <-timer.C:
		t.Fatalf("sleep deadline timer fired while inactive at %v", fired)
	default:
	}
}

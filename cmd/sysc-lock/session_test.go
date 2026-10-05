package main

import (
	"github.com/Nomadcxx/sysc-lock/internal/session"
	"testing"
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

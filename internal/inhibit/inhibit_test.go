package inhibit

import (
	"errors"
	"testing"
)

// fakeBackend exercises the Inhibitor contract without dbus.
type fakeBackend struct {
	taken, released int
	err             error
}

func (f *fakeBackend) Inhibit(what, who, why, mode string) error {
	if f.err != nil {
		return f.err
	}
	f.taken++
	return nil
}
func (f *fakeBackend) Release() { f.released++ }

func TestTakeReleaseIdempotent(t *testing.T) {
	b := &fakeBackend{}
	rel, err := Take(b)
	if err != nil {
		t.Fatal(err)
	}
	if b.taken != 1 {
		t.Fatal("not taken")
	}
	rel()
	rel()
	if b.released != 1 {
		t.Fatalf("release not idempotent: %d", b.released)
	}
}

func TestTakeError(t *testing.T) {
	b := &fakeBackend{err: errors.New("no bus")}
	if _, err := Take(b); err == nil {
		t.Fatal("expected error passthrough")
	}
}

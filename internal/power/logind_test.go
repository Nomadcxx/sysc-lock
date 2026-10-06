package power

import (
	"errors"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

// fakeCaller records calls and returns canned answers, so no test touches the
// bus.
type fakeCaller struct {
	calls   []string
	args    [][]any
	props   map[string]string
	propErr map[string]error
	err     error
	block   chan struct{}
}

func (f *fakeCaller) Property(n string) (string, error) {
	f.calls = append(f.calls, n)
	if f.propErr != nil {
		if err, ok := f.propErr[n]; ok {
			return "", err
		}
	}
	return f.props[n], nil
}

func (f *fakeCaller) Method(n string, a ...any) error {
	f.calls = append(f.calls, n)
	f.args = append(f.args, a)
	if f.block != nil {
		<-f.block
	}
	return f.err
}

func TestCheckHidesAnythingButYes(t *testing.T) {
	f := &fakeCaller{props: map[string]string{"CanReboot": "yes", "CanPowerOff": "challenge"}}
	if got := Check(f); !got.Reboot || got.Shutdown {
		t.Fatalf("challenge must hide the item: %+v", got)
	}
	for _, v := range []string{"no", "na", "", "YES"} {
		f = &fakeCaller{props: map[string]string{"CanReboot": v, "CanPowerOff": "yes"}}
		if Check(f).Reboot {
			t.Fatalf("%q must hide reboot", v)
		}
	}
	f = &fakeCaller{props: map[string]string{"CanReboot": "yes", "CanPowerOff": "yes"}, propErr: map[string]error{"CanReboot": errors.New("no such interface")}}
	if Check(f).Reboot {
		t.Fatal("a query error hides the item")
	}
	if equal(contains(f.calls, "Logout"), []string{"Logout"}) {
		t.Fatal("log out is never queried")
	}
}

func contains(calls []string, want string) []string {
	for _, c := range calls {
		if c == want {
			return []string{c}
		}
	}
	return nil
}

func TestRunUsesTheRightMethodAndArguments(t *testing.T) {
	for a, want := range map[Action]struct {
		method string
		args   []any
	}{
		Reboot:    {"Reboot", []any{false}},
		Shutdown:  {"PowerOff", []any{false}},
		Logout:    {"TerminateSession", []any{"c2"}},
		Suspend:   {"Suspend", []any{false}},
		Hibernate: {"Hibernate", []any{false}},
	} {
		f := &fakeCaller{}
		if got := (Executor{Caller: f, Session: "c2"}).Run(a); got != OK {
			t.Fatalf("%s: nil error is OK, got %v", a, got)
		}
		if len(f.calls) != 1 || f.calls[0] != want.method {
			t.Fatalf("%s called %v", a, f.calls)
		}
		if len(f.args[0]) != len(want.args) || f.args[0][0] != want.args[0] {
			t.Fatalf("%s args %v", a, f.args[0])
		}
	}
}

func TestRunRefusesLogOutWithoutASession(t *testing.T) {
	f := &fakeCaller{}
	if got := (Executor{Caller: f}).Run(Logout); got != Failed {
		t.Fatalf("no session id means no log out, got %v", got)
	}
	if len(f.calls) != 0 {
		t.Fatal("logind must not be called without a session id", f.calls)
	}
}

func TestRunMapsDeniedToRefused(t *testing.T) {
	f := &fakeCaller{err: Denied{Name: "org.freedesktop.DBus.Error.AccessDenied"}}
	if got := (Executor{Caller: f}).Run(Reboot); got != Refused {
		t.Fatalf("a denial is Refused, got %v", got)
	}
}

func TestRunMapsEveryOtherErrorToFailed(t *testing.T) {
	for name, err := range map[string]error{
		"bus error":   errors.New("org.freedesktop.login1.Error.Internal"),
		"plain error": errors.New("boom"),
	} {
		if got := (Executor{Caller: &fakeCaller{err: err}}).Run(Reboot); got != Failed {
			t.Fatalf("%s must be Failed, got %v", name, got)
		}
	}
}

func TestRunTimesOut(t *testing.T) {
	block := make(chan struct{})
	defer close(block)
	f := &fakeCaller{block: block}
	start := time.Now()
	if got := (Executor{Caller: f, Timeout: 20 * time.Millisecond}).Run(Shutdown); got != Failed {
		t.Fatalf("a wedged bus is Failed, got %v", got)
	}
	if d := time.Since(start); d > time.Second {
		t.Fatalf("the timeout must bound the wait, took %v", d)
	}
}

func TestClassifyReadsTheBusNameNotTheDescription(t *testing.T) {
	denied := dbus.Error{Name: "org.freedesktop.DBus.Error.AccessDenied", Body: []any{"ignored description"}}
	var d Denied
	if err := classify("Reboot", denied); !errors.As(err, &d) {
		t.Fatalf("AccessDenied must be Denied from the Name field, got %v", err)
	}
	interactive := dbus.Error{Name: "org.freedesktop.login1.Error.InteractiveAuthenticationRequired", Body: []any{"auth"}}
	if err := classify("Reboot", interactive); !errors.As(err, &d) {
		t.Fatalf("interactive auth must be Denied, got %v", err)
	}
	other := dbus.Error{Name: "org.freedesktop.login1.Error.Internal", Body: []any{"boom"}}
	if err := classify("Reboot", other); errors.As(err, &d) {
		t.Fatal("an internal error is not Denied")
	}
}

func TestRunRejectsCancel(t *testing.T) {
	f := &fakeCaller{}
	if got := (Executor{Caller: f}).Run(Cancel); got != Failed {
		t.Fatal("Cancel is not an action")
	}
	if len(f.calls) != 0 {
		t.Fatal("Cancel must never reach logind", f.calls)
	}
}

func TestDefaultTimeoutIsUsedWhenUnset(t *testing.T) {
	f := &fakeCaller{}
	if got := (Executor{Caller: f}).Run("nonsense"); got != Failed {
		t.Fatal("an unknown action fails")
	}
	if Timeout != 5*time.Second {
		t.Fatalf("Timeout is %v", Timeout)
	}
}

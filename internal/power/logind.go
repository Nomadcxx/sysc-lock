package power

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/godbus/dbus/v5"

	"github.com/Nomadcxx/sysc-lock/internal/inhibit"
)

const (
	managerBus   = "org.freedesktop.login1"
	managerPath  = dbus.ObjectPath("/org/freedesktop/login1")
	managerIface = "org.freedesktop.login1.Manager"
)

// Outcome is what one action reports to the screen.
type Outcome int

const (
	// Failed is the zero value: a missed outcome must not look like success.
	Failed Outcome = iota
	// Refused means logind said no. Nothing is retried.
	Refused
	// OK means the bus call returned no error. Keys stay ignored; the
	// machine should go down.
	OK
)

// Caller is the slice of the logind connection the model needs. A fake
// implements it in tests, so no test touches the bus.
type Caller interface {
	// Property reads one login1 manager property, such as CanReboot.
	Property(name string) (string, error)
	// Method performs one login1 manager call.
	Method(name string, args ...any) error
}

// Denied marks a call logind refused for want of authority. The dbus adapter
// returns it so this package needs no bus types; the screen shows the outcome,
// never the name.
type Denied struct{ Name string }

func (d Denied) Error() string { return "Not permitted" }

// Executor performs power actions. The zero Timeout uses Timeout.
type Executor struct {
	Caller  Caller
	Session string
	Timeout time.Duration
}

// Run performs a and reports the outcome. The call runs on its own goroutine so
// a wedged bus cannot freeze the wayland owner, and the timeout is enforced
// here so a fake can be timed out in a test.
func (e Executor) Run(a Action) Outcome {
	done := make(chan error, 1)
	go func() { done <- e.call(a) }()
	limit := e.Timeout
	if limit <= 0 {
		limit = Timeout
	}
	select {
	case err := <-done:
		if err == nil {
			return OK
		}
		var denied Denied
		if errors.As(err, &denied) {
			return Refused
		}
		return Failed
	case <-time.After(limit):
		return Failed
	}
}

func (e Executor) call(a Action) error {
	switch a {
	case Reboot:
		return e.Caller.Method("Reboot", false)
	case Shutdown:
		return e.Caller.Method("PowerOff", false)
	case Logout:
		if e.Session == "" {
			return fmt.Errorf("no session to terminate")
		}
		return e.Caller.Method("TerminateSession", e.Session)
	}
	return fmt.Errorf("unknown action %q", a)
}

// Check asks logind once what this acquisition may do. Anything other than a
// plain yes hides the item: challenge would fail against interactive=false,
// and a query error hides it too. Ending your own session needs no permission.
func Check(c Caller) Availability {
	return Availability{Reboot: yes(c, "CanReboot"), Shutdown: yes(c, "CanPowerOff")}
}

func yes(c Caller, prop string) bool {
	v, err := c.Property(prop)
	return err == nil && v == "yes"
}

// DBusCaller adapts the logind connection the locker holds.
type DBusCaller struct{ conn *dbus.Conn }

// NewCaller wraps an opened logind connection.
func NewCaller(l *inhibit.Logind) *DBusCaller { return &DBusCaller{conn: l.Connection()} }

// Property reads one manager property with a bounded context, the same
// Properties.Get call inhibit already uses.
func (d *DBusCaller) Property(name string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), Timeout)
	defer cancel()
	var v dbus.Variant
	err := d.conn.Object(managerBus, managerPath).CallWithContext(ctx, "org.freedesktop.DBus.Properties.Get", 0, managerIface, name).Store(&v)
	if err != nil {
		return "", classify(name, err)
	}
	if s, ok := v.Value().(string); ok {
		return s, nil
	}
	return "", nil
}

// Method performs one manager call with a bounded context. interactive is
// never requested, so logind can never put a prompt on a sealed screen.
func (d *DBusCaller) Method(name string, args ...any) error {
	ctx, cancel := context.WithTimeout(context.Background(), Timeout)
	defer cancel()
	call := d.conn.Object(managerBus, managerPath).CallWithContext(ctx, managerIface+"."+name, 0, args...)
	if call.Err != nil {
		return classify(name, call.Err)
	}
	return nil
}

// classify turns a bus refusal into Denied so the model can map it without
// importing the bus package at the call site. dbus.Error.Error returns only
// the description, never the name, so the name has to come off the type.
func classify(method string, err error) error {
	var be dbus.Error
	if errors.As(err, &be) {
		fmt.Fprintf(os.Stderr, "sysc-lock: power: %s: %s: %v\n", method, be.Name, be)
		switch be.Name {
		case "org.freedesktop.DBus.Error.AccessDenied",
			"org.freedesktop.login1.Error.InteractiveAuthenticationRequired":
			return Denied{Name: be.Name}
		}
	}
	return err
}

package auth

import (
	"errors"
	"strings"
	"testing"
)

// testFake lives here only; no production bypass exists (design §5).
type testFake struct {
	password string
	calls    int
}

func (f *testFake) Verify(user string, response PromptFunc) (Result, error) {
	f.calls++
	if user == "" {
		return Result{}, errors.New("fake: empty user")
	}
	pw, err := response("Password: ", false)
	if err != nil {
		return Result{}, err
	}
	if pw == f.password {
		return Result{OK: true}, nil
	}
	return Result{OK: false, Message: "Incorrect password"}, nil
}

func TestFakeContract(t *testing.T) {
	var a Authenticator = &testFake{password: "hunter2"}
	res, err := a.Verify("nomadx", func(prompt string, echo bool) (string, error) {
		if prompt != "Password: " || echo {
			t.Fatalf("unexpected prompt %q echo=%v", prompt, echo)
		}
		return "hunter2", nil
	})
	if err != nil || !res.OK {
		t.Fatalf("got %+v, %v", res, err)
	}
	res, err = a.Verify("nomadx", func(string, bool) (string, error) { return "nope", nil })
	if err != nil || res.OK || !strings.Contains(res.Message, "Incorrect") {
		t.Fatalf("wrong-password got %+v, %v", res, err)
	}
}

func TestZero(t *testing.T) {
	b := []byte("secretpw")
	Zero(b)
	for i, v := range b {
		if v != 0 {
			t.Fatalf("byte %d not zeroed: %d", i, v)
		}
	}
	Zero(nil) // must not panic
}

func TestPAMEmptyUser(t *testing.T) {
	var a Authenticator = &PAM{}
	if _, err := a.Verify("", func(string, bool) (string, error) { return "", nil }); err == nil {
		t.Fatal("empty user must error before touching PAM")
	}
}

func TestPAMMissingService(t *testing.T) {
	a := &PAM{Service: "no-such-service-xyz"}
	_, err := a.Verify("someone", func(string, bool) (string, error) { return "", nil })
	if err == nil || !strings.Contains(err.Error(), "unavailable") {
		t.Fatalf("got %v, want service-unavailable error", err)
	}
	var se error = err
	_ = se
}

func TestPAMImplementsInterface(t *testing.T) {
	var _ Authenticator = (*PAM)(nil)
}

package auth

import (
	"errors"
	"strings"
	"testing"

	"github.com/msteinert/pam/v2"
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

func TestAdditionalPamPromptRejected(t *testing.T) {
	response := PasswordPrompt("secret")
	if got, err := response("Password", false); err != nil || got != "secret" {
		t.Fatal(err)
	}
	if got, err := response("OTP", false); err == nil || got != "" {
		t.Fatal("password reused for OTP")
	}
	if got, err := PasswordPrompt("secret")("Visible", true); err == nil || got != "" {
		t.Fatal("visible prompt accepted")
	}
}

func TestPAMRejectedConversationCannotSucceed(t *testing.T) {
	for _, stage := range []string{"authenticate", "account"} {
		t.Run(stage, func(t *testing.T) {
			var conversationErr error
			accounts := 0
			auth := func(pam.Flags) error {
				if stage == "authenticate" {
					conversationErr = errors.New("rejected prompt")
				}
				return nil // A module may ignore a conversation failure.
			}
			account := func(pam.Flags) error {
				accounts++
				if stage == "account" {
					conversationErr = errors.New("rejected prompt")
				}
				return nil
			}
			result := verifyTransaction(auth, account, &conversationErr)
			if result.OK || result.Message != "Additional prompt unsupported" {
				t.Fatalf("conversation failure accepted: %+v", result)
			}
			if stage == "authenticate" && accounts != 0 {
				t.Fatal("account management ran after rejected authentication conversation")
			}
		})
	}
}

func TestPAMConversationRejectsUnsupportedStyles(t *testing.T) {
	for _, style := range []pam.Style{pam.PromptEchoOn, pam.Style(999)} {
		var failure error
		calls := 0
		conversation := pamConversation(func(string, bool) (string, error) { calls++; return "secret", nil }, &failure)
		if answer, err := conversation(style, "Unsupported"); answer != "" || err == nil || failure == nil {
			t.Fatalf("accepted style %d", style)
		}
		if answer, err := conversation(pam.PromptEchoOff, "Password"); answer != "" || err == nil || calls != 0 {
			t.Fatal("continued after failed conversation")
		}
	}
	var failure error
	conversation := pamConversation(func(string, bool) (string, error) { return "secret", nil }, &failure)
	if answer, err := conversation(pam.PromptEchoOff, "Password"); answer != "secret" || err != nil {
		t.Fatal(answer, err)
	}
	if answer, err := conversation(pam.PromptEchoOff, "OTP"); answer != "" || err == nil || failure == nil {
		t.Fatal("accepted second hidden prompt")
	}
}

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
	pw, err := response(Prompt{Kind: Secret, Message: "Password: "})
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
	res, err := a.Verify("nomadx", func(p Prompt) (string, error) {
		if p.Kind != Secret || p.Message != "Password: " {
			t.Fatalf("unexpected prompt %+v", p)
		}
		return "hunter2", nil
	})
	if err != nil || !res.OK {
		t.Fatalf("got %+v, %v", res, err)
	}
	res, err = a.Verify("nomadx", func(Prompt) (string, error) { return "nope", nil })
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
	if _, err := a.Verify("", func(Prompt) (string, error) { return "", nil }); err == nil {
		t.Fatal("empty user must error before touching PAM")
	}
}

func TestPAMMissingService(t *testing.T) {
	a := &PAM{Service: "no-such-service-xyz"}
	_, err := a.Verify("someone", func(Prompt) (string, error) { return "", nil })
	if err == nil || !strings.Contains(err.Error(), "unavailable") {
		t.Fatalf("got %v, want service-unavailable error", err)
	}
	var se error = err
	_ = se
}

func TestPAMImplementsInterface(t *testing.T) {
	var _ Authenticator = (*PAM)(nil)
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
			if result.OK || result.Message != "Authentication failed" {
				t.Fatalf("conversation failure accepted: %+v", result)
			}
			if stage == "authenticate" && accounts != 0 {
				t.Fatal("account management ran after rejected authentication conversation")
			}
		})
	}
}

func TestPAMConversationRejectsUnsupportedStyles(t *testing.T) {
	var failure error
	calls := 0
	conversation := pamConversation(func(Prompt) (string, error) { calls++; return "secret", nil }, &failure)
	if answer, err := conversation(pam.Style(999), "Crazy"); answer != "" || err == nil || failure == nil {
		t.Fatal("unsupported style accepted")
	}
	if answer, err := conversation(pam.PromptEchoOff, "Password"); answer != "" || err == nil {
		t.Fatalf("sticky failure not reused: %v", err)
	}
	if calls != 0 {
		t.Fatal("responder ran after failure")
	}
}

func TestPamConversationDrivesEveryPromptKind(t *testing.T) {
	var got []Prompt
	answers := []string{"pw1", "visible", "123456"}
	secret := 0
	response := func(p Prompt) (string, error) {
		got = append(got, p)
		if p.Kind == Secret || p.Kind == Visible {
			a := answers[secret]
			secret++
			return a, nil
		}
		return "ignored", nil
	}
	var failure error
	conversation := pamConversation(response, &failure)
	for _, step := range []struct {
		style pam.Style
		msg   string
		want  string
	}{
		{pam.TextInfo, "Present finger", ""},
		{pam.PromptEchoOff, "Password: ", "pw1"},
		{pam.PromptEchoOn, "Confirm: ", "visible"},
		{pam.PromptEchoOff, "OTP: ", "123456"},
		{pam.ErrorMsg, "Try again", ""},
	} {
		answer, err := conversation(step.style, step.msg)
		if err != nil || answer != step.want || failure != nil {
			t.Fatalf("step %d got %q %v failure %v", step.style, answer, err, failure)
		}
	}
	kinds := []PromptKind{Info, Secret, Visible, Secret, Problem}
	for i, k := range kinds {
		if got[i].Kind != k {
			t.Fatalf("prompt %d kind %v want %v", i, got[i].Kind, k)
		}
	}
	if got[1].Message != "Password: " || got[3].Message != "OTP: " {
		t.Fatal("prompt message lost")
	}
}

func TestPamConversationWithoutResponderFailsClosed(t *testing.T) {
	var failure error
	conversation := pamConversation(nil, &failure)
	if answer, err := conversation(pam.TextInfo, "Present finger"); answer != "" || err != nil {
		t.Fatal("info without responder must pass through")
	}
	if answer, err := conversation(pam.PromptEchoOff, "Password: "); answer != "" || err == nil || failure == nil {
		t.Fatal("secret without responder must fail closed")
	}
}

// Package auth verifies the logged-in user through PAM, in-process, behind
// a single seam. The CGO boundary of this module is exactly this package's
// dependency on msteinert/pam/v2; everything else is pure Go.
//
// Limitation: Go strings are immutable, so the answer to the password prompt
// cannot be reliably zeroed inside this package. Callers that buffer input
// as []byte must Zero() it (see the UI password ring buffer).
package auth

import (
	"errors"
	"fmt"
	"os"

	"github.com/msteinert/pam/v2"
)

// Result is the outcome of one verification attempt.
type Result struct {
	OK       bool
	Message  string
	Terminal bool // further attempts are futile (perm denied, expired, maxtries)
}

// PromptFunc receives PAM conversation prompts. echo=false means secret.
type PromptFunc func(prompt string, echo bool) (string, error)

// Authenticator verifies a user. Implementations are safe to call only
// sequentially (one conversation at a time).
type Authenticator interface {
	Verify(user string, response PromptFunc) (Result, error)
}

// Zero clears a byte buffer (password hygiene).
func Zero(b []byte) {
	for i := range b {
		b[i] = 0
	}
}

// PAM is the only production Authenticator.
type PAM struct {
	Service string // defaults to "login"; never modified on disk
}

// NewPAM returns the PAM authenticator for the "login" service.
func NewPAM() *PAM { return &PAM{Service: "login"} }

// Verify runs authenticate + acct_mgmt for user.
func (p *PAM) Verify(user string, response PromptFunc) (Result, error) {
	if user == "" {
		return Result{}, errors.New("auth: empty user")
	}
	service := p.Service
	if service == "" {
		service = "login"
	}
	if _, err := os.Stat("/etc/pam.d/" + service); err != nil {
		return Result{}, fmt.Errorf("auth: pam service %q unavailable: %w", service, err)
	}
	var conversationErr error
	tx, err := pam.StartFunc(service, user, pamConversation(response, &conversationErr))
	if err != nil {
		return Result{}, fmt.Errorf("auth: start: %w", err)
	}
	defer tx.End() //nolint:errcheck // end-of-transact is best-effort

	return verifyTransaction(tx.Authenticate, tx.AcctMgmt, &conversationErr), nil
}

// verifyTransaction also checks the conversation outcome; modules own PAM stages.
func verifyTransaction(authenticate, acctMgmt func(pam.Flags) error, conversationErr *error) Result {
	for _, check := range []func(pam.Flags) error{authenticate, acctMgmt} {
		err := check(0)
		// PAM modules can ignore callback failures; they never authorize unlock.
		if *conversationErr != nil {
			return Result{Message: "Additional prompt unsupported"}
		}
		if err != nil {
			return mapPamError(err)
		}
	}
	return Result{OK: true}
}

func pamConversation(response PromptFunc, failure *error) pam.ConversationFunc {
	prompted := false
	return func(style pam.Style, msg string) (string, error) {
		if *failure != nil {
			return "", *failure
		}
		switch style {
		case pam.PromptEchoOff:
			if !prompted && response != nil {
				prompted = true
				answer, err := response(msg, false)
				if err != nil {
					*failure = err
					return "", err
				}
				return answer, nil
			}
		case pam.ErrorMsg, pam.TextInfo:
			// Raw module text can contain sensitive details; stage outcomes are sanitized.
			return "", nil
		}
		*failure = errors.New("additional or unsupported authentication prompt")
		return "", *failure
	}
}

func mapPamError(err error) Result {
	var pe pam.Error
	if errors.As(err, &pe) {
		switch pe {
		case pam.ErrAuth:
			return Result{OK: false, Message: "Incorrect password"}
		case pam.ErrMaxtries:
			return Result{OK: false, Terminal: true, Message: "Too many attempts - locked out"}
		case pam.ErrPermDenied:
			return Result{OK: false, Terminal: true, Message: "Permission denied"}
		case pam.ErrAcctExpired:
			return Result{OK: false, Terminal: true, Message: "Account expired"}
		}
	}
	return Result{OK: false, Message: "Authentication unavailable"}
}

// PasswordPrompt supports one hidden prompt; a second could be an OTP.
func PasswordPrompt(password string) PromptFunc {
	used := false
	return func(_ string, echo bool) (string, error) {
		if used || echo {
			return "", errors.New("Additional authentication prompt unsupported")
		}
		used = true
		return password, nil
	}
}

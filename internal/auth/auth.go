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
	tx, err := pam.StartFunc(service, user, func(style pam.Style, msg string) (string, error) {
		switch style {
		case pam.PromptEchoOff:
			return response(msg, false)
		case pam.PromptEchoOn:
			return response(msg, true)
		case pam.ErrorMsg, pam.TextInfo:
			// Surface non-prompt messages, never as secret.
			return "", nil
		default:
			return "", fmt.Errorf("auth: unsupported prompt style %d", style)
		}
	})
	if err != nil {
		return Result{}, fmt.Errorf("auth: start: %w", err)
	}
	defer tx.End() //nolint:errcheck // end-of-transact is best-effort

	if err := tx.Authenticate(0); err != nil {
		return mapPamError(err), nil
	}
	if err := tx.AcctMgmt(0); err != nil {
		return mapPamError(err), nil
	}
	return Result{OK: true}, nil
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
	return Result{OK: false, Message: err.Error()}
}

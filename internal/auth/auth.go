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

// PromptKind tells the UI how to treat one PAM conversation message.
type PromptKind int

const (
	Secret  PromptKind = iota // hidden answer, required
	Visible                   // echoed answer, required
	Info                      // display only, never blocks
	Problem                   // display-only module error
)

// Prompt is one PAM conversation message. Message text comes from modules
// and may be sensitive; display it, never log it.
type Prompt struct {
	Kind    PromptKind
	Message string
}

// PromptFunc receives PAM conversation prompts in sequence.
type PromptFunc func(Prompt) (string, error)

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
			return Result{Message: "Authentication failed"}
		}
		if err != nil {
			return mapPamError(err)
		}
	}
	return Result{OK: true}
}

func pamConversation(response PromptFunc, failure *error) pam.ConversationFunc {
	return func(style pam.Style, msg string) (string, error) {
		if *failure != nil {
			return "", *failure
		}
		var kind PromptKind
		switch style {
		case pam.PromptEchoOff:
			kind = Secret
		case pam.PromptEchoOn:
			kind = Visible
		case pam.TextInfo:
			kind = Info
		case pam.ErrorMsg:
			kind = Problem
		default:
			*failure = errors.New("unsupported authentication prompt")
			return "", *failure
		}
		if response == nil {
			if kind == Info || kind == Problem {
				return "", nil
			}
			*failure = errors.New("no prompt responder")
			return "", *failure
		}
		answer, err := response(Prompt{Kind: kind, Message: msg})
		if err != nil {
			*failure = err
			return "", err
		}
		if kind == Info || kind == Problem {
			return "", nil
		}
		return answer, nil
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

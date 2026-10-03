//go:build lockdev

// Development-only bypass. This file is excluded from release builds by the
// build tag and is DELETED by Task 14 after gate G4 has been exercised once.
// It replaces only the authenticator; every protocol/lifecycle gate
// (G1, G3, G5, G6, G7) still runs against the real session-lock state machine.

package main

import "github.com/Nomadcxx/sysc-lock/internal/auth"

const devPassword = "sysc-dev-unlock"

type devAuth struct{}

func (devAuth) User() string { return currentUser() }

func (devAuth) Verify(_ string, response auth.PromptFunc) (auth.Result, error) {
	pass, err := response("dev password: ", false)
	if err != nil {
		return auth.Result{}, err
	}
	if pass == devPassword {
		return auth.Result{OK: true}, nil
	}
	return auth.Result{Message: "Incorrect password"}, nil
}

func newAuthenticator() authenticator { return devAuth{} }

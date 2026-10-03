package main

import "github.com/Nomadcxx/sysc-lock/internal/auth"

type pamAuth struct{ *auth.PAM }

func (p *pamAuth) User() string { return currentUser() }

func newAuthenticator() authenticator { return &pamAuth{auth.NewPAM()} }

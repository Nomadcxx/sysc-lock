package main

import "github.com/Nomadcxx/sysc-lock/internal/auth"

type pamAuth struct {
	*auth.PAM
	user string
}

func (p *pamAuth) User() string                  { return p.user }
func newAuthenticator(user string) authenticator { return &pamAuth{auth.NewPAM(), user} }

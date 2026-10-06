package main

import "testing"

func TestValidatePrefixMatchesScript(t *testing.T) {
	cases := []struct {
		prefix string
		errMsg string
	}{
		{"/home/test/.local", ""},
		{"/tmp/a-b_c.d/e", ""},
		{"/", "use a user prefix"},
		{"", "use a user prefix"},
		{"relative/x", "prefix must be absolute"},
		{"ünï/x", "prefix must be absolute"},
		{"/ünï", "prefix contains unsupported characters"},
		{"/tmp/a b", "prefix contains unsupported characters"},
		{"/tmp/a$b", "prefix contains unsupported characters"},
	}
	for _, c := range cases {
		err := validatePrefix(c.prefix)
		got := ""
		if err != nil {
			got = err.Error()
		}
		if got != c.errMsg {
			t.Errorf("validatePrefix(%q) = %q, want %q", c.prefix, got, c.errMsg)
		}
	}
}

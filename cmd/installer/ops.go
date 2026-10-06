package main

import (
	"errors"
	"regexp"
	"strings"
)

// ponytail: mirrors scripts/install checks in script order; a full path encoder
// is only needed if unit-prefixed paths ever go through here.
var prefixChars = regexp.MustCompile(`^[a-zA-Z0-9_./-]+$`)

func validatePrefix(prefix string) error {
	if prefix == "/" || prefix == "" {
		return errors.New("use a user prefix")
	}
	if !strings.HasPrefix(prefix, "/") {
		return errors.New("prefix must be absolute")
	}
	if !prefixChars.MatchString(prefix) {
		return errors.New("prefix contains unsupported characters")
	}
	return nil
}

package main

import (
	"errors"
	"os"
	"regexp"

	"golang.org/x/sys/unix"
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

// checkCandidate mirrors the script's `[ -f candidate ] && [ -x candidate ]`.
func checkCandidate(path string) error {
	const msg = "candidate must be an executable regular file"
	fi, err := os.Stat(path)
	if err != nil || !fi.Mode().IsRegular() {
		return errors.New(msg)
	}
	if err := unix.Access(path, unix.X_OK); err != nil {
		return errors.New(msg)
	}
	return nil
}

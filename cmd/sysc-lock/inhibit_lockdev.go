//go:build lockdev

package main

import (
	"fmt"
	"os"

	"github.com/Nomadcxx/sysc-lock/internal/inhibit"
)

// takeInhibit is best-effort ONLY under the lockdev build tag, for nested
// gate harnesses that are not in an active logind session (block-mode inhibit
// is polkit-denied there). Deleted together with auth_lockdev.go in Task 14;
// the default build keeps the strict exit-4 contract.
func takeInhibit() func() {
	logind, err := inhibit.NewLogind()
	if err != nil {
		fmt.Fprintf(os.Stderr, "sysc-lock(dev): inhibit unavailable: %v\n", err)
		return func() {}
	}
	release, err := inhibit.Take(logind)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sysc-lock(dev): inhibit denied: %v\n", err)
		logind.Release()
		return func() {}
	}
	return release
}


package main

import (
	"fmt"
	"os"

	"github.com/Nomadcxx/sysc-lock/internal/inhibit"
)

// takeInhibit enforces the sleep-guard invariant: the locker refuses to lock
// without a logind block inhibitor (exit 4).
func takeInhibit() func() {
	logind, err := inhibit.NewLogind()
	if err != nil {
		fmt.Fprintln(os.Stderr, "sysc-lock: no sleep inhibitor:", err)
		os.Exit(4)
	}
	release, err := inhibit.Take(logind)
	if err != nil {
		fmt.Fprintln(os.Stderr, "sysc-lock: no sleep inhibitor:", err)
		logind.Release()
		os.Exit(4)
	}
	return release
}

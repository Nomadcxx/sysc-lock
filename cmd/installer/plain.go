package main

import (
	"context"
	"errors"
	"fmt"
	"io"
)

// runPlain drives the same task table without Bubble Tea: one line per task as
// it settles, then the summary line the caller prints.
func runPlain(ctx context.Context, r *runner, out io.Writer) (int, error) {
	report := func(i int, s taskStatus) {
		switch s {
		case statusDone:
			fmt.Fprintf(out, "[OK]   %s\n", r.tasks[i].name)
		case statusSkipped:
			fmt.Fprintf(out, "[SKIP] %s (%s)\n", r.tasks[i].name, r.snapshot().skips[i])
		}
	}
	err := r.runAll(ctx, report)
	if err == nil {
		return exitOK, nil
	}
	st := r.snapshot()
	name := "task"
	if st.failedIdx >= 0 && st.failedIdx < len(r.tasks) {
		name = r.tasks[st.failedIdx].name
	}
	if errors.Is(err, errCancelled) {
		fmt.Fprintf(out, "[FAIL] %s: cancelled\n", name)
		return exitCancelled, err
	}
	fmt.Fprintf(out, "[FAIL] %s: %s\n", name, err)
	return exitFailed, err
}

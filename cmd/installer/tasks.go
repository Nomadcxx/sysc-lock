package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
)

var (
	goCmd    = exec.CommandContext
	lookPath = exec.LookPath
)

type options struct {
	prefix    string
	candidate string
	root      string
	logPath   string
	uninstall bool
	yes       bool
}

type taskStatus int

const (
	statusPending taskStatus = iota
	statusRunning
	statusDone
	statusSkipped
	statusFailed
)

type skipError struct{ reason string }

func (e skipError) Error() string { return e.reason }

var errCancelled = errors.New("cancelled")

type taskFunc func(ctx context.Context, r *runner, sub func(i int, s taskStatus)) error

type task struct {
	name        string
	description string
	sub         []string
	fn          taskFunc
}

// runner owns all shared install state; the model only ever reads copies
// through snapshot, which is what keeps the UI goroutine race-free.
type runner struct {
	ctx       context.Context
	cancelFn  context.CancelFunc
	mu        sync.Mutex
	opts      options
	log       *logger
	tasks     []task
	minTask   time.Duration
	buildDir  string
	status    []taskStatus
	subStat   [][]taskStatus
	skips     map[int]string
	notes     []string
	failedIdx int
	errDetail string
	cmdLine   string
}

type runnerState struct {
	status []taskStatus
	sub    [][]taskStatus
	skips  map[int]string
	notes  []string
}

func newRunner(opts options, log *logger) *runner {
	tasks := installTasks()
	if opts.uninstall {
		tasks = uninstallTasks()
	}
	ctx, cancel := context.WithCancel(context.Background())
	r := &runner{ctx: ctx, cancelFn: cancel, opts: opts, log: log, tasks: tasks, failedIdx: -1, skips: map[int]string{}}
	r.status = make([]taskStatus, len(tasks))
	r.subStat = make([][]taskStatus, len(tasks))
	for i, t := range tasks {
		r.subStat[i] = make([]taskStatus, len(t.sub))
	}
	return r
}

func (r *runner) cancel() {
	if r.cancelFn != nil {
		r.cancelFn()
	}
}

func (r *runner) cancelled() bool { return r.ctx.Err() != nil }

func (r *runner) taskIs(i int, name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return i >= 0 && i < len(r.tasks) && r.tasks[i].name == name
}

func (r *runner) snapshot() runnerState {
	r.mu.Lock()
	defer r.mu.Unlock()
	st := runnerState{
		status: append([]taskStatus(nil), r.status...),
		sub:    make([][]taskStatus, len(r.subStat)),
		skips:  map[int]string{},
		notes:  append([]string(nil), r.notes...),
	}
	for i, s := range r.subStat {
		st.sub[i] = append([]taskStatus(nil), s...)
	}
	for k, v := range r.skips {
		st.skips[k] = v
	}
	return st
}

func (r *runner) binaryPath() string {
	if r.opts.candidate != "" {
		return r.opts.candidate
	}
	return r.buildDir + "/sysc-lock"
}

func (r *runner) setStatus(i int, s taskStatus, report func(int, taskStatus)) {
	r.mu.Lock()
	r.status[i] = s
	r.mu.Unlock()
	if report != nil {
		report(i, s)
	}
}

// lineSink tees command output into the log line by line and keeps the lines
// in memory for the last-20-lines error rule.
type lineSink struct {
	log     *logger
	partial strings.Builder
	lines   []string
}

func (s *lineSink) Write(p []byte) (int, error) {
	full := s.partial.String() + string(p)
	s.partial.Reset()
	for {
		i := strings.IndexByte(full, '\n')
		if i < 0 {
			break
		}
		line := strings.TrimSuffix(full[:i], "\r")
		full = full[i+1:]
		s.lines = append(s.lines, line)
		s.log.Printf("%s", line)
	}
	s.partial.WriteString(full)
	return len(p), nil
}

func (s *lineSink) flush() {
	if s.partial.Len() > 0 {
		s.lines = append(s.lines, s.partial.String())
		s.log.Printf("%s", s.partial.String())
		s.partial.Reset()
	}
}

func (s *lineSink) text() string { return strings.Join(s.lines, "\n") }

func (r *runner) runLogged(taskName string, cmd *exec.Cmd) (string, error) {
	r.log.Printf("[%s] Running: %s", taskName, cmd.String())
	sink := &lineSink{log: r.log}
	cmd.Stdout = sink
	cmd.Stderr = sink
	err := cmd.Run()
	sink.flush()
	if err != nil {
		r.log.Printf("[%s] Error: %v", taskName, err)
	} else {
		r.log.Printf("[%s] Success", taskName)
	}
	return sink.text(), err
}

func lastLine(out string, max int) string {
	lines := strings.Split(out, "\n")
	if n := len(lines); n > max {
		lines = lines[n-max:]
	}
	for i := len(lines) - 1; i >= 0; i-- {
		if l := strings.TrimSpace(lines[i]); l != "" {
			return l
		}
	}
	return ""
}

func (r *runner) runAll(ctx context.Context, report func(int, taskStatus)) error {
	defer func() {
		if r.buildDir != "" {
			os.RemoveAll(r.buildDir)
		}
	}()
	for i := range r.tasks {
		if ctx.Err() != nil {
			return errCancelled
		}
		t := r.tasks[i]
		r.log.Printf("[%s] Started", t.name)
		r.setStatus(i, statusRunning, report)
		start := time.Now()
		err := t.fn(ctx, r, func(j int, s taskStatus) {
			r.mu.Lock()
			r.subStat[i][j] = s
			r.mu.Unlock()
		})
		if d := r.minTask - time.Since(start); d > 0 && err == nil {
			select {
			case <-time.After(d):
			case <-ctx.Done():
			}
		}
		var se skipError
		switch {
		case err == nil:
			r.log.Printf("[%s] Done", t.name)
			r.setStatus(i, statusDone, report)
		case errors.As(err, &se):
			r.log.Printf("[%s] Skipped: %s", t.name, se.reason)
			r.mu.Lock()
			r.skips[i] = se.reason
			r.mu.Unlock()
			r.setStatus(i, statusSkipped, report)
		default:
			r.setStatus(i, statusFailed, report)
			r.mu.Lock()
			r.failedIdx = i
			if ctx.Err() != nil || errors.Is(err, errCancelled) {
				r.log.Printf("[%s] Failed: %v", t.name, errCancelled)
				r.mu.Unlock()
				return errCancelled
			}
			if r.errDetail == "" {
				r.errDetail = err.Error()
			}
			r.log.Printf("[%s] Failed: %v", t.name, err)
			r.mu.Unlock()
			return err
		}
	}
	return nil
}

func installTasks() []task {
	return []task{
		{name: "Check prefix", description: "Validating install prefix",
			fn: func(_ context.Context, r *runner, _ func(int, taskStatus)) error {
				return validatePrefix(r.opts.prefix)
			}},
		{name: "Check toolchain", description: "Checking go and libpam headers",
			fn: func(ctx context.Context, r *runner, _ func(int, taskStatus)) error {
				if r.opts.candidate != "" {
					return skipError{"using --candidate"}
				}
				if _, err := lookPath("go"); err != nil {
					return errors.New("go not found in PATH: install Go (go.mod needs 1.26) or pass --candidate PATH")
				}
				cmd := goCmd(ctx, "go", "env", "CC")
				cmd.Dir = r.opts.root
				out, err := r.runLogged("Check toolchain", cmd)
				if err != nil {
					return err
				}
				cc := strings.TrimSpace(out)
				probe := goCmd(ctx, cc, "-E", "-x", "-")
				probe.Dir = r.opts.root
				probe.Stdin = strings.NewReader("#include <security/pam_appl.h>\n")
				if _, err := r.runLogged("Check toolchain", probe); err != nil {
					return errors.New("libpam headers missing (security/pam_appl.h): install them " +
						"(Arch: pam, Debian/Ubuntu: libpam0g-dev, Fedora: pam-devel)")
				}
				return nil
			}},
		{name: "Build sysc-lock", description: "Building sysc-lock (CGO_ENABLED=1 go build)",
			fn: func(ctx context.Context, r *runner, _ func(int, taskStatus)) error {
				if r.opts.candidate != "" {
					return skipError{"using --candidate"}
				}
				dir, err := os.MkdirTemp("", "sysc-lock-build-*")
				if err != nil {
					return err
				}
				r.buildDir = dir
				dst := dir + "/sysc-lock"
				cmd := goCmd(ctx, "go", "build", "-o", dst, "./cmd/sysc-lock")
				cmd.Dir = r.opts.root
				cmd.Env = append(os.Environ(), "CGO_ENABLED=1")
				cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
				cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM) }
				cmd.WaitDelay = 3 * time.Second
				out, err := r.runLogged("Build sysc-lock", cmd)
				if err != nil {
					if ctx.Err() != nil {
						return errCancelled
					}
					r.mu.Lock()
					r.errDetail = lastLine(out, 20)
					if r.errDetail == "" {
						r.errDetail = err.Error()
					}
					r.cmdLine = fmt.Sprintf("CGO_ENABLED=1 go build -o %s ./cmd/sysc-lock", dst)
					r.mu.Unlock()
					return errors.New(r.errDetail)
				}
				return nil
			}},
		{name: "Check candidate", description: "Checking the candidate executable",
			fn: func(_ context.Context, r *runner, _ func(int, taskStatus)) error {
				return checkCandidate(r.binaryPath())
			}},
		{name: "Create directories", description: "Creating bin and systemd user directories",
			fn: func(_ context.Context, r *runner, _ func(int, taskStatus)) error {
				return installDirs(r.opts.prefix)
			}},
		{name: "Install binary", description: "Installing sysc-lock",
			sub: []string{"Copy to bin/sysc-lock.new", "Replace bin/sysc-lock"},
			fn: func(_ context.Context, r *runner, sub func(int, taskStatus)) error {
				if err := stageBinary(r.opts.prefix, r.binaryPath()); err != nil {
					return err
				}
				sub(0, statusDone)
				if err := replaceBinary(r.opts.prefix); err != nil {
					return err
				}
				sub(1, statusDone)
				return nil
			}},
		{name: "Install user unit", description: "Writing sysc-lock-session.service",
			fn: func(_ context.Context, r *runner, _ func(int, taskStatus)) error {
				if err := installUnit(r.opts.root, r.opts.prefix); err != nil {
					return err
				}
				for _, o := range runningLockOwners(r.opts.prefix + "/bin/sysc-lock") {
					r.mu.Lock()
					r.notes = append(r.notes, fmt.Sprintf(
						"A running sysc-lock (pid %d) keeps the previous binary until sysc-lock-session.service restarts.", o.pid))
					r.mu.Unlock()
				}
				return nil
			}},
	}
}

// uninstallTasks reverses exactly the files an install wrote: the user unit
// and the binary, plus a stale .new. Directories, user config, runtime state
// and PAM are never touched.
func uninstallTasks() []task {
	return []task{
		{name: "Check prefix", description: "Validating install prefix",
			fn: func(_ context.Context, r *runner, _ func(int, taskStatus)) error {
				return validatePrefix(r.opts.prefix)
			}},
		{name: "Check service", description: "Checking for a running or enabled sysc-lock",
			fn: func(_ context.Context, r *runner, _ func(int, taskStatus)) error {
				if owners := runningLockOwners(r.opts.prefix + "/bin/sysc-lock"); len(owners) > 0 {
					return fmt.Errorf("sysc-lock is running (pid %d) from %s: "+
						"stop sysc-lock-session.service in a coordinated Niri session first",
						owners[0].pid, owners[0].exe)
				}
				links, err := enabledUnitLinks()
				if err != nil {
					return err
				}
				if len(links) > 0 {
					return fmt.Errorf("sysc-lock-session.service is enabled (%s): disable it first", links[0])
				}
				return nil
			}},
		{name: "Remove user unit", description: "Removing sysc-lock-session.service",
			fn: func(_ context.Context, r *runner, _ func(int, taskStatus)) error {
				removed, err := removeIfExists(r.opts.prefix + "/share/systemd/user/sysc-lock-session.service")
				if err != nil {
					return err
				}
				if !removed {
					return skipError{"unit not installed"}
				}
				return nil
			}},
		{name: "Remove binary", description: "Removing sysc-lock",
			fn: func(_ context.Context, r *runner, _ func(int, taskStatus)) error {
				removed, err := removeIfExists(r.opts.prefix + "/bin/sysc-lock")
				if err != nil {
					return err
				}
				staged, err := removeIfExists(r.opts.prefix + "/bin/sysc-lock.new")
				if err != nil {
					return err
				}
				if !removed && !staged {
					return skipError{"nothing installed"}
				}
				return nil
			}},
	}
}

var _ io.Writer = (*lineSink)(nil)

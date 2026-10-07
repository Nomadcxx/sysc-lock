package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/term"
)

const defaultLogPath = "/tmp/sysc-lock-installer.log"

var errHelp = errors.New("help requested")

// ttyOK reports whether a Bubble Tea session can own the terminal: stdout must
// be a terminal, and input needs either stdin or an openable /dev/tty, which
// is what keeps the curl|sh path able to draw the frame.
var ttyOK = func() bool {
	if !term.IsTerminal(1) {
		return false
	}
	if term.IsTerminal(0) {
		return true
	}
	f, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return false
	}
	f.Close()
	return true
}

func usage(w io.Writer, fs *flag.FlagSet) {
	fmt.Fprintln(w, "usage: sysc-lock-installer [flags]")
	fmt.Fprintln(w, "\nInstall or remove sysc-lock and its user unit. Writes only under the")
	fmt.Fprintln(w, "prefix; never enables a service and never edits PAM.")
	fmt.Fprintln(w)
	fs.SetOutput(w)
	fs.PrintDefaults()
}

func parseArgs(args []string, stdout, stderr io.Writer) (options, error) {
	opts := options{logPath: defaultLogPath}
	fs := flag.NewFlagSet("sysc-lock-installer", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { usage(stderr, fs) }
	fs.BoolVar(&opts.yes, "yes", false, "run without prompts, plain-text output")
	fs.BoolVar(&opts.yes, "y", false, "run without prompts (shorthand for --yes)")
	fs.BoolVar(&opts.uninstall, "uninstall", false, "remove the binary and user unit")
	fs.StringVar(&opts.prefix, "prefix", "", "install prefix (default $HOME/.local)")
	fs.StringVar(&opts.candidate, "candidate", "", "install this pre-built sysc-lock executable")
	fs.StringVar(&opts.logPath, "log", defaultLogPath, "installer log file")
	// flag writes --help usage to stderr; the spec wants it on stdout.
	for _, a := range args {
		if a == "-h" || a == "--help" {
			usage(stdout, fs)
			return opts, errHelp
		}
	}
	if err := fs.Parse(args); err != nil {
		return opts, err
	}
	if fs.NArg() > 0 {
		fs.Usage()
		return opts, fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	return opts, nil
}

func modeName(opts options) string {
	if opts.uninstall {
		return "uninstall"
	}
	return "install"
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run is the whole installer: preflight, then the TUI or the plain runner.
func run(args []string, stdout, stderr io.Writer) int {
	opts, err := parseArgs(args, stdout, stderr)
	if errors.Is(err, errHelp) {
		return exitOK
	}
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitUsage
	}

	if opts.prefix == "" {
		if geteuid() == 0 {
			fmt.Fprintln(stderr, "running as root: pass --prefix explicitly (sysc-lock installs into a user prefix)")
			return exitUsage
		}
		home, err := os.UserHomeDir()
		if err != nil {
			fmt.Fprintln(stderr, err)
			return exitUsage
		}
		opts.prefix = filepath.Join(home, ".local")
	}
	if !opts.yes && !ttyOK() {
		fmt.Fprintln(stderr, "no terminal: rerun with --yes")
		return exitUsage
	}
	if err := validatePrefix(opts.prefix); err != nil {
		fmt.Fprintln(stderr, err)
		return exitUsage
	}
	root, err := findRepoRoot()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitUsage
	}
	opts.root = root

	log, err := openLog(opts.logPath)
	if err != nil {
		fmt.Fprintf(stderr, "no log file: %v\n", err)
	}
	source := "go build ./cmd/sysc-lock"
	if opts.candidate != "" {
		source = opts.candidate
	}
	logHeader(log, modeName(opts), opts.prefix, source, root)

	if opts.yes {
		ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
		defer stop()
		r := newRunner(opts, log)
		code, runErr := runPlain(ctx, r, stdout)
		path := log.Path()
		log.Close()
		m := newModel(opts)
		m.log, m.run, m.exitCode, m.finalErr = log, r, code, runErr
		m.cancelled = code == exitCancelled
		printSummary(m, stdout, stderr)
		if path == "" {
			path = "none"
		}
		fmt.Fprintf(stdout, "Log: %s\n", path)
		return code
	}

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(sigs)
	m := newModel(opts)
	m.log, m.sigs = log, sigs
	// No alt screen: the final frame stays in the terminal.
	final, err := tea.NewProgram(m, tea.WithoutSignalHandler()).Run()
	log.Close()
	if err != nil {
		fmt.Fprintf(stderr, "sysc-lock installer: %v\n", err)
		return exitUsage
	}
	fm, ok := final.(model)
	if !ok {
		fmt.Fprintln(stderr, "sysc-lock installer: unexpected program result")
		return exitUsage
	}
	printSummary(fm, stdout, stderr)
	return fm.exitCode
}

// printSummary is the one line the standard renderer would have scrolled away.
func printSummary(m model, stdout, stderr io.Writer) {
	switch {
	case m.exitCode == exitOK && m.opts.uninstall:
		fmt.Fprintln(stdout, "sysc-lock installer: uninstall complete")
	case m.exitCode == exitOK:
		fmt.Fprintf(stdout, "sysc-lock installer: install complete: %s, %s\n", m.installedBinary(), m.installedUnit())
	case m.cancelled:
		fmt.Fprintf(stderr, "sysc-lock installer: %s cancelled (log: %s)\n", modeName(m.opts), m.logPath())
	default:
		name, msg := "task", ""
		if m.run != nil {
			st := m.run.snapshot()
			if st.failedIdx >= 0 && st.failedIdx < len(m.run.tasks) {
				name = m.run.tasks[st.failedIdx].name
			}
			msg = st.errDetail
		}
		if m.finalErr != nil {
			msg = m.finalErr.Error()
		}
		if msg == "" {
			msg = "unknown error"
		}
		fmt.Fprintf(stderr, "sysc-lock installer: %s failed at %q: %s (log: %s)\n",
			modeName(m.opts), name, msg, m.logPath())
	}
}

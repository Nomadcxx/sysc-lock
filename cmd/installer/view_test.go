package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

var updateGoldens = flag.Bool("update", false, "regenerate the installer view snapshots")

// snapshotNames is every screen the spec pins; each is rendered at two sizes.
var snapshotNames = []string{
	"welcome",
	"welcome_uninstall",
	"review_install",
	"review_install_candidate",
	"review_uninstall",
	"installing_build",
	"installing_files",
	"complete_install",
	"complete_install_running_note",
	"complete_uninstall",
	"failed_build",
	"failed_uninstall_running",
	"cancelled_build",
}

// fixture builds a model with fixed values so the goldens cannot drift with
// the machine: prefix /home/test/.local, root /src/sysc-lock and a fixed log.
func fixture(t *testing.T, name string) model {
	t.Helper()
	opts := options{
		prefix:  "/home/test/.local",
		root:    "/src/sysc-lock",
		logPath: "/tmp/sysc-lock-installer.log",
	}
	// The canvas is blank until the animation runs, and the animation steps
	// through a random generator, so snapshots stay on the blank canvas.
	m := newModel(opts)
	log := testLogger(t)
	run := func(uninstall bool) *runner {
		o := opts
		o.uninstall = uninstall
		return newRunner(o, log)
	}

	switch name {
	case "welcome", "review_install":
		m.step = stepWelcome
		if strings.HasPrefix(name, "review") {
			m.step = stepReview
		}
	case "welcome_uninstall", "review_uninstall":
		m.opts.uninstall = true
		m.step = stepWelcome
		if strings.HasPrefix(name, "review") {
			m.step = stepReview
		}
	case "review_install_candidate":
		m.step = stepReview
		m.opts.candidate = "/src/sysc-lock/build/sysc-lock"
	case "installing_build":
		m.step = stepInstalling
		m.run = run(false)
		m.run.status[2] = statusRunning
		m.current = 2
	case "installing_files":
		m.step = stepInstalling
		m.run = run(false)
		m.run.status[2] = statusDone
		m.run.status[5] = statusRunning
		m.run.subStat[5] = []taskStatus{statusDone, statusRunning}
		m.current = 5
	case "complete_install":
		m.step = stepComplete
		m.run = run(false)
		for i := range m.run.status {
			m.run.status[i] = statusDone
		}
	case "complete_install_running_note":
		m.step = stepComplete
		m.run = run(false)
		for i := range m.run.status {
			m.run.status[i] = statusDone
		}
		m.run.notes = []string{"A running sysc-lock (pid 4242) keeps the previous binary until sysc-lock-session.service restarts."}
	case "complete_uninstall":
		m.opts.uninstall = true
		m.step = stepComplete
		m.run = run(true)
		for i := range m.run.status {
			m.run.status[i] = statusDone
		}
	case "failed_build":
		m.step = stepFailed
		m.run = run(false)
		m.run.status[2] = statusFailed
		m.run.failedIdx = 2
		m.run.errDetail = "security/pam_appl.h: No such file or directory"
		m.run.cmdLine = "CGO_ENABLED=1 go build -o /tmp/sysc-lock-build-1842/sysc-lock ./cmd/sysc-lock"
	case "failed_uninstall_running":
		m.opts.uninstall = true
		m.step = stepFailed
		m.run = run(true)
		m.run.status[1] = statusFailed
		m.run.failedIdx = 1
		m.run.errDetail = "sysc-lock is running (pid 4242) from /home/test/.local/bin/sysc-lock: stop sysc-lock-session.service in a coordinated Niri session first"
	case "cancelled_build":
		m.step = stepFailed
		m.cancelled = true
		m.run = run(false)
		m.run.status[2] = statusFailed
		m.run.failedIdx = 2
		m.run.errDetail = "cancelled"
	default:
		t.Fatalf("unknown fixture %q", name)
	}
	return m
}

func atSize(m model, width, height int) model {
	m, _ = m.update(tea.WindowSizeMsg{Width: width, Height: height})
	return m
}

func viewAt(t *testing.T, name string, width, height int) []string {
	t.Helper()
	out := ansi.Strip(atSize(fixture(t, name), width, height).View())
	return strings.Split(strings.TrimRight(out, "\n"), "\n")
}

func TestViewFitsSmallTerminal(t *testing.T) {
	for _, name := range snapshotNames {
		for _, sz := range [][2]int{{80, 24}, {120, 40}, {60, 16}} {
			lines := viewAt(t, name, sz[0], sz[1])
			if len(lines) > sz[1] {
				t.Errorf("%s at %dx%d: %d lines", name, sz[0], sz[1], len(lines))
			}
		}
	}
}

func TestBannerOnlyWhenEveryColumnFits(t *testing.T) {
	wide := artWidth()
	if wide < 60 {
		t.Fatalf("banner art is %d columns wide, narrower than the minimum terminal", wide)
	}
	m := newTestModel(t)
	m, _ = m.update(tea.WindowSizeMsg{Width: wide, Height: 40})
	if !m.bannerFits() {
		t.Errorf("banner must fit at its own width, %d", wide)
	}
	m, _ = m.update(tea.WindowSizeMsg{Width: wide - 1, Height: 40})
	if m.bannerFits() {
		t.Errorf("banner must not fit one column short of %d", wide)
	}
	fits := len(viewAt(t, "welcome", wide, 40))
	short := len(viewAt(t, "welcome", wide-1, 40))
	if want := fits - short; want != bannerRows+1 {
		t.Errorf("banner adds %d rows when it fits, want %d", want, bannerRows+1)
	}
}

func TestTinyTerminalShowsTheMinimum(t *testing.T) {
	lines := viewAt(t, "failed_build", 40, 10)
	if len(lines) != 2 {
		t.Fatalf("tiny terminal produced %d lines, want 2: %q", len(lines), lines)
	}
	if !strings.Contains(lines[0], "Enlarge the terminal to at least 60×16") {
		t.Errorf("first line = %q", lines[0])
	}
}

func TestFailedRowTruncatesLongOutput(t *testing.T) {
	m := atSize(fixture(t, "failed_build"), 80, 24)
	m.run.errDetail = strings.Repeat("x", 400)
	for _, row := range strings.Split(ansi.Strip(m.View()), "\n") {
		if !strings.Contains(row, "└─ Error: ") {
			continue
		}
		if w := lipgloss.Width(row); w > 80 {
			t.Errorf("error row is %d columns wide", w)
		}
		if !strings.Contains(row, "…") {
			t.Errorf("truncated row has no ellipsis: %q", row)
		}
		return
	}
	t.Fatal("no error row in the failed view")
}

func TestSnapshots(t *testing.T) {
	for _, name := range snapshotNames {
		for _, sz := range [][2]int{{80, 24}, {120, 40}} {
			got := strings.Join(viewAt(t, name, sz[0], sz[1]), "\n") + "\n"
			path := filepath.Join("testdata", fmt.Sprintf("%s_%dx%d.golden", name, sz[0], sz[1]))
			if *updateGoldens {
				if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
				continue
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if got != string(want) {
				t.Errorf("%s differs from %s", name, path)
			}
		}
	}
}

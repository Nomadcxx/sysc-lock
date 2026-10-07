package main

import (
	"errors"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Palette and status marks ported from the sysc-greet installer frame.
const (
	Secondary    = lipgloss.Color("#cccccc")
	FgPrimary    = lipgloss.Color("#ffffff")
	FgSecondary  = lipgloss.Color("#cccccc")
	FgMuted      = lipgloss.Color("#666666")
	ErrorColor   = lipgloss.Color("#ffffff")
	WarningColor = lipgloss.Color("#888888")
)

var (
	checkMark = lipgloss.NewStyle().Foreground(Secondary).SetString("[OK]")
	failMark  = lipgloss.NewStyle().Foreground(ErrorColor).SetString("[FAIL]")
	skipMark  = lipgloss.NewStyle().Foreground(WarningColor).SetString("[SKIP]")
)

// asciiHeaderLines is the block banner; the canvas adds two padding rows and a
// status line, which is the 11-row header the spec pins.
var asciiHeaderLines = []string{
	" ████░░░██   ██░ ████░░░ ░█████░ ██░░░░░ ░█████░ ░█████░██░░░██",
	"███████░██░░░██░███████░███████░ ██░░░░░███████░███████░██░░██░",
	"███░   ░ ██░██░░███░   ░██░    ░ ██░░░░░██░░░████░    ░██░██░░",
	" ████░░░  ███░░░ ████░░░██░      ██░░░░░██░░░████░     ████░░░",
	" ░█████░  ██░░░░ ░█████░██░      ██░░░░░██░░░████░     ████░░░",
	"     ██  ██░░░░     █████████░ ██░░░░░███████░███████░██░██░░",
	"███████░  ██░░░░███████░ ░█████░ ███████ ░█████░ ░█████░██░░██░",
	" ░█████░  ██░░░░ ░█████░ ░░███░░ ███████ ░███░░ ░░███░░██░░░██",
}

// bannerRows is the spec's header height: eight art rows plus padding, and
// the canvas renders exactly this many rows whatever the window does.
const bannerRows = 11

type step int

const (
	stepWelcome step = iota
	stepReview
	stepInstalling
	stepComplete
	stepFailed
)

const (
	exitOK        = 0
	exitFailed    = 1
	exitCancelled = 130
)

type tickMsg time.Time

type runDoneMsg struct{ err error }

type taskUpdateMsg struct{}

func tickCmd() tea.Cmd {
	return tea.Tick(50*time.Millisecond, func(t time.Time) tea.Msg { return tickMsg(t) })
}

type model struct {
	opts      options
	log       *logger
	beams     *BeamsTextEffect
	spinner   spinner.Model
	step      step
	showHelp  bool
	width     int
	height    int
	run       *runner
	updates   chan taskUpdateMsg
	current   int // index of the running task, -1 when none
	exitCode  int
	cancelled bool
}

func newModel(opts options) model {
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = lipgloss.NewStyle().Foreground(Secondary)
	return model{
		opts:    opts,
		beams:   NewBeamsTextEffect(80, bannerRows, strings.Join(asciiHeaderLines, "\n")),
		spinner: sp,
		current: -1,
	}
}

func (m model) Init() tea.Cmd { return tickCmd() }

func (m model) Update(msg tea.Msg) (model, tea.Cmd) { return m.update(msg) }

// update is Update without the interface, so tests read model fields directly.
func (m model) update(msg tea.Msg) (model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.beams.Resize(m.width, bannerRows)
		return m, nil
	case tea.KeyMsg:
		return m.key(msg)
	case tickMsg:
		return m.tick()
	case taskUpdateMsg:
		m.refresh()
		return m, m.waitUpdate()
	case runDoneMsg:
		return m.runDone(msg)
	}
	return m, nil
}

// stepBeams advances the canvas one frame as a tea.Cmd.
func (m model) stepBeams() tea.Cmd {
	return func() tea.Msg {
		m.beams.Update()
		return nil
	}
}

func (m model) tick() (model, tea.Cmd) {
	if m.step == stepInstalling {
		m.refresh()
		return m, tea.Batch(m.stepBeams(), m.spinner.Tick, tickCmd())
	}
	return m, tea.Batch(m.stepBeams(), tickCmd())
}

func (m model) key(k tea.KeyMsg) (model, tea.Cmd) {
	if k.String() == "?" && m.step != stepInstalling {
		m.showHelp = !m.showHelp
		return m, nil
	}
	switch m.step {
	case stepWelcome:
		switch k.String() {
		case "up", "k":
			m.opts.uninstall = false
		case "down", "j":
			m.opts.uninstall = true
		case "enter":
			m.step = stepReview
		case "q", "ctrl+c":
			return m.quit(exitCancelled)
		}
	case stepReview:
		switch k.String() {
		case "enter":
			return m.start()
		case "esc":
			m.step = stepWelcome
		case "q", "ctrl+c":
			return m.quit(exitCancelled)
		}
	case stepInstalling:
		// Only the build is interruptible; every other task is writing files
		// or probing, and a half-done rename is worse than a wait.
		if k.String() == "ctrl+c" && m.cancellable() {
			m.run.cancel()
		}
	case stepComplete:
		switch k.String() {
		case "enter", "q", "ctrl+c":
			return m.quit(exitOK)
		}
	case stepFailed:
		switch k.String() {
		case "enter", "q", "ctrl+c":
			return m.quit(m.exitCode)
		}
	}
	return m, nil
}

func (m model) quit(code int) (model, tea.Cmd) {
	m.exitCode = code
	return m, tea.Quit
}

// start hands the whole task sequence to one tea.Cmd goroutine; the update
// loop only reads runner snapshots, which is what keeps the UI race-free.
func (m model) start() (model, tea.Cmd) {
	m.step = stepInstalling
	m.run = newRunner(m.opts, m.log)
	m.run.minTask = 200 * time.Millisecond
	m.updates = make(chan taskUpdateMsg, 64)
	return m, tea.Batch(m.waitUpdate(), runTasks(m.run, m.updates), tickCmd())
}

func (m model) waitUpdate() tea.Cmd {
	return func() tea.Msg { return <-m.updates }
}

func runTasks(r *runner, updates chan taskUpdateMsg) tea.Cmd {
	report := func(int, taskStatus) {
		select {
		case updates <- taskUpdateMsg{}:
		default: // a dropped hint still redraws on the next tick
		}
	}
	return func() tea.Msg { return runDoneMsg{err: r.runAll(r.ctx, report)} }
}

// refresh copies the running-task index out of the runner snapshot so the
// cancel rule never reads runner state directly.
func (m *model) refresh() {
	if m.run == nil {
		return
	}
	st := m.run.snapshot()
	m.current = -1
	for i, s := range st.status {
		if s == statusRunning {
			m.current = i
		}
	}
}

func (m *model) markTaskRunning(i int) { m.current = i }

// cancellable is true before the build starts and while it runs: nothing is
// under the prefix yet. After the build every task writes, so cancelling
// mid-task is refused rather than left half done.
func (m model) cancellable() bool {
	if m.step != stepInstalling || m.run == nil {
		return false
	}
	return m.current < 0 || m.run.taskIs(m.current, "Build sysc-lock")
}

func (m model) runDone(msg runDoneMsg) (model, tea.Cmd) {
	switch {
	case msg.err == nil:
		m.step = stepComplete
		m.exitCode = exitOK
	case errors.Is(msg.err, errCancelled):
		m.step = stepFailed
		m.cancelled = true
		m.exitCode = exitCancelled
	default:
		m.step = stepFailed
		m.cancelled = false
		m.exitCode = exitFailed
	}
	return m, nil
}

func (m model) helpText() string {
	switch m.step {
	case stepWelcome:
		return "↑/↓: Navigate  •  Enter: Continue  •  Ctrl+C: Quit"
	case stepReview:
		if m.opts.uninstall {
			return "Enter: Uninstall  •  Esc: Back  •  Ctrl+C: Quit"
		}
		return "Enter: Install  •  Esc: Back  •  Ctrl+C: Quit"
	case stepInstalling:
		if m.cancellable() {
			return "Ctrl+C: Cancel build"
		}
		return "Writing files…"
	default:
		return "Enter: Exit  •  Ctrl+C: Quit"
	}
}

func bannerLines(m model) []string { return strings.Split(m.beams.Render(), "\n") }

// View is replaced by view.go with the full greet layout.
func (m model) View() string { return strings.Join(bannerLines(m), "\n") }

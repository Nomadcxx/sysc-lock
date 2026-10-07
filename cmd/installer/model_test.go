package main

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func newTestModel(t *testing.T) model {
	t.Helper()
	m := newModel(testOpts(t))
	m.update(tea.WindowSizeMsg{Width: 100, Height: 30})
	return m
}

func TestWelcomeEnterGoesToReview(t *testing.T) {
	m := newTestModel(t)
	if m.step != stepWelcome {
		t.Fatalf("fresh model step=%v, want welcome", m.step)
	}
	m, _ = m.update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.step != stepReview {
		t.Fatalf("step=%v, want review", m.step)
	}
}

func TestWelcomeArrowsSelectUninstall(t *testing.T) {
	m := newTestModel(t)
	m, _ = m.update(tea.KeyMsg{Type: tea.KeyDown})
	if !m.opts.uninstall {
		t.Fatal("down must select the uninstall row")
	}
	m, _ = m.update(tea.KeyMsg{Type: tea.KeyUp})
	if m.opts.uninstall {
		t.Fatal("up must return to the install row")
	}
}

func TestWelcomeQuitCodes(t *testing.T) {
	m := newTestModel(t)
	var cmd tea.Cmd
	m, cmd = m.update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	if m.exitCode != exitCancelled || cmd == nil {
		t.Fatalf("exitCode=%d cmd=%v, want 130 and a quit command", m.exitCode, cmd)
	}
	m = newTestModel(t)
	m, cmd = m.update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if m.exitCode != exitCancelled || cmd == nil {
		t.Fatalf("exitCode=%d cmd=%v, want 130 and a quit command", m.exitCode, cmd)
	}
}

func TestReviewEnterStartsInstalling(t *testing.T) {
	m := newTestModel(t)
	m, _ = m.update(tea.KeyMsg{Type: tea.KeyEnter})
	m, cmd := m.update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.step != stepInstalling {
		t.Fatalf("step=%v, want installing", m.step)
	}
	if cmd == nil {
		t.Fatal("starting the run must return the run command")
	}
	if m.run == nil {
		t.Fatal("the runner must exist once installing")
	}
}

func TestReviewEscGoesBackAndQuitIs130(t *testing.T) {
	m := newTestModel(t)
	m, _ = m.update(tea.KeyMsg{Type: tea.KeyEnter})
	m, cmd := m.update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.step != stepWelcome {
		t.Fatalf("step=%v, want welcome", m.step)
	}
	if cmd != nil {
		t.Fatal("esc from review must not quit")
	}
	m, _ = m.update(tea.KeyMsg{Type: tea.KeyEnter})
	m, cmd = m.update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	if m.exitCode != exitCancelled || cmd == nil {
		t.Fatalf("exitCode=%d cmd=%v, want 130 and a quit command", m.exitCode, cmd)
	}
}

func TestCancelOnlyDuringBuild(t *testing.T) {
	m := newTestModel(t)
	m, _ = m.update(tea.KeyMsg{Type: tea.KeyEnter})
	m, _ = m.update(tea.KeyMsg{Type: tea.KeyEnter})
	if !m.cancellable() {
		t.Fatal("the build task must accept Ctrl+C")
	}
	m, _ = m.update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if !m.run.cancelled() {
		t.Fatal("Ctrl+C during Build must cancel")
	}
	m.step = stepInstalling
	m.markTaskRunning(2) // the build task itself
	if !m.cancellable() {
		t.Fatal("the build task must still accept Ctrl+C")
	}
	m.markTaskRunning(5) // Install binary, a task that writes
	if m.cancellable() {
		t.Fatal("Ctrl+C after Build must not cancel")
	}
	m, _ = m.update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if m.step != stepInstalling {
		t.Fatalf("step=%v, want the run to continue", m.step)
	}
}

func TestQIgnoredWhileInstalling(t *testing.T) {
	m := newTestModel(t)
	m, _ = m.update(tea.KeyMsg{Type: tea.KeyEnter})
	m, _ = m.update(tea.KeyMsg{Type: tea.KeyEnter})
	_, cmd := m.update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	if cmd != nil {
		t.Fatal("q must be ignored while installing")
	}
	if m.exitCode != 0 {
		t.Fatalf("exitCode=%d, want untouched", m.exitCode)
	}
}

func TestRunDoneCompleteAndFailed(t *testing.T) {
	m := newTestModel(t)
	m, _ = m.update(tea.KeyMsg{Type: tea.KeyEnter})
	m, _ = m.update(tea.KeyMsg{Type: tea.KeyEnter})
	m, _ = m.runDone(runDoneMsg{})
	if m.step != stepComplete || m.exitCode != exitOK {
		t.Fatalf("step=%v exit=%d, want complete/0", m.step, m.exitCode)
	}
	var cmd tea.Cmd
	m, cmd = m.update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil || m.exitCode != exitOK {
		t.Fatalf("cmd=%v exit=%d, want quit with 0", cmd, m.exitCode)
	}

	m = newTestModel(t)
	m, _ = m.update(tea.KeyMsg{Type: tea.KeyEnter})
	m, _ = m.update(tea.KeyMsg{Type: tea.KeyEnter})
	m, _ = m.runDone(runDoneMsg{err: errCancelled})
	if m.step != stepFailed || !m.cancelled {
		t.Fatalf("step=%v cancelled=%v, want failed/true", m.step, m.cancelled)
	}
	if m.exitCode != exitCancelled {
		t.Fatalf("exit=%d, want 130", m.exitCode)
	}
	m, _ = m.runDone(runDoneMsg{err: errTestFailure})
	if m.step != stepFailed || m.cancelled || m.exitCode != exitFailed {
		t.Fatalf("step=%v cancelled=%v exit=%d, want failed/false/1", m.step, m.cancelled, m.exitCode)
	}
}

func TestFailedStepQuitIsOne(t *testing.T) {
	m := newTestModel(t)
	m, _ = m.update(tea.KeyMsg{Type: tea.KeyEnter})
	m, _ = m.update(tea.KeyMsg{Type: tea.KeyEnter})
	m, _ = m.runDone(runDoneMsg{err: errTestFailure})
	var cmd tea.Cmd
	m, cmd = m.update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	if cmd == nil || m.exitCode != exitFailed {
		t.Fatalf("cmd=%v exit=%d, want quit with 1", cmd, m.exitCode)
	}
}

func TestResizeKeepsBannerElevenRows(t *testing.T) {
	m := newTestModel(t)
	m, _ = m.update(tea.WindowSizeMsg{Width: 100, Height: 12})
	if got := len(bannerLines(m)); got != 11 {
		t.Fatalf("banner rows=%d, want 11 at height 12", got)
	}
	m, _ = m.update(tea.WindowSizeMsg{Width: 120, Height: 40})
	if got := len(bannerLines(m)); got != 11 {
		t.Fatalf("banner rows=%d, want 11 at height 40", got)
	}
}

func TestHelpToggle(t *testing.T) {
	m := newTestModel(t)
	if m.showHelp {
		t.Fatal("help starts hidden")
	}
	m, _ = m.update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("?")})
	if !m.showHelp {
		t.Fatal("? must reveal the help")
	}
}

func TestHelpTextMatchesTheKeysThatWork(t *testing.T) {
	m := newTestModel(t)
	if got := m.helpText(); !strings.Contains(got, "Ctrl+C: Quit") {
		t.Fatalf("welcome help=%q", got)
	}
	m, _ = m.update(tea.KeyMsg{Type: tea.KeyEnter})
	if got := m.helpText(); strings.Contains(got, "↑/↓") {
		t.Fatalf("review help must not advertise arrows: %q", got)
	}
	m, _ = m.update(tea.KeyMsg{Type: tea.KeyEnter})
	m.markTaskRunning(2)
	if got := m.helpText(); got != "Ctrl+C: Cancel build" {
		t.Fatalf("installing help=%q, want the cancel hint", got)
	}
	m.markTaskRunning(5)
	if got := m.helpText(); got != "Writing files…" {
		t.Fatalf("installing help=%q, want the writing hint", got)
	}
}

var errTestFailure = &testFailure{}

type testFailure struct{}

func (*testFailure) Error() string { return "test failure" }

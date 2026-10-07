package main

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// boxBg matches the banner canvas so the frame reads as one surface.
const boxBg = lipgloss.Color("#1a1a1a")

// tooSmall is the spec's floor: keys keep working below it.
const tooSmall = "Enlarge the terminal to at least 60×16"

// artWidth is the widest banner row. The canvas clips instead of wrapping, so
// the banner is only drawn when every column fits.
func artWidth() int {
	w := 0
	for _, l := range asciiHeaderLines {
		if n := lipgloss.Width(l); n > w {
			w = n
		}
	}
	return w
}

func (m model) bannerFits() bool { return m.width >= artWidth() }

func (m model) title() string {
	if m.opts.uninstall {
		return "sysc-lock uninstaller"
	}
	return "sysc-lock installer"
}

func note(s string) string {
	return lipgloss.NewStyle().Italic(true).Foreground(FgMuted).Render(s)
}

func muted(s string) string {
	return lipgloss.NewStyle().Foreground(FgMuted).Render(s)
}

func primary(s string) string {
	return lipgloss.NewStyle().Foreground(FgPrimary).Render(s)
}

func secondary(s string) string {
	return lipgloss.NewStyle().Foreground(Secondary).Render(s)
}

func bold(s string) string {
	return lipgloss.NewStyle().Bold(true).Foreground(FgPrimary).Render(s)
}

func (m model) centred(s string) string {
	return lipgloss.NewStyle().Width(m.width).Align(lipgloss.Center).Render(s)
}

func (m model) box(content string, pad int) string {
	s := lipgloss.NewStyle().Background(boxBg).Border(lipgloss.RoundedBorder()).Padding(pad, 2)
	if w := m.width - 4; w > 8 {
		s = s.Width(w)
	}
	return s.Render(content)
}

func (m model) banner() string {
	return lipgloss.PlaceHorizontal(m.width, lipgloss.Center, strings.Join(bannerLines(m), "\n"))
}

func (m model) helpLine() string { return m.centred(note(m.helpText())) }

func (m model) installedBinary() string { return m.opts.prefix + "/bin/sysc-lock" }

func (m model) installedUnit() string {
	return m.opts.prefix + "/share/systemd/user/sysc-lock-session.service"
}

func (m model) logPath() string {
	if m.log != nil {
		return m.log.Path()
	}
	return m.opts.logPath
}

// trunc keeps a failure line inside the box: the prefix counts against the
// inner width, and the full text stays in the log.
func (m model) trunc(s string, prefix int) string {
	if s == "" {
		return ""
	}
	w := m.width - 8 - prefix
	if w < 16 {
		w = 16
	}
	if lipgloss.Width(s) <= w {
		return s
	}
	return ansi.Truncate(s, w, "…")
}

func (m model) welcomeBody() string {
	sel, selDesc := "▸ Install sysc-lock", "    Builds sysc-lock and installs the binary and user unit"
	other, otherDesc := "  Uninstall sysc-lock", "    Removes the binary and user unit"
	if m.opts.uninstall {
		sel, selDesc, other, otherDesc = other, otherDesc, sel, selDesc
	}
	return strings.Join([]string{
		"Select an option:",
		"",
		sel,
		selDesc,
		"",
		other,
		otherDesc,
		"",
		note("Runs as you. Writes only under the prefix; never enables services or edits PAM."),
	}, "\n")
}

func (m model) reviewBody() string {
	verb, list := "Install into", "Will write:"
	if m.opts.uninstall {
		verb, list = "Uninstall from", "Will remove:"
	}
	source := "Source: build ./cmd/sysc-lock in " + m.opts.root
	if m.opts.candidate != "" {
		source = "Source: " + m.opts.candidate
	}
	return strings.Join([]string{
		bold(verb + " " + m.opts.prefix),
		source,
		"",
		list,
		"  " + m.installedBinary(),
		"  " + m.installedUnit(),
		"",
		note("Change with --prefix PATH. Nothing is enabled or started; PAM and config are untouched."),
	}, "\n")
}

func (m model) completeBody() string {
	var rows []string
	if m.opts.uninstall {
		rows = append(rows, bold("Uninstall complete."), "sysc-lock has been removed.", "")
	} else {
		rows = append(rows,
			bold("Installation complete."),
			"Binary: "+m.installedBinary(),
			"Unit:   "+m.installedUnit(),
			"",
			note("Nothing was enabled or started. Activate the unit in a coordinated Niri session (README → Usage)."),
			"")
	}
	for _, n := range m.notes() {
		rows = append(rows, note(n))
	}
	if len(m.notes()) > 0 {
		rows = append(rows, "")
	}
	return strings.Join(append(rows, note(">see you space cowboy"), "", "Press Enter to exit"), "\n")
}

func (m model) failedSummary() string {
	what := "Installation failed."
	if m.opts.uninstall {
		what = "Uninstall failed."
	}
	if m.cancelled {
		what = "Installation cancelled."
	}
	return strings.Join([]string{bold(what), "Full log: " + m.logPath(), "", "Press Enter to exit"}, "\n")
}

func (m model) failedBody() string {
	rows := append(m.taskRows(), "")
	rows = append(rows, m.failedSummary())
	return strings.Join(rows, "\n")
}

func (m model) notes() []string {
	if m.run == nil {
		return nil
	}
	return m.run.snapshot().notes
}

// taskRows renders the runner snapshot: greet's marks, sub-step prefixes and a
// single error block under the row that failed.
func (m model) taskRows() []string {
	if m.run == nil {
		return nil
	}
	st := m.run.snapshot()
	var rows []string
	for i, t := range m.run.tasks {
		switch st.status[i] {
		case statusRunning:
			rows = append(rows, secondary(m.spinner.View()+" "+t.description))
		case statusDone:
			rows = append(rows, checkMark.Render()+" "+primary(t.name))
		case statusSkipped:
			rows = append(rows, skipMark.Render()+" "+secondary(t.name))
		case statusFailed:
			rows = append(rows, failMark.Render()+" "+primary(t.name))
			if i == st.failedIdx {
				rows = append(rows,
					"  └─ Error: "+m.trunc(st.errDetail, lipgloss.Width("  └─ Error: ")),
					"  └─ Command: "+m.trunc(st.cmdLine, lipgloss.Width("  └─ Command: ")))
			}
		default:
			rows = append(rows, muted("  "+t.name))
		}
		for j, sub := range t.sub {
			s := statusPending
			if j < len(st.sub[i]) {
				s = st.sub[i][j]
			}
			rows = append(rows, m.subRow(s, j == len(t.sub)-1, sub))
		}
	}
	return rows
}

func (m model) subRow(s taskStatus, last bool, name string) string {
	branch := "  ├─ "
	if last {
		branch = "  └─ "
	}
	switch s {
	case statusRunning:
		return secondary(branch + m.spinner.View() + " " + name)
	case statusDone:
		return secondary(branch + name)
	case statusFailed:
		return primary(branch + name)
	default:
		return muted(branch + name)
	}
}

func (m model) body() string {
	switch m.step {
	case stepWelcome:
		return m.welcomeBody()
	case stepReview:
		return m.reviewBody()
	case stepInstalling:
		return strings.Join(m.taskRows(), "\n")
	case stepComplete:
		return m.completeBody()
	case stepFailed:
		return m.failedBody()
	}
	return ""
}

// View lays out the greet frame: banner, centred title, rounded body box and
// the help footer. The cascade below trades decoration for fit, and ends at
// the minimum-size message while every key keeps working.
func (m model) View() string {
	help := m.helpLine()
	if m.width < 60 || m.height < 16 {
		return m.centred(note(tooSmall)) + "\n" + help
	}
	frame := func(banner, pad, spacer int) []string {
		var rows []string
		if banner == 1 {
			rows = append(rows, m.banner(), "")
		}
		rows = append(rows, m.centred(bold(m.title())))
		for i := 0; i < spacer; i++ {
			rows = append(rows, "")
		}
		rows = append(rows, m.box(m.body(), pad), "", help)
		return rows
	}
	height := func(rows []string) int { return lipgloss.Height(strings.Join(rows, "\n")) }

	// The banner is drawn only when every column fits; below that width the
	// frame drops it rather than clipping glyphs.
	banner := 0
	if m.bannerFits() {
		banner = 1
	}
	rows := frame(banner, 1, 2)
	if height(rows) > m.height {
		rows = frame(0, 0, 0)
	}
	if height(rows) > m.height {
		rows = []string{m.centred(bold(m.title())), m.trimmedBody(), "", help}
	}
	if height(rows) > m.height {
		rows = []string{m.centred(note(tooSmall)), help}
	}
	return strings.Join(rows, "\n")
}

// trimmedBody drops the task list when the terminal cannot hold it: the
// outcome lines and the log path are what the operator needs, and the log
// keeps everything.
func (m model) trimmedBody() string {
	if m.step == stepFailed {
		return m.failedSummary()
	}
	return m.body()
}

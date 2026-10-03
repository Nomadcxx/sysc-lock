// Package input holds the lock-screen password entry state: append/backspace/
// clear, masking for display, and buffer zeroing on clear.
package input

type Model struct {
	Pass []rune
}

func (m *Model) Append(text string) {
	for _, r := range text {
		m.Pass = append(m.Pass, r)
	}
}

func (m *Model) Backspace() {
	if len(m.Pass) > 0 {
		m.Pass = m.Pass[:len(m.Pass)-1]
	}
}

// Clear wipes the buffer (zeroing the rune memory, like auth.Zero for bytes).
func (m *Model) Clear() {
	for i := range m.Pass {
		m.Pass[i] = 0
	}
	m.Pass = m.Pass[:0]
}

// Password returns a copy of the current buffer as text (for PAM prompts).
func (m *Model) Password() string {
	return string(m.Pass)
}

// Mask returns the display string: one dot per character.
func (m *Model) Mask() string {
	if len(m.Pass) == 0 {
		return ""
	}
	out := make([]rune, len(m.Pass))
	for i := range out {
		out[i] = '●'
	}
	return string(out)
}

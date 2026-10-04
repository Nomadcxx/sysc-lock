// Package input holds the lock-screen password entry state: append/backspace/
// clear, masking for display, and buffer zeroing on clear.
package input

import (
	"errors"
	"unicode/utf8"
)

const MaxPasswordBytes = 4096

var ErrLength = errors.New("Password exceeds 4096 bytes")
var ErrEncoding = errors.New("Invalid text input")

type Model struct {
	Pass  []rune
	bytes int
}

func (m *Model) Append(text string) error {
	if !utf8.ValidString(text) {
		return ErrEncoding
	}
	if len(text) > MaxPasswordBytes-m.bytes {
		return ErrLength
	}
	if text == "" {
		return nil
	}
	// ponytail: one fixed 16 KiB rune allocation avoids secret copies during growth.
	if m.Pass == nil {
		m.Pass = make([]rune, 0, MaxPasswordBytes)
	}
	for _, r := range text {
		m.Pass = append(m.Pass, r)
	}
	m.bytes += len(text)
	return nil
}

func (m *Model) Backspace() {
	if n := len(m.Pass); n > 0 {
		m.bytes -= utf8.RuneLen(m.Pass[n-1])
		m.Pass[n-1] = 0
		m.Pass = m.Pass[:n-1]
	}
}

// Clear wipes deleted slots and retained capacity, as well as the live entry.
func (m *Model) Clear() {
	clear(m.Pass[:cap(m.Pass)])
	m.Pass = m.Pass[:0]
	m.bytes = 0
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

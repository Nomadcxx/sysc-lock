package lockd

import (
	"strings"
	"testing"

	"github.com/Nomadcxx/sysc-lock/internal/input"
)

func TestPickMimePrefersUTF8Plain(t *testing.T) {
	if got := pickMime([]string{"image/png", "text/plain", "UTF8_STRING"}); got != "text/plain" {
		t.Fatalf("got %q", got)
	}
	if pickMime([]string{"image/png"}) != "" {
		t.Fatal("must ignore non-text")
	}
}

func TestReadPasteBoundsAndUTF8(t *testing.T) {
	got, err := readPaste(strings.NewReader("secret"))
	if err != nil || got != "secret" {
		t.Fatalf("got %q %v", got, err)
	}
	if _, err := readPaste(strings.NewReader(strings.Repeat("a", input.MaxPasswordBytes+1))); err != input.ErrLength {
		t.Fatalf("oversize: %v", err)
	}
	if _, err := readPaste(strings.NewReader("\xff")); err != input.ErrEncoding {
		t.Fatalf("junk: %v", err)
	}
}

func TestPasteChord(t *testing.T) {
	if !pasteChord(true, false, 'v') || !pasteChord(true, true, 'V') {
		t.Fatal("Ctrl+V")
	}
	if !pasteChord(false, true, symInsert) {
		t.Fatal("Shift+Insert")
	}
	if pasteChord(false, false, 'v') || pasteChord(true, false, 'c') {
		t.Fatal("must not steal ordinary keys")
	}
}

package lockd

import "testing"

func TestPickTextMimePrefersUtf8Plain(t *testing.T) {
	if got := pickTextMime([]string{"image/png", "text/plain", "text/plain;charset=utf-8"}); got != "text/plain;charset=utf-8" {
		t.Fatalf("got %q", got)
	}
	if got := pickTextMime([]string{"image/png", "UTF8_STRING", "text/uri-list"}); got != "UTF8_STRING" {
		t.Fatalf("got %q", got)
	}
	if got := pickTextMime([]string{"image/png"}); got != "" {
		t.Fatalf("got %q", got)
	}
}

func TestSanitizePasteDropsNewlines(t *testing.T) {
	// A pasted newline must never reach PAM as an Enter.
	if got := sanitizePaste("pw\n1\txy\x07z"); got != "pw1xyz" {
		t.Fatalf("got %q", got)
	}
	if got := sanitizePaste("ünïcødé ✓"); got != "ünïcødé ✓" {
		t.Fatalf("got %q", got)
	}
	// NBSP must not silently shift characters.
	if got := sanitizePaste("a\u00a0 b"); got != "a  b" {
		t.Fatalf("got %q", got)
	}
}

func TestPasteWithoutClipboardIsNoop(t *testing.T) {
	if got := (&Client{}).Paste(); got != "" {
		t.Fatalf("got %q", got)
	}
}

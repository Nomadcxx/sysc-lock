package lockd

import "testing"

func TestHasGlyphGoMono(t *testing.T) {
	// Force the embedded font so the check does not depend on a system Fira Code.
	oldPaths, oldParsed := fontPaths, parsed
	fontPaths, parsed = nil, nil
	t.Cleanup(func() {
		fontPaths, parsed = oldPaths, oldParsed
	})

	if !HasGlyph('П') {
		t.Fatal("embedded Go Mono must draw Cyrillic 'П'")
	}
	if HasGlyph('パ') {
		t.Fatal("embedded Go Mono must not claim kana 'パ'")
	}
	if HasGlyph('비') {
		t.Fatal("embedded Go Mono must not claim hangul '비'")
	}
}

func TestCoveredMatchesHasGlyph(t *testing.T) {
	oldPaths, oldParsed := fontPaths, parsed
	fontPaths, parsed = nil, nil
	t.Cleanup(func() {
		fontPaths, parsed = oldPaths, oldParsed
	})

	for _, r := range []rune{'a', 'П', '…', 'パ', '비'} {
		if Covered(r) != HasGlyph(r) {
			t.Fatalf("Covered(%q) and HasGlyph(%q) disagree", r, r)
		}
	}
}

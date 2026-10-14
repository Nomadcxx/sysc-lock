package lockd

import (
	"testing"

	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gomono"
	"golang.org/x/image/font/opentype"
)

// pinEmbeddedFont swaps in the embedded Go Mono so coverage assertions do not
// depend on which Fira Code the test host happens to ship.
func pinEmbeddedFont(t *testing.T) {
	t.Helper()
	f, err := opentype.Parse(gomono.TTF)
	if err != nil {
		t.Fatal(err)
	}
	oldParsed, oldCache := parsed, faceCach
	parsed, faceCach = f, map[int]font.Face{}
	t.Cleanup(func() {
		for _, c := range faceCach {
			_ = c.Close()
		}
		parsed, faceCach = oldParsed, oldCache
	})
}

// TestCoveredReportsMissingGlyphs is the premise of the i18n guard: the only
// faces this package loads draw Latin and Cyrillic but no CJK.
func TestCoveredReportsMissingGlyphs(t *testing.T) {
	pinEmbeddedFont(t)
	for _, r := range []rune{'a', 'П', '•', '↑', '…'} {
		if !Covered(r) {
			t.Errorf("Covered(%U) = false, want true", r)
		}
	}
	for _, r := range []rune{'パ', '비', '密'} {
		if Covered(r) {
			t.Errorf("Covered(%U) = true, want false", r)
		}
	}
}

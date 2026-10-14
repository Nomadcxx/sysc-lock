package main

import (
	"testing"

	"github.com/Nomadcxx/sysc-lock/internal/lockd"
)

// isolateLocale keeps the real ~/.config/sysc-shell/config.json out of the
// environment-driven resolution, like the i18n precedence tests.
func isolateLocale(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("LC_ALL", "")
	t.Setenv("LC_MESSAGES", "")
}

func TestResolveLocaleFallsBackWhenCatalogHasNoGlyphs(t *testing.T) {
	isolateLocale(t)
	t.Setenv("LANG", "ja_JP.UTF-8")

	// A font with Latin and Cyrillic coverage but no kana, like Fira Code and
	// Go Mono: the ja session must read English, not missing-glyph boxes.
	has := func(r rune) bool { return r <= 0x2FFF }
	if got := resolveLocale(has); got != "en" {
		t.Fatalf("ja without kana coverage: got %q, want en", got)
	}

	// Cyrillic coverage keeps ru translated.
	t.Setenv("LANG", "ru_RU.UTF-8")
	if got := resolveLocale(has); got != "ru" {
		t.Fatalf("ru with Cyrillic coverage: got %q, want ru", got)
	}
}

func TestResolveLocaleKeepsCoveredLocalesWithTheActiveFont(t *testing.T) {
	isolateLocale(t)
	for _, tc := range []struct {
		lang, want string
	}{
		{"es_MX.UTF-8", "es"},
		{"pt_BR.UTF-8", "pt"},
		{"ru_RU.UTF-8", "ru"},
	} {
		t.Setenv("LANG", tc.lang)
		if got := resolveLocale(lockd.HasGlyph); got != tc.want {
			t.Errorf("LANG=%s: got %q, want %q", tc.lang, got, tc.want)
		}
	}
}

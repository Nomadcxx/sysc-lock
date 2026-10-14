package i18n

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestMatch(t *testing.T) {
	for _, tc := range []struct{ tag, want string }{
		{"", ""}, {"C", ""}, {"POSIX", ""}, {"en", "en"}, {"en_US.UTF-8", "en"},
		{"es", "es"}, {"es_MX@variant", "es"}, {"pt-BR", "pt"}, {"JA", "ja"},
		{"ko_KR", "ko"}, {"ru", "ru"}, {"zh_CN", ""}, {"de", ""}, {"nonsense", ""},
	} {
		if got := Match(tc.tag); got != tc.want {
			t.Errorf("Match(%q) = %q, want %q", tc.tag, got, tc.want)
		}
	}
}

func TestResolvePrecedence(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("LC_ALL", "")
	t.Setenv("LC_MESSAGES", "")
	t.Setenv("LANG", "ja_JP.UTF-8")
	if got := Resolve(); got != "ja" {
		t.Fatalf("env LANG: got %q", got)
	}
	t.Setenv("LC_ALL", "ru")
	if got := Resolve(); got != "ru" {
		t.Fatalf("LC_ALL beats LANG: got %q", got)
	}
	if err := os.MkdirAll(filepath.Join(dir, "sysc-shell"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(dir, "sysc-shell", "config.json")
	if err := os.WriteFile(cfg, []byte(`{"session":{"language":"es"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := Resolve(); got != "es" {
		t.Fatalf("saved language beats env: got %q", got)
	}
	if err := os.WriteFile(cfg, []byte(`{"session":{"language":"de"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := Resolve(); got != "ru" {
		t.Fatalf("unsupported saved value falls to env: got %q", got)
	}
	if err := os.WriteFile(cfg, []byte(`{`), 0o644); err != nil { // malformed
		t.Fatal(err)
	}
	t.Setenv("LC_ALL", "")
	for _, tc := range []struct {
		name, all, messages, lang string
	}{
		{"LC_ALL C", "C", "ja", "ru"},
		{"LC_ALL POSIX", "POSIX", "ja", "ru"},
		{"LC_ALL unsupported", "de_DE.UTF-8", "ja", "ru"},
		{"LC_MESSAGES C", "", "C", "ru"},
		{"LC_MESSAGES POSIX", "", "POSIX", "ru"},
		{"LC_MESSAGES unsupported", "", "de_DE.UTF-8", "ru"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("LC_ALL", tc.all)
			t.Setenv("LC_MESSAGES", tc.messages)
			t.Setenv("LANG", tc.lang)
			if got := Resolve(); got != "en" {
				t.Fatalf("Resolve() = %q, want English for %s", got, tc.name)
			}
		})
	}
}

func TestT(t *testing.T) {
	if got := T("en", "Password:"); got != "Password:" {
		t.Fatalf("english identity: %q", got)
	}
	if got := T("es", "Password:"); got != "Contraseña:" {
		t.Fatalf("es: %q", got)
	}
	if got := T("ru", "Not in catalog"); got != "Not in catalog" {
		t.Fatalf("miss fallback: %q", got)
	}
}

func TestCatalogsAreConsistent(t *testing.T) {
	locales := []string{"es", "pt", "ja", "ko", "ru"}
	sets := map[string]map[string]string{}
	for _, loc := range locales {
		data, err := catalogFS.ReadFile("catalog/" + loc + ".json")
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]string
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatal(loc, err)
		}
		sets[loc] = m
	}
	base := sets["es"]
	for _, loc := range locales[1:] {
		m := sets[loc]
		if len(m) != len(base) {
			t.Fatalf("%s has %d keys, es has %d", loc, len(m), len(base))
		}
		for k := range base {
			v, ok := m[k]
			if !ok || v == "" {
				t.Fatalf("%s missing/empty %q", loc, k)
			}
		}
	}
}

// TestRenderableRejectsMissingRunes pins the guard that stops the lock screen
// offering a catalog the font cannot draw: a face without CJK must reject ja
// and ko, while the Latin-1 and Cyrillic catalogs stay renderable.
func TestRenderableRejectsMissingRunes(t *testing.T) {
	noCJK := func(r rune) bool { return r <= 0x2FFF }
	for _, tc := range []struct {
		locale string
		want   bool
	}{
		{"en", true}, {"es", true}, {"pt", true}, {"ru", true},
		{"ja", false}, {"ko", false},
		{"de", true}, // no catalog, nothing to draw
	} {
		if got := Renderable(tc.locale, noCJK); got != tc.want {
			t.Errorf("Renderable(%q) = %v, want %v", tc.locale, got, tc.want)
		}
	}
}

func TestRenderableIgnoresWhitespace(t *testing.T) {
	for _, r := range []rune{' ', '\t', '\n'} {
		if !Renderable("ru", func(g rune) bool { return g != r }) {
			t.Errorf("whitespace rune %U rejected", r)
		}
	}
}

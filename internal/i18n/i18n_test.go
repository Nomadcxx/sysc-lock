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
	t.Setenv("LANG", "C")
	if got := Resolve(); got != "en" {
		t.Fatalf("C locale: got %q", got)
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

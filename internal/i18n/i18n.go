// Package i18n translates the lock screen's own prompt strings into the
// session language. Catalogs are keyed by the exact English literal drawn on
// screen, so a missing translation falls back to the English on the screen,
// not to a lookup miss. English-as-key avoids touching the draw call sites'
// structure. The shell's saved session.language wins over the environment;
// anything unknown (including zh or C) stays English.
package i18n

import (
	"embed"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

var codes = []string{"en", "es", "pt", "ja", "ko", "ru"}

// Match normalizes an RFC 5646 style tag ("es_MX.UTF-8") to a supported base
// code, or "" when unsupported.
func Match(tag string) string {
	if tag == "" {
		return ""
	}
	base := strings.ToLower(tag)
	for i, c := range base {
		if c == '_' || c == '.' || c == '-' || c == '@' {
			base = base[:i]
			break
		}
	}
	for _, c := range codes {
		if base == c {
			return c
		}
	}
	return ""
}

// Resolve picks the lock screen's UI language: the shell's saved
// session.language first, then the standard locale environment, then English.
// It never mutates the environment.
func Resolve() string {
	if l := Match(shellConfigLanguage()); l != "" {
		return l
	}
	for _, key := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
		if l := Match(os.Getenv(key)); l != "" {
			return l
		}
	}
	return "en"
}

func shellConfigLanguage() string {
	base, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(base, "sysc-shell", "config.json"))
	if err != nil {
		return ""
	}
	var doc struct {
		Session struct {
			Language string `json:"language"`
		} `json:"session"`
	}
	if json.Unmarshal(data, &doc) != nil {
		return ""
	}
	return doc.Session.Language
}

//go:embed catalog/*.json
var catalogFS embed.FS

var (
	loadOnce sync.Once
	byLocale map[string]map[string]string
)

func load() {
	byLocale = map[string]map[string]string{}
	for _, locale := range codes[1:] { // en has no file: keys are English
		data, err := catalogFS.ReadFile("catalog/" + locale + ".json")
		if err != nil {
			continue
		}
		var m map[string]string
		if err := json.Unmarshal(data, &m); err != nil {
			panic("i18n: bad catalog " + locale + ": " + err.Error())
		}
		byLocale[locale] = m
	}
}

// T returns the translation of the exact English literal, or the literal
// itself for English, unknown locales and unmapped strings.
func T(locale, english string) string {
	loadOnce.Do(load)
	if m := byLocale[locale]; m != nil {
		if s, ok := m[english]; ok && s != "" {
			return s
		}
	}
	return english
}

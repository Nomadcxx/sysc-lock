package art

import (
	_ "embed"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unicode"
	"unicode/utf8"

	"github.com/mattn/go-runewidth"
)

const MaxHeaderBytes = 64 << 10

//go:embed headers.conf
var defaultHeaderConf string

type Header struct{ ID, Text string }

func DefaultHeaders() []Header {
	// ponytail: shipped artwork is validated by Header tests; no mutable cache.
	hs, _ := ParseHeaders(defaultHeaderConf)
	return hs
}

// ParseHeaders preserves literal artwork inside greet-style triple quotes.
func ParseHeaders(data string) ([]Header, error) {
	if len(data) > MaxHeaderBytes || !utf8.ValidString(data) {
		return nil, fmt.Errorf("header config exceeds byte budget or is not UTF-8")
	}
	var hs []Header
	var id string
	var rows []string
	seen := map[string]bool{}
	for lineNo, line := range strings.Split(strings.ReplaceAll(data, "\r\n", "\n"), "\n") {
		trimmed := strings.TrimSpace(line)
		if id != "" {
			if trimmed == `"""` {
				text := strings.Join(rows, "\n")
				if strings.TrimSpace(text) == "" {
					return nil, fmt.Errorf("empty header at line %d", lineNo+1)
				}
				hs = append(hs, Header{id, text})
				seen[id] = true
				id = ""
				rows = nil
				continue
			}
			line = strings.ReplaceAll(line, "\t", "    ")
			if len(rows) >= 16 || runewidth.StringWidth(line) > 120 {
				return nil, fmt.Errorf("header dimensions exceed 120 columns or 16 rows at line %d", lineNo+1)
			}
			for _, r := range line {
				if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
					return nil, fmt.Errorf("header contains control text at line %d", lineNo+1)
				}
			}
			rows = append(rows, line)
			continue
		}
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		key, value, ok := strings.Cut(trimmed, "=")
		key = strings.TrimSpace(key)
		if !ok || !strings.HasPrefix(key, "ascii_") || len(key) <= 6 || len(key) > 32 || strings.TrimSpace(value) != `"""` {
			return nil, fmt.Errorf("expected ascii_name=triple quotes at line %d", lineNo+1)
		}
		for _, r := range key {
			if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_') {
				return nil, fmt.Errorf("invalid header name at line %d", lineNo+1)
			}
		}
		if seen[key] || len(hs) >= 16 {
			return nil, fmt.Errorf("duplicate header or more than 16 variants at line %d", lineNo+1)
		}
		id = key
	}
	if id != "" || len(hs) == 0 {
		return nil, fmt.Errorf("header config has an unclosed block or no headers")
	}
	return hs, nil
}

// LoadHeaders cannot block on a special file or let broken decoration deny a lock.
func LoadHeaders(path string) ([]Header, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW, 0)
	if os.IsNotExist(err) {
		return DefaultHeaders(), nil
	}
	if err != nil {
		return DefaultHeaders(), err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return DefaultHeaders(), err
	}
	if !st.Mode().IsRegular() || st.Size() > MaxHeaderBytes {
		return DefaultHeaders(), fmt.Errorf("header config must be a bounded regular file")
	}
	data, err := io.ReadAll(io.LimitReader(f, MaxHeaderBytes+1))
	if err != nil {
		return DefaultHeaders(), err
	}
	hs, err := ParseHeaders(string(data))
	if err != nil {
		return DefaultHeaders(), err
	}
	return hs, nil
}

// SeedHeaders creates the editable catalogue on first acquisition. Linking a
// complete temporary file preserves existing files, including dangling links.
func SeedHeaders(path string) error {
	if path == "" {
		return fmt.Errorf("missing config directory")
	}
	if _, err := os.Lstat(path); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".headers-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if _, err = f.WriteString(defaultHeaderConf); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Link(f.Name(), path); os.IsExist(err) {
		return nil
	}
	return err
}

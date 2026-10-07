// Package theme reads the shell's palette file (sysc-shell PaletteFile JSON
// shape: {"name":..,"dark":{"surface":"#RRGGBB",..},"light":{..}}) and exposes
// the M3 roles the lock UI needs as colors. Missing/broken files yield the
// built-in dark defaults; a half-written file must never crash the locker.
package theme

import (
	"encoding/json"
	"io"
	"os"
	"regexp"
	"syscall"

	"image/color"

	"github.com/Nomadcxx/sysc-Go/animations"
)

// Palette carries the roles sysc-lock renders with. Ground/Banner/Accent/
// ClockInk/DateInk drive the greeter chrome and are overlaid from the same
// animations theme the background effects use (WithScheme); Surface and the
// rest keep the shell palette file as fallback.
type Palette struct {
	Surface, OnSurface, Error, Primary        color.NRGBA
	Ground, Banner, Accent, ClockInk, DateInk color.NRGBA
}

var hexRe = regexp.MustCompile(`^#([0-9a-fA-F]{6})$`)

func parseHex(s string) (color.NRGBA, bool) {
	m := hexRe.FindStringSubmatch(s)
	if m == nil {
		return color.NRGBA{}, false
	}
	var v [3]byte
	for i := 0; i < 3; i++ {
		hi, lo := m[1][i*2], m[1][i*2+1]
		n := hexVal(hi)<<4 | hexVal(lo)
		if n == 0xFF && (hi != 'f' && hi != 'F' || lo != 'f' && lo != 'F') {
			// hexVal returns 0xFF only for invalid digits
			return color.NRGBA{}, false
		}
		v[i] = n
	}
	return color.NRGBA{R: v[0], G: v[1], B: v[2], A: 0xFF}, true
}

func hexVal(b byte) byte {
	switch {
	case b >= '0' && b <= '9':
		return b - '0'
	case b >= 'a' && b <= 'f':
		return b - 'a' + 10
	case b >= 'A' && b <= 'F':
		return b - 'A' + 10
	}
	return 0xFF
}

// Default returns the built-in dark fallback palette. Chrome roles stay
// unset; the view falls back to its fixed ink floor until WithScheme loads
// the user's theme.
func Default() Palette {
	return Palette{
		Surface:   color.NRGBA{R: 0x10, G: 0x10, B: 0x14, A: 0xFF},
		OnSurface: color.NRGBA{R: 0xE2, G: 0xE2, B: 0xE9, A: 0xFF},
		Error:     color.NRGBA{R: 0xF2, G: 0xB8, B: 0xB5, A: 0xFF},
		Primary:   color.NRGBA{R: 0xB4, G: 0xC5, B: 0xFF, A: 0xFF},
	}
}

// WithScheme overlays the animations screensaver palette
// [background, ascii_primary, ascii_secondary, clock_primary, clock_secondary,
// date_color] so chrome and effects share one theme. Unknown names or bad
// colors keep the current palette.
func (p Palette) WithScheme(name string) Palette {
	stops := animations.GetScreensaverPalette(name)
	if len(stops) < 6 {
		return p
	}
	if c, ok := parseHex(stops[0]); ok {
		p.Surface, p.Ground = c, c
	}
	if c, ok := parseHex(stops[1]); ok {
		p.Banner = c
	}
	if c, ok := parseHex(stops[2]); ok {
		p.Accent = c
	}
	if c, ok := parseHex(stops[3]); ok {
		p.ClockInk = c
	}
	if c, ok := parseHex(stops[5]); ok {
		p.DateInk = c
	}
	return p
}

// Load reads the palette file at path. Absent or unparsable files return
// Default with nil error (the locker must always start).
func Load(path string) (Palette, error) {
	pal := Default()
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return pal, nil // ponytail: missing palette file = defaults, not an error
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() < 0 || info.Size() > 64<<10 {
		return pal, nil
	}
	data, err := io.ReadAll(io.LimitReader(f, (64<<10)+1))
	if err != nil || len(data) > 64<<10 {
		return pal, nil
	}
	var file struct {
		Dark map[string]string `json:"dark"`
	}
	if json.Unmarshal(data, &file) != nil || file.Dark == nil {
		return pal, nil // half-written / wrong shape: defaults
	}
	for role, dst := range map[string]*color.NRGBA{
		"surface":    &pal.Surface,
		"on-surface": &pal.OnSurface,
		"error":      &pal.Error,
		"primary":    &pal.Primary,
	} {
		if c, ok := parseHex(file.Dark[role]); ok {
			*dst = c
		}
	}
	return pal, nil
}

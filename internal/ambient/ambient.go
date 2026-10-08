// Package ambient owns the lock-screen ambient snapshot: the one-line status
// string (battery, link, media, weather), its atomic file, and the collectors
// that fill it. The locker owner only ever reads the file; every source and
// every network or bus call lives in the --ambient child process.
package ambient

import (
	"fmt"
	"math"
	"strings"
	"time"
	"unicode"
)

const (
	LinkWifi  = "wifi"
	LinkWired = "wired"
	Playing   = "playing"
	Paused    = "paused"
	Stopped   = "stopped"
	Sep       = " • "

	// Battery power states, normalized from sysfs "status".
	PowerCharging    = "charging"
	PowerDischarging = "discharging"
	PowerFull        = "full"
	PowerPlugged     = "plugged" // "Not charging": on AC, held below full
)

type Snapshot struct {
	AsOf       time.Time `json:"as_of"`
	BatteryPct *int      `json:"battery_pct,omitempty"`
	Power      string    `json:"power,omitempty"`
	Link       string    `json:"link,omitempty"`
	Media      string    `json:"media,omitempty"`
	Title      string    `json:"title,omitempty"`
	Artist     string    `json:"artist,omitempty"`
	Temp       *float64  `json:"temp,omitempty"`
	Unit       string    `json:"unit,omitempty"`
}

// BatteryBar renders the battery as a block gauge: [████░░░░░░] 82%.
func BatteryBar(pct int) string {
	filled := (pct + 5) / 10
	filled = max(0, min(10, filled))
	return "[" + strings.Repeat("█", filled) + strings.Repeat("░", 10-filled) + fmt.Sprintf("] %d%%", pct)
}

func (s Snapshot) Line(maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}
	var parts []string
	if s.BatteryPct != nil {
		parts = append(parts, BatteryBar(*s.BatteryPct))
	}
	switch s.Link {
	case LinkWifi:
		parts = append(parts, "Wi-Fi")
	case LinkWired:
		parts = append(parts, "Wired")
	}
	for len(parts) > 0 && len([]rune(strings.Join(parts, Sep))) > maxRunes {
		parts = parts[:len(parts)-1]
	}
	remaining := maxRunes - len([]rune(strings.Join(parts, Sep)))
	if len(parts) > 0 {
		remaining -= len([]rune(Sep))
	}
	if s.Media == Playing {
		if media := s.mediaLine(remaining); media != "" {
			parts = append(parts, media)
		}
	}
	if s.Temp != nil {
		candidate := append(parts, fmt.Sprintf("%.0f°", math.Round(*s.Temp)))
		if len([]rune(strings.Join(candidate, Sep))) <= maxRunes {
			parts = candidate
		}
	}
	return strings.Join(parts, Sep)
}

func (s Snapshot) mediaLine(width int) string {
	title, artist := cleanMetadata(s.Title), cleanMetadata(s.Artist)
	if title == "" {
		if width >= len(Playing) {
			return Playing
		}
		return ""
	}
	if artist != "" && len([]rune(title))+3+len([]rune(artist)) <= width {
		return title + " - " + artist
	}
	runes := []rune(title)
	if len(runes) <= width {
		return title
	}
	if width > 3 {
		return string(runes[:width-3]) + "..."
	}
	return ""
}

// cleanMetadata bounds untrusted player text at collection and file boundaries.
func cleanMetadata(text string) string {
	var out strings.Builder
	count, space := 0, false
	for _, r := range text {
		if unicode.IsSpace(r) {
			space = count > 0
			continue
		}
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			continue
		}
		if space {
			out.WriteByte(' ')
			count++
			space = false
		}
		if count == 128 {
			break
		}
		out.WriteRune(r)
		count++
		if count == 128 {
			break
		}
	}
	return strings.TrimSpace(out.String())
}

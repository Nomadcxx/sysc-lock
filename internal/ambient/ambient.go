// Package ambient owns the lock-screen ambient snapshot: battery, link, media
// and weather, its atomic file, the collectors that fill it, and the formatter
// that turns it into the status corner and caption. The locker owner only ever reads the file; every source and
// every network or bus call lives in the --ambient child process.
package ambient

import (
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

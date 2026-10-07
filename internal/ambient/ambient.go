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
)

const (
	LinkWifi  = "wifi"
	LinkWired = "wired"
	Playing   = "playing"
	Paused    = "paused"
	Stopped   = "stopped"
	Sep       = " • "
)

type Snapshot struct {
	AsOf       time.Time `json:"as_of"`
	BatteryPct *int      `json:"battery_pct,omitempty"`
	Charging   *bool     `json:"charging,omitempty"`
	Link       string    `json:"link,omitempty"`
	Media      string    `json:"media,omitempty"`
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
	if s.Media == Playing {
		parts = append(parts, Playing)
	}
	if s.Temp != nil {
		parts = append(parts, fmt.Sprintf("%.0f°", math.Round(*s.Temp)))
	}
	for len(parts) > 0 {
		line := strings.Join(parts, Sep)
		if maxRunes <= 0 || len([]rune(line)) <= maxRunes {
			return line
		}
		parts = parts[:len(parts)-1]
	}
	return ""
}

package ambient

import (
	"context"
	"fmt"
	"time"
)

// Run writes a fresh snapshot every tick until the context is cancelled. A
// failed write is dropped, not fatal: the owner already tolerates a missing
// file, and the next tick retries.
func Run(ctx context.Context, path string, tick time.Duration, gather func(time.Time) Snapshot) error {
	if path == "" {
		return fmt.Errorf("ambient path empty")
	}
	write := func() {
		s := gather(time.Now())
		s.AsOf = time.Now()
		_ = Write(path, s)
	}
	write()
	ticker := time.NewTicker(tick)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			write()
		}
	}
}

// NewGather returns the production snapshot collector over sysfs, procfs,
// the session bus, and the cached weather fetcher.
func NewGather() func(time.Time) Snapshot {
	weather := newWeatherFromConfig()
	return func(now time.Time) Snapshot {
		s := Snapshot{AsOf: now}
		if pct, charging, ok := readBattery(); ok {
			s.BatteryPct = &pct
			s.Charging = &charging
		}
		s.Link = readLink()
		s.Media = readMedia()
		if weather != nil {
			if temp, ok := weather.Get(now); ok {
				s.Temp = &temp
				s.Unit = weather.unit
			}
		}
		return s
	}
}

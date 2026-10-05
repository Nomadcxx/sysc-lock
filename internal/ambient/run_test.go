package ambient

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestRunWritesUntilCancel(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ambient.json")
	ctx, cancel := context.WithCancel(context.Background())
	writes := 0
	gather := func(now time.Time) Snapshot {
		writes++
		if writes == 2 {
			cancel()
		}
		pct := 82
		return Snapshot{AsOf: now, BatteryPct: &pct}
	}
	if err := Run(ctx, path, 5*time.Millisecond, gather); err != nil {
		t.Fatal(err)
	}
	if writes < 2 {
		t.Fatalf("writes=%d", writes)
	}
	s, err := Load(path, time.Now())
	if err != nil {
		t.Fatalf("last snapshot did not load: %v", err)
	}
	if s.BatteryPct == nil || *s.BatteryPct != 82 {
		t.Fatalf("got %+v", s)
	}
}

func TestRunEmptyPathErrors(t *testing.T) {
	if err := Run(context.Background(), "", time.Second, func(now time.Time) Snapshot {
		return Snapshot{AsOf: now}
	}); err == nil {
		t.Fatal("empty path must error")
	}
}

package inhibit

import (
	"testing"
	"time"
)

func TestSleepDelayLimitBounds(t *testing.T) {
	for _, us := range []uint64{0, 60000001, ^uint64(0)} {
		if _, err := delayLimit(us); err == nil {
			t.Fatalf("accepted %d us", us)
		}
	}
	for _, us := range []uint64{1, 5000000, 60000000} {
		got, err := delayLimit(us)
		if err != nil || got != time.Duration(us)*time.Microsecond {
			t.Fatal(got, err)
		}
	}
}

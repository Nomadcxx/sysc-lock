package art

import "time"

const (
	// PrintDuration caps the whole print reveal however many cells it has.
	PrintDuration = time.Second
	// JoltDuration is the length of the wrong-password shake.
	JoltDuration = 120 * time.Millisecond
)

// PrintLimit is how many of total cells have been revealed after elapsed.
func PrintLimit(elapsed time.Duration, total int) int {
	switch {
	case elapsed <= 0:
		return 0
	case elapsed >= PrintDuration:
		return total
	}
	return int(int64(total) * int64(elapsed) / int64(PrintDuration))
}

// Jolt is the horizontal shift in cells at elapsed: left, right, then rest.
func Jolt(elapsed time.Duration) int {
	switch {
	case elapsed < 0, elapsed >= JoltDuration:
		return 0
	case elapsed < 40*time.Millisecond:
		return -1
	case elapsed < 80*time.Millisecond:
		return 1
	}
	return 0
}

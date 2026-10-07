package lockd

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

// effectPolicy decides where background frames are painted and when a GPU
// backend is dropped for the CPU one. Demotion is one-way: GPU never returns.
type effectPolicy struct {
	GPU      bool          // try the GPU backend first
	Interval time.Duration // nominal frame budget; zero disables overrun checks
	MaxSlow  int           // consecutive overruns that trigger demotion
}

// resolveEffectPolicy maps config choices to a runtime policy. battery is
// injected so tests never touch /sys. auto pins CPU while discharging; the
// power-save cap holds those frames at 30fps or slower.
func resolveEffectPolicy(backend string, powerSave bool, interval time.Duration, battery func() bool) effectPolicy {
	p := effectPolicy{Interval: interval, MaxSlow: 5}
	onBatt := powerSave && battery()
	switch backend {
	case "cpu":
		p.GPU = false
	case "gpu":
		p.GPU = true
	default: // auto
		p.GPU = !onBatt
	}
	if onBatt && p.Interval > 0 && p.Interval < time.Second/30 {
		p.Interval = time.Second / 30
	}
	return p
}

func (p effectPolicy) factory() backendFactory {
	if p.GPU {
		return newGpuBackend
	}
	return newCpuBackend
}

// onBattery reports whether any battery is actively discharging. Charging,
// full, and unknown statuses mean wall power: a plugged-in laptop should keep
// the GPU path even under power-save.
// ponytail: 'Not charging' counts as wall power; per-battery thresholds can
// wait until someone reports a wrong pick.
func onBattery(read func(string) ([]byte, error), glob func(string) ([]string, error)) bool {
	names, err := glob("/sys/class/power_supply/BAT*")
	if err != nil {
		return false
	}
	for _, name := range names {
		typ, err := read(filepath.Join(name, "type"))
		if err != nil || strings.TrimSpace(string(typ)) != "Battery" {
			continue
		}
		status, err := read(filepath.Join(name, "status"))
		if err != nil || strings.TrimSpace(string(status)) != "Discharging" {
			continue
		}
		return true
	}
	return false
}

func systemBattery() bool {
	return onBattery(os.ReadFile, filepath.Glob)
}

package lockd

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type slowBackend struct {
	draws int
}

func (s *slowBackend) Resize(int, int) error { return nil }
func (s *slowBackend) Step() error           { return nil }
func (s *slowBackend) Close() error          { return nil }
func (s *slowBackend) Draw(pixels []byte, stride int) error {
	s.draws++
	time.Sleep(25 * time.Millisecond)
	return nil
}

func TestBackgroundGpuErrorDemotesToCpu(t *testing.T) {
	tries := 0
	fail := func(string, string, int, int) (EffectBackend, error) {
		tries++
		return nil, errors.New("no gpu here")
	}
	b := newBackgroundWorker("rain", "nord", nil, func() {}, effectPolicy{GPU: true}, fail)
	defer b.stop()
	b.jobs <- backgroundJob{width: 32, height: 32}
	frame := <-b.results
	if frame.err != nil {
		t.Fatal(frame.err)
	}
	if len(frame.pixels) != 32*32*4 {
		t.Fatalf("frame size %d", len(frame.pixels))
	}
	if !b.demoted || tries != 1 {
		t.Fatalf("demoted=%v gpu tries=%d", b.demoted, tries)
	}
}

func TestBackgroundSlowGpuDemotesAfterMaxSlow(t *testing.T) {
	sink := &slowBackend{}
	b := newBackgroundWorker("rain", "nord", nil, func() {},
		effectPolicy{GPU: true, Interval: 10 * time.Millisecond, MaxSlow: 5},
		func(string, string, int, int) (EffectBackend, error) { return sink, nil })
	defer b.stop()
	for range 6 {
		b.jobs <- backgroundJob{width: 32, height: 32}
		if frame := <-b.results; frame.err != nil {
			t.Fatal(frame.err)
		}
	}
	if sink.draws != 5 {
		t.Fatalf("gpu draws=%d, want 5 then cpu forever", sink.draws)
	}
	if !b.demoted {
		t.Fatal("slow gpu never demoted")
	}
}

func TestResolveEffectPolicyMatrix(t *testing.T) {
	onBatt, onAC := func() bool { return true }, func() bool { return false }
	if p := resolveEffectPolicy("cpu", false, 0, onAC); p.GPU {
		t.Fatal("cpu must never start on gpu")
	}
	if p := resolveEffectPolicy("gpu", true, 0, onBatt); !p.GPU {
		t.Fatal("gpu choice ignores battery")
	}
	if p := resolveEffectPolicy("auto", true, 0, onBatt); p.GPU {
		t.Fatal("auto on battery with power save must start on cpu")
	}
	if p := resolveEffectPolicy("auto", false, 0, onBatt); !p.GPU {
		t.Fatal("auto ignores power save when disabled")
	}
	if p := resolveEffectPolicy("auto", true, 0, onAC); !p.GPU {
		t.Fatal("auto on ac must start on gpu")
	}
	capped := resolveEffectPolicy("gpu", true, 5*time.Millisecond, onBatt)
	if want := time.Second / 30; capped.Interval != want {
		t.Fatalf("power-save interval %v, want %v", capped.Interval, want)
	}
	if p := resolveEffectPolicy("gpu", true, 5*time.Millisecond, onAC); p.Interval != 5*time.Millisecond {
		t.Fatal("ac must not cap the interval")
	}
}

func TestOnBatteryFakeFilesystem(t *testing.T) {
	fs := map[string]string{}
	read := func(path string) ([]byte, error) {
		if v, ok := fs[path]; ok {
			return []byte(v), nil
		}
		return nil, fmt.Errorf("missing %s", path)
	}
	root := "/sys/class/power_supply"
	glob := func(pattern string) ([]string, error) {
		prefix := strings.TrimSuffix(pattern, "*")
		var out []string
		for _, d := range []string{filepath.Join(root, "BAT0"), filepath.Join(root, "AC")} {
			if strings.HasPrefix(d, prefix) {
				if _, err := read(filepath.Join(d, "type")); err == nil {
					out = append(out, d)
				}
			}
		}
		return out, nil
	}
	if onBattery(read, glob) {
		t.Fatal("no supplies, found battery")
	}
	fs[filepath.Join(root, "AC", "type")] = "Mains"
	if onBattery(read, glob) {
		t.Fatal("mains only, found battery")
	}
	fs[filepath.Join(root, "BAT0", "type")] = "Mains"
	if onBattery(read, glob) {
		t.Fatal("BAT* directory with wrong type treated as battery")
	}
	fs[filepath.Join(root, "BAT0", "type")] = "Battery\n"
	fs[filepath.Join(root, "BAT0", "status")] = "Discharging\n"
	if !onBattery(read, glob) {
		t.Fatal("discharging battery missed")
	}
	fs[filepath.Join(root, "BAT0", "status")] = "Full"
	if onBattery(read, glob) {
		t.Fatal("full battery treated as draining")
	}
}

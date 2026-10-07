package config

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Nomadcxx/sysc-lock/internal/power"
)

func TestLockConfigPreservesUnknownFields(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.json")
	_ = os.WriteFile(p, []byte(`{"effect":"rain","palette":"nord","future":{"a":1}}`), 0600)
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	c.ReducedMotion = true
	if err = Save(p, c); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(p)
	if !strings.Contains(string(data), `"future"`) || !strings.Contains(string(data), `"a": 1`) {
		t.Fatal("lost future fields", string(data))
	}
}
func TestLockConfigRejectsTextEffectAndOversize(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.json")
	for _, data := range []string{`{"effect":"fire-text"}`, `{"palette":"invalid"}`, `{"reduced_motion":null}`, strings.Repeat(" ", 65537)} {
		_ = os.WriteFile(p, []byte(data), 0600)
		if _, err := Load(p); err == nil {
			t.Fatal("accepted invalid config")
		}
	}
}
func TestLockConfigAtomicFailureKeepsOldFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.json")
	old := []byte(`{"effect":"rain","palette":"nord"}`)
	_ = os.WriteFile(p, old, 0600)
	c := Default()
	c.Effect = "invalid"
	if err := Save(p, c); err == nil {
		t.Fatal("invalid save")
	}
	got, _ := os.ReadFile(p)
	if string(got) != string(old) {
		t.Fatal("destroyed existing config")
	}
}

func TestLockConfigRejectsSymlink(t *testing.T) {
	dir := t.TempDir()
	target, link := filepath.Join(dir, "actual.json"), filepath.Join(dir, "config.json")
	if err := os.WriteFile(target, []byte(`{"effect":"rain"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(link); err == nil {
		t.Fatal("followed config symlink")
	}
	if err := Save(link, Default()); err == nil {
		t.Fatal("replaced config symlink")
	}
}

func TestLockConfigFIFOReadDoesNotBlock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := syscall.Mkfifo(path, 0600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := Load(path); done <- err }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("accepted FIFO config")
		}
	case <-time.After(200 * time.Millisecond):
		// Release the old blocking reader before failing, leaving no blocked worker.
		writer, err := os.OpenFile(path, os.O_WRONLY|syscall.O_NONBLOCK, 0)
		if err == nil {
			writer.Close()
			<-done
		}
		t.Fatal("FIFO config blocked startup")
	}
}

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(p, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestPresentationDefaults(t *testing.T) {
	c := Default()
	if c.ClockStyle != "kompaktblk" || c.Clock24h || c.EffectFPS != DefaultFPS || DefaultFPS != 20 {
		t.Fatalf("%+v", c)
	}
	got, err := Load(writeConfig(t, `{}`))
	if err != nil || !reflect.DeepEqual(got, c) {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestEffectNoneAcceptedWithValidPalette(t *testing.T) {
	got, err := Load(writeConfig(t, `{"effect":"none","palette":"eldritch"}`))
	if err != nil || got.Effect != EffectNone || got.Palette != "eldritch" {
		t.Fatalf("%+v %v", got, err)
	}
	if _, err := Load(writeConfig(t, `{"effect":"none","palette":"bogus"}`)); err == nil {
		t.Fatal("none must still validate its palette")
	}
}

func TestClockStyleNormalizes(t *testing.T) {
	for body, want := range map[string]string{
		`{"clock_style":"phm_blocky_reverse"}`: "phm_blocky_reverse",
		`{"clock_style":"plain"}`:              "plain",
		`{"clock_style":"nope"}`:               "kompaktblk",
		`{"clock_style":""}`:                   "kompaktblk",
	} {
		c, err := Load(writeConfig(t, body))
		if err != nil || c.ClockStyle != want {
			t.Fatalf("%s: %q %v", body, c.ClockStyle, err)
		}
	}
}

func TestEffectFPSClamps(t *testing.T) {
	for body, want := range map[string]int{
		`{"effect_fps":0}`: DefaultFPS, `{"effect_fps":-5}`: MinFPS, `{"effect_fps":1}`: MinFPS,
		`{"effect_fps":60}`: 60, `{"effect_fps":1000000}`: MaxFPS, `{}`: DefaultFPS,
	} {
		c, err := Load(writeConfig(t, body))
		if err != nil || c.EffectFPS != want {
			t.Fatalf("%s: %d %v", body, c.EffectFPS, err)
		}
	}
	if _, err := Load(writeConfig(t, `{"effect_fps":"fast"}`)); err == nil {
		t.Fatal("a non-number rate must be rejected")
	}
}

func TestClock24hRoundTrips(t *testing.T) {
	p := writeConfig(t, `{"clock_24h":true}`)
	c, err := Load(p)
	if err != nil || !c.Clock24h {
		t.Fatalf("%+v %v", c, err)
	}
	if err = Save(p, c); err != nil {
		t.Fatal(err)
	}
	if again, err := Load(p); err != nil || !reflect.DeepEqual(again, c) {
		t.Fatalf("%+v %v", again, err)
	}
}

func TestLockConfigSaveBudgetIncludesNewline(t *testing.T) {
	for _, delta := range []int{-1, 0} {
		t.Run(strconv.Itoa(delta), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")
			known, _ := json.Marshal(Default())
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(known, &fields); err != nil {
				t.Fatal(err)
			}
			fields["padding"] = json.RawMessage(`""`)
			base, _ := json.MarshalIndent(fields, "", "  ")
			fields["padding"], _ = json.Marshal(strings.Repeat("x", MaxBytes-len(base)+delta))
			original, _ := json.Marshal(fields)
			if err := os.WriteFile(path, original, 0600); err != nil {
				t.Fatal(err)
			}
			err := Save(path, Default())
			if delta == 0 {
				if err == nil {
					t.Fatal("save excluded newline from budget")
				}
				after, _ := os.ReadFile(path)
				if !bytes.Equal(after, original) {
					t.Fatal("oversize save destroyed old config")
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				if _, err := Load(path); err != nil {
					t.Fatal("saved config cannot be loaded", err)
				}
				info, _ := os.Stat(path)
				if info.Size() != MaxBytes {
					t.Fatal(info.Size())
				}
			}
		})
	}
}

func TestPowerActionsDefaultAndNormalize(t *testing.T) {
	d := Default()
	if len(d.PowerActions) != 3 || d.PowerActions[0] != power.Logout || d.PowerActions[1] != power.Reboot || d.PowerActions[2] != power.Shutdown {
		t.Fatalf("default order %v", d.PowerActions)
	}
	got, err := Load(writeConfig(t, `{"power_actions":["shutdown","nope","logout","shutdown"]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(got.PowerActions) != 2 || got.PowerActions[0] != power.Shutdown || got.PowerActions[1] != power.Logout {
		t.Fatalf("unknown and duplicate names are dropped, order kept: %v", got.PowerActions)
	}
}

func TestPowerActionsEmptyListRemovesTheMenu(t *testing.T) {
	got, err := Load(writeConfig(t, `{"power_actions":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(got.PowerActions) != 0 {
		t.Fatalf("an empty list is not the default: %v", got.PowerActions)
	}
}

func TestPowerActionsAbsentKeepsTheDefault(t *testing.T) {
	got, err := Load(writeConfig(t, `{"effect":"rain"}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(got.PowerActions) != 3 {
		t.Fatalf("an absent key keeps the default: %v", got.PowerActions)
	}
}

func TestPowerActionsRoundTrip(t *testing.T) {
	path := writeConfig(t, `{}`)
	c := Default()
	c.PowerActions = []power.Action{power.Shutdown, power.Logout}
	if err := Save(path, c); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.PowerActions) != 2 || got.PowerActions[0] != power.Shutdown {
		t.Fatalf("round trip %v", got.PowerActions)
	}
}

func TestBlurBackdropDefaultsAndClamp(t *testing.T) {
	if !Default().BlurBackdrop() || Default().BlurRadiusPx() != DefaultBlurRadius {
		t.Fatal("blur defaults changed")
	}
	got, err := Load(writeConfig(t, `{"blur_radius":-5}`))
	if err != nil || !got.BlurBackdrop() || got.BlurRadiusPx() != MinBlurRadius {
		t.Fatalf("%+v %v", got, err)
	}
	got, err = Load(writeConfig(t, `{"blur_radius":100}`))
	if err != nil || got.BlurRadiusPx() != MaxBlurRadius {
		t.Fatalf("%+v %v", got, err)
	}
	got, err = Load(writeConfig(t, `{"blur_backdrop":false,"blur_radius":7}`))
	if err != nil || got.BlurBackdrop() || got.BlurRadiusPx() != 7 {
		t.Fatalf("%+v %v", got, err)
	}
	c := writeConfig(t, `{"blur_backdrop":false,"blur_radius":7}`)
	got, err = Load(c)
	if err != nil {
		t.Fatal(err)
	}
	p2 := filepath.Join(t.TempDir(), "config.json")
	if err = Save(p2, got); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(p2)
	if err != nil || !strings.Contains(string(raw), `"blur_backdrop"`) || !strings.Contains(string(raw), `"blur_radius"`) {
		t.Fatalf("save dropped blur keys: %s %v", raw, err)
	}
}

func TestEffectBackendKeys(t *testing.T) {
	if got := Default().BackendChoice(); got != "auto" {
		t.Fatalf("default backend %q", got)
	}
	if !Default().GpuPowerSave() {
		t.Fatal("power save should default on")
	}
	for body, want := range map[string]string{
		`{"effect_backend":"gpu"}`:      "gpu",
		`{"effect_backend":"cpu"}`:      "cpu",
		`{"effect_backend":"nonsense"}`: "auto",
		`{}`:                            "auto",
	} {
		c, err := Load(writeConfig(t, body))
		if err != nil || c.BackendChoice() != want {
			t.Fatalf("%s: %q %v", body, c.BackendChoice(), err)
		}
	}
	for body, want := range map[string]bool{
		`{"effect_gpu_power_save":false}`: false,
		`{"effect_gpu_power_save":true}`:  true,
		`{}`:                              true,
	} {
		c, err := Load(writeConfig(t, body))
		if err != nil || c.GpuPowerSave() != want {
			t.Fatalf("%s: %v %v", body, c.GpuPowerSave(), err)
		}
	}
}

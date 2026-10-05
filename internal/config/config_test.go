package config

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
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
	if err != nil || got != c {
		t.Fatalf("%+v %v", got, err)
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
	if again, err := Load(p); err != nil || again != c {
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

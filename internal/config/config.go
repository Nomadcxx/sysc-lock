// Package config owns lock presentation. Idle policy belongs to sysc-shell.
package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/Nomadcxx/sysc-terminal/renderer"
	"io"

	"github.com/Nomadcxx/sysc-lock/internal/art"
	"os"
	"path/filepath"
	"syscall"
)

const MaxBytes = 64 << 10

const (
	MinFPS     = 10
	MaxFPS     = 120
	DefaultFPS = 20
)

type Config struct {
	Effect        string `json:"effect"`
	Palette       string `json:"palette"`
	ReducedMotion bool   `json:"reduced_motion"`
	ClockStyle    string `json:"clock_style"`
	Clock24h      bool   `json:"clock_24h"`
	// EffectFPS is effect ticks per second. The effects advance one fixed step
	// per tick, so it also scales animation speed.
	EffectFPS int `json:"effect_fps"`
}

func Default() Config {
	return Config{Effect: "rain", Palette: "nord", ClockStyle: art.DefaultStyle, EffectFPS: DefaultFPS}
}
func Path() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "sysc-lock", "config.json")
}
func read(path string) (map[string]json.RawMessage, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW, 0)
	if os.IsNotExist(err) {
		return make(map[string]json.RawMessage), nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > MaxBytes {
		return nil, fmt.Errorf("lock config exceeds file budget")
	}
	data, err := io.ReadAll(io.LimitReader(f, MaxBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > MaxBytes {
		return nil, fmt.Errorf("lock config exceeds file budget")
	}
	var fields map[string]json.RawMessage
	if err = json.Unmarshal(data, &fields); err != nil {
		return nil, err
	}
	if fields == nil {
		return nil, fmt.Errorf("lock config must be an object")
	}
	return fields, nil
}
func Load(path string) (Config, error) {
	c := Default()
	fields, err := read(path)
	if err != nil {
		return c, err
	}
	for _, key := range []string{"effect", "palette", "reduced_motion"} {
		if value, ok := fields[key]; ok && bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return c, fmt.Errorf("null lock setting %s", key)
		}
	}
	data, _ := json.Marshal(fields)
	if err = json.Unmarshal(data, &c); err != nil {
		return c, err
	}
	c.ClockStyle = art.Lookup(c.ClockStyle).Name
	switch {
	case fields["effect_fps"] == nil || c.EffectFPS == 0:
		c.EffectFPS = DefaultFPS
	default:
		c.EffectFPS = max(MinFPS, min(MaxFPS, c.EffectFPS))
	}
	return c, renderer.Validate(c.Effect, c.Palette)
}
func Save(path string, c Config) error {
	if err := renderer.Validate(c.Effect, c.Palette); err != nil {
		return err
	}
	fields, err := read(path)
	if err != nil {
		return err
	}
	known, _ := json.Marshal(c)
	var patch map[string]json.RawMessage
	_ = json.Unmarshal(known, &patch)
	for key, value := range patch {
		fields[key] = value
	}
	data, err := json.MarshalIndent(fields, "", "  ")
	if err != nil {
		return err
	}
	if len(data)+1 > MaxBytes {
		return fmt.Errorf("lock config exceeds file budget")
	}
	dir := filepath.Dir(path)
	if err = os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".config-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if _, err = f.Write(append(data, '\n')); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

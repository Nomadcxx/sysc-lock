// Package config owns lock presentation. Idle policy belongs to sysc-shell.
package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/Nomadcxx/sysc-terminal/renderer"
	"io"

	"github.com/Nomadcxx/sysc-lock/internal/art"
	"github.com/Nomadcxx/sysc-lock/internal/power"
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

const (
	MinBlurRadius     = 0
	MaxBlurRadius     = 64
	DefaultBlurRadius = 24
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
	// BlurBackdrop captures and blurs the desktop once at lock. Nil means
	// the default (enabled); SYSC_LOCK_WALLPAPER overrides it either way.
	Blur *bool `json:"blur_backdrop"`
	// BlurRadius is the box-blur radius in pixels, clamped to [0,64].
	BlurRadius *int `json:"blur_radius"`
	// EffectBackend selects the effect engine: auto, cpu or gpu. Nil means auto.
	EffectBackend *string `json:"effect_backend"`
	// EffectGpuPowerSave caps the GPU path while on battery. Nil means enabled.
	EffectGpuPowerSave *bool `json:"effect_gpu_power_save"`
	// PowerActions is the ordered Power Options menu. An empty list removes
	// the menu, the F4 hint and the help-line mention.
	PowerActions []power.Action `json:"power_actions"`
}

// BlurBackdrop reports whether the frozen blurred backdrop is wanted.
func (c Config) BlurBackdrop() bool { return c.Blur == nil || *c.Blur }

// BackendChoice returns the effect engine: "cpu", "gpu", or "auto" when unset
// or unrecognized.
func (c Config) BackendChoice() string {
	if c.EffectBackend == nil {
		return "auto"
	}
	switch *c.EffectBackend {
	case "cpu", "gpu":
		return *c.EffectBackend
	}
	return "auto"
}

// GpuPowerSave reports whether the on-battery GPU cap applies.
func (c Config) GpuPowerSave() bool { return c.EffectGpuPowerSave == nil || *c.EffectGpuPowerSave }

// BlurRadiusPx returns the configured radius clamped to [0,64].
func (c Config) BlurRadiusPx() int {
	if c.BlurRadius == nil {
		return DefaultBlurRadius
	}
	return min(max(*c.BlurRadius, 0), MaxBlurRadius)
}

func Default() Config {
	return Config{Effect: "fire", Palette: "nord", ClockStyle: art.DefaultStyle, EffectFPS: DefaultFPS, PowerActions: append([]power.Action{}, power.DefaultOrder...)}
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
	c.PowerActions = power.Normalize(c.PowerActions)
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

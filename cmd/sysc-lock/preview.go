package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image/png"
	"io"
	"time"

	"github.com/Nomadcxx/sysc-Go/animations"
	"github.com/Nomadcxx/sysc-lock/internal/art"
	"github.com/Nomadcxx/sysc-lock/internal/config"
	"github.com/Nomadcxx/sysc-lock/internal/input"
	"github.com/Nomadcxx/sysc-lock/internal/lockd"
	"github.com/Nomadcxx/sysc-lock/internal/render"
	"github.com/Nomadcxx/sysc-lock/internal/theme"
	"github.com/Nomadcxx/sysc-terminal/renderer"
)

// runPreview renders an ordinary still, without session, PAM or user-file work.
func runPreview(in io.Reader, out io.Writer) error {
	data, err := io.ReadAll(io.LimitReader(in, config.MaxBytes+1))
	if err != nil {
		return err
	}
	if len(data) > config.MaxBytes {
		return fmt.Errorf("preview exceeds JSON budget")
	}
	data = bytes.TrimSpace(data)
	if len(data) == 0 || len(data) > config.MaxBytes || data[0] != '{' {
		return fmt.Errorf("preview requires a bounded JSON object")
	}
	req := struct {
		Config config.Config `json:"config"`
		Width  int           `json:"width"`
		Height int           `json:"height"`
	}{Config: config.Default(), Width: 960, Height: 540}
	if err = json.Unmarshal(data, &req); err != nil {
		return err
	}
	if req.Width < 1 || req.Height < 1 || req.Width > 1920 || req.Height > 1920 || req.Width*req.Height > 1920*1080 {
		return fmt.Errorf("preview geometry exceeds pixel budget")
	}
	if err = req.Config.Validate(); err != nil {
		return err
	}
	view := lockd.NewView(theme.Default().WithScheme(req.Config.Palette), "Preview", "")
	view.Entry = &input.Model{}
	view.StyleName, view.Clock24, view.Reduced = req.Config.ClockStyle, req.Config.Clock24h, req.Config.ReducedMotion
	view.Hint = "F1 Options - Enter Unlock"
	view.Ambient = "[||||||....] 63% - Wi-Fi - playing"
	now := time.Date(2026, time.October, 8, 13, 24, 0, 0, time.UTC)
	view.Reveal.Show(now)
	fb := render.New(req.Width, req.Height)
	var background []byte
	if req.Config.Effect != config.EffectNone {
		r, err := renderer.New(renderer.Config{Effect: req.Config.Effect, Palette: req.Config.Palette, Width: req.Width, Height: req.Height})
		if err != nil {
			return err
		}
		// A still must show warmed content; rain starts above the visible area.
		for range 20 {
			if err = r.Step(); err != nil {
				return err
			}
		}
		background = make([]byte, len(fb.Pix))
		if _, err = r.Draw(background, fb.Stride, nil); err != nil {
			return err
		}
		lockd.DimBackground(background)
	}
	paint := func(at time.Time) {
		if background == nil {
			view.Render(fb, at)
		} else {
			copy(fb.Pix, background)
			view.RenderForeground(fb, at)
		}
	}
	paint(now)
	paint(now.Add(art.PrintDuration))
	return png.Encode(out, fb)
}

func writeDescription(out io.Writer) error {
	return json.NewEncoder(out).Encode(struct {
		ClockStyles []string      `json:"clock_styles"`
		Effects     []string      `json:"effects"`
		Palettes    []string      `json:"palettes"`
		Defaults    config.Config `json:"defaults"`
	}{art.Names(), effectChoices(), animations.GetThemeNames(), config.Default()})
}

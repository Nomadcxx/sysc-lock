package lockd

import (
	"bytes"
	"fmt"
	"github.com/Nomadcxx/sysc-lock/internal/art"
	"github.com/Nomadcxx/sysc-lock/internal/config"
	"github.com/Nomadcxx/sysc-lock/internal/render"
	"github.com/Nomadcxx/sysc-lock/internal/theme"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Optional synthetic evidence uses the real backends; it never locks or captures a desktop.
func TestVisualEvidence(t *testing.T) {
	dir := os.Getenv("SYSC_LOCK_VISUAL_EVIDENCE")
	if dir == "" {
		t.Skip("set SYSC_LOCK_VISUAL_EVIDENCE")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	save := func(name string, fb *render.Framebuffer) {
		t.Helper()
		f, err := os.Create(filepath.Join(dir, name+".png"))
		if err != nil {
			t.Fatal(err)
		}
		err = png.Encode(f, fb)
		closeErr := f.Close()
		if err != nil || closeErr != nil {
			t.Fatal(err, closeErr)
		}
	}
	for _, effect := range config.Effects() {
		if effect == config.EffectNone {
			continue
		}
		for _, tc := range []struct {
			name    string
			factory backendFactory
		}{{"cpu", newCpuBackend}, {"gpu", newGpuBackend}} {
			b, err := tc.factory(effect, "nord", 960, 540)
			if err != nil {
				if tc.name == "cpu" {
					t.Fatal(effect, err)
				}
				t.Logf("%s/%s: %v", effect, tc.name, err)
				continue
			}
			fb := render.New(960, 540)
			for step := 1; step <= 80; step++ {
				if effect == "ak-47" || effect == "ak-47-rama" {
					time.Sleep(50 * time.Millisecond)
				}
				if err = b.Step(); err != nil {
					t.Fatal(err)
				}
				if step != 1 && step != 80 {
					continue
				}
				if err = b.Draw(fb.Pix, fb.Stride); err != nil {
					t.Fatal(err)
				}
				lit := 0
				for i := 0; i < len(fb.Pix); i += 4 {
					if fb.Pix[i]|fb.Pix[i+1]|fb.Pix[i+2] != 0 {
						lit++
					}
				}
				t.Logf("%s/%s step=%d lit=%d/%d", effect, tc.name, step, lit, fb.Width*fb.Height)
				save(fmt.Sprintf("%s-%s-%d-raw", effect, tc.name, step), fb)
				// A captured blurred desktop cannot alter an active effect's selected frame.
				out := &lockOut{w: 960, h: 540, background: &backgroundWorker{cached: backgroundFrame{width: 960, height: 540, pixels: fb.Pix}}}
				off := append([]byte(nil), out.backgroundPixels()...)
				out.backdrop = render.Blur(fb, 4, 24)
				out.blurW, out.blurH = 960, 540
				if !bytes.Equal(off, out.backgroundPixels()) {
					t.Fatal("blur changed active effect")
				}
				DimBackground(off)
				dim := &render.Framebuffer{Width: 960, Height: 540, Stride: 960 * 4, Pix: off}
				save(fmt.Sprintf("%s-%s-%d-composed", effect, tc.name, step), dim)
			}
			if err := b.Close(); err != nil {
				t.Fatal(err)
			}
		}
	}
	now := time.Date(2026, 10, 14, 13, 24, 56, 0, time.UTC)
	for _, effect := range config.TextEffects() {
		v := NewView(theme.Default(), "Sample", "desktop")
		v.Header = art.DefaultHeaders()[0].Text
		v.TextEffect, v.TextPalette = effect, "nord"
		for tick := 0; tick <= 160; tick++ {
			fb := render.New(520, 96)
			v.drawHeader(fb, fb.Bounds(), now.Add(time.Duration(tick)*50*time.Millisecond))
			if tick == 0 || tick == 160 {
				save(fmt.Sprintf("header-%s-%d", effect, tick), fb)
			}
		}
	}

	for _, size := range [][2]int{{3440, 1440}, {2560, 1440}} {
		for _, style := range art.Names() {
			for _, mode := range []string{"prompt", "saver", "options"} {
				v := NewView(theme.Default(), "Sample", "desktop")
				v.Reduced = true
				v.Header = art.DefaultHeaders()[0].Text
				v.StyleName = style
				v.Render(render.New(1, 1), now)
				at := now
				if mode == "saver" {
					at = now.Add(5 * time.Minute)
				}
				if mode == "options" {
					v.Options = &MenuView{Open: true, Title: "Options", Progress: -1, Rows: []PowerRow{{Title: "Background", Value: "matrix", Selected: true}, {Title: "Theme", Value: "nord"}, {Title: "Header", Value: "ascii_1"}, {Title: "Text effect", Value: "none"}}}
				}
				fb := render.New(size[0], size[1])
				v.Render(fb, at)
				save(fmt.Sprintf("%s-%s-%dx%d", style, mode, size[0], size[1]), fb)
			}
		}
	}
}

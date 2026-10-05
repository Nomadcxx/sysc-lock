package lockd

import (
	"fmt"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Nomadcxx/sysc-lock/internal/input"
	"github.com/Nomadcxx/sysc-lock/internal/render"
	"github.com/Nomadcxx/sysc-lock/internal/theme"
	"github.com/Nomadcxx/sysc-terminal/renderer"
)

func luminance(c color.NRGBA) float64 {
	linear := func(v uint8) float64 {
		x := float64(v) / 255
		if x <= .04045 {
			return x / 12.92
		}
		return math.Pow((x+.055)/1.055, 2.4)
	}
	return .2126*linear(c.R) + .7152*linear(c.G) + .0722*linear(c.B)
}
func panelContrast(c color.NRGBA) float64 {
	a, b := luminance(c), luminance(panelGround)
	return (max(a, b) + .05) / (min(a, b) + .05)
}
func TestPanelRoleContrast(t *testing.T) {
	for _, item := range []struct {
		name    string
		c       color.NRGBA
		minimum float64
	}{{"text", panelInk, 4.5}, {"status", panelDanger, 4.5}, {"control/focus", panelAccent, 3}} {
		ratio := panelContrast(item.c)
		t.Logf("%s %.2f:1", item.name, ratio)
		if ratio < item.minimum {
			t.Fatalf("%s %.2f below %.1f", item.name, ratio, item.minimum)
		}
	}
}

// TestOfflineViewSnapshots writes optional evidence using fake account/input data.
// It calls only the shared raster renderer and view: no Wayland or PAM setup.
func TestOfflineViewSnapshots(t *testing.T) {
	dir := os.Getenv("SYSC_LOCK_TEST_SNAPSHOT_DIR")
	if dir == "" {
		t.Skip("set SYSC_LOCK_TEST_SNAPSHOT_DIR to capture offline evidence")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 5, 21, 47, 0, 0, time.UTC)
	for _, name := range []string{"normal", "busy", "error", "reduced-motion", "narrow"} {
		t.Run(name, func(t *testing.T) {
			w, h := 960, 720
			if name == "narrow" {
				w, h = 320, 240
			}
			fb := render.New(w, h)
			v := NewView(theme.Default(), "Sample Account", "example")
			v.Layout = "English (US)"
			v.Entry = &input.Model{}
			if name == "busy" {
				v.Busy = true
				v.Entry.Append("fake password")
			}
			if name == "error" {
				v.SetError("Incorrect password", now)
				v.Caps = true
			}
			if name == "reduced-motion" {
				v.Render(fb, now)
			} else {
				r, err := renderer.New(renderer.Config{Effect: "rain", Palette: "nord", Width: w, Height: h, PixelSize: 12})
				if err != nil {
					t.Fatal(err)
				}
				// Advance to a visible frame; this remains an ordinary offline raster.
				for range 20 {
					if err = r.Step(); err != nil {
						t.Fatal(err)
					}
				}
				if _, err = r.Draw(fb.Pix, fb.Stride, nil); err != nil {
					t.Fatal(err)
				}
				for i := 0; i < len(fb.Pix); i += 4 {
					fb.Pix[i] /= 3
					fb.Pix[i+1] /= 3
					fb.Pix[i+2] /= 3
				}
				v.RenderForeground(fb, now)
			}
			f, err := os.Create(filepath.Join(dir, name+".png"))
			if err != nil {
				t.Fatal(err)
			}
			err = png.Encode(f, fb)
			closeErr := f.Close()
			if err != nil {
				t.Fatal(err)
			}
			if closeErr != nil {
				t.Fatal(closeErr)
			}
		})
	}
	evidence := fmt.Sprintf("Offline raster evidence only; no lock/PAM/session qualification.\nShared renderer: github.com/Nomadcxx/sysc-terminal v0.0.0-20261004174459-4e522749ac8b, rain/nord, 20 steps.\nFake account: Sample Account. Fixed UTC clock: 2026-10-05 21:47.\n960x720 and compact 320x240, scale 1. Reduced-motion sample uses the approved solid fallback.\nOpaque foreground role WCAG luminance ratios against panel #10141c:\ntext #f0f4fa %.2f:1 (minimum 4.5)\nstatus #ffb4b4 %.2f:1 (minimum 4.5)\ncontrol/focus #93c5fd %.2f:1 (minimum 3)\nGlyph edge antialiasing is excluded from WCAG role contrast.\n", panelContrast(panelInk), panelContrast(panelDanger), panelContrast(panelAccent))
	if err := os.WriteFile(filepath.Join(dir, "offline-render-evidence.txt"), []byte(evidence), 0600); err != nil {
		t.Fatal(err)
	}
}

package lockd

import (
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Nomadcxx/sysc-lock/internal/input"
	"github.com/Nomadcxx/sysc-lock/internal/power"
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
	for _, name := range []string{"hidden", "revealed", "busy", "error", "reduced-motion", "narrow", "power", "power-hold", "ambient"} {
		t.Run(name, func(t *testing.T) {
			w, h := 960, 720
			if name == "narrow" {
				w, h = 320, 240
			}
			fb := render.New(w, h)
			v := NewView(theme.Default(), "Sample Account", "example")
			v.Layout = "English (US)"
			v.Reduced = true // stills show the final frame, not the print reveal
			v.Entry = &input.Model{}
			if name != "hidden" {
				v.Reveal.Show(now)
			}
			if name == "busy" {
				v.Busy = true
				v.Entry.Append("fake password")
			}
			if name == "error" {
				v.SetError("Incorrect password", now)
				v.Caps = true
			}
			if name == "power" || name == "power-hold" {
				m := power.New(power.DefaultOrder, power.Availability{Reboot: true, Shutdown: true}, "c2")
				m.Press(power.Key{F4: true}, now)
				progress := -1
				if name == "power-hold" {
					m.Press(power.Key{Enter: true}, now)
					progress = m.Progress(now.Add(750 * time.Millisecond))
				}
				v.Hint = power.ScreenHelp
				v.Power = &PowerView{Open: m.Open(), Title: power.Title, Help: power.Help, Progress: progress}
				for i, a := range m.Items() {
					v.Power.Rows = append(v.Power.Rows, PowerRow{Title: a.Label(), Selected: i == m.Selected()})
				}
			}
			if name == "ambient" {
				v.Reveal.Show(now)
				v.Hint = power.ScreenHelp
				v.Ambient = "82% · Wi-Fi · playing · 18°"
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
				DimBackground(fb.Pix)
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
			if name == "hidden" || name == "ambient" {
				s := Layout(w, h, 1, "", v.clockText(now))
				ground := 0
				for y := s.Ambient.Min.Y; y < s.Ambient.Max.Y; y++ {
					for x := s.Ambient.Min.X; x < s.Ambient.Max.X; x++ {
						if color.NRGBAModel.Convert(fb.At(x, y)).(color.NRGBA) == panelGround {
							ground++
						}
					}
				}
				if name == "hidden" && ground != 0 {
					t.Fatalf("hidden: ambient slot painted %d ground pixels", ground)
				}
				if name == "ambient" && ground < 100 {
					t.Fatalf("ambient: row missing its ground, counted %d pixels", ground)
				}
			}
			if name == "power" || name == "power-hold" {
				s := Layout(w, h, 1, "", v.clockText(now))
				danger := 0
				for y := 0; y < h; y++ {
					for x := 0; x < w; x++ {
						c := color.NRGBAModel.Convert(fb.At(x, y)).(color.NRGBA)
						edge := x == 0 || y == 0 || x == w-1 || y == h-1
						if edge && (c == panelGround || c == panelInk || c == panelDanger || c == panelAccent || c == panelMuted) {
							t.Fatalf("%s: chrome on the output edge at %d,%d", name, x, y)
						}
						if c == panelDanger {
							danger++
							if !image.Pt(x, y).In(s.Menu) {
								t.Fatalf("%s: danger ink outside the popup at %d,%d", name, x, y)
							}
						}
					}
				}
				if danger < 500 {
					t.Fatalf("%s: selected bar missing, counted %d danger pixels", name, danger)
				}
			}
		})
	}
	evidence := fmt.Sprintf("Offline raster evidence only; no lock/PAM/session qualification.\nShared renderer: github.com/Nomadcxx/sysc-terminal v0.0.0-20261004174459-4e522749ac8b, rain/nord, 20 steps.\nFake account: Sample Account. Fixed UTC clock: 2026-10-05 21:47.\n960x720 and compact 320x240, scale 1. Reduced-motion sample uses the approved solid fallback.\nOpaque foreground role WCAG luminance ratios against panel #10141c:\ntext #f0f4fa %.2f:1 (minimum 4.5)\nstatus #ffb4b4 %.2f:1 (minimum 4.5)\ncontrol/focus #93c5fd %.2f:1 (minimum 3)\nhelp/muted #828a96 %.2f:1 (minimum 4.5)\nGlyph edge antialiasing is excluded from WCAG role contrast.\n", panelContrast(panelInk), panelContrast(panelDanger), panelContrast(panelAccent), panelContrast(panelMuted))
	worst := color.NRGBA{R: 127, G: 127, B: 127, A: 255}
	artRatio := (max(luminance(panelInk), luminance(worst)) + .05) / (min(luminance(panelInk), luminance(worst)) + .05)
	evidence += fmt.Sprintf("Clock-forward composition, 12-hour clock. Art ink over the brightest dimmed effect pixel (white halved): %.2f:1 (minimum 3).\n", artRatio)
	if err := os.WriteFile(filepath.Join(dir, "offline-render-evidence.txt"), []byte(evidence), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestArtInkReadsOverTheBrightestDimmedEffect(t *testing.T) {
	// Clock, date and wordmark sit on the dimmed effect without a backing; the
	// brightest dimmed pixel is white halved.
	worst := color.NRGBA{R: 127, G: 127, B: 127, A: 255}
	a, b := luminance(panelInk), luminance(worst)
	if ratio := (max(a, b) + .05) / (min(a, b) + .05); ratio < 3 {
		t.Fatalf("art ink %.2f:1 below the 3:1 large-text floor", ratio)
	}
}

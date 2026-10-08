package lockd

import (
	"image"
	"strings"
	"time"

	"github.com/Nomadcxx/sysc-lock/internal/render"
	"github.com/Nomadcxx/sysc-terminal/renderer"
)

type headerConfig struct {
	text, effect, palette string
	width, height, px     int
}

func (v *View) animateHeader() bool {
	return v.Header != "" && v.TextEffect != "" && v.TextEffect != "none" && !v.Reduced && !v.Terminal() && !v.Busy && v.Prompt == "" && v.Powering == "" && (v.Entry == nil || len(v.Entry.Pass) == 0)
}

func (v *View) drawHeader(fb *render.Framebuffer, box image.Rectangle, now time.Time) {
	if box.Empty() {
		return
	}
	rows := strings.Split(v.Header, "\n")
	width := 1
	for _, row := range rows {
		width = max(width, len([]rune(row)))
	}
	// Leave space for moving text while keeping every artwork in the same slot.
	px := max(1, min(int(16*max(1, v.Scale)), box.Dy()*2/(max(4, len(rows))*3), box.Dx()/max(21, width)))
	if v.animateHeader() {
		cfg := headerConfig{v.Header, v.TextEffect, v.TextPalette, box.Dx(), box.Dy(), px}
		if cfg != v.headerConfig {
			v.headerConfig = cfg
			// ponytail: one bounded surface cache; differing multi-output sizes rebuild it.
			// Add per-output caches only when multi-output header demand is measured.
			v.headerRenderer, _ = renderer.New(renderer.Config{Text: cfg.text, Effect: cfg.effect, Palette: cfg.palette, Width: cfg.width, Height: cfg.height, PixelSize: cfg.px})
			v.headerPixels = make([]byte, box.Dx()*box.Dy()*4)
			v.headerStep = time.Time{}
		}
		r := v.headerRenderer
		if r != nil {
			if v.headerStep.IsZero() || !now.Before(v.headerStep.Add(50*time.Millisecond)) {
				if err := r.Step(); err != nil {
					v.headerRenderer = nil
				} else if _, err := r.Draw(v.headerPixels, box.Dx()*4, nil); err != nil {
					v.headerRenderer = nil
				} else {
					v.headerStep = now
				}
			}
			if v.headerRenderer != nil {
				// The shared renderer's black canvas is transparent over the background.
				for y := 0; y < box.Dy(); y++ {
					for x := 0; x < box.Dx(); x++ {
						src := (y*box.Dx() + x) * 4
						if v.headerPixels[src]|v.headerPixels[src+1]|v.headerPixels[src+2] == 0 {
							continue
						}
						dst := (box.Min.Y+y)*fb.Stride + (box.Min.X+x)*4
						copy(fb.Pix[dst:dst+4], v.headerPixels[src:src+4])
					}
				}
				return
			}
		}
	}
	// Static artwork shares one left edge, preserving leading spaces and shape.
	blockW := 0
	for _, row := range rows {
		blockW = max(blockW, textWidth(px, row))
	}
	lineH := max(1, px*3/2)
	x, y := box.Min.X+(box.Dx()-blockW)/2, box.Min.Y+(box.Dy()-lineH*len(rows))/2
	ink := v.artInk(v.banner(), 3)
	for i, row := range rows {
		line := image.Rect(x, y+i*lineH, box.Max.X, y+(i+1)*lineH).Intersect(box)
		drawTextBoxLeft(fb, line, line.Min.Y+px, row, px, ink)
	}
}

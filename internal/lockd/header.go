package lockd

import (
	"image"
	"image/color"
	"strings"
	"time"

	"github.com/Nomadcxx/sysc-Go/animations"
	"github.com/Nomadcxx/sysc-lock/internal/render"
	"github.com/charmbracelet/x/ansi"
	"golang.org/x/image/font"
	"golang.org/x/image/math/fixed"
)

type headerConfig struct {
	text, effect, palette string
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
	// Keep literal leading spaces and a common left edge, including animated frames.
	for i, row := range rows {
		rows[i] = row + strings.Repeat(" ", width-len([]rune(row)))
	}
	px := max(1, min(int(16*max(1, v.Scale)), box.Dy()*2/(max(4, len(rows))*3), box.Dx()/max(21, width)))
	if v.animateHeader() {
		rows = v.animatedHeader(rows, width, now)
	}
	// Plain font rendering preserves the compact catalogue rather than rasterizing blocks.
	blockW := textWidth(px, strings.Repeat("M", width))
	lineH := max(1, px*3/2)
	x, y := box.Min.X+(box.Dx()-blockW)/2, box.Min.Y+(box.Dy()-lineH*len(rows))/2
	ink := v.artInk(v.banner(), 3)
	for i, row := range rows {
		line := image.Rect(x, y+i*lineH, box.Max.X, y+(i+1)*lineH).Intersect(box)
		v.drawHeaderRow(fb, line, line.Min.Y+px, row, px, ink)
	}
}

// drawHeaderRow paints the library's foreground colors with the existing font.
// The library emits SGR colors; cursor commands and background fills are ignored.
func (v *View) drawHeaderRow(fb *render.Framebuffer, box image.Rectangle, baseline int, row string, px int, ink color.NRGBA) {
	box = box.Intersect(fb.Bounds())
	if box.Empty() {
		return
	}
	d := font.Drawer{Dst: clippedText{fb, box}, Face: face(px), Dot: fixed.P(box.Min.X, baseline), Src: image.NewUniform(ink)}
	parser := ansi.NewParser()
	var state byte
	for len(row) > 0 {
		seq, width, n, next := ansi.DecodeSequence(row, state, parser)
		row, state = row[n:], next
		if width > 0 {
			seq = strings.Map(func(r rune) rune {
				if _, ok := d.Face.GlyphAdvance(r); !ok {
					return '?'
				}
				return r
			}, seq)
			d.DrawString(seq)
			continue
		}
		if !strings.HasPrefix(seq, "\x1b[") || parser.Command() != 'm' {
			continue
		}
		params := parser.Params()
		if len(params) == 0 {
			d.Src = image.NewUniform(ink)
		}
		for i := 0; i < len(params); i++ {
			switch params[i].Param(0) {
			case 0, 39:
				d.Src = image.NewUniform(ink)
			case 38:
				var c color.Color
				if count := ansi.ReadStyleColor(params[i:], &c); count > 0 {
					if c != nil {
						d.Src = image.NewUniform(v.artInk(color.NRGBAModel.Convert(c).(color.NRGBA), 3))
					}
					i += count - 1
				}
			}
		}
	}
}

// animatedHeader contains library failures so decoration cannot break the form.
func (v *View) animatedHeader(rows []string, width int, now time.Time) (frame []string) {
	frame = rows
	defer func() {
		if recover() != nil {
			v.headerRenderer = nil
			frame = rows
		}
	}()
	cfg := headerConfig{v.Header, v.TextEffect, v.TextPalette}
	if cfg != v.headerConfig {
		v.headerConfig = cfg
		// The animation grid follows the artwork, so outputs share its phase.
		text := strings.Join(rows, "\n")
		pal := animations.GetParticlePalette(cfg.palette)
		switch cfg.effect {
		case "print":
			// Ten updates cap the compact reveal at 500ms, then it holds.
			slots := max(1, 10/len(rows))
			speed := (width + slots - 1) / slots
			v.headerRenderer = animations.NewPrintEffect(animations.PrintConfig{Width: width, Height: len(rows), Text: text, PrintSpeed: speed, GradientStops: pal, Display: true})
		case "blackhole":
			v.headerRenderer = animations.NewBlackholeEffect(animations.BlackholeConfig{Width: width, Height: len(rows), Text: text, StarColors: pal, FinalGradientStops: pal})
		case "matrix-art":
			v.headerRenderer = animations.NewMatrixArtEffect(width, len(rows), animations.GetMatrixPalette(cfg.palette), text)
		case "rain-art":
			v.headerRenderer = animations.NewRainArtEffect(width, len(rows), animations.GetRainPalette(cfg.palette), text)
		case "beam-text":
			v.headerRenderer = animations.NewBeamTextEffect(animations.BeamTextConfig{Width: width, Height: len(rows), Text: text, BeamGradientStops: pal, FinalGradientStops: pal})
		case "pour":
			v.headerRenderer = animations.NewPourEffect(animations.PourConfig{Width: width, Height: len(rows), Text: text, PourDirection: "down", PourSpeed: 4, MovementSpeed: .5, StartingColor: pal[len(pal)-1], FinalGradientStops: pal, FinalGradientSteps: 10, FinalGradientFrames: 2})
		default:
			v.headerRenderer = nil
		}
		v.headerStep = time.Time{}
		v.headerTicks, v.headerDone = 0, false
	}
	if r := v.headerRenderer; r != nil {
		if !v.headerDone && (v.headerStep.IsZero() || !now.Before(v.headerStep.Add(50*time.Millisecond))) {
			steps := 1
			if v.TextEffect == "print" {
				steps = max(1, (len(rows)+9)/10)
			}
			for range steps {
				r.Update()
			}
			v.headerStep = now
			v.headerTicks++
			v.headerDone = v.TextEffect == "print" && v.headerTicks >= 10
		}
		frame = strings.Split(r.Render(), "\n")
	}
	return frame
}

package lockd

import (
	"time"

	"github.com/Nomadcxx/sysc-lock/internal/input"
	"github.com/Nomadcxx/sysc-lock/internal/render"
	"github.com/Nomadcxx/sysc-lock/internal/theme"
)

// View composes the lock screen: static background (wallpaper file or palette
// surface color), centered clock, user@host, masked entry, status line with
// DMS-parity 4s auto-clear, attempts count, keyboard layout label.
type View struct {
	Pal        theme.Palette
	Background string // wallpaper path for this output; "" = solid palette
	User, Host string
	Layout     string // e.g. "us"; "" hides the indicator
	Caps       bool
	Attempts   int
	Entry      *input.Model

	errMsg   string
	errUntil time.Time
	errTerm  bool
}

func NewView(pal theme.Palette, user, host string) *View {
	return &View{Pal: pal, User: user, Host: host}
}

func (v *View) SetError(msg string, now time.Time) {
	v.errMsg, v.errUntil, v.errTerm = msg, now.Add(4*time.Second), false
}

// SetErrorTerminal shows a message that does not auto-clear (lockout).
func (v *View) SetErrorTerminal(msg string, _ time.Time) {
	v.errMsg, v.errTerm = msg, true
}

func (v *View) NoteAttempt(now time.Time) { v.Attempts++ }

// StatusLine is the visible error text at now (empty after the 4s window).
func (v *View) StatusLine(now time.Time) string {
	if v.errMsg == "" {
		return ""
	}
	if !v.errTerm && now.After(v.errUntil) {
		v.errMsg = ""
	}
	return v.errMsg
}

// Render paints one full frame into fb at pixel size (fb dims). now drives the
// clock (minute resolution) and error auto-clear.
func (v *View) Render(fb *render.Framebuffer, now time.Time) {
	if err := fb.Background(v.Background, v.Pal.Surface); err != nil {
		v.Background = "" // once unreadable, stop retrying per frame
		fb.Fill(v.Pal.Surface)
	}
	cx := fb.Width / 2
	drawText(fb, cx, fb.Height/2-70, now.Format("15:04"), 42, v.Pal.OnSurface)
	drawText(fb, cx, fb.Height/2-14, v.User+"@"+v.Host, 18, v.Pal.OnSurface)
	mask := ""
	if v.Entry != nil {
		mask = v.Entry.Mask()
	}
	if mask == "" {
		mask = "Password..."
	}
	drawText(fb, cx, fb.Height/2+18, mask, 22, v.Pal.OnSurface)
	if v.Caps {
		drawText(fb, cx, fb.Height/2+52, "Caps Lock ON", 14, v.Pal.Error)
	}
	if s := v.StatusLine(now); s != "" {
		drawText(fb, cx, fb.Height/2+76, s, 16, v.Pal.Error)
	}
	if v.Attempts > 0 {
		drawText(fb, 8, fb.Height-24, itoa(v.Attempts)+" failed attempt(s)", 12, v.Pal.OnSurface)
	}
	if v.Layout != "" {
		drawText(fb, fb.Width-8, 24, v.Layout, 14, v.Pal.OnSurface) // right-aligned
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [8]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

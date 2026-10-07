package lockd

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/Nomadcxx/sysc-wayland/client"
	xkb "github.com/thegrumpylion/xkb-go"
	"golang.org/x/sys/unix"
)

// xkb keysyms for the specials the lock screen cares about.
const (
	symReturn    = 0xff0d
	symKP_Enter  = 0xff8d
	symBackspace = 0xff08
	symEscape    = 0xff1b
	symUp        = 0xff52
	symDown      = 0xff54
	symLeft      = 0xff51
	symRight     = 0xff53
	symF1        = 0xffbe
	symF4        = 0xffc1
	symInsert    = 0xff63
)

type keymap struct {
	state              *xkb.State
	mapData            *xkb.Keymap
	compose            *xkb.ComposeState
	depressed, latched uint32
	locked, group      uint32
	lastComposed       bool
}

func loadKeymap(fd int, size uint32) (*keymap, error) {
	if size == 0 || size > 8<<20 {
		return nil, fmt.Errorf("empty keymap")
	}
	var info unix.Stat_t
	if err := unix.Fstat(fd, &info); err != nil {
		return nil, err
	}
	if int64(size) > info.Size {
		return nil, fmt.Errorf("keymap size exceeds descriptor")
	}
	data, err := unix.Mmap(fd, 0, int(size), unix.PROT_READ, unix.MAP_SHARED)
	if err != nil {
		return nil, fmt.Errorf("keymap mmap: %w", err)
	}
	text := string(data)
	unix.Munmap(data)
	ctx := xkb.NewContext(context.Background(), xkb.ContextNoFlags)
	km, err := ctx.NewKeymapFromString([]byte(text), xkb.KeymapFormatTextV1)
	if err != nil {
		return nil, fmt.Errorf("keymap parse: %w", err)
	}
	k := &keymap{state: km.NewState(), mapData: km}
	locale := "C"
	for _, name := range []string{"LC_ALL", "LC_CTYPE", "LANG"} {
		if value := os.Getenv(name); value != "" {
			locale = value
			break
		}
	}
	if table, err := ctx.NewComposeTableFromLocale(locale, xkb.ComposeCompileNoFlags); err == nil {
		k.compose = table.NewState(xkb.ComposeStateNoFlags)
	}
	return k, nil
}

func (k *keymap) setMask(depressed, latched, locked, group uint32) {
	k.depressed, k.latched, k.locked, k.group = depressed, latched, locked, group
	k.state.UpdateMask(xkb.ModMask(depressed), xkb.ModMask(latched),
		xkb.ModMask(locked), 0, 0, xkb.Group(group))
}

const (
	modIndexShift = 0 // xkb modifier index: Shift
	modIndexCaps  = 1 // index: Lock (Caps Lock)
	modIndexCtrl  = 2 // xkb modifier index: Control
)

func (k *keymap) mods() (shift, ctrl, caps bool) {
	all := k.depressed | k.latched | k.locked
	return all&(1<<modIndexShift) != 0, all&(1<<modIndexCtrl) != 0, all&(1<<modIndexCaps) != 0
}

// resolve maps an evdev code to keysym + printable text under the current
// mask. xkb-go v0.1.0 does not apply Caps Lock to letters, so we do.
func (k *keymap) resolve(code uint32) (uint32, string) {
	k.lastComposed = false
	s := xkb.Keycode(int32(code) + 8) // evdev -> xkb keycode offset
	sym := uint32(k.state.KeyGetOneSym(s))
	text := k.state.KeyGetUTF8(s)
	// ponytail: v0.1.0 cancels compose on modifier symbols; exclude those until upstream fixes sysc-631.
	modifier := sym >= 0xffe1 && sym <= 0xffee || sym >= 0xfe01 && sym <= 0xfe13 || sym == 0xff7e || sym == 0xff7f
	if k.compose != nil && !modifier {
		if k.compose.Feed(xkb.Keysym(sym)) == xkb.ComposeFeedAccepted {
			switch k.compose.GetStatus() {
			case xkb.ComposeComposing:
				return sym, ""
			case xkb.ComposeComposed:
				k.lastComposed = true
				text = k.compose.GetUTF8()
				k.compose.Reset()
			case xkb.ComposeCancelled:
				k.compose.Reset()
				return sym, ""
			}
		}
	}
	if shift, _, caps := k.mods(); caps && text != "" {
		r, size := utf8.DecodeRuneInString(text)
		if size == len(text) && unicode.IsLetter(r) {
			if shift {
				text = string(unicode.ToLower(r))
			} else {
				text = string(unicode.ToUpper(r))
			}
		}
	}
	return sym, printable(text)
}

// printable keeps single printable runes; control chars arrive as keysyms.
func printable(s string) string {
	if !utf8.ValidString(s) {
		return ""
	}
	for _, r := range s {
		if !unicode.IsPrint(r) {
			return ""
		}
	}
	return s
}
func (k *keymap) indicators() Key {
	shift, ctrl, caps := k.mods()
	layout := k.mapData.GroupName(xkb.Group(k.group))
	if layout == "" {
		layout = "Group " + strconv.Itoa(int(k.group)+1)
	}
	return Key{Shift: shift, Ctrl: ctrl, CapsLock: caps, NumLock: (k.depressed|k.latched|k.locked)&(1<<4) != 0, Layout: layout}
}
func (c *Client) updateModifiers(depressed, latched, locked, group uint32) {
	if c.keymap == nil {
		return
	}
	c.keymap.setMask(depressed, latched, locked, group)
	if c.onKey != nil {
		c.onKey(c.keymap.indicators())
	}
}

type keyRepeat struct {
	rate  int32
	delay time.Duration
	code  uint32
	key   Key
	next  time.Time
}

func (r *keyRepeat) press(code uint32, key Key, now time.Time) {
	r.next = time.Time{}
	if r.rate <= 0 || key.Enter || key.Escape || key.Ctrl || (key.Text == "" && !key.Backspace) {
		return
	}
	r.code, r.key = code, key
	r.next = now.Add(r.delay)
}
func (r *keyRepeat) release(code uint32) {
	if r.code == code {
		r.next = time.Time{}
	}
}
func (r *keyRepeat) due(now time.Time) bool {
	if r.next.IsZero() || now.Before(r.next) || r.rate <= 0 {
		return false
	}
	r.next = now.Add(time.Second / time.Duration(r.rate))
	return true
}

func (c *Client) setupKeyboard() {
	kbd, err := c.seat.GetKeyboard()
	if err != nil {
		fmt.Fprintf(os.Stderr, "sysc-lock: no keyboard: %v\n", err)
		return
	}
	c.keyboard = kbd
	kbd.SetKeymapHandler(func(ev client.KeyboardKeymapEvent) {
		defer unix.Close(ev.Fd)
		km, err := loadKeymap(ev.Fd, ev.Size)
		if err != nil {
			fmt.Fprintf(os.Stderr, "sysc-lock: keymap: %v\n", err)
			return
		}
		c.keymap = km
		c.repeat.next = time.Time{}
		c.updateModifiers(0, 0, 0, 0)
	})
	kbd.SetModifiersHandler(func(ev client.KeyboardModifiersEvent) {
		if c.keymap != nil {
			c.updateModifiers(ev.ModsDepressed, ev.ModsLatched, ev.ModsLocked, ev.Group)
		}
	})
	kbd.SetRepeatInfoHandler(func(ev client.KeyboardRepeatInfoEvent) {
		c.repeat.rate = max(0, min(ev.Rate, 100))
		c.repeat.delay = time.Duration(max(0, min(ev.Delay, 10000))) * time.Millisecond
		if c.repeat.rate == 0 {
			c.repeat.next = time.Time{}
		}
	})
	kbd.SetLeaveHandler(func(client.KeyboardLeaveEvent) {
		c.repeat.next = time.Time{}
		// The clipboard offer is invalid once we lose the keyboard; drop and
		// destroy it (the bound manager is v3, so destroy is legal).
		if c.clipOffer != nil {
			_ = c.clipOffer.Destroy()
		}
		c.clipOffer, c.clipFormats = nil, nil
		if c.keymap != nil && c.keymap.compose != nil {
			c.keymap.compose.Reset()
		}
		// A missed release would leave a hold-to-confirm running and fire a
		// reboot; leaving the keyboard releases everything.
		c.enterCode = 0
		if c.onKey != nil {
			c.onKey(Key{Released: true})
		}
	})
	kbd.SetKeyHandler(func(ev client.KeyboardKeyEvent) {
		if ev.State != 1 {
			c.repeat.release(ev.Key)
			// resolve feeds the compose state, so a release must not call it.
			// Deliver only the Enter release the hold bar needs.
			if c.enterCode != 0 && ev.Key == c.enterCode && c.onKey != nil {
				c.enterCode = 0
				c.onKey(Key{Enter: true, Released: true})
			}
			return
		}
		if c.keymap == nil {
			return
		}
		// Paste must accept() with a serial from a keyboard event on this seat.
		c.lastKeySerial = ev.Serial
		sym, text := c.keymap.resolve(ev.Key)
		k := c.keymap.indicators()
		k.Text = text
		k.composed = c.keymap.lastComposed
		k.Backspace = sym == symBackspace
		k.Escape = sym == symEscape
		k.Enter, k.Up, k.Down, k.Left, k.Right, k.F1, k.F4, k.Insert = specials(sym)
		if k.Enter {
			c.enterCode = ev.Key
		}
		c.repeat.press(ev.Key, k, time.Now())
		if !c.keymap.mapData.KeyRepeats(xkb.Keycode(ev.Key + 8)) {
			c.repeat.next = time.Time{}
		}
		if c.onKey != nil {
			c.onKey(k)
		}
	})
}

// specials reports the key flags a keysym carries. Nothing else in this file
// knows the numeric keysyms, so the mapping is testable without a seat.
func specials(sym uint32) (enter, up, down, left, right, f1, f4, insert bool) {
	switch sym {
	case symReturn, symKP_Enter:
		enter = true
	case symUp:
		up = true
	case symDown:
		down = true
	case symLeft:
		left = true
	case symRight:
		right = true
	case symF1:
		f1 = true
	case symF4:
		f4 = true
	case symInsert:
		insert = true
	}
	return
}

func (c *Client) repeatKey() Key {
	k := c.keymap.indicators()
	if k.Ctrl {
		// Ctrl was added after the repeat armed; a repeating Ctrl+V would
		// paste the clipboard on every tick. Ctrl combos never repeat.
		return Key{Ctrl: true, CapsLock: k.CapsLock, NumLock: k.NumLock, Layout: k.Layout}
	}
	k.Backspace = c.repeat.key.Backspace
	if c.repeat.key.composed {
		k.Text = c.repeat.key.Text
	} else if !k.Backspace {
		// Re-resolve the held key against the latest group/modifiers, without feeding compose twice.
		compose := c.keymap.compose
		c.keymap.compose = nil
		_, k.Text = c.keymap.resolve(c.repeat.code)
		c.keymap.compose = compose
	}
	return k
}

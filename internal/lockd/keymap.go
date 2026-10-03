package lockd

import (
	"context"
	"fmt"
	"os"
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
)

type keymap struct {
	state              *xkb.State
	depressed, latched uint32
	locked, group      uint32
}

func loadKeymap(fd int, size uint32) (*keymap, error) {
	if size == 0 {
		return nil, fmt.Errorf("empty keymap")
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
	return &keymap{state: km.NewState()}, nil
}

func (k *keymap) setMask(depressed, latched, locked, group uint32) {
	k.depressed, k.latched, k.locked, k.group = depressed, latched, locked, group
	k.state.UpdateMask(xkb.ModMask(depressed), xkb.ModMask(latched),
		xkb.ModMask(locked), 0, 0, xkb.Group(group))
}

const (
	modIndexShift = 0 // xkb modifier index: Shift
	modIndexCaps  = 1 // index: Lock (Caps Lock)
)

func (k *keymap) mods() (shift, caps bool) {
	all := k.depressed | k.latched | k.locked
	return all&(1<<modIndexShift) != 0, all&(1<<modIndexCaps) != 0
}

// resolve maps an evdev code to keysym + printable text under the current
// mask. xkb-go v0.1.0 does not apply Caps Lock to letters, so we do. No
// compose/dead-key support: parity with swaylock's password path (ponytail).
func (k *keymap) resolve(code uint32) (uint32, string) {
	s := xkb.Keycode(int32(code) + 8) // evdev -> xkb keycode offset
	sym := uint32(k.state.KeyGetOneSym(s))
	text := k.state.KeyGetUTF8(s)
	if shift, caps := k.mods(); caps && text != "" {
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
	r, size := utf8.DecodeRuneInString(s)
	if s == "" || size != len(s) || !unicode.IsPrint(r) {
		return ""
	}
	return s
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
	})
	kbd.SetModifiersHandler(func(ev client.KeyboardModifiersEvent) {
		if c.keymap != nil {
			c.keymap.setMask(ev.ModsDepressed, ev.ModsLatched, ev.ModsLocked, ev.Group)
		}
	})
	kbd.SetKeyHandler(func(ev client.KeyboardKeyEvent) {
		if ev.State != 1 || c.keymap == nil { // 1 = pressed; repeats ignored
			return
		}
		sym, text := c.keymap.resolve(ev.Key)
		shift, caps := c.keymap.mods()
		k := Key{Text: text, Shift: shift, CapsLock: caps}
		switch sym {
		case symReturn, symKP_Enter:
			k.Enter = true
		case symBackspace:
			k.Backspace = true
		case symEscape:
			k.Escape = true
		}
		if c.onKey != nil {
			c.onKey(k)
		}
	})
}

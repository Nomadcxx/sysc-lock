package lockd

import (
	"strings"
	"time"
	"unicode"

	"github.com/Nomadcxx/sysc-wayland/client"
	"golang.org/x/sys/unix"
)

const (
	pasteTimeout  = 500 * time.Millisecond
	pasteReadCap  = 4096
	pasteReadSize = 1024
)

// textMimePreference lists the clipboard flavours we accept, best first.
var textMimePreference = []string{
	"text/plain;charset=utf-8",
	"text/plain",
	"UTF8_STRING",
}

// pickTextMime returns the preferred mime the offer advertises, "" for none.
func pickTextMime(formats []string) string {
	for _, want := range textMimePreference {
		for _, f := range formats {
			if f == want {
				return want
			}
		}
	}
	return ""
}

// sanitizePaste keeps only printable runes. Newlines (which would otherwise
// smuggle an Enter into PAM), tabs and control characters are dropped.
func sanitizePaste(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if unicode.IsPrint(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// setupDataDevice binds the seat clipboard. Missing manager or a failed
// device create only disables paste; the lock keeps working.
func (c *Client) setupDataDevice() {
	if c.dataMgr == nil || c.seat == nil {
		return
	}
	dd, err := c.dataMgr.GetDataDevice(c.seat)
	if err != nil {
		return
	}
	c.dataDevice = dd
	dd.SetDataOfferHandler(func(ev client.DataDeviceDataOfferEvent) {
		c.clipFormats = nil
		ev.Id.SetOfferHandler(func(e client.DataOfferOfferEvent) {
			c.clipFormats = append(c.clipFormats, e.MimeType)
		})
	})
	dd.SetSelectionHandler(func(ev client.DataDeviceSelectionEvent) {
		if c.clipOffer != nil {
			_ = c.clipOffer.Destroy()
		}
		c.clipOffer = ev.Id
		if ev.Id == nil {
			c.clipFormats = nil
		}
	})
}

// Paste reads the current clipboard text. It runs on the pump goroutine and
// blocks at most pasteTimeout: the data flows through a pipe written by the
// clipboard owner, not through the event loop, so the pump cannot deadlock.
// ponytail: synchronous read, one bounded stall per Ctrl+V; make it async if
// slow clipboard owners ever bite.
func (c *Client) Paste() string {
	mime := pickTextMime(c.clipFormats)
	if c.clipOffer == nil || mime == "" || c.lastKeySerial == 0 {
		return ""
	}
	pipe := make([]int, 2)
	if err := unix.Pipe2(pipe, unix.O_CLOEXEC|unix.O_NONBLOCK); err != nil {
		return ""
	}
	r, w := pipe[0], pipe[1]
	defer unix.Close(r)
	if err := c.clipOffer.Accept(c.lastKeySerial, mime); err != nil {
		unix.Close(w)
		return ""
	}
	if err := c.clipOffer.Receive(mime, w); err != nil {
		unix.Close(w)
		return ""
	}
	unix.Close(w)
	deadline := time.Now().Add(pasteTimeout)
	buf := make([]byte, pasteReadSize)
	var out []byte
	for len(out) < pasteReadCap {
		d := time.Until(deadline)
		if d <= 0 {
			break
		}
		n, err := unix.Poll([]unix.PollFd{{Fd: int32(r), Events: unix.POLLIN}}, int(d.Milliseconds())+1)
		if n <= 0 || err != nil {
			break
		}
		nr, err := unix.Read(r, buf)
		if nr <= 0 || err != nil {
			break
		}
		out = append(out, buf[:nr]...)
	}
	if len(out) > pasteReadCap {
		out = out[:pasteReadCap]
	}
	return sanitizePaste(string(out))
}

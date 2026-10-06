package lockd

import (
	"io"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Nomadcxx/sysc-lock/internal/input"
	"github.com/Nomadcxx/sysc-wayland/client"
	"golang.org/x/sys/unix"
)

func (c *Client) setupClipboard() {
	if c.dataDev != nil || c.dataMgr == nil || c.seat == nil {
		return
	}
	dev, err := c.dataMgr.GetDataDevice(c.seat)
	if err != nil {
		return
	}
	c.dataDev = dev
	c.offerMimes = map[*client.DataOffer][]string{}
	dev.SetDataOfferHandler(func(ev client.DataDeviceDataOfferEvent) {
		offer := ev.Id
		c.offerMimes[offer] = nil
		offer.SetOfferHandler(func(e client.DataOfferOfferEvent) {
			c.offerMimes[offer] = append(c.offerMimes[offer], e.MimeType)
		})
	})
	dev.SetSelectionHandler(func(ev client.DataDeviceSelectionEvent) {
		if c.clip != nil && c.clip != ev.Id {
			_ = c.clip.Destroy()
		}
		c.clip, c.clipMime = ev.Id, pickMime(c.offerMimes[ev.Id])
	})
}

func pickMime(offered []string) string {
	for _, want := range []string{"text/plain;charset=utf-8", "text/plain", "UTF8_STRING", "TEXT"} {
		for _, got := range offered {
			if strings.EqualFold(got, want) {
				return got
			}
		}
	}
	return ""
}

func (c *Client) requestPaste() {
	if c.pasting || c.clip == nil || c.clipMime == "" {
		return
	}
	p := make([]int, 2)
	if err := unix.Pipe2(p, unix.O_CLOEXEC); err != nil {
		return
	}
	r, w := p[0], p[1]
	if err := c.clip.Receive(c.clipMime, w); err != nil {
		unix.Close(r)
		unix.Close(w)
		return
	}
	unix.Close(w)
	c.pasting = true
	go func() {
		f := os.NewFile(uintptr(r), "paste")
		ch := make(chan struct {
			s string
			e error
		}, 1)
		go func() {
			s, e := readPaste(f)
			ch <- struct {
				s string
				e error
			}{s, e}
		}()
		var text string
		var err error
		select {
		case got := <-ch:
			text, err = got.s, got.e
		case <-time.After(2 * time.Second):
			f.Close()
			<-ch
			err = os.ErrDeadlineExceeded
		}
		_ = f.Close()
		c.Post(func() {
			c.pasting = false
			if err != nil || text == "" || c.onKey == nil {
				return
			}
			c.onKey(Key{Text: text, Paste: true})
		})
	}()
}

func readPaste(r io.Reader) (string, error) {
	body, err := io.ReadAll(io.LimitReader(r, int64(input.MaxPasswordBytes)+1))
	if err != nil {
		return "", err
	}
	if len(body) > input.MaxPasswordBytes {
		return "", input.ErrLength
	}
	if !utf8.Valid(body) {
		return "", input.ErrEncoding
	}
	return string(body), nil
}

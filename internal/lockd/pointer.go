package lockd

import (
	"github.com/Nomadcxx/sysc-wayland/client"
)

func (c *Client) setupPointer() {
	p, err := c.seat.GetPointer()
	if err != nil {
		c.failUI(err)
		return
	}
	c.pointer = p
	p.SetEnterHandler(func(ev client.PointerEnterEvent) {
		c.pointerOut = nil
		for _, out := range c.outputs {
			if out.surface == ev.Surface {
				c.pointerOut = out
				break
			}
		}
		c.pointerX, c.pointerY = ev.SurfaceX, ev.SurfaceY
	})
	p.SetLeaveHandler(func(client.PointerLeaveEvent) { c.pointerOut = nil })
	p.SetMotionHandler(func(ev client.PointerMotionEvent) { c.pointerX, c.pointerY = ev.SurfaceX, ev.SurfaceY })
	p.SetButtonHandler(func(ev client.PointerButtonEvent) {
		out := c.pointerOut
		if out == nil || out.removed || ev.State != 1 || ev.Button != 0x110 {
			return
		}
		if c.onKey != nil {
			k := Key{}
			if c.keymap != nil {
				k = c.keymap.indicators()
			}
			c.onKey(k) // a click only reveals the entry; Enter submits
		}
	})
}

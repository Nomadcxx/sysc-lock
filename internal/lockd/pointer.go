package lockd

import (
	"github.com/Nomadcxx/sysc-wayland/client"
	"image"
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
		_, _, scale, err := out.geometry()
		if err != nil {
			return
		}
		panel := PanelGeometry(out.w, out.h, scale)
		point := image.Pt(int(c.pointerX*scale), int(c.pointerY*scale))
		if point.In(panel.Unlock) && c.onKey != nil {
			k := Key{Enter: true}
			if c.keymap != nil {
				k = c.keymap.indicators()
				k.Enter = true
			}
			c.onKey(k)
		}
	})
}

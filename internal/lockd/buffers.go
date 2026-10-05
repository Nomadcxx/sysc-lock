package lockd

import (
	"fmt"
	"github.com/Nomadcxx/sysc-wayland/client"
	"golang.org/x/sys/unix"
	"math"
	"time"
)

const maxPixelBytes = 256 << 20

func bufferBytes(w, h int) (int, error) {
	if w <= 0 || h <= 0 || w > math.MaxInt32/4 || h > math.MaxInt32 || int64(w)*4*int64(h) > maxPixelBytes {
		return 0, fmt.Errorf("lock geometry exceeds pixel budget")
	}
	return w * 4 * h, nil
}
func (out *lockOut) availableBuffer(w, h int) *shmBuffer {
	for _, b := range out.buffers {
		if !b.busy && !b.retired && b.w == w && b.h == h {
			return b
		}
	}
	return nil
}
func (out *lockOut) canResize(w, h int) bool {
	for _, b := range out.buffers {
		if b.retired && b.busy {
			return false
		}
	}
	return true
}
func (c *Client) pixelBytes() int {
	total := 0
	if c.wallpaper != nil {
		total += wallpaperReservation
	}
	count := func(out *lockOut) {
		for _, b := range out.buffers {
			total += len(b.data)
		}
		if out.background != nil {
			total += out.background.storageBytes
		}
	}
	for _, out := range c.outputs {
		count(out)
	}
	for _, out := range c.removed {
		count(out)
	}
	return total
}
func (c *Client) allocateBuffer(out *lockOut, w, h int) (*shmBuffer, error) {
	size, err := bufferBytes(w, h)
	if err != nil {
		return nil, err
	}
	if size > maxPixelBytes-c.pixelBytes() {
		return nil, fmt.Errorf("aggregate lock pixel budget exhausted")
	}
	fd, err := unix.MemfdCreate("sysc-lock", unix.MFD_CLOEXEC)
	if err != nil {
		return nil, err
	}
	defer unix.Close(fd)
	if err = unix.Ftruncate(fd, int64(size)); err != nil {
		return nil, err
	}
	data, err := unix.Mmap(fd, 0, size, unix.PROT_READ|unix.PROT_WRITE, unix.MAP_SHARED)
	if err != nil {
		return nil, err
	}
	pool, err := c.shm.CreatePool(fd, int32(size))
	if err != nil {
		unix.Munmap(data)
		return nil, err
	}
	buf, err := pool.CreateBuffer(0, int32(w), int32(h), int32(w*4), c.shmFmt)
	if err != nil {
		pool.Destroy()
		unix.Munmap(data)
		return nil, err
	}
	sb := &shmBuffer{pool: pool, buf: buf, data: data, id: out.id, w: w, h: h}
	buf.SetReleaseHandler(func(client.BufferReleaseEvent) { c.releaseBuffer(out, sb) })
	out.buffers = append(out.buffers, sb)
	return sb, nil
}
func (c *Client) prepareBuffers(out *lockOut, w, h int) (bool, error) {
	for _, b := range out.buffers {
		if !b.retired && b.w == w && b.h == h {
			return true, nil
		}
	}
	if !out.canResize(w, h) {
		return false, nil
	}
	old := append([]*shmBuffer(nil), out.buffers...)
	for _, b := range old {
		b.retired = true
		if !b.busy {
			c.freeBuffer(out, b)
		}
	}
	size, err := bufferBytes(w, h)
	if err != nil {
		return false, err
	}
	if err := c.reserveForeground(2 * size); err != nil {
		return false, err
	}
	for range 2 {
		if _, err = c.allocateBuffer(out, w, h); err != nil {
			c.discardNewBuffers(out)
			return false, err
		}
	}
	return true, nil
}
func (c *Client) collectRemoved() {
	retained := c.removed[:0]
	for _, out := range c.removed {
		keep := len(out.buffers) > 0
		if b := out.background; b != nil && b.done != nil {
			select {
			case <-b.done:
			default:
				keep = true
			}
		}
		if keep {
			retained = append(retained, out)
		}
	}
	c.removed = retained
}

func (c *Client) reserveForeground(bytes int) error {
	if bytes <= maxPixelBytes-c.pixelBytes() {
		return nil
	}
	// Decoration never takes admission priority over opaque input surfaces.
	for _, out := range c.outputs {
		if b := out.background; b != nil {
			b.failed = true
			b.jobDeadline = time.Time{}
			b.stop()
			c.releaseStoppedBackground(b)
		}
	}
	c.collectRemoved()
	c.releaseWallpaperIfStopped()
	if bytes > maxPixelBytes-c.pixelBytes() {
		return fmt.Errorf("lock generation exceeds aggregate budget")
	}
	return nil
}

func (c *Client) discardNewBuffers(out *lockOut) {
	for _, b := range append([]*shmBuffer(nil), out.buffers...) {
		if !b.retired {
			c.freeBuffer(out, b)
		}
	}
}
func (c *Client) releaseBuffer(out *lockOut, sb *shmBuffer) {
	sb.busy = false
	if sb.retired || out.removed {
		c.freeBuffer(out, sb)
	}
	if !out.removed && out.pending {
		c.paint(out)
	}
	c.collectRemoved()
}

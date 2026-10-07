package lockd

import (
	"github.com/Nomadcxx/sysc-lock/internal/lockd/screencopy"
	"github.com/Nomadcxx/sysc-lock/internal/render"
	"github.com/Nomadcxx/sysc-wayland/client"
	"golang.org/x/sys/unix"
)

const (
	maxCaptureBytes       = 64 << 20
	backdropDownsample    = 4
	captureRoundtripLimit = 32
)

// EnableBlur turns the frozen screencopy backdrop on or off. The radius
// is in output pixels and clamped by the config loader.
// upscaleBackdrop stretches the reduced backdrop to full size with
// nearest-neighbour sampling; the pixels are already blurred, so blocky
// is free.
func (out *lockOut) upscaleBackdrop(dst []byte) {
	src := out.backdrop
	for y := 0; y < out.h; y++ {
		sy := y * src.Height / out.h
		for x := 0; x < out.w; x++ {
			s := sy*src.Stride + (x*src.Width/out.w)*4
			d := (y*out.w + x) * 4
			copy(dst[d:d+4], src.Pix[s:s+4])
		}
	}
}

func (c *Client) EnableBlur(enabled bool, radius int) {
	c.blur, c.blurRadius = enabled, radius
}

// CaptureBlur grabs one frame per output and keeps the blurred copy.
// It must run before Lock: after the session locks, compositor surfaces
// go black. Every failure path returns silently to the plain backdrop —
// the capture is decoration and never decides whether locking succeeds.
func (c *Client) CaptureBlur() {
	// A manual wallpaper is an explicit override; it wins over capture.
	if !c.blur || c.screencopy == nil || c.shm == nil || c.wallpaper != nil {
		return
	}
	for _, out := range c.outputs {
		c.captureOutput(out)
	}
}

func (c *Client) captureOutput(out *lockOut) {
	if out.removed || out.output == nil {
		return
	}
	frame, err := c.screencopy.CaptureOutput(0, out.output)
	if err != nil {
		return
	}
	defer frame.Destroy()
	var (
		offered, enumerated, copied, finished, yInvert bool
		format, width, height, stride                  uint32
	)
	frame.SetBufferHandler(func(e screencopy.ZwlrScreencopyFrameV1BufferEvent) {
		if offered || !captureBufferFits(e.Format, e.Width, e.Height, e.Stride, maxPixelBytes-c.pixelBytes()) {
			return
		}
		offered = true
		format, width, height, stride = e.Format, e.Width, e.Height, e.Stride
	})
	frame.SetBufferDoneHandler(func(screencopy.ZwlrScreencopyFrameV1BufferDoneEvent) {
		enumerated = true
	})
	frame.SetFlagsHandler(func(e screencopy.ZwlrScreencopyFrameV1FlagsEvent) {
		yInvert = e.Flags&uint32(screencopy.ZwlrScreencopyFrameV1FlagsYInvert) != 0
	})
	frame.SetReadyHandler(func(screencopy.ZwlrScreencopyFrameV1ReadyEvent) {
		copied, finished = true, true
	})
	frame.SetFailedHandler(func(screencopy.ZwlrScreencopyFrameV1FailedEvent) {
		copied, finished = false, true
	})
	// v3 ends the offer list with buffer_done; the first offer is not
	// necessarily one we can use.
	if !c.pumpUntil(func() bool { return enumerated || finished }) || !offered {
		return
	}
	buf := newCaptureBuffer(c.shm, int32(width), int32(height), format)
	if buf == nil {
		return
	}
	defer buf.destroy()
	if frame.Copy(buf.buffer) != nil {
		return
	}
	if !c.pumpUntil(func() bool { return finished }) || !copied {
		return
	}
	fullPix := make([]byte, int(width)*4*int(height))
	normaliseCapture(fullPix, buf.data, int(width), int(height), int(stride),
		yInvert, format == uint32(client.ShmFormatXrgb8888))
	fb := &render.Framebuffer{Width: int(width), Height: int(height), Stride: int(width) * 4, Pix: fullPix}
	blur := render.Blur(fb, backdropDownsample, c.blurRadius)
	clear(fullPix)
	if blur == nil {
		return
	}
	out.backdrop, out.blurW, out.blurH = blur, int(width), int(height)
}

// pumpUntil spins roundtrips until ready or the connection dies. Safe
// only before Run: the pump goroutine must never re-enter dispatch.
func (c *Client) pumpUntil(ready func() bool) bool {
	for i := 0; i < captureRoundtripLimit; i++ {
		if ready() {
			return true
		}
		if c.fatal != nil || c.display.Roundtrip() != nil {
			return false
		}
	}
	return ready()
}

// captureBufferFits reports whether we can allocate a matching shm buffer.
// Feeding Copy a mismatched buffer is a protocol error, so declined
// offers simply fall through to the plain backdrop.
func captureBufferFits(format, width, height, stride uint32, remaining int) bool {
	size, err := bufferBytes(int(width), int(height))
	if err != nil || size > maxCaptureBytes || stride != width*4 {
		return false
	}
	// The offer supplies dimensions before any lock surface is configured.
	// Budget shm + normalization + reduced backdrop + blur scratch lines.
	rw := (int(width) + backdropDownsample - 1) / backdropDownsample
	rh := (int(height) + backdropDownsample - 1) / backdropDownsample
	if 2*size+rw*rh*4+3*(rw+rh)*4 > remaining {
		return false
	}
	switch format {
	case uint32(client.ShmFormatArgb8888), uint32(client.ShmFormatXrgb8888):
		return true
	}
	return false
}

// normaliseCapture copies stride-wide rows into tightly packed dst
// rows, flipping vertically when the compositor offers Y-inverted
// frames, and forcing opaque where the format leaves alpha undefined.
func normaliseCapture(dst, src []byte, width, height, stride int, yInvert, forceOpaque bool) {
	row := width * 4
	if stride < row || len(src) < height*stride || len(dst) < height*row {
		return
	}
	for y := 0; y < height; y++ {
		s := y
		if yInvert {
			s = height - 1 - y
		}
		copy(dst[y*row:(y+1)*row], src[s*stride:s*stride+row])
	}
	if forceOpaque {
		for i := 3; i < len(dst); i += 4 {
			dst[i] = 0xff
		}
	}
}

type captureBuffer struct {
	fd     int
	data   []byte
	pool   *client.ShmPool
	buffer *client.Buffer
}

func newCaptureBuffer(shm *client.Shm, width, height int32, format uint32) *captureBuffer {
	stride := width * 4
	size := int64(stride) * int64(height)
	if size > maxCaptureBytes || size <= 0 {
		return nil
	}
	fd, err := unix.MemfdCreate("sysc-lock-capture", unix.MFD_CLOEXEC)
	if err != nil {
		return nil
	}
	if unix.Ftruncate(fd, size) != nil {
		unix.Close(fd)
		return nil
	}
	data, err := unix.Mmap(fd, 0, int(size), unix.PROT_READ|unix.PROT_WRITE, unix.MAP_SHARED)
	if err != nil {
		unix.Close(fd)
		return nil
	}
	// one buffer per pool: screencopy rejects pool-backed buffers whose
	// pool spans more than the target buffer.
	pool, err := shm.CreatePool(fd, int32(size))
	if err != nil {
		unix.Munmap(data)
		unix.Close(fd)
		return nil
	}
	buffer, err := pool.CreateBuffer(0, width, height, stride, format)
	if err != nil {
		pool.Destroy()
		unix.Munmap(data)
		unix.Close(fd)
		return nil
	}
	return &captureBuffer{fd: fd, data: data, pool: pool, buffer: buffer}
}

func (b *captureBuffer) destroy() {
	if b.buffer != nil {
		b.buffer.Destroy()
	}
	if b.pool != nil {
		b.pool.Destroy()
	}
	// zero the pixels before unmapping; the capture held live desktop art
	if b.data != nil {
		clear(b.data)
		unix.Munmap(b.data)
	}
	unix.Close(b.fd)
}

package lockd

import (
	"bytes"
	"encoding/binary"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Nomadcxx/sysc-lock/internal/lockd/screencopy"
	"github.com/Nomadcxx/sysc-lock/internal/render"
	"github.com/Nomadcxx/sysc-lock/internal/theme"
	"github.com/Nomadcxx/sysc-wayland/client"
	"github.com/Nomadcxx/sysc-wayland/sessionlock"
)

// This check captures into memory before any lock request, never into a file.
func TestLivePrelockBlurCapture(t *testing.T) {
	if os.Getenv("SYSC_LOCK_LIVE_CAPTURE_TEST") != "1" {
		t.Skip("explicit live capture check only")
	}
	c, err := Connect(New(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.EnableBlur(true, 24)
	c.CaptureBlur()
	if len(c.outputs) == 0 {
		t.Fatal("no outputs")
	}
	for id, out := range c.outputs {
		if out.backdrop == nil {
			t.Fatalf("output %d has no pre-lock backdrop", id)
		}
		t.Logf("output %d captured %dx%d, reduced to %dx%d", id, out.blurW, out.blurH, out.backdrop.Width, out.backdrop.Height)
	}
}

func TestCaptureBeforeLockSurfaceConfigure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wayland-test")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	request := make(chan []byte, 1)
	go func() {
		conn, err := listener.AcceptUnix()
		if err != nil {
			request <- nil
			return
		}
		defer conn.Close()
		conn.SetReadDeadline(time.Now().Add(time.Second))
		packet := make([]byte, 20)
		if _, err := io.ReadFull(conn, packet); err != nil {
			request <- nil
			return
		}
		request <- packet
		// Closing the peer makes the decoration fail safely after the request.
	}()
	display, err := client.Connect(path)
	if err != nil {
		t.Fatal(err)
	}
	defer display.Context().Close()
	manager := screencopy.NewZwlrScreencopyManagerV1(display.Context())
	output := client.NewOutput(display.Context())
	c := &Client{display: display, screencopy: manager}
	c.captureOutput(&lockOut{output: output}, time.Now().Add(captureBudget)) // w/h are unknown until Lock.
	packet := <-request
	if len(packet) != 20 || binary.NativeEndian.Uint32(packet[:4]) != manager.ID() {
		t.Fatal("pre-lock capture did not request the compositor's geometry")
	}
}

func TestHeldCaptureFallsBackAndStillRequestsLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wayland-test")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	lockRequest := make(chan []byte, 1)
	display, err := client.Connect(path)
	if err != nil {
		t.Fatal(err)
	}
	defer display.Context().Close()
	ctx := display.Context()
	displayID := display.ID()
	output := client.NewOutput(ctx)
	c := &Client{
		display: display, state: New(),
		outputs:    map[OutputID]*lockOut{1: {output: output}},
		screencopy: screencopy.NewZwlrScreencopyManagerV1(ctx),
		shm:        client.NewShm(ctx),
		compositor: client.NewCompositor(ctx),
		mgr:        sessionlock.NewExtSessionLockManagerV1(ctx),
	}
	lockManagerID := c.mgr.ID()
	go func() {
		conn, err := listener.AcceptUnix()
		if err != nil {
			return
		}
		defer conn.Close()
		var syncCallbackID uint32
		for {
			packet, err := readWaylandFrame(conn)
			if err != nil {
				return
			}
			senderID := binary.NativeEndian.Uint32(packet[:4])
			opcode := binary.NativeEndian.Uint32(packet[4:8]) & 0xffff
			if senderID == displayID && opcode == 0 {
				syncCallbackID = binary.NativeEndian.Uint32(packet[8:12])
			}
			if senderID == lockManagerID && opcode == 1 {
				if syncCallbackID == 0 {
					lockRequest <- nil
					return
				}
				// A timed-out sync may still reply after the lock request. Its
				// zombie proxy must consume that event without poisoning dispatch.
				var event [12]byte
				binary.NativeEndian.PutUint32(event[:4], syncCallbackID)
				binary.NativeEndian.PutUint32(event[4:8], 12<<16)
				if _, err := conn.Write(event[:]); err != nil {
					return
				}
				lockRequest <- packet
				continue
			}
		}
	}()
	c.EnableBlur(true, 24)
	out := c.outputs[1]
	started := time.Now()
	captureDone := make(chan struct{})
	go func() {
		c.CaptureBlur()
		close(captureDone)
	}()
	select {
	case <-captureDone:
	case <-time.After(250 * time.Millisecond):
		_ = display.Context().Close()
		select {
		case <-captureDone:
		case <-time.After(time.Second):
			t.Fatal("capture stayed blocked after closing the mock display")
		}
		t.Fatal("decorative capture did not yield when the compositor withheld its sync response")
	}
	if elapsed := time.Since(started); elapsed > 250*time.Millisecond {
		t.Fatalf("decorative capture took %v with no compositor replies", elapsed)
	}
	if out.backdrop != nil {
		t.Fatal("capture without a compositor response did not fall back to opaque")
	}
	if err := c.Lock(); err != nil {
		t.Fatalf("lock request after capture timeout: %v", err)
	}
	select {
	case packet := <-lockRequest:
		if binary.NativeEndian.Uint32(packet[4:8])&0xffff != 1 {
			t.Fatalf("lock opcode = %d, want 1", binary.NativeEndian.Uint32(packet[4:8])&0xffff)
		}
	case <-time.After(time.Second):
		t.Fatal("capture timeout left the protocol stream unable to send the lock request")
	}
	if err := ctx.Dispatch(); err != nil {
		t.Fatalf("late sync reply after capture timeout: %v", err)
	}
}

func readWaylandFrame(conn *net.UnixConn) ([]byte, error) {
	header := make([]byte, 8)
	if _, err := io.ReadFull(conn, header); err != nil {
		return nil, err
	}
	size := binary.NativeEndian.Uint32(header[4:8]) >> 16
	if size < 8 {
		return nil, io.ErrUnexpectedEOF
	}
	packet := make([]byte, int(size))
	copy(packet, header)
	_, err := io.ReadFull(conn, packet[8:])
	return packet, err
}

func TestCaptureBufferFits(t *testing.T) {
	argb := uint32(client.ShmFormatArgb8888)
	xrgb := uint32(client.ShmFormatXrgb8888)
	if !captureBufferFits(argb, 8, 4, 32, maxPixelBytes) || !captureBufferFits(xrgb, 8, 4, 32, maxPixelBytes) {
		t.Fatal("32-bit formats must be accepted")
	}
	if captureBufferFits(0x38415258, 8, 4, 32, maxPixelBytes) {
		t.Fatal("foreign fourcc accepted")
	}
	if captureBufferFits(argb, 8, 4, 40, maxPixelBytes) {
		t.Fatal("padded stride accepted")
	}
	if captureBufferFits(argb, 0, 4, 16, maxPixelBytes) {
		t.Fatal("zero width accepted")
	}
	if captureBufferFits(argb, 8, 4, 32, 256) {
		t.Fatal("transient pixels over remaining budget accepted")
	}
	if captureBufferFits(argb, 1<<30|1, 4, 4, maxPixelBytes) {
		t.Fatal("overflowed width accepted")
	}
	if captureBufferFits(argb, 8192, 8192, 8192*4, maxPixelBytes) {
		t.Fatal("capture larger than the allocation cap accepted")
	}
}

func TestNormaliseCaptureInvertsAndSealsAlpha(t *testing.T) {
	// Two rows of two pixels at stride 12; the trailing junk must be skipped.
	src := []byte{
		1, 2, 3, 0, 4, 5, 6, 0, 9, 9, 9, 9,
		11, 12, 13, 14, 21, 22, 23, 24, 9, 9, 9, 9,
	}
	dst := make([]byte, 16)
	normaliseCapture(dst, src, 2, 2, 12, true, true)
	want := []byte{11, 12, 13, 0xff, 21, 22, 23, 0xff, 1, 2, 3, 0xff, 4, 5, 6, 0xff}
	for i := range want {
		if dst[i] != want[i] {
			t.Fatalf("y-invert/alpha wrong: got %v want %v", dst, want)
		}
	}
	normaliseCapture(dst, src[:23], 2, 2, 12, false, false)
	if dst[0] != 11 {
		t.Fatal("short source rewrote dst")
	}
}

func TestUpscaleBackdropNearestNeighbour(t *testing.T) {
	src := render.New(2, 2)
	src.Pix[0], src.Pix[4], src.Pix[8], src.Pix[12] = 10, 20, 30, 40
	out := &lockOut{w: 4, h: 4, backdrop: src}
	dst := make([]byte, 4*4*4)
	out.upscaleBackdrop(dst)
	at := func(x, y int) byte { return dst[(y*4+x)*4] }
	if at(0, 0) != 10 || at(1, 0) != 10 || at(2, 0) != 20 || at(3, 3) != 40 || at(0, 2) != 30 {
		t.Fatalf("nearest stretch wrong: %v %v %v %v %v", at(0, 0), at(1, 0), at(2, 0), at(3, 3), at(0, 2))
	}
}

func TestUpscaleBackdropPreservesOddGeometryPixels(t *testing.T) {
	for _, shape := range [][4]int{{2, 3, 7, 11}, {5, 7, 1, 2}, {3, 2, 13, 7}} {
		src := render.New(shape[0], shape[1])
		for i := range src.Pix {
			src.Pix[i] = byte(i * 31)
		}
		out := &lockOut{w: shape[2], h: shape[3], backdrop: src}
		dst := make([]byte, out.w*out.h*4)
		out.upscaleBackdrop(dst)
		for y := range out.h {
			for x := range out.w {
				s := (y*src.Height/out.h)*src.Stride + (x*src.Width/out.w)*4
				d := (y*out.w + x) * 4
				for c := range 4 {
					if dst[d+c] != src.Pix[s+c] {
						t.Fatalf("shape %v pixel %d,%d channel %d changed", shape, x, y, c)
					}
				}
			}
		}
	}
}

func BenchmarkUpscaleBackdrop1080p(b *testing.B) {
	out := &lockOut{w: 1920, h: 1080, backdrop: render.New(480, 270)}
	dst := make([]byte, out.w*out.h*4)
	b.ReportAllocs()
	for b.Loop() {
		out.upscaleBackdrop(dst)
	}
}

func TestDimBeforeUpscalePreservesPixels(t *testing.T) {
	for _, shape := range [][4]int{{2, 3, 7, 11}, {5, 7, 13, 9}, {3, 2, 13, 7}} {
		src := render.New(shape[0], shape[1])
		for i := range src.Pix {
			src.Pix[i] = byte(i*31 + 17)
		}
		wantOut := &lockOut{w: shape[2], h: shape[3], backdrop: src}
		want := make([]byte, shape[2]*shape[3]*4)
		wantOut.upscaleBackdrop(want)
		DimBackground(want)

		dimmed := render.New(shape[0], shape[1])
		copy(dimmed.Pix, src.Pix)
		DimBackground(dimmed.Pix)
		got := make([]byte, shape[2]*shape[3]*4)
		(&lockOut{w: shape[2], h: shape[3], backdrop: dimmed}).upscaleBackdrop(got)
		if !bytes.Equal(got, want) {
			t.Fatalf("dim before upscale changed pixels for %v", shape)
		}
	}
}

func BenchmarkFrozenBackdropRepaint(b *testing.B) {
	// Captured blur is dimmed at reduced resolution before it is retained.
	for _, output := range []struct {
		name   string
		width  int
		height int
	}{{"DP-1", 3440, 1440}, {"DP-3", 2560, 1440}} {
		b.Run(output.name, func(b *testing.B) {
			w, h := output.width, output.height
			out := &lockOut{w: w, h: h, backdrop: render.New((w+3)/4, (h+3)/4)}
			for i := 0; i < len(out.backdrop.Pix); i += 4 {
				out.backdrop.Pix[i], out.backdrop.Pix[i+1], out.backdrop.Pix[i+2], out.backdrop.Pix[i+3] = 96, 72, 48, 255
			}
			DimBackground(out.backdrop.Pix)
			fb := render.New(w, h)
			view := NewView(theme.Default(), "user", "host")
			view.Reduced = true
			b.ReportAllocs()
			b.SetBytes(int64(w * h * 4))
			b.ResetTimer()
			for b.Loop() {
				out.upscaleBackdrop(fb.Pix)
				view.RenderForeground(fb, time.Now())
			}
		})
	}
}

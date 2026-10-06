package lockd

import (
	"fmt"
	"github.com/Nomadcxx/sysc-lock/internal/lockd/fractionalscale"
	"github.com/Nomadcxx/sysc-lock/internal/lockd/viewporter"
	"github.com/Nomadcxx/sysc-lock/internal/render"
	"os"
	"sync"
	"syscall"
	"time"

	"github.com/Nomadcxx/sysc-wayland/client"
	"github.com/Nomadcxx/sysc-wayland/sessionlock"
	"golang.org/x/sys/unix"
)

// Key is one pressed key, decoded through the wayland keymap before it
// reaches the input model. Text carries the printable result ("" for control
// keys). Runs on the pump goroutine.
type Key struct {
	Text      string
	Enter     bool
	Backspace bool
	Escape    bool
	Shift     bool
	Ctrl      bool
	CapsLock  bool
	NumLock   bool
	Layout    string
	composed  bool // a completed compose sequence repeats its committed character
	Up        bool
	Down      bool
	F4        bool
	// Released marks a key going up. Enter releases drive the hold-to-confirm
	// bar, and the keyboard-leave event releases every key at once so a missed
	// release can never leave a hold running.
	Released bool
}

// KeyFunc receives every pressed key.
type KeyFunc func(Key)

// FrameFunc composes foreground into caller-owned opaque BGRA storage.
type FrameFunc func(fb *render.Framebuffer, scale float64, background []byte) error

// Client owns the Wayland connection, the session lock, and one lock surface
// per output. All protocol mutations run on the single pump goroutine; other
// goroutines must go through Post.
type Client struct {
	display      *client.Display
	registry     *client.Registry
	state        *State
	onKey        KeyFunc
	frame        FrameFunc
	BeforeUnlock func() error   // persistent owner atomically admits authenticated unlock
	OnEvent      func(Snapshot) // called only on the Wayland owner
	fatal        error
	uiError      string
	lastSnapshot *Snapshot

	compositor *client.Compositor
	shm        *client.Shm
	shmFmt     uint32
	haveFmt    bool
	seat       *client.Seat
	dataMgr    *client.DataDeviceManager
	dataDevice *client.DataDevice
	clipOffer  *client.DataOffer
	// clipFormats is the mime list of the current clipboard offer.
	clipFormats   []string
	lastKeySerial uint32
	keymap        *keymap
	keyboard      *client.Keyboard
	// enterCode is the keysym of the Enter key that is currently down, so its
	// release can be delivered. Zero means Enter is not down.
	enterCode          uint32
	pointer            *client.Pointer
	pointerOut         *lockOut
	pointerX, pointerY float64
	repeat             keyRepeat
	requestDeadline    time.Time
	mgr                *sessionlock.ExtSessionLockManagerV1
	lock               *sessionlock.ExtSessionLockV1
	outputs            map[OutputID]*lockOut
	removed            []*lockOut
	viewporter         *viewporter.WpViewporter
	scaleManager       *fractionalscale.WpFractionalScaleManagerV1
	effect, palette    string
	wallpaper          *wallpaperAsset
	reduced, frozen    bool
	effectEvery        time.Duration
	resumeAt           time.Time
	OnDeadline         func(time.Time) time.Time
	deadline           time.Time
	closed             bool

	wlFD   int
	wakeR  int
	wakeW  int
	pendMu sync.Mutex
	pend   []func()
}

type lockOut struct {
	id                 OutputID
	output             *client.Output
	scale              int32
	surface            *client.Surface
	lockSurf           *sessionlock.ExtSessionLockSurfaceV1
	w, h               int // pixel size from the last configure ack
	committed          bool
	buffers            []*shmBuffer
	logicalW, logicalH int
	scale120           uint32
	viewport           *viewporter.WpViewport
	fractional         *fractionalscale.WpFractionalScaleV1
	pending, removed   bool
	callback           *client.Callback
	lastFrame          time.Time
	background         *backgroundWorker
}

type shmBuffer struct {
	pool          *client.ShmPool
	buf           *client.Buffer
	data          []byte
	id            OutputID
	w, h          int
	busy, retired bool
}

func Connect(state *State, onKey KeyFunc, frame FrameFunc) (*Client, error) {
	display, err := client.Connect(os.Getenv("WAYLAND_DISPLAY"))
	if err != nil {
		return nil, fmt.Errorf("wayland connect: %w", err)
	}
	c := &Client{
		display: display,
		state:   state,
		onKey:   onKey,
		frame:   frame,
		outputs: make(map[OutputID]*lockOut),
	}
	ctx := display.Context()
	if err := ctx.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		ctx.Close()
		return nil, err
	}
	reg, err := display.GetRegistry()
	if err != nil {
		ctx.Close()
		return nil, fmt.Errorf("get_registry: %w", err)
	}
	c.registry = reg
	c.registry.SetGlobalHandler(func(g client.RegistryGlobalEvent) { c.global(g) })
	c.registry.SetGlobalRemoveHandler(func(g client.RegistryGlobalRemoveEvent) { c.removeOutput(OutputID(g.Name)) })
	if err := display.Roundtrip(); err != nil {
		ctx.Close()
		return nil, fmt.Errorf("wayland roundtrip: %w", err)
	}
	if c.mgr == nil {
		ctx.Close()
		return nil, fmt.Errorf("compositor exposes no ext_session_lock_manager_v1")
	}
	if c.compositor == nil || c.shm == nil || c.seat == nil {
		ctx.Close()
		return nil, fmt.Errorf("compositor exposes no compositor/shm/seat")
	}
	c.setupDataDevice()
	if err := ctx.SetReadDeadline(time.Time{}); err != nil {
		ctx.Close()
		return nil, err
	}
	var w int
	if err := ctx.ControlFD(func(fd int) error {
		wl, err := unix.FcntlInt(uintptr(fd), unix.F_DUPFD_CLOEXEC, 0)
		if err == nil {
			w = wl
		}
		return err
	}); err != nil {
		ctx.Close()
		return nil, fmt.Errorf("control fd: %w", err)
	}
	c.wlFD = w
	pipe := make([]int, 2)
	if err := unix.Pipe2(pipe, unix.O_CLOEXEC|unix.O_NONBLOCK); err != nil {
		unix.Close(w)
		ctx.Close()
		return nil, fmt.Errorf("wake pipe: %w", err)
	}
	c.wakeR, c.wakeW = pipe[0], pipe[1]
	return c, nil
}

func (c *Client) global(g client.RegistryGlobalEvent) {
	ctx := c.display.Context()
	// ponytail: cap per-interface versions at what we actually use; raise
	// when a feature needs more (wl_compositor surfaces need >=3 for
	// set_buffer_scale, outputs >=4 for the name event).
	bind := func(id client.Proxy, want uint32) error {
		v := g.Version
		if v > want {
			v = want
		}
		return c.registry.Bind(g.Name, g.Interface, v, id)
	}
	switch g.Interface {
	case "wl_compositor":
		if c.compositor == nil {
			o := client.NewCompositor(ctx)
			if bind(o, 6) == nil {
				c.compositor = o
			}
		}
	case "wl_shm":
		if c.shm == nil {
			o := client.NewShm(ctx)
			if bind(o, 1) == nil {
				o.SetFormatHandler(func(ev client.ShmFormatEvent) {
					if ev.Format == 0 { // ARGB8888
						c.shmFmt, c.haveFmt = ev.Format, true
					}
				})
				c.shm = o
			}
		}
	case "wl_seat":
		if c.seat == nil {
			o := client.NewSeat(ctx)
			if bind(o, 5) == nil {
				c.seat = o
				o.SetCapabilitiesHandler(func(ev client.SeatCapabilitiesEvent) {
					if ev.Capabilities&2 != 0 && c.keyboard == nil {
						c.setupKeyboard()
					}
					if ev.Capabilities&1 != 0 && c.pointer == nil {
						c.setupPointer()
					}
					if ev.Capabilities&2 == 0 && c.keyboard != nil {
						_ = c.keyboard.Release()
						c.keyboard = nil
						c.keymap = nil
						c.repeat.next = time.Time{}
					}
					if ev.Capabilities&1 == 0 && c.pointer != nil {
						_ = c.pointer.Release()
						c.pointer = nil
						c.pointerOut = nil
					}
				})
			}
		}
	case "wl_data_device_manager":
		if c.dataMgr == nil {
			o := client.NewDataDeviceManager(ctx)
			if bind(o, 1) == nil {
				c.dataMgr = o
			}
		}
	case "wp_viewporter":
		p := viewporter.NewWpViewporter(ctx)
		if err := bind(p, 1); err != nil {
			c.fatal = err
		} else {
			c.viewporter = p
		}
	case "wp_fractional_scale_manager_v1":
		p := fractionalscale.NewWpFractionalScaleManagerV1(ctx)
		if err := bind(p, 1); err != nil {
			c.fatal = err
		} else {
			c.scaleManager = p
		}
	case "wl_output":
		o := client.NewOutput(ctx)
		if bind(o, 4) == nil {
			c.addOutput(OutputID(g.Name), o)
		}
	case sessionlock.ExtSessionLockManagerV1InterfaceName:
		if c.mgr == nil {
			o := sessionlock.NewExtSessionLockManagerV1(ctx)
			if c.registry.Bind(g.Name, g.Interface, 1, o) == nil {
				c.mgr = o
			}
		}
	}
}

// Lock requests the session lock and starts creating lock surfaces for the
// outputs already known. Call once, before Run.
func (c *Client) Lock() error {
	if err := c.state.LockRequested(); err != nil {
		return err
	}
	lock, err := c.mgr.Lock()
	if err != nil {
		return fmt.Errorf("lock: %w", err)
	}
	c.lock = lock
	c.requestDeadline = time.Now().Add(3 * time.Second)
	c.publish()
	lock.SetLockedHandler(func(sessionlock.ExtSessionLockV1LockedEvent) {
		if err := c.state.Locked(); err != nil {
			c.fatal = err
		}
		c.publish()
	})
	lock.SetFinishedHandler(func(sessionlock.ExtSessionLockV1FinishedEvent) {
		// finished before locked is a refusal; after locked it is a
		// compositor-initiated end. Finished() itself is terminal-safe.
		if err := c.state.Finished(); err != nil {
			c.fatal = err
		}
		c.publish()
		switch c.state.FinishedDestructor() {
		case "destroy":
			c.fatal = c.lock.Destroy()
		case "unlock_and_destroy":
			c.fatal = c.lock.UnlockAndDestroy()
		}
		for _, out := range c.outputs {
			c.destroyOut(out)
		}
	})
	for id, out := range c.outputs {
		if out.lockSurf == nil {
			c.createLockSurface(id, out)
		}
	}
	return nil
}

func (c *Client) addOutput(id OutputID, o *client.Output) {
	out := &lockOut{id: id, output: o, scale: 1}
	o.SetScaleHandler(func(ev client.OutputScaleEvent) {
		if ev.Factor > 0 {
			if c.outputs[id] == out {
				out.scale = ev.Factor
				if out.scale120 == 0 && out.logicalW > 0 {
					c.resizeOutput(out)
				}
			}
		}
	})
	c.outputs[id] = out
	if err := c.state.AddOutput(id, 0, 0); err != nil {
		c.failUI(err)
	}
	if c.lock != nil && c.state.Phase() >= Requesting && c.state.Phase() < Done {
		c.createLockSurface(id, out)
	}
}

func (c *Client) createLockSurface(id OutputID, out *lockOut) {
	if c.state.Phase() > Locked || c.lock == nil {
		return // no new lock surfaces once unlocking
	}
	surf, err := c.compositor.CreateSurface()
	if err != nil {
		c.failUI(err)
		return
	}
	ls, err := c.lock.GetLockSurface(surf, out.output)
	if err != nil {
		c.failUI(err)
		if e := surf.Destroy(); e != nil {
			c.failUI(e)
		}
		return
	}
	out.surface, out.lockSurf = surf, ls
	ls.SetConfigureHandler(func(ev sessionlock.ExtSessionLockSurfaceV1ConfigureEvent) {
		c.configure(out, ev.Serial, int(ev.Width), int(ev.Height))
	})
	if c.viewporter != nil && c.scaleManager != nil {
		viewport, err := c.viewporter.GetViewport(surf)
		if err != nil {
			c.scaleFallback(err)
			return
		}
		out.viewport = viewport
		fractional, err := c.scaleManager.GetFractionalScale(surf)
		if err != nil {
			if e := viewport.Destroy(); e != nil {
				c.failUI(e)
			}
			out.viewport = nil
			c.scaleFallback(err)
			return
		}
		out.fractional = fractional
		fractional.SetPreferredScaleHandler(func(ev fractionalscale.WpFractionalScaleV1PreferredScaleEvent) {
			if c.outputs[id] != out {
				return
			}
			if ev.Scale == 0 || ev.Scale > 480 {
				c.failUI(fmt.Errorf("invalid fractional scale"))
				return
			}
			out.scale120 = ev.Scale
			if out.logicalW > 0 {
				c.resizeOutput(out)
			}
		})
	}
}

func (c *Client) configure(out *lockOut, serial uint32, w, h int) {
	if out.lockSurf == nil || c.outputs[out.id] != out {
		return
	}
	out.logicalW, out.logicalH = w, h
	pw, ph, _, err := out.geometry()
	if err != nil {
		c.failUI(err)
		return
	}
	if err = c.state.Configure(out.id, serial, uint32(pw), uint32(ph)); err != nil {
		c.failUI(err)
		return
	}
	ack, err := c.state.Ack(out.id)
	if err != nil {
		c.failUI(err)
		return
	}
	if err = out.lockSurf.AckConfigure(ack); err != nil {
		c.failUI(err)
		return
	}
	out.w, out.h = pw, ph
	out.pending = true
	c.paint(out)
}
func (out *lockOut) geometry() (int, int, float64, error) {
	scale := float64(out.scale)
	if out.viewport != nil && out.scale120 != 0 {
		scale = float64(max(out.scale120, 120)) / 120
	}
	if scale < 1 || scale > 4 || out.logicalW <= 0 || out.logicalH <= 0 || out.logicalW > 1<<20 || out.logicalH > 1<<20 {
		return 0, 0, 0, fmt.Errorf("invalid configured geometry")
	}
	w, h := int(float64(out.logicalW)*scale+0.999), int(float64(out.logicalH)*scale+0.999)
	_, err := bufferBytes(w, h)
	return w, h, scale, err
}
func (c *Client) resizeOutput(out *lockOut) {
	w, h, _, err := out.geometry()
	if err != nil {
		c.failUI(err)
		return
	}
	if w == out.w && h == out.h {
		return
	}
	// Scale changes preserve the already-acknowledged configure serial.
	if err = c.state.Resize(out.id, uint32(w), uint32(h)); err != nil {
		c.failUI(err)
		return
	}
	out.w, out.h = w, h
	out.pending = true
	c.paint(out)
}
func (c *Client) paint(out *lockOut) {
	if !out.pending || out.removed || out.surface == nil || c.state.Phase() > Locked {
		return
	}
	ready, err := c.prepareBuffers(out, out.w, out.h)
	if err != nil {
		c.failUI(err)
		return
	}
	if !ready {
		return
	}
	sb := out.availableBuffer(out.w, out.h)
	if sb == nil {
		return
	}
	_, _, scale, err := out.geometry()
	if err != nil {
		c.failUI(err)
		return
	}
	pixels := out.backgroundPixels()
	fb := &render.Framebuffer{Width: out.w, Height: out.h, Stride: out.w * 4, Pix: sb.data}
	if err = c.frame(fb, scale, pixels); err != nil {
		c.failUI(err)
		return
	}
	bufferScale := out.scale
	if out.viewport != nil {
		bufferScale = 1
		if err = out.viewport.SetDestination(int32(out.logicalW), int32(out.logicalH)); err != nil {
			c.failUI(err)
			return
		}
	}
	if err = out.surface.SetBufferScale(bufferScale); err != nil {
		c.failUI(err)
		return
	}
	if err = out.surface.Attach(sb.buf, 0, 0); err != nil {
		c.failUI(err)
		return
	}
	if err = out.surface.Damage(0, 0, int32(out.logicalW), int32(out.logicalH)); err != nil {
		c.failUI(err)
		return
	}
	if err = c.armFrame(out, false); err != nil {
		c.failUI(err)
		return
	}
	sb.busy = true
	if err = out.surface.Commit(); err != nil {
		sb.busy = false
		c.failUI(err)
		return
	}
	first := !out.committed
	out.committed = true
	out.pending = false
	if err = c.state.Commit(out.id, uint32(out.w), uint32(out.h)); err != nil {
		c.failUI(err)
		return
	}
	if first {
		c.publish()
	}
	c.scheduleBackground(out, time.Now())
}
func (c *Client) armFrame(out *lockOut, commit bool) error {
	if out.callback != nil {
		return nil
	}
	cb, err := out.surface.Frame()
	if err != nil {
		return err
	}
	out.callback = cb
	cb.SetDoneHandler(func(client.CallbackDoneEvent) {
		if err := cb.Destroy(); err != nil {
			c.failUI(err)
		}
		if out.callback == cb {
			out.callback = nil
		}
		if out.removed {
			return
		}
		c.scheduleBackground(out, time.Now())
	})
	if commit {
		return out.surface.Commit()
	}
	return nil
}

func (c *Client) freeBuffer(out *lockOut, sb *shmBuffer) {
	if sb.buf != nil {
		if err := sb.buf.Destroy(); err != nil {
			c.failUI(err)
		}
		sb.buf = nil
	}
	if sb.pool != nil {
		if err := sb.pool.Destroy(); err != nil {
			c.failUI(err)
		}
		sb.pool = nil
	}
	if sb.data != nil {
		if err := unix.Munmap(sb.data); err != nil {
			c.failUI(err)
		}
		sb.data = nil
	}
	for i, b := range out.buffers {
		if b == sb {
			out.buffers = append(out.buffers[:i], out.buffers[i+1:]...)
			break
		}
	}
}

func (c *Client) destroyOut(out *lockOut) {
	out.removed = true
	if out.background != nil {
		out.background.stop()
	}
	if out.callback != nil {
		if err := out.callback.Destroy(); err != nil {
			c.failUI(err)
		}
		out.callback = nil
	}
	if out.fractional != nil {
		if err := out.fractional.Destroy(); err != nil {
			c.failUI(err)
		}
		out.fractional = nil
	}
	if out.viewport != nil {
		if err := out.viewport.Destroy(); err != nil {
			c.failUI(err)
		}
		out.viewport = nil
	}
	if out.lockSurf != nil {
		if err := out.lockSurf.Destroy(); err != nil {
			c.failUI(err)
		}
		out.lockSurf = nil
	}
	for _, b := range append([]*shmBuffer(nil), out.buffers...) {
		b.retired = true
		if !b.busy {
			c.freeBuffer(out, b)
		}
	}
	if out.surface != nil {
		if err := out.surface.Destroy(); err != nil {
			c.failUI(err)
		}
		out.surface = nil
	}
}

// UnlockAndQuit drives the successful-auth path: unlock, tear surfaces down,
// let Run exit. Safe to call from a Post closure.
func (c *Client) UnlockAndQuit() error {
	if c.state.Phase() != Locked {
		return ErrInvalidUnlock
	}
	if c.BeforeUnlock != nil {
		if err := c.BeforeUnlock(); err != nil {
			return err
		}
	}
	if err := c.display.Context().SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		c.fatal = err
		return err
	}
	err := c.state.CompleteUnlock(c.lock.UnlockAndDestroy, c.display.Roundtrip, c.publish)
	if err != nil {
		c.fatal = fmt.Errorf("unlock confirmation: %w", err)
		return c.fatal
	}
	for _, out := range c.outputs {
		c.destroyOut(out)
	}
	c.publish()
	return nil
}

func (c *Client) publish() {
	if c.OnEvent != nil {
		v := c.state.Snapshot()
		v.UIError = c.uiError
		v.Background = c.backgroundStatus(time.Now())
		if c.lastSnapshot != nil {
			previous := *c.lastSnapshot
			previous.Sequence = v.Sequence
			if previous == v {
				return
			}
		}
		c.lastSnapshot = &v
		c.OnEvent(v)
	}
}
func (c *Client) failUI(err error) {
	c.uiError = "Lock display unavailable"
	fmt.Fprintf(os.Stderr, "sysc-lock: display: %v\n", err)
	c.publish()
}
func (c *Client) removeOutput(id OutputID) {
	if out := c.outputs[id]; out != nil {
		delete(c.outputs, id)
		c.destroyOut(out)
		c.removed = append(c.removed, out)
		if out.output != nil {
			if err := out.output.Release(); err != nil {
				c.failUI(err)
			}
		}
	}
	c.state.RemoveOutput(id)
	c.publish()
}

// AbortBeforeLocked must run on the owner. Signals never infer state from UI.
func (c *Client) AbortBeforeLocked() bool {
	ph := c.state.Phase()
	if ph != Idle && ph != Requesting {
		return false
	}
	c.fatal = ErrAborted
	return true
}

var ErrAborted = fmt.Errorf("lock request aborted")

// Post schedules fn to run on the pump goroutine and wakes Run. Safe from any
// goroutine.
func (c *Client) Post(fn func()) {
	c.pendMu.Lock()
	if c.closed {
		c.pendMu.Unlock()
		return
	}
	c.pend = append(c.pend, fn)
	var b [1]byte = [1]byte{1}
	_, _ = unix.Write(c.wakeW, b[:])
	c.pendMu.Unlock()
}

func (c *Client) drainWake() {
	b := make([]byte, 64)
	for {
		if _, err := unix.Read(c.wakeR, b); err != nil {
			return // EWOULDBLOCK or error
		}
	}
}

// Run pumps wayland events and posted closures until the state machine
// reaches a terminal phase (Done, Refused, Terminated) or the connection
// breaks.
func (c *Client) Run() error {
	defer c.close()
	for {
		c.pendMu.Lock()
		pending := c.pend
		c.pend = nil
		c.pendMu.Unlock()
		for _, fn := range pending {
			fn()
		}
		if c.fatal != nil {
			return c.fatal
		}
		ph := c.state.Phase()
		if ph == Done || ph == Refused || ph == Terminated {
			return nil
		}
		fds := []unix.PollFd{
			{Fd: int32(c.wlFD), Events: unix.POLLIN},
			{Fd: int32(c.wakeR), Events: unix.POLLIN},
		}
		now := time.Now()
		c.expireBackground(now)
		if ph == Requesting && !now.Before(c.requestDeadline) {
			return fmt.Errorf("lock acquisition deadline expired")
		}
		if c.repeat.due(now) && c.onKey != nil {
			k := c.repeatKey()
			c.onKey(k)
		}
		if c.OnDeadline != nil && (c.deadline.IsZero() || !now.Before(c.deadline)) {
			c.repaintOwner()
			c.deadline = c.OnDeadline(now)
		}
		if !c.resumeAt.IsZero() && !now.Before(c.resumeAt) {
			c.resumeAt = time.Time{}
			c.publish()
			for _, out := range c.outputs {
				c.scheduleBackground(out, now)
			}
		}
		wait := -1
		next := c.deadline
		for _, out := range c.outputs {
			if b := out.background; b != nil && !b.failed && !b.jobDeadline.IsZero() && (next.IsZero() || b.jobDeadline.Before(next)) {
				next = b.jobDeadline
			}
		}
		if ph == Requesting && (next.IsZero() || c.requestDeadline.Before(next)) {
			next = c.requestDeadline
		}
		if !c.repeat.next.IsZero() && (next.IsZero() || c.repeat.next.Before(next)) {
			next = c.repeat.next
		}
		if !c.resumeAt.IsZero() && (next.IsZero() || c.resumeAt.Before(next)) {
			next = c.resumeAt
		}
		if !next.IsZero() {
			wait = max(0, int(time.Until(next).Milliseconds()))
		}
		n, err := unix.Poll(fds, wait)
		if err != nil {
			if err == syscall.EINTR {
				continue
			}
			return fmt.Errorf("poll: %w", err)
		}
		_ = n
		if fds[1].Revents&unix.POLLIN != 0 {
			c.drainWake()
		}
		if fds[0].Revents&unix.POLLIN != 0 {
			if err := c.dispatchAll(); err != nil {
				return err
			}
		}
		if fds[0].Revents&(unix.POLLERR|unix.POLLHUP|unix.POLLNVAL) != 0 {
			return fmt.Errorf("wayland connection lost")
		}
	}
}

func (c *Client) dispatchAll() error {
	ctx := c.display.Context()
	if err := ctx.Dispatch(); err != nil {
		return fmt.Errorf("dispatch: %w", err)
	}
	for {
		ready, err := pollIn(c.wlFD, 0)
		if err != nil || !ready {
			return err
		}
		if err := ctx.Dispatch(); err != nil {
			return fmt.Errorf("dispatch: %w", err)
		}
	}
}

func pollIn(fd int, timeoutMS int) (bool, error) {
	fds := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
	n, err := unix.Poll(fds, timeoutMS)
	if err != nil {
		if err == syscall.EINTR {
			return false, nil
		}
		return false, err
	}
	return n > 0 && fds[0].Revents&unix.POLLIN != 0, nil
}

func (c *Client) close() {
	c.pendMu.Lock()
	if c.closed {
		c.pendMu.Unlock()
		return
	}
	c.closed = true
	c.pend = nil
	c.pendMu.Unlock()
	for _, out := range c.outputs {
		c.destroyOut(out)
	}
	if c.wakeR > 0 {
		unix.Close(c.wakeR)
		unix.Close(c.wakeW)
	}
	if c.wlFD > 0 {
		unix.Close(c.wlFD)
	}
	c.display.Context().Close()
	for _, out := range c.outputs {
		for len(out.buffers) > 0 {
			c.freeBuffer(out, out.buffers[0])
		}
	}
	for _, out := range c.removed {
		for len(out.buffers) > 0 {
			c.freeBuffer(out, out.buffers[0])
		}
	}
}

// State exposes the state machine (read phase from any goroutine).
func (c *Client) State() *State { return c.state }

// HandshakeReady reads only the synchronized protocol phase.
func (c *Client) HandshakeReady() bool { return c.state.HandshakeReady() }

// Repaint schedules a fresh frame commit for every output that already has
// one, on the pump goroutine. Keystrokes and clock ticks route through this.
func (c *Client) Repaint() { c.Post(c.repaintOwner) }
func (c *Client) repaintOwner() {
	for _, out := range c.outputs {
		if out.w > 0 && out.h > 0 {
			out.pending = true
			c.paint(out)
		}
	}
	if c.OnDeadline != nil {
		c.deadline = c.OnDeadline(time.Now())
	}
}

// Close releases an acquisition that failed before Run could start.
func (c *Client) Close() { c.close() }

func (c *Client) scaleFallback(err error) {
	fmt.Fprintf(os.Stderr, "sysc-lock: fractional scale unavailable: %v\n", err)
	c.uiError = "Using integer output scaling"
	c.publish()
}

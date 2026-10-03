package lockd

import (
	"fmt"
	"os"
	"sync"
	"syscall"

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
	CapsLock  bool
}

// KeyFunc receives every pressed key.
type KeyFunc func(Key)

// FrameFunc produces an RGBA pixel buffer of exactly w*4*h bytes for one
// lock surface at pixel size w,h.
type FrameFunc func(w, h int) ([]byte, error)

// Client owns the Wayland connection, the session lock, and one lock surface
// per output. All protocol mutations run on the single pump goroutine; other
// goroutines must go through Post.
type Client struct {
	display  *client.Display
	registry *client.Registry
	state    *State
	onKey    KeyFunc
	frame    FrameFunc

	compositor *client.Compositor
	shm        *client.Shm
	shmFmt     uint32
	haveFmt    bool
	seat       *client.Seat
	keymap     *keymap
	keyboard   *client.Keyboard
	mgr        *sessionlock.ExtSessionLockManagerV1
	lock       *sessionlock.ExtSessionLockV1
	outputs    map[OutputID]*lockOut

	wlFD   int
	wakeR  int
	wakeW  int
	pendMu sync.Mutex
	pend   []func()
}

type lockOut struct {
	id        OutputID
	output    *client.Output
	scale     int32
	surface   *client.Surface
	lockSurf  *sessionlock.ExtSessionLockSurfaceV1
	w, h      int // pixel size from the last configure ack
	committed bool
	buffers   []*shmBuffer
}

type shmBuffer struct {
	pool *client.ShmPool
	buf  *client.Buffer
	data []byte
	id   OutputID
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
	c.registry = client.NewRegistry(ctx)
	c.registry.SetGlobalHandler(func(g client.RegistryGlobalEvent) { c.global(g) })
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
		ctx.Close()
		return nil, fmt.Errorf("wake pipe: %w", err)
	}
	c.wakeR, c.wakeW = pipe[0], pipe[1]
	return c, nil
}

func (c *Client) global(g client.RegistryGlobalEvent) {
	ctx := c.display.Context()
	bind := func(id client.Proxy) error {
		v := g.Version
		if v > 1 {
			v = 1
		}
		return c.registry.Bind(g.Name, g.Interface, v, id)
	}
	switch g.Interface {
	case "wl_compositor":
		if c.compositor == nil {
			o := client.NewCompositor(ctx)
			if bind(o) == nil {
				c.compositor = o
			}
		}
	case "wl_shm":
		if c.shm == nil {
			o := client.NewShm(ctx)
			if bind(o) == nil {
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
			if bind(o) == nil {
				c.seat = o
				c.setupKeyboard()
			}
		}
	case "wl_output":
		o := client.NewOutput(ctx)
		if bind(o) == nil {
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
	lock.SetLockedHandler(func(sessionlock.ExtSessionLockV1LockedEvent) {
		c.state.Locked()
	})
	lock.SetFinishedHandler(func(sessionlock.ExtSessionLockV1FinishedEvent) {
		// finished before locked is a refusal; after locked it is a
		// compositor-initiated end. Finished() itself is terminal-safe.
		c.state.Finished()
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
			c.outputs[id].scale = ev.Factor
		}
	})
	c.outputs[id] = out
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
		return
	}
	ls, err := c.lock.GetLockSurface(surf, out.output)
	if err != nil {
		surf.Destroy()
		return
	}
	c.state.AddOutput(id, 0, 0)
	out.surface, out.lockSurf = surf, ls
	ls.SetConfigureHandler(func(ev sessionlock.ExtSessionLockSurfaceV1ConfigureEvent) {
		c.configure(out, ev.Serial, int(ev.Width), int(ev.Height))
	})
}

func (c *Client) configure(out *lockOut, serial uint32, w, h int) {
	if out.lockSurf == nil {
		return
	}
	// The FSM guards the exact pixel size we will commit: logical
	// configure size times output scale.
	pw, ph := uint32(w)*uint32(out.scale), uint32(h)*uint32(out.scale)
	if err := c.state.Configure(out.id, serial, pw, ph); err != nil {
		return
	}
	ack, err := c.state.Ack(out.id)
	if err != nil {
		return
	}
	if err := out.lockSurf.AckConfigure(ack); err != nil {
		return
	}
	if err := c.commitFrame(out, int(pw), int(ph)); err != nil {
		fmt.Fprintf(os.Stderr, "sysc-lock: frame: %v\n", err)
		return
	}
	c.state.Commit(out.id, pw, ph)
}

func (c *Client) commitFrame(out *lockOut, w, h int) error {
	if w <= 0 || h <= 0 {
		return fmt.Errorf("invalid size %dx%d", w, h)
	}
	stride := w * 4
	size := stride * h
	if size <= 0 || int64(size) > 1<<30 {
		return fmt.Errorf("buffer too large")
	}
	px, err := c.frame(w, h)
	if err != nil {
		return err
	}
	if len(px) != size {
		return fmt.Errorf("frame %d bytes, want %d", len(px), size)
	}
	if !c.haveFmt {
		return fmt.Errorf("no shm format advertised")
	}
	fd, err := unix.MemfdCreate("sysc-lock", unix.MFD_CLOEXEC)
	if err != nil {
		return fmt.Errorf("memfd: %w", err)
	}
	if err := unix.Ftruncate(fd, int64(size)); err != nil {
		unix.Close(fd)
		return fmt.Errorf("ftruncate: %w", err)
	}
	data, err := unix.Mmap(fd, 0, size, unix.PROT_READ|unix.PROT_WRITE, unix.MAP_SHARED)
	if err != nil {
		unix.Close(fd)
		return fmt.Errorf("mmap: %w", err)
	}
	copy(data, px)
	// ponytail: one fresh pool+buffer per frame (redraws are <2 fps: clock
	// tick and keystrokes); the wl_buffer.release handler frees it. Pool
	// recycling only matters if release lags behind redraws.
	pool, err := c.shm.CreatePool(fd, int32(size))
	unix.Close(fd)
	if err != nil {
		unix.Munmap(data)
		return fmt.Errorf("create pool: %w", err)
	}
	buf, err := pool.CreateBuffer(0, int32(w), int32(h), int32(stride), c.shmFmt)
	if err != nil {
		pool.Destroy()
		unix.Munmap(data)
		return fmt.Errorf("create buffer: %w", err)
	}
	sb := &shmBuffer{pool: pool, buf: buf, data: data, id: out.id}
	buf.SetReleaseHandler(func(client.BufferReleaseEvent) {
		c.freeBuffer(out, sb)
	})
	out.surface.SetBufferScale(out.scale)
	if err := out.surface.Attach(buf, 0, 0); err != nil {
		c.freeBuffer(out, sb)
		return fmt.Errorf("attach: %w", err)
	}
	out.surface.Damage(0, 0, int32(w), int32(h))
	if err := out.surface.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	out.w, out.h = w, h
	out.committed = true
	out.buffers = append(out.buffers, sb)
	return nil
}

func (c *Client) freeBuffer(out *lockOut, sb *shmBuffer) {
	sb.buf.Destroy()
	sb.pool.Destroy()
	unix.Munmap(sb.data)
	for i, b := range out.buffers {
		if b == sb {
			out.buffers = append(out.buffers[:i], out.buffers[i+1:]...)
			break
		}
	}
}

func (c *Client) destroyOut(out *lockOut) {
	if out.lockSurf != nil {
		out.lockSurf.Destroy()
		out.lockSurf = nil
	}
	for _, b := range out.buffers {
		b.buf.Destroy()
		b.pool.Destroy()
		unix.Munmap(b.data)
	}
	out.buffers = nil
	if out.surface != nil {
		out.surface.Destroy()
		out.surface = nil
	}
}

// UnlockAndQuit drives the successful-auth path: unlock, tear surfaces down,
// let Run exit. Safe to call from a Post closure.
func (c *Client) UnlockAndQuit() {
	if err := c.state.Unlock(); err != nil {
		return
	}
	c.lock.UnlockAndDestroy()
	for _, out := range c.outputs {
		c.destroyOut(out)
	}
	c.state.Done()
}

// Post schedules fn to run on the pump goroutine and wakes Run. Safe from any
// goroutine.
func (c *Client) Post(fn func()) {
	c.pendMu.Lock()
	c.pend = append(c.pend, fn)
	c.pendMu.Unlock()
	var b [1]byte = [1]byte{1}
	_, _ = unix.Write(c.wakeW, b[:])
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
		ph := c.state.Phase()
		if ph == Done || ph == Refused || ph == Terminated {
			return nil
		}
		fds := []unix.PollFd{
			{Fd: int32(c.wlFD), Events: unix.POLLIN},
			{Fd: int32(c.wakeR), Events: unix.POLLIN},
		}
		n, err := unix.Poll(fds, -1)
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
}

// State exposes the state machine (read phase from any goroutine).
func (c *Client) State() *State { return c.state }

// HandshakeReady reports whether locked was acknowledged and every known
// output has committed a first frame.
func (c *Client) HandshakeReady() bool {
	if c.state.Phase() != Locked {
		return false
	}
	for _, out := range c.outputs {
		if out.lockSurf != nil && !out.committed {
			return false
		}
	}
	return true
}

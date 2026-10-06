package lockd

/*
#cgo LDFLAGS: -lEGL -lGLESv2
#include <EGL/egl.h>
#include <EGL/eglext.h>
#include <GLES2/gl2.h>
#include <stdlib.h>

// cgo gives every `void *` EGL typedef an opaque Go type that cannot be compared
// against nil, so the null handles are fetched through typed C helpers instead.
static EGLDisplay gpu_no_display(void) { return EGL_NO_DISPLAY; }
static EGLContext gpu_no_context(void) { return EGL_NO_CONTEXT; }
static EGLSurface gpu_no_surface(void) { return EGL_NO_SURFACE; }
static EGLConfig gpu_no_config(void) { return (EGLConfig)0; }
static EGLDisplay gpu_default_display(void) { return eglGetDisplay(EGL_DEFAULT_DISPLAY); }

// eglGetPlatformDisplay is core in EGL 1.5, but libEGL only guarantees the
// legacy egl* symbols for direct linking, and a bare C code pointer cannot be
// turned into a Go func value, so lookup and call both stay in C.
static EGLDisplay gpu_surfaceless_display(void) {
	typedef EGLDisplay (*get_platform_fn)(EGLenum, void *, const EGLint *);
	get_platform_fn get = (get_platform_fn)eglGetProcAddress("eglGetPlatformDisplay");
	if (get == NULL) { return EGL_NO_DISPLAY; }
	return get(EGL_PLATFORM_SURFACELESS_MESA, NULL, NULL);
}
*/
import "C"

import (
	"fmt"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"unsafe"

	"github.com/Nomadcxx/sysc-Go/animations"
)

// paletteStops is the fixed size the shaders declare as `uniform vec3 uPalette[8]`.
const paletteStopsN = 8

// glMu serializes every EGL and GLES call. The worker owns exactly one context,
// but makeCurrent binds to the OS thread and Go may move a goroutine between
// calls, so newGpuBackend pins the goroutine with LockOSThread and this mutex
// guards the calls themselves.
var glMu sync.Mutex

// cpuEffectIDs are the non-text effects the renderer supports, and therefore the
// only effects the GPU backend will attempt.
var cpuEffectIDs = []string{"matrix", "fire", "fireworks", "rain", "beams", "aquarium"}

// paletteStops maps an effect id to its animations getter. Same mapping
// sysc-terminal's effect registry uses, so GPU and CPU resolve identical
// colors from one source of truth. Nil means the effect cannot run on GPU.
func paletteStops(effect, palette string) []string {
	switch effect {
	case "rain":
		return animations.GetRainPalette(palette)
	case "matrix":
		return animations.GetMatrixPalette(palette)
	case "fire":
		return animations.GetFirePalette(palette)
	case "fireworks":
		return animations.GetFireworksPalette(palette)
	case "beams":
		return animations.GetParticlePalette(palette)
	case "aquarium":
		return animations.GetScreensaverPalette(palette)
	}
	return nil
}

// parseHexColors turns "#rrggbb" stops into the flat vec3 array the shaders
// index. Always paletteStopsN*3 floats: a short palette repeats its last stop,
// a long one truncates, so shader lookups never need a bound check.
func parseHexColors(stops []string) ([]float32, error) {
	if len(stops) == 0 {
		return nil, fmt.Errorf("palette has no stops")
	}
	out := make([]float32, paletteStopsN*3)
	for i := 0; i < paletteStopsN; i++ {
		s := stops[min(i, len(stops)-1)]
		if len(s) != 7 || s[0] != '#' {
			return nil, fmt.Errorf("stop %d is not #rrggbb: %q", i, s)
		}
		v, err := strconv.ParseUint(s[1:], 16, 32)
		if err != nil {
			return nil, fmt.Errorf("stop %d: %w", i, err)
		}
		out[i*3+0] = float32((v>>16)&0xff) / 255
		out[i*3+1] = float32((v>>8)&0xff) / 255
		out[i*3+2] = float32(v&0xff) / 255
	}
	return out, nil
}

// The null EGL handles, fetched through the C helpers in the preamble.
var (
	eglNone      = C.gpu_no_display()
	eglNoContext = C.gpu_no_context()
	eglNoSurface = C.gpu_no_surface()
)

type gpuBackend struct {
	effect, palette string
	pal             []float32

	display C.EGLDisplay
	ctx     C.EGLContext
	surface C.EGLSurface

	prog  C.GLuint
	vbo   C.GLuint
	loc   C.GLint // uColor
	aPos  C.GLint
	fbo   C.GLuint
	tex   C.GLuint
	w, h  int
	ticks int

	scratch []byte

	threaded bool
	closed   bool
}

func newGpuBackend(effect, palette string, width, height int) (EffectBackend, error) {
	stops := paletteStops(effect, palette)
	if len(stops) == 0 {
		// The GPU path refuses work it cannot map rather than guessing colors.
		return nil, fmt.Errorf("gpu: effect %q resolved no palette %q", effect, palette)
	}
	pal, err := parseHexColors(stops)
	if err != nil {
		return nil, fmt.Errorf("gpu: palette %q: %w", palette, err)
	}

	glMu.Lock()
	defer glMu.Unlock()

	// makeCurrent binds to the OS thread, and Go is free to move this goroutine
	// onto another one between calls, which would leave GL without a context.
	runtime.LockOSThread()
	b := &gpuBackend{effect: effect, palette: palette, pal: pal, threaded: true}

	if err := b.init(); err != nil {
		b.close() // tolerates a half-built state and unpins the thread
		return nil, err
	}
	if err := b.resize(width, height); err != nil {
		b.close()
		return nil, err
	}
	return b, nil
}

func (b *gpuBackend) init() error {
	dpy, err := eglOpenDisplay()
	if err != nil {
		return err
	}
	b.display = dpy

	cfg, err := eglChooseConfig(dpy)
	if err != nil {
		return err
	}
	if b.ctx, err = eglCreateContext(dpy, cfg); err != nil {
		return err
	}
	// A 1x1 pbuffer exists only to satisfy makeCurrent; the FBO does the real
	// rendering. A real surface keeps one code path on drivers that do not
	// advertise EGL_KHR_no_config_context.
	if b.surface, err = eglCreatePbuffer(dpy, cfg); err != nil {
		return err
	}
	if err = eglMakeCurrent(dpy, b.surface, b.surface, b.ctx); err != nil {
		return err
	}
	if b.prog, err = buildProgram(solidVS, solidFS); err != nil {
		return err
	}
	b.loc = glUniformLocation(b.prog, "uColor")
	b.aPos = glAttribLocation(b.prog, "aPos")
	return b.uploadFullscreenTriangle()
}

// uploadFullscreenTriangle feeds one oversized triangle that covers the
// viewport, so no index buffer or clipping maths is needed anywhere.
func (b *gpuBackend) uploadFullscreenTriangle() error {
	verts := []C.GLfloat{-1, -1, 3, -1, -1, 3}
	C.glGenBuffers(1, &b.vbo)
	C.glBindBuffer(C.GL_ARRAY_BUFFER, b.vbo)
	C.glBufferData(C.GL_ARRAY_BUFFER,
		C.GLsizeiptr(len(verts)*4), unsafe.Pointer(&verts[0]), C.GL_STATIC_DRAW)
	C.glEnableVertexAttribArray(C.GLuint(b.aPos))
	C.glVertexAttribPointer(C.GLuint(b.aPos), 2, C.GL_FLOAT, C.GL_FALSE, 0, nil)
	return glErr("uploadFullscreenTriangle")
}

func (b *gpuBackend) Resize(w, h int) error {
	glMu.Lock()
	defer glMu.Unlock()
	return b.resize(w, h)
}

// resize assumes glMu is already held. The public methods lock and delegate here
// so that construction can reuse them without re-entering the mutex.
func (b *gpuBackend) resize(w, h int) error {
	if w <= 0 || h <= 0 {
		return fmt.Errorf("gpu: bad geometry %dx%d", w, h)
	}
	if b.fbo != 0 && w == b.w && h == b.h {
		return nil
	}
	if b.fbo != 0 {
		C.glDeleteFramebuffers(1, &b.fbo)
		C.glDeleteTextures(1, &b.tex)
		b.fbo, b.tex = 0, 0
	}
	b.w, b.h = w, h

	C.glGenTextures(1, &b.tex)
	C.glBindTexture(C.GL_TEXTURE_2D, b.tex)
	C.glTexImage2D(C.GL_TEXTURE_2D, 0, C.GL_RGBA, C.GLsizei(w), C.GLsizei(h), 0,
		C.GL_RGBA, C.GL_UNSIGNED_BYTE, nil)
	C.glTexParameteri(C.GL_TEXTURE_2D, C.GL_TEXTURE_MIN_FILTER, C.GL_LINEAR)
	C.glTexParameteri(C.GL_TEXTURE_2D, C.GL_TEXTURE_MAG_FILTER, C.GL_LINEAR)
	C.glTexParameteri(C.GL_TEXTURE_2D, C.GL_TEXTURE_WRAP_S, C.GL_CLAMP_TO_EDGE)
	C.glTexParameteri(C.GL_TEXTURE_2D, C.GL_TEXTURE_WRAP_T, C.GL_CLAMP_TO_EDGE)

	C.glGenFramebuffers(1, &b.fbo)
	C.glBindFramebuffer(C.GL_FRAMEBUFFER, b.fbo)
	C.glFramebufferTexture2D(C.GL_FRAMEBUFFER, C.GL_COLOR_ATTACHMENT0,
		C.GL_TEXTURE_2D, b.tex, 0)
	if st := C.glCheckFramebufferStatus(C.GL_FRAMEBUFFER); st != C.GL_FRAMEBUFFER_COMPLETE {
		return fmt.Errorf("gpu: framebuffer incomplete (0x%x)", uint32(st))
	}
	C.glBindFramebuffer(C.GL_FRAMEBUFFER, 0)
	return glErr("resize")
}

// Step advances time only. The caller may tick many times between draws, so the
// accumulated count is what Draw consumes.
func (b *gpuBackend) Step() error {
	b.ticks++
	return nil
}

// Draw paints one frame. Task 4 and 5 render a solid colour: that proves the
// FBO, the program and the readback line up before any effect shader exists.
func (b *gpuBackend) Draw(pixels []byte, stride int) error {
	glMu.Lock()
	defer glMu.Unlock()

	if b.ticks == 0 {
		b.ticks = 1
	}
	C.glBindFramebuffer(C.GL_FRAMEBUFFER, b.fbo)
	C.glViewport(0, 0, C.GLsizei(b.w), C.GLsizei(b.h))
	C.glClearColor(0, 0, 0, 0)
	C.glClear(C.GL_COLOR_BUFFER_BIT)
	C.glUseProgram(b.prog)
	C.glUniform4f(b.loc, 1, 0, 0, 1)
	C.glDrawArrays(C.GL_TRIANGLES, 0, 3)
	C.glFinish()
	if err := glErr("draw"); err != nil {
		return err
	}
	return b.readback(pixels, stride)
}

// readback copies the FBO into the caller's BGRA memory. GL returns rows
// bottom-up; every frame the CPU renderer produces is top-down, so rows are
// flipped here rather than in each effect's maths.
func (b *gpuBackend) readback(pixels []byte, stride int) error {
	rowBytes := b.w * 4
	if want := rowBytes * b.h; len(b.scratch) < want {
		b.scratch = make([]byte, want)
	}
	C.glReadPixels(0, 0, C.GLsizei(b.w), C.GLsizei(b.h),
		C.GL_RGBA, C.GL_UNSIGNED_BYTE, unsafe.Pointer(&b.scratch[0]))

	if stride < rowBytes {
		stride = rowBytes // the caller's rows overlap rather than being short
	}
	for y := 0; y < b.h; y++ {
		dst := y * stride
		if dst+rowBytes > len(pixels) {
			break // caller buffer is smaller than the geometry; keep what fits
		}
		s := b.scratch[(b.h-1-y)*rowBytes:]
		p := pixels[dst : dst+rowBytes]
		for i := 0; i+3 < rowBytes; i += 4 {
			p[i+0] = s[i+2] // B
			p[i+1] = s[i+1] // G
			p[i+2] = s[i+0] // R
			p[i+3] = 0xff
		}
	}
	return glErr("readback")
}

func (b *gpuBackend) Close() error {
	glMu.Lock()
	defer glMu.Unlock()
	return b.close()
}

// close assumes glMu is already held, so construction failure paths can reuse it.
func (b *gpuBackend) close() error {
	if b.closed {
		return nil
	}
	b.closed = true
	if b.display != eglNone {
		C.eglMakeCurrent(b.display, eglNoSurface, eglNoSurface, eglNoContext)
		if b.fbo != 0 {
			C.glDeleteFramebuffers(1, &b.fbo)
		}
		if b.tex != 0 {
			C.glDeleteTextures(1, &b.tex)
		}
		if b.vbo != 0 {
			C.glDeleteBuffers(1, &b.vbo)
		}
		if b.prog != 0 {
			C.glDeleteProgram(b.prog)
		}
		if b.ctx != eglNoContext {
			C.eglDestroyContext(b.display, b.ctx)
		}
		if b.surface != eglNoSurface {
			C.eglDestroySurface(b.display, b.surface)
		}
		C.eglTerminate(b.display)
	}
	if b.threaded {
		runtime.UnlockOSThread()
		b.threaded = false
	}
	return nil
}

// --- EGL setup -----------------------------------------------------------

// eglGetPlatformDisplay is core in EGL 1.5, but libEGL only guarantees the
// legacy egl* symbols for direct linking (this box exports the core name and not
// eglGetPlatformDisplayEXT), so it is fetched by name.
type eglPlatformDisplayFn func(C.EGLenum, unsafe.Pointer, *C.EGLint) C.EGLDisplay

func eglOpenDisplay() (C.EGLDisplay, error) {
	// Mesa advertises surfaceless rendering as a client-side platform extension,
	// visible through a NULL-display query. NVIDIA's EGL does not, which is why
	// the default-display path below has to stay.
	if eglHasExtension(eglNone, "EGL_MESA_platform_surfaceless") {
		if d := C.gpu_surfaceless_display(); d != eglNone {
			if eglInitialize(d) == nil {
				return d, nil
			}
			C.eglTerminate(d)
		}
	}
	d := C.gpu_default_display()
	if d == eglNone {
		return eglNone, fmt.Errorf("gpu: no EGL display")
	}
	if err := eglInitialize(d); err != nil {
		return eglNone, err
	}
	return d, nil
}

func eglInitialize(d C.EGLDisplay) error {
	major, minor := C.EGLint(0), C.EGLint(0)
	if C.eglInitialize(d, &major, &minor) != C.EGL_TRUE {
		return fmt.Errorf("gpu: eglInitialize failed (EGL error 0x%x)", eglErr())
	}
	return nil
}

func eglChooseConfig(dpy C.EGLDisplay) (C.EGLConfig, error) {
	attribs := []C.EGLint{
		C.EGL_SURFACE_TYPE, C.EGL_PBUFFER_BIT,
		C.EGL_RENDERABLE_TYPE, C.EGL_OPENGL_ES2_BIT,
		C.EGL_RED_SIZE, 8,
		C.EGL_GREEN_SIZE, 8,
		C.EGL_BLUE_SIZE, 8,
		C.EGL_ALPHA_SIZE, 8,
		C.EGL_NONE,
	}
	var cfg C.EGLConfig
	var n C.EGLint
	if C.eglChooseConfig(dpy, &attribs[0], &cfg, 1, &n) != C.EGL_TRUE || n == 0 {
		return C.gpu_no_config(), fmt.Errorf("gpu: no ES2 pbuffer config (EGL error 0x%x)", eglErr())
	}
	return cfg, nil
}

func eglCreateContext(dpy C.EGLDisplay, cfg C.EGLConfig) (C.EGLContext, error) {
	attribs := []C.EGLint{C.EGL_CONTEXT_CLIENT_VERSION, 2, C.EGL_NONE}
	ctx := C.eglCreateContext(dpy, cfg, eglNoContext, &attribs[0])
	if ctx == eglNoContext {
		return nil, fmt.Errorf("gpu: eglCreateContext failed (EGL error 0x%x)", eglErr())
	}
	return ctx, nil
}

func eglCreatePbuffer(dpy C.EGLDisplay, cfg C.EGLConfig) (C.EGLSurface, error) {
	attribs := []C.EGLint{C.EGL_WIDTH, 1, C.EGL_HEIGHT, 1, C.EGL_NONE}
	s := C.eglCreatePbufferSurface(dpy, cfg, &attribs[0])
	if s == eglNoSurface {
		return nil, fmt.Errorf("gpu: eglCreatePbufferSurface failed (EGL error 0x%x)", eglErr())
	}
	return s, nil
}

func eglMakeCurrent(dpy C.EGLDisplay, surf C.EGLSurface, read C.EGLSurface, ctx C.EGLContext) error {
	if C.eglMakeCurrent(dpy, surf, read, ctx) != C.EGL_TRUE {
		return fmt.Errorf("gpu: eglMakeCurrent failed (EGL error 0x%x)", eglErr())
	}
	return nil
}

func eglHasExtension(dpy C.EGLDisplay, name string) bool {
	list := C.eglQueryString(dpy, C.EGL_EXTENSIONS)
	if list == nil {
		return false
	}
	// eglQueryString returns a space separated list; match on whole tokens so
	// "surfaceless" cannot satisfy a query for a longer name.
	return strings.Contains(" "+C.GoString(list)+" ", " "+name+" ")
}

func eglErr() uint32 { return uint32(C.eglGetError()) }

// --- GL helpers ----------------------------------------------------------

func glErr(op string) error {
	if e := C.glGetError(); e != C.GL_NO_ERROR {
		return fmt.Errorf("gpu: %s: gl error 0x%x", op, uint32(e))
	}
	return nil
}

func buildProgram(vs, fs string) (C.GLuint, error) {
	v, err := compileShader(C.GL_VERTEX_SHADER, vs)
	if err != nil {
		return 0, err
	}
	f, err := compileShader(C.GL_FRAGMENT_SHADER, fs)
	if err != nil {
		C.glDeleteShader(v)
		return 0, err
	}
	p := C.glCreateProgram()
	C.glAttachShader(p, v)
	C.glAttachShader(p, f)
	C.glLinkProgram(p)
	C.glDeleteShader(v)
	C.glDeleteShader(f)
	var ok C.GLint
	C.glGetProgramiv(p, C.GL_LINK_STATUS, &ok)
	if ok != C.GL_TRUE {
		C.glDeleteProgram(p)
		return 0, fmt.Errorf("gpu: link failed: %s", programLog(p))
	}
	return p, nil
}

func compileShader(kind C.GLenum, src string) (C.GLuint, error) {
	cs := C.CString(src)
	defer C.free(unsafe.Pointer(cs))
	s := C.glCreateShader(kind)
	if s == 0 {
		return 0, fmt.Errorf("gpu: glCreateShader returned 0")
	}
	C.glShaderSource(s, 1, &cs, nil)
	C.glCompileShader(s)
	var ok C.GLint
	C.glGetShaderiv(s, C.GL_COMPILE_STATUS, &ok)
	if ok != C.GL_TRUE {
		log := shaderLog(s)
		C.glDeleteShader(s)
		return 0, fmt.Errorf("gpu: compile failed: %s", log)
	}
	return s, nil
}

func shaderLog(s C.GLuint) string {
	var n C.GLint
	C.glGetShaderiv(s, C.GL_INFO_LOG_LENGTH, &n)
	if n <= 0 {
		return "no log"
	}
	buf := make([]byte, int(n))
	C.glGetShaderInfoLog(s, n, nil, (*C.GLchar)(unsafe.Pointer(&buf[0])))
	return string(buf[:len(buf)-1])
}

func programLog(p C.GLuint) string {
	var n C.GLint
	C.glGetProgramiv(p, C.GL_INFO_LOG_LENGTH, &n)
	if n <= 0 {
		return "no log"
	}
	buf := make([]byte, int(n))
	C.glGetProgramInfoLog(p, n, nil, (*C.GLchar)(unsafe.Pointer(&buf[0])))
	return string(buf[:len(buf)-1])
}

func glUniformLocation(p C.GLuint, name string) C.GLint {
	c := C.CString(name)
	defer C.free(unsafe.Pointer(c))
	return C.glGetUniformLocation(p, (*C.GLchar)(unsafe.Pointer(c)))
}

func glAttribLocation(p C.GLuint, name string) C.GLint {
	c := C.CString(name)
	defer C.free(unsafe.Pointer(c))
	return C.glGetAttribLocation(p, (*C.GLchar)(unsafe.Pointer(c)))
}

// The solid pair exists only to prove the EGL/FBO/readback plumbing before any
// effect shader does; Task 6 replaces it with the real programs.
const solidVS = `
attribute vec2 aPos;
void main() { gl_Position = vec4(aPos, 0.0, 1.0); }
`

const solidFS = `
precision mediump float;
uniform vec4 uColor;
void main() { gl_FragColor = uColor; }
`

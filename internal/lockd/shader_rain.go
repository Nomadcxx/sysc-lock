package lockd

// GPU effect shaders live here as embedded GLSL strings; nothing is downloaded
// or hot-loaded. Each effect supplies a "step" program that advances a ping-
// ponged state texture one tick, and a "draw" program that turns that state
// into the visible RGBA frame. Both share one uniform contract (uPrev, uTime,
// uSeed, uGrid, uPalette) so the Go side has one upload path per pass.

// effectShaders resolves an effect id to its step and draw fragment sources.
// Effects without a GPU shader are refused at construction, which makes the
// Task 7 policy demote them to the CPU backend instead of showing garbage.
func effectShaders(effect string) (step, draw string, ok bool) {
	switch effect {
	case "rain":
		return rainStepFS, rainDrawFS, true
	case "matrix":
		return matrixStepFS, matrixDrawFS, true
	case "fire":
		return nullStepFS, fireFS, true
	case "fireworks":
		return nullStepFS, fireworksFS, true
	}
	return "", "", false
}

// effectFSHead is the shared preamble for the stateless draw shaders: the
// uniform contract plus hash and the spec-legal constant-index palette
// lookup. Uniforms a shader never references resolve to -1 and the Go side
// skips them, so declaring the full set is free.
const effectFSHead = `
precision highp float;
varying vec2 vUv;
uniform vec3 uPalette[8];
uniform int uSeed;
uniform float uTime;
uniform vec2 uGrid;

float rhash(vec2 p) {
	return fract(sin(dot(p, vec2(127.1, 311.7))) * 43758.5453);
}

vec3 paletteAt(int k) {
	vec3 c = uPalette[0];
	for (int i = 0; i < 8; i++) {
		if (i == k) {
			c = uPalette[i];
		}
	}
	return c;
}
`

// nullStepFS is the step program for stateless effects: the frame is a pure
// function of uTime, so the ping-pong state pair is allocated and stepped to
// zero but never sampled.
// ponytail: wasted w x h textures per lock; folding the state pass away for
// stateless effects until someone cares about the memory.
const nullStepFS = `
precision highp float;
void main() {
	gl_FragColor = vec4(0.0);
}
`

// effectVS is the shared fullscreen-triangle vertex shader; aPos is forced to
// location 0 in buildProgram so one VBO setup serves every program.
const effectVS = `
attribute vec2 aPos;
varying vec2 vUv;
void main() {
	vUv = aPos * 0.5 + 0.5;
	gl_Position = vec4(aPos, 0.0, 1.0);
}
`

// rainStepFS advances the streak field one row per tick. The R channel marks
// the head cell, G holds the decaying trail. Screen row 0 is the top edge:
// readback flips rows, so "down" must mean decreasing vUv.y here.
//
// ponytail: the sin-based hash is deterministic on one driver but not
// identical across GPU vendors; cross-device byte equality was never promised.
// highp in the fragment stage is required for hash precision; it is supported
// by both Mesa iris and NVIDIA here, and a driver without it fails init and
// demotes to CPU.
const rainStepFS = `
precision highp float;
varying vec2 vUv;
uniform sampler2D uPrev;
uniform float uTime;
uniform int uSeed;
uniform vec2 uGrid;

float rhash(vec2 p) {
	return fract(sin(dot(p, vec2(127.1, 311.7))) * 43758.5453);
}

void main() {
	vec2 prev = texture2D(uPrev, vUv).rg;
	float col = floor(vUv.x * uGrid.x);
	float row = uGrid.y - 1.0 - floor(vUv.y * uGrid.y);
	float s = float(uSeed);

	// One streak per epoch per column: 30% of columns roll a new start row
	// every 64 ticks, and the head sits on that row + ticks-since-epoch.
	float epoch = floor(uTime / 64.0);
	float spawn = 1.0 - step(0.3, rhash(vec2(col + 17.0, epoch + s)));
	float pos = floor(rhash(vec2(col, epoch + s)) * (uGrid.y - 20.0)) + mod(uTime, 64.0);
	float head = spawn * (1.0 - step(0.5, abs(row - pos)));

	float trail = clamp(prev.g * 0.82 + prev.r * 0.8 + head * 0.9, 0.0, 1.0);
	gl_FragColor = vec4(head, trail, 0.0, 1.0);
}
`

// rainDrawFS turns streak state into color: each column picks a palette stop
// by hash, the trail glows in that tint, and the head mixes toward white.
// Deliberately glyph-free; the GPU frame is pixel art, not cell art.
const rainDrawFS = `
precision highp float;
varying vec2 vUv;
uniform sampler2D uPrev;
uniform vec3 uPalette[8];
uniform int uSeed;
uniform vec2 uGrid;

float rhash(vec2 p) {
	return fract(sin(dot(p, vec2(127.1, 311.7))) * 43758.5453);
}

// GLES2 forbids dynamic uniform-array indexing; the constant-index loop is
// the spec-legal lookup.
vec3 paletteAt(int k) {
	vec3 c = uPalette[0];
	for (int i = 0; i < 8; i++) {
		if (i == k) {
			c = uPalette[i];
		}
	}
	return c;
}

void main() {
	vec2 streak = texture2D(uPrev, vUv).rg;
	float col = floor(vUv.x * uGrid.x);
	int k = int(rhash(vec2(col, float(uSeed))) * 5.99);
	vec3 tint = paletteAt(k);
	vec3 c = tint * streak.g + mix(tint, vec3(1.0), streak.r) * streak.r;
	gl_FragColor = vec4(min(c, vec3(1.0)), 1.0);
}
`

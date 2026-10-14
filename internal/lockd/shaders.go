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
	case "fire":
		return nullStepFS, fireFS, true
	case "fireworks":
		return nullStepFS, fireworksFS, true
	case "beams":
		return nullStepFS, beamsFS, true
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

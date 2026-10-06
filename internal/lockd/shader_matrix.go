package lockd

// Matrix reuses the rain streak state machine with denser spawns and longer
// trails, then draws with a green-dominant ramp instead of per-column tints.

const matrixStepFS = `
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

	// Denser than rain: 55% of columns roll a streak per epoch, decay keeps
	// trails longer.
	float epoch = floor(uTime / 64.0);
	float spawn = 1.0 - step(0.55, rhash(vec2(col + 17.0, epoch + s)));
	float pos = floor(rhash(vec2(col, epoch + s)) * (uGrid.y - 12.0)) + mod(uTime, 64.0);
	float head = spawn * (1.0 - step(0.5, abs(row - pos)));

	float trail = clamp(prev.g * 0.85 + prev.r * 0.8 + head * 0.9, 0.0, 1.0);
	gl_FragColor = vec4(head, trail, 0.0, 1.0);
}
`

const matrixDrawFS = `
precision highp float;
varying vec2 vUv;
uniform sampler2D uPrev;
uniform vec3 uPalette[8];
uniform int uSeed;
uniform vec2 uGrid;

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
	float jitter = fract(sin(dot(vec2(col, float(uSeed)), vec2(127.1, 311.7))) * 43758.5453);
	vec3 tint = mix(vec3(0.1, 0.9, 0.2), paletteAt(int(jitter * 5.99)), 0.3 * jitter);
	vec3 c = tint * streak.g + mix(tint, vec3(1.0), streak.r) * streak.r;
	gl_FragColor = vec4(min(c, vec3(1.0)), 1.0);
}
`

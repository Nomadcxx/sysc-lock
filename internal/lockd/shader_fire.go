package lockd

// Fire is a single-pass upward value-noise flame, thresholded into palette
// ramp rows. It keeps no state: the frame is a pure function of uTime, so
// the step pass is the null program. In this space vUv.y = 1 is the screen
// top (readback flips rows), so the base is at low y and the field rises as
// uTime grows.

const fireFS = effectFSHead + `
float vnoise(vec2 p) {
	vec2 i = floor(p);
	vec2 f = fract(p);
	f = f * f * (3.0 - 2.0 * f);
	float a = rhash(i);
	float b = rhash(i + vec2(1.0, 0.0));
	float c = rhash(i + vec2(0.0, 1.0));
	float d = rhash(i + vec2(1.0, 1.0));
	return mix(mix(a, b, f.x), mix(c, d, f.x), f.y);
}

void main() {
	float s = float(uSeed);
	vec2 p = vec2(vUv.x * 8.0 + s, vUv.y * 8.0 - uTime * 0.5);
	float n = vnoise(p) * 0.6 + vnoise(p * 2.0) * 0.3 + vnoise(p * 4.0) * 0.1;

	float heat = clamp(n + (1.0 - vUv.y) * 0.55 - 0.3, 0.0, 1.0);
	vec3 col = step(0.001, heat) * paletteAt(int(heat * 7.99)) * (0.4 + heat);
	gl_FragColor = vec4(min(col, vec3(1.0)), 1.0);
}
`

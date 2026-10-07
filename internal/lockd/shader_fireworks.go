package lockd

// Fireworks places seven shells at hashed positions, each expanding a ring
// with a fade-out on a black field. Stateless: shells are a pure function of
// time and seed, so the step pass is the null program.

const fireworksFS = effectFSHead + `
void main() {
	float s = float(uSeed);
	vec3 col = vec3(0.0);
	// Constant loop bound: GLES2 requires it, and seven shells is enough.
	for (int i = 0; i < 7; i++) {
		float k = float(i);
		vec2 center = vec2(rhash(vec2(k, s)), rhash(vec2(k + 31.0, s + 7.0)));
		float start = rhash(vec2(k + 53.0, s)) * 47.0;
		float age = mod(uTime - start, 47.0);
		float fade = 1.0 - age / 47.0;

		// Ring radius grows with age; brightness is the fade times a sharp
		// radial falloff, with a faint core just after launch.
		float d = length(vUv - center);
		float ring = smoothstep(0.04, 0.0, abs(d - age * 0.008));
		float core = smoothstep(0.02, 0.0, d) * step(age, 4.0);
		col += paletteAt(int(rhash(vec2(k + 71.0, s)) * 7.99)) * (ring + core) * fade;
	}
	gl_FragColor = vec4(min(col, vec3(1.0)), 1.0);
}
`

package lockd

// Beams renders six soft vertical beams whose positions drift and whose
// brightness re-rolls on a hash epoch. Stateless: the step pass is the null
// program.

const beamsFS = effectFSHead + `
void main() {
	float s = float(uSeed);
	float epoch = floor(uTime * 0.05);
	vec3 col = vec3(0.0);
	for (int i = 0; i < 6; i++) {
		float k = float(i);
		float bucket = (k + 0.5) / 6.0;
		float drift = (rhash(vec2(k, epoch + s)) - 0.5) * 0.12;
		float pos = bucket + drift;
		float amp = rhash(vec2(k + 91.0, epoch + s));

		float dx = vUv.x - pos;
		float beam = amp * exp(-dx * dx * 400.0);

		// Vertical gradient: bright at the top, fading down the column.
		col += paletteAt(int(rhash(vec2(k + 17.0, s)) * 7.99)) * beam * vUv.y;
	}
	gl_FragColor = vec4(min(col, vec3(1.0)), 1.0);
}
`

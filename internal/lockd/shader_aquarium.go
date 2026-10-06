package lockd

// Aquarium paints three slow sine bands plus four oval "fish" cruising on
// hashed lanes. Stateless: the step pass is the null program.

const aquariumFS = effectFSHead + `
void main() {
	float s = float(uSeed);
	vec3 col = vec3(0.0);

	for (int i = 0; i < 3; i++) {
		float k = float(i);
		float band = 0.5 + 0.5 * sin(vUv.y * (k + 1.0) * 6.2831853 + uTime * 0.04 + k * 2.0);
		col += paletteAt(int(k * 2.0)) * band * 0.08;
	}

	for (int j = 0; j < 4; j++) {
		float k = float(j);
		float speed = 0.004 + rhash(vec2(k, s)) * 0.008;
		float lane = 0.15 + rhash(vec2(k + 9.0, s)) * 0.7;
		float cx = fract(rhash(vec2(k + 3.0, s)) + uTime * speed);
		float bob = lane + 0.05 * sin(uTime * 0.1 + k * 1.7);

		vec2 d = vec2(vUv.x - cx, (vUv.y - bob) * 3.0);
		float fish = smoothstep(0.09, 0.02, length(d));
		col += paletteAt(3 + int(rhash(vec2(k + 5.0, s + 11.0)) * 4.99)) * fish;
	}
	gl_FragColor = vec4(min(col, vec3(1.0)), 1.0);
}
`

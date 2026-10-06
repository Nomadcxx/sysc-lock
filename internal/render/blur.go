package render

// Blur returns src downsampled by factor and box-blurred by radius in
// pixels, as a smaller framebuffer. The backdrop is only ever drawn
// upscaled behind text, so the reduced copy is the product: keeping the
// full-resolution blur would cost 16x the memory for pixels nobody sees
// at native scale. radius below factor only downsamples. Ported from
// sysc-shell; three passes approximate a Gaussian cheaply.
func Blur(src *Framebuffer, factor, radius int) *Framebuffer {
	if src == nil || src.Width < 1 || src.Height < 1 || len(src.Pix) == 0 {
		return nil
	}
	if factor < 1 {
		factor = 1
	}
	w := (src.Width + factor - 1) / factor
	h := (src.Height + factor - 1) / factor
	dst := New(w, h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var sum [3]int
			n := 0
			for dy := 0; dy < factor; dy++ {
				sy := y*factor + dy
				if sy >= src.Height {
					continue
				}
				for dx := 0; dx < factor; dx++ {
					sx := x*factor + dx
					if sx >= src.Width {
						continue
					}
					i := sy*src.Stride + sx*4
					for c := 0; c < 3; c++ {
						sum[c] += int(src.Pix[i+c])
					}
					n++
				}
			}
			if n == 0 {
				n = 1
			}
			o := y*dst.Stride + x*4
			for c := 0; c < 3; c++ {
				dst.Pix[o+c] = uint8(sum[c] / n)
			}
			dst.Pix[o+3] = 0xff
		}
	}
	if radius < 1 {
		return dst
	}
	k := radius / factor
	if k < 1 {
		k = 1
	}
	for pass := 0; pass < 3; pass++ {
		boxH(dst.Pix, dst.Width, dst.Height, dst.Stride, k)
		boxV(dst.Pix, dst.Width, dst.Height, dst.Stride, k)
	}
	return dst
}

// boxH blurs each row in place with a window of 2k+1 taps, clamping at
// the edges by replicating the end pixels rather than wrapping.
func boxH(pix []byte, width, height, stride, k int) {
	div := 2*k + 1
	line := make([]byte, width*4)
	for y := 0; y < height; y++ {
		row := pix[y*stride : y*stride+width*4]
		copy(line, row)
		for x := 0; x < width; x++ {
			var sum [3]int
			for t := -k; t <= k; t++ {
				sx := clampIdx(x+t, width)
				for c := 0; c < 3; c++ {
					sum[c] += int(line[sx*4+c])
				}
			}
			for c := 0; c < 3; c++ {
				row[x*4+c] = uint8(sum[c] / div)
			}
			row[x*4+3] = 0xff
		}
	}
}

// boxV is boxH over columns. Columns are gathered into a scratch line,
// blurred, and scattered back.
func boxV(pix []byte, width, height, stride, k int) {
	div := 2*k + 1
	line := make([]byte, height*4)
	for x := 0; x < width; x++ {
		for y := 0; y < height; y++ {
			copy(line[y*4:y*4+4], pix[y*stride+x*4:y*stride+x*4+4])
		}
		for y := 0; y < height; y++ {
			var sum [3]int
			for t := -k; t <= k; t++ {
				sy := clampIdx(y+t, height)
				for c := 0; c < 3; c++ {
					sum[c] += int(line[sy*4+c])
				}
			}
			for c := 0; c < 3; c++ {
				pix[y*stride+x*4+c] = uint8(sum[c] / div)
			}
			pix[y*stride+x*4+3] = 0xff
		}
	}
}

func clampIdx(i, n int) int {
	if i < 0 {
		return 0
	}
	if i >= n {
		return n - 1
	}
	return i
}

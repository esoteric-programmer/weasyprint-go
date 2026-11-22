package images

import (
	"image"
	"image/color"
	"image/draw"
)

// Resample scales 8-bit samples (comps per pixel) from w×h to nw×nh by
// averaging the source pixels covered by each destination pixel.
func Resample(src []byte, comps, w, h, nw, nh int) []byte {
	out := make([]byte, comps*nw*nh)
	sum := make([]float64, comps)
	for y := 0; y < nh; y++ {
		y0 := float64(y) * float64(h) / float64(nh)
		y1 := float64(y+1) * float64(h) / float64(nh)
		for x := 0; x < nw; x++ {
			x0 := float64(x) * float64(w) / float64(nw)
			x1 := float64(x+1) * float64(w) / float64(nw)
			for i := range sum {
				sum[i] = 0
			}
			total := 0.0
			for sy := int(y0); sy < h && float64(sy) < y1; sy++ {
				wy := minF(y1, float64(sy+1)) - maxF(y0, float64(sy))
				for sx := int(x0); sx < w && float64(sx) < x1; sx++ {
					wx := minF(x1, float64(sx+1)) - maxF(x0, float64(sx))
					wt := wx * wy
					p := (sy*w + sx) * comps
					for c := 0; c < comps; c++ {
						sum[c] += float64(src[p+c]) * wt
					}
					total += wt
				}
			}
			p := (y*nw + x) * comps
			for c := 0; c < comps; c++ {
				if total > 0 {
					out[p+c] = byte(sum[c]/total + 0.5)
				}
			}
		}
	}
	return out
}

func minF(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

func maxF(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

// Scale scales an image to w×h.
func Scale(img image.Image, w, h int) image.Image {
	b := img.Bounds()
	switch img.ColorModel() {
	case color.GrayModel, color.Gray16Model:
		g := image.NewGray(image.Rect(0, 0, b.Dx(), b.Dy()))
		draw.Draw(g, g.Bounds(), img, b.Min, draw.Src)
		out := image.NewGray(image.Rect(0, 0, w, h))
		out.Pix = Resample(g.Pix, 1, b.Dx(), b.Dy(), w, h)
		return out
	case color.CMYKModel:
		if c, ok := img.(*image.CMYK); ok {
			out := image.NewCMYK(image.Rect(0, 0, w, h))
			src := c
			if c.Stride != 4*b.Dx() || b.Min != (image.Point{}) {
				src = image.NewCMYK(image.Rect(0, 0, b.Dx(), b.Dy()))
				draw.Draw(src, src.Bounds(), c, b.Min, draw.Src)
			}
			out.Pix = Resample(src.Pix, 4, b.Dx(), b.Dy(), w, h)
			return out
		}
	}
	n := image.NewNRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(n, n.Bounds(), img, b.Min, draw.Src)
	out := image.NewNRGBA(image.Rect(0, 0, w, h))
	out.Pix = Resample(n.Pix, 4, b.Dx(), b.Dy(), w, h)
	return out
}

// ResampleAlpha scales color samples and their alpha channel together,
// weighting colors by their opacity so that transparent pixels do not
// bleed into visible ones.
func ResampleAlpha(src []byte, comps int, alpha []byte, w, h, nw, nh int) ([]byte, []byte) {
	pre := make([]byte, 0, (comps+1)*w*h)
	for i := 0; i < w*h; i++ {
		pre = append(pre, src[comps*i:comps*i+comps]...)
		pre = append(pre, alpha[i])
	}
	// Premultiplied samples, kept at 16 bits through a float average.
	n := comps + 1
	out := make([]byte, comps*nw*nh)
	outA := make([]byte, nw*nh)
	sum := make([]float64, n)
	for y := 0; y < nh; y++ {
		y0 := float64(y) * float64(h) / float64(nh)
		y1 := float64(y+1) * float64(h) / float64(nh)
		for x := 0; x < nw; x++ {
			x0 := float64(x) * float64(w) / float64(nw)
			x1 := float64(x+1) * float64(w) / float64(nw)
			for i := range sum {
				sum[i] = 0
			}
			total := 0.0
			for sy := int(y0); sy < h && float64(sy) < y1; sy++ {
				wy := minF(y1, float64(sy+1)) - maxF(y0, float64(sy))
				for sx := int(x0); sx < w && float64(sx) < x1; sx++ {
					wt := (minF(x1, float64(sx+1)) - maxF(x0, float64(sx))) * wy
					p := (sy*w + sx) * n
					a := float64(pre[p+comps])
					for c := 0; c < comps; c++ {
						sum[c] += wt * a * float64(pre[p+c])
					}
					sum[comps] += wt * a
					total += wt
				}
			}
			o := y*nw + x
			if total == 0 {
				continue
			}
			outA[o] = byte(sum[comps]/total + 0.5)
			for c := 0; c < comps; c++ {
				if sum[comps] > 0 {
					out[o*comps+c] = byte(minF(255, sum[c]/sum[comps]+0.5))
				}
			}
		}
	}
	return out, outA
}

package images

import (
	"errors"
	"image"
	"math"
)

// Block compression (BC1–BC7, a.k.a. DXT) decoders, computing like
// Pillow's BcnDecode.c.

type bcPixel [4]int

func decode565(c int) bcPixel {
	r := (c & 0xf800) >> 8
	g := (c & 0x7e0) >> 3
	b := (c & 0x1f) << 3
	return bcPixel{r | r>>5, g | g>>6, b | b>>5, 255}
}

// bc1Colors decodes a BC1 color block into out.
func bc1Colors(out *[16]bcPixel, s []byte, separateAlpha bool) {
	c0 := int(s[0]) | int(s[1])<<8
	c1 := int(s[2]) | int(s[3])<<8
	var p [4]bcPixel
	p[0], p[1] = decode565(c0), decode565(c1)
	if c0 > c1 || separateAlpha {
		for k := 0; k < 3; k++ {
			p[2][k] = (2*p[0][k] + p[1][k]) / 3
			p[3][k] = (p[0][k] + 2*p[1][k]) / 3
		}
		p[2][3], p[3][3] = 255, 255
	} else {
		for k := 0; k < 3; k++ {
			p[2][k] = (p[0][k] + p[1][k]) / 2
		}
		p[2][3] = 255
		p[3] = bcPixel{0, 0, 0, 0}
	}
	lut := uint32(s[4]) | uint32(s[5])<<8 | uint32(s[6])<<16 | uint32(s[7])<<24
	for i := 0; i < 16; i++ {
		out[i] = p[lut>>(2*uint(i))&3]
	}
}

// bc3Alpha decodes a BC3 alpha block (or a BC4/BC5 channel) into channel
// ch of out.
func bc3Alpha(out *[16]bcPixel, s []byte, ch int, signed bool) {
	var a [8]int
	if signed {
		a0, a1 := int(int8(s[0])), int(int8(s[1]))
		a[0], a[1] = a0, a1
		if a0 > a1 {
			for i := 2; i < 8; i++ {
				a[i] = ((8-i)*a0 + (i-1)*a1) / 7
			}
		} else {
			for i := 2; i < 6; i++ {
				a[i] = ((6-i)*a0 + (i-1)*a1) / 5
			}
			a[6], a[7] = -128, 127
		}
		for i := range a {
			a[i] += 128
		}
	} else {
		a0, a1 := int(s[0]), int(s[1])
		a[0], a[1] = a0, a1
		if a0 > a1 {
			for i := 2; i < 8; i++ {
				a[i] = ((8-i)*a0 + (i-1)*a1) / 7
			}
		} else {
			for i := 2; i < 6; i++ {
				a[i] = ((6-i)*a0 + (i-1)*a1) / 5
			}
			a[6], a[7] = 0, 255
		}
	}
	var lut uint64
	for i := 0; i < 6; i++ {
		lut |= uint64(s[2+i]) << (8 * uint(i))
	}
	for i := 0; i < 16; i++ {
		out[i][ch] = a[lut>>(3*uint(i))&7]
	}
}

// decodeBCn decodes BC1 to BC5 (n = -5 for signed BC5) data.
func decodeBCn(data []byte, w, h, n int, noAlpha bool) (image.Image, error) {
	bs := 16
	if n == 1 || n == 4 {
		bs = 8
	}
	bw, bh := (w+3)/4, (h+3)/4
	if len(data) < bw*bh*bs {
		return nil, errCorrupt
	}
	var gray *image.Gray
	var img *image.NRGBA
	if n == 4 {
		gray = newGray(w, h)
	} else {
		img = newNRGBA(w, h)
	}
	for by := 0; by < bh; by++ {
		for bx := 0; bx < bw; bx++ {
			s := data[(by*bw+bx)*bs:]
			var px [16]bcPixel
			switch n {
			case 1:
				bc1Colors(&px, s, false)
			case 2:
				bc1Colors(&px, s[8:], true)
				for i := 0; i < 16; i++ {
					a := int(s[i/2]>>(4*uint(i&1))) & 15
					px[i][3] = a | a<<4
				}
			case 3:
				bc1Colors(&px, s[8:], true)
				bc3Alpha(&px, s, 3, false)
			case 4:
				bc3Alpha(&px, s, 0, false)
			case 5, -5:
				bc3Alpha(&px, s, 0, n < 0)
				bc3Alpha(&px, s[8:], 1, n < 0)
				for i := range px {
					px[i][3] = 255
				}
			}
			for i := 0; i < 16; i++ {
				x, y := bx*4+i%4, by*4+i/4
				if x >= w || y >= h {
					continue
				}
				if gray != nil {
					gray.Pix[y*w+x] = byte(px[i][0])
					continue
				}
				o := img.PixOffset(x, y)
				for k := 0; k < 4; k++ {
					img.Pix[o+k] = byte(px[i][k])
				}
				if noAlpha {
					img.Pix[o+3] = 255
				}
			}
		}
	}
	if gray != nil {
		return gray, nil
	}
	return img, nil
}

// ---------------------------------------------------------------- BC7

type bitReader128 struct {
	s   []byte
	pos int
}

func (b *bitReader128) bits(n int) int {
	v := 0
	for i := 0; i < n; i++ {
		p := b.pos + i
		v |= int(b.s[p>>3]>>uint(p&7)&1) << uint(i)
	}
	b.pos += n
	return v
}

var (
	bcWeights2 = []int{0, 21, 43, 64}
	bcWeights3 = []int{0, 9, 18, 27, 37, 46, 55, 64}
	bcWeights4 = []int{0, 4, 9, 13, 17, 21, 26, 30, 34, 38, 43, 47, 51, 55, 60, 64}
)

func bcWeights(bits int) []int {
	switch bits {
	case 2:
		return bcWeights2
	case 3:
		return bcWeights3
	}
	return bcWeights4
}

type bc7Mode struct {
	ns, pb, rb, isb, cb, ab, epb, spb, ib, ib2 int
}

var bc7Modes = [8]bc7Mode{
	{3, 4, 0, 0, 4, 0, 1, 0, 3, 0},
	{2, 6, 0, 0, 6, 0, 0, 1, 3, 0},
	{3, 6, 0, 0, 5, 0, 0, 0, 2, 0},
	{2, 6, 0, 0, 7, 0, 1, 0, 2, 0},
	{1, 0, 2, 1, 5, 6, 0, 0, 2, 3},
	{1, 0, 2, 0, 7, 8, 0, 0, 2, 2},
	{1, 0, 0, 0, 7, 7, 1, 0, 4, 0},
	{2, 6, 0, 0, 5, 5, 1, 0, 2, 0},
}

func bc7Subset(ns, part, i int) int {
	switch ns {
	case 2:
		return int(bc7Partitions2[part]>>uint(i)) & 1
	case 3:
		return int(bc7Partitions3[part]>>(2*uint(i))) & 3
	}
	return 0
}

func bc7IsAnchor(ns, part, i int) bool {
	if i == 0 {
		return true
	}
	switch ns {
	case 2:
		return i == int(bc7Anchor2[part])
	case 3:
		return i == int(bc7Anchor3a[part]) || i == int(bc7Anchor3b[part])
	}
	return false
}

func bc7Block(out *[16]bcPixel, s []byte) {
	mode := 0
	for mode < 8 && s[0]&(1<<uint(mode)) == 0 {
		mode++
	}
	if mode == 8 {
		*out = [16]bcPixel{}
		return
	}
	m := bc7Modes[mode]
	br := &bitReader128{s: s, pos: mode + 1}
	part := br.bits(m.pb)
	rot := br.bits(m.rb)
	isel := br.bits(m.isb)
	var ep [6][4]int // endpoints: subset*2 + k
	for c := 0; c < 3; c++ {
		for e := 0; e < 2*m.ns; e++ {
			ep[e][c] = br.bits(m.cb)
		}
	}
	for e := 0; e < 2*m.ns; e++ {
		if m.ab > 0 {
			ep[e][3] = br.bits(m.ab)
		} else {
			ep[e][3] = 255
		}
	}
	cb, ab := m.cb, m.ab
	if m.epb > 0 || m.spb > 0 {
		var pbits [6]int
		if m.epb > 0 {
			for e := 0; e < 2*m.ns; e++ {
				pbits[e] = br.bits(1)
			}
		} else {
			for sub := 0; sub < m.ns; sub++ {
				pb := br.bits(1)
				pbits[2*sub], pbits[2*sub+1] = pb, pb
			}
		}
		for e := 0; e < 2*m.ns; e++ {
			for c := 0; c < 3; c++ {
				ep[e][c] = ep[e][c]<<1 | pbits[e]
			}
			if m.ab > 0 {
				ep[e][3] = ep[e][3]<<1 | pbits[e]
			}
		}
		cb++
		if m.ab > 0 {
			ab++
		}
	}
	expand := func(v, n int) int {
		v <<= uint(8 - n)
		return v | v>>uint(n)
	}
	for e := 0; e < 2*m.ns; e++ {
		for c := 0; c < 3; c++ {
			ep[e][c] = expand(ep[e][c], cb)
		}
		if m.ab > 0 {
			ep[e][3] = expand(ep[e][3], ab)
		}
	}
	var idx1, idx2 [16]int
	for i := 0; i < 16; i++ {
		n := m.ib
		if bc7IsAnchor(m.ns, part, i) && (i == 0 || bc7Subset(m.ns, part, i) != 0) {
			n--
		}
		idx1[i] = br.bits(n)
	}
	if m.ib2 > 0 {
		for i := 0; i < 16; i++ {
			n := m.ib2
			if i == 0 {
				n--
			}
			idx2[i] = br.bits(n)
		}
	}
	for i := 0; i < 16; i++ {
		sub := bc7Subset(m.ns, part, i)
		e0, e1 := ep[2*sub], ep[2*sub+1]
		cw, aw := bcWeights(m.ib)[idx1[i]], bcWeights(m.ib)[idx1[i]]
		if m.ib2 > 0 {
			w2 := bcWeights(m.ib2)[idx2[i]]
			if isel != 0 {
				cw = w2
			} else {
				aw = w2
			}
		}
		var p bcPixel
		for c := 0; c < 3; c++ {
			p[c] = ((64-cw)*e0[c] + cw*e1[c] + 32) >> 6
		}
		p[3] = ((64-aw)*e0[3] + aw*e1[3] + 32) >> 6
		switch rot {
		case 1:
			p[0], p[3] = p[3], p[0]
		case 2:
			p[1], p[3] = p[3], p[1]
		case 3:
			p[2], p[3] = p[3], p[2]
		}
		out[i] = p
	}
}

func decodeBC7(data []byte, w, h int) (image.Image, error) {
	bw, bh := (w+3)/4, (h+3)/4
	if len(data) < bw*bh*16 {
		return nil, errCorrupt
	}
	img := newNRGBA(w, h)
	for by := 0; by < bh; by++ {
		for bx := 0; bx < bw; bx++ {
			var px [16]bcPixel
			bc7Block(&px, data[(by*bw+bx)*16:])
			for i := 0; i < 16; i++ {
				x, y := bx*4+i%4, by*4+i/4
				if x < w && y < h {
					o := img.PixOffset(x, y)
					for k := 0; k < 4; k++ {
						img.Pix[o+k] = byte(px[i][k])
					}
				}
			}
		}
	}
	return img, nil
}

// ---------------------------------------------------------------- BC6H

type bc6Mode struct {
	ns, tr, epb, rb, gb, bb int
}

var bc6Modes = [14]bc6Mode{
	{2, 1, 10, 5, 5, 5}, {2, 1, 7, 6, 6, 6},
	{2, 1, 11, 5, 4, 4}, {2, 1, 11, 4, 5, 4}, {2, 1, 11, 4, 4, 5}, {2, 1, 9, 5, 5, 5},
	{2, 1, 8, 6, 5, 5}, {2, 1, 8, 5, 6, 5}, {2, 1, 8, 5, 5, 6}, {2, 0, 6, 6, 6, 6},
	{1, 0, 10, 10, 10, 10}, {1, 1, 11, 9, 9, 9}, {1, 1, 12, 8, 8, 8}, {1, 1, 16, 4, 4, 4},
}

func signExtend(v, bits int) int {
	if bits >= 32 {
		return v
	}
	if v&(1<<uint(bits-1)) != 0 {
		return v - 1<<uint(bits)
	}
	return v
}

func bc6Unquantize(v, bits int, signed bool) int {
	if !signed {
		switch {
		case bits >= 15:
			return v
		case v == 0:
			return 0
		case v == 1<<uint(bits)-1:
			return 0xffff
		}
		return ((v << 15) + 0x4000) >> uint(bits-1)
	}
	if bits >= 16 {
		return v
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var u int
	switch {
	case v == 0:
		u = 0
	case v >= 1<<uint(bits-1)-1:
		u = 0x7fff
	default:
		u = ((v << 15) + 0x4000) >> uint(bits-1)
	}
	if neg {
		u = -u
	}
	return u
}

func halfToFloat(h uint16) float64 {
	sign := float64(1)
	if h&0x8000 != 0 {
		sign = -1
	}
	e := int(h>>10) & 31
	m := float64(h & 0x3ff)
	switch e {
	case 0:
		return sign * m * math.Pow(2, -24)
	case 31:
		if m == 0 {
			return sign * math.Inf(1)
		}
		return math.NaN()
	}
	return sign * (1 + m/1024) * math.Pow(2, float64(e-15))
}

func bc6Block(out *[16][3]float64, s []byte, signed bool) {
	mode := int(s[0] & 0x1f)
	bit, epbits, ib := 5, 75, 3
	switch mode & 3 {
	case 0, 1:
		mode &= 3
		bit = 2
	case 2:
		mode = 2 + mode>>2
		epbits = 72
	default:
		mode = 10 + mode>>2
		epbits = 60
		ib = 4
	}
	if mode >= 14 {
		*out = [16][3]float64{}
		return
	}
	m := bc6Modes[mode]
	br := &bitReader128{s: s, pos: bit}
	var ep [12]int
	for i := 0; i < epbits; i++ {
		di := int(bc6BitPackings[mode][i])
		ep[di>>4] |= br.bits(1) << uint(di&15)
	}
	part := 0
	if m.ns == 2 {
		part = br.bits(5)
	}
	numep := 6
	if m.ns == 2 {
		numep = 12
	}
	widths := [3]int{m.rb, m.gb, m.bb}
	if signed {
		for c := 0; c < 3; c++ {
			ep[c] = signExtend(ep[c], m.epb)
		}
	}
	for i := 3; i < numep; i++ {
		c := i % 3
		if m.tr != 0 {
			ep[i] = signExtend(ep[i], widths[c])
			ep[i] = (ep[c] + ep[i]) & (1<<uint(m.epb) - 1)
			if signed {
				ep[i] = signExtend(ep[i], m.epb)
			}
		} else if signed {
			ep[i] = signExtend(ep[i], m.epb)
		}
	}
	for i := 0; i < numep; i++ {
		ep[i] = bc6Unquantize(ep[i], m.epb, signed)
	}
	for i := 0; i < 16; i++ {
		n := ib
		sub := 0
		if m.ns == 2 {
			sub = bc7Subset(2, part, i)
		}
		if i == 0 || (m.ns == 2 && i == int(bc7Anchor2[part]) && sub == 1) {
			n--
		}
		w := bcWeights(ib)[br.bits(n)]
		for c := 0; c < 3; c++ {
			e0, e1 := ep[6*sub+c], ep[6*sub+3+c]
			v := ((64-w)*e0 + w*e1 + 32) >> 6
			var hv uint16
			if signed {
				if v < 0 {
					hv = uint16(((-v)*31)>>5) | 0x8000
				} else {
					hv = uint16((v * 31) >> 5)
				}
			} else {
				hv = uint16((v * 31) >> 6)
			}
			out[i][c] = halfToFloat(hv)
		}
	}
}

func decodeBC6H(data []byte, w, h int, signed bool) (image.Image, error) {
	bw, bh := (w+3)/4, (h+3)/4
	if len(data) < bw*bh*16 {
		return nil, errors.New("truncated BC6H data")
	}
	img := newNRGBA(w, h)
	for by := 0; by < bh; by++ {
		for bx := 0; bx < bw; bx++ {
			var px [16][3]float64
			bc6Block(&px, data[(by*bw+bx)*16:], signed)
			for i := 0; i < 16; i++ {
				x, y := bx*4+i%4, by*4+i/4
				if x < w && y < h {
					o := img.PixOffset(x, y)
					for k := 0; k < 3; k++ {
						img.Pix[o+k] = clamp8(px[i][k] * 255)
					}
					img.Pix[o+3] = 255
				}
			}
		}
	}
	return img, nil
}

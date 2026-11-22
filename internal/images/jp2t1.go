package images

// JPEG 2000 tier-1 decoding: the MQ arithmetic decoder and the bit-plane
// passes of code-blocks (ITU-T T.800 Annexes C and D).

type mqState struct {
	qe         uint32
	nmps, nlps uint8
	sw         bool
}

var mqStates = [47]mqState{
	{0x5601, 1, 1, true}, {0x3401, 2, 6, false}, {0x1801, 3, 9, false}, {0x0ac1, 4, 12, false},
	{0x0521, 5, 29, false}, {0x0221, 38, 33, false}, {0x5601, 7, 6, true}, {0x5401, 8, 14, false},
	{0x4801, 9, 14, false}, {0x3801, 10, 14, false}, {0x3001, 11, 17, false}, {0x2401, 12, 18, false},
	{0x1c01, 13, 20, false}, {0x1601, 29, 21, false}, {0x5601, 15, 14, true}, {0x5401, 16, 14, false},
	{0x5101, 17, 15, false}, {0x4801, 18, 16, false}, {0x3801, 19, 17, false}, {0x3401, 20, 18, false},
	{0x3001, 21, 19, false}, {0x2801, 22, 19, false}, {0x2401, 23, 20, false}, {0x2201, 24, 21, false},
	{0x1c01, 25, 22, false}, {0x1801, 26, 23, false}, {0x1601, 27, 24, false}, {0x1401, 28, 25, false},
	{0x1201, 29, 26, false}, {0x1101, 30, 27, false}, {0x0ac1, 31, 28, false}, {0x09c1, 32, 29, false},
	{0x08a1, 33, 30, false}, {0x0521, 34, 31, false}, {0x0441, 35, 32, false}, {0x02a1, 36, 33, false},
	{0x0221, 37, 34, false}, {0x0141, 38, 35, false}, {0x0111, 39, 36, false}, {0x0085, 40, 37, false},
	{0x0049, 41, 38, false}, {0x0025, 42, 39, false}, {0x0015, 43, 40, false}, {0x0009, 44, 41, false},
	{0x0005, 45, 42, false}, {0x0001, 45, 43, false}, {0x5601, 46, 46, false},
}

const (
	ctxRun = 17
	ctxUni = 18
)

type mqContexts struct {
	idx [19]uint8
	mps [19]uint8
}

func (cx *mqContexts) reset() {
	*cx = mqContexts{}
	cx.idx[0] = 4
	cx.idx[ctxRun] = 3
	cx.idx[ctxUni] = 46
}

type mqDecoder struct {
	data []byte
	pos  int
	a, c uint32
	ct   int
}

func (m *mqDecoder) byteAt(i int) uint32 {
	if i < len(m.data) {
		return uint32(m.data[i])
	}
	return 0xff
}

func (m *mqDecoder) init(data []byte) {
	m.data, m.pos = data, 0
	m.c = m.byteAt(0) << 16
	m.byteIn()
	m.c <<= 7
	m.ct -= 7
	m.a = 0x8000
}

func (m *mqDecoder) byteIn() {
	if m.byteAt(m.pos) == 0xff {
		b1 := m.byteAt(m.pos + 1)
		if b1 > 0x8f {
			m.c += 0xff00
			m.ct = 8
		} else {
			m.pos++
			m.c += b1 << 9
			m.ct = 7
		}
	} else {
		m.pos++
		m.c += m.byteAt(m.pos) << 8
		m.ct = 8
	}
}

func (m *mqDecoder) renorm() {
	for {
		if m.ct == 0 {
			m.byteIn()
		}
		m.a <<= 1
		m.c <<= 1
		m.ct--
		if m.a&0x8000 != 0 {
			break
		}
	}
}

func (m *mqDecoder) decode(cx *mqContexts, i int) int {
	st := &mqStates[cx.idx[i]]
	m.a -= st.qe
	var d int
	if m.c>>16 < st.qe {
		// LPS sub-interval (with conditional exchange).
		if m.a < st.qe {
			d = int(cx.mps[i])
			cx.idx[i] = st.nmps
		} else {
			d = 1 - int(cx.mps[i])
			if st.sw {
				cx.mps[i] = 1 - cx.mps[i]
			}
			cx.idx[i] = st.nlps
		}
		m.a = st.qe
	} else {
		m.c -= st.qe << 16
		if m.a&0x8000 != 0 {
			return int(cx.mps[i])
		}
		if m.a < st.qe {
			d = 1 - int(cx.mps[i])
			if st.sw {
				cx.mps[i] = 1 - cx.mps[i]
			}
			cx.idx[i] = st.nlps
		} else {
			d = int(cx.mps[i])
			cx.idx[i] = st.nmps
		}
	}
	m.renorm()
	return d
}

// rawDecoder reads the bits of bypassed (raw) passes.
type rawDecoder struct {
	data []byte
	pos  int
	c    uint32
	ct   int
}

func (r *rawDecoder) init(data []byte) { *r = rawDecoder{data: data} }

func (r *rawDecoder) bit() int {
	if r.ct == 0 {
		next := uint32(0xff)
		if r.pos < len(r.data) {
			next = uint32(r.data[r.pos])
		}
		if r.c == 0xff {
			if next > 0x8f {
				r.c = 0xff
				r.ct = 8
			} else {
				r.c = next
				r.pos++
				r.ct = 7
			}
		} else {
			r.c = next
			r.pos++
			r.ct = 8
		}
	}
	r.ct--
	return int(r.c>>uint(r.ct)) & 1
}

// Code-block styles.
const (
	cbBypass   = 0x01
	cbReset    = 0x02
	cbTermAll  = 0x04
	cbVCausal  = 0x08
	cbSegSym   = 0x20
	bandLL     = 0
	bandHL     = 1
	bandLH     = 2
	bandHH     = 3
	flagSig    = 1
	flagVisit  = 2
	flagRefine = 4
	flagNeg    = 8
)

type cbSegment struct {
	data   []byte
	passes int // passes in the segment so far
	max    int // passes the segment can hold
	raw    bool
}

// sigContext returns the significance context of a coefficient.
func sigContext(band, h, v, d int) int {
	if band == bandHL {
		h, v = v, h
	}
	if band == bandHH {
		hv := h + v
		switch {
		case d >= 3:
			return 8
		case d == 2:
			if hv >= 1 {
				return 7
			}
			return 6
		case d == 1:
			switch {
			case hv >= 2:
				return 5
			case hv == 1:
				return 4
			}
			return 3
		}
		switch {
		case hv >= 2:
			return 2
		case hv == 1:
			return 1
		}
		return 0
	}
	switch h {
	case 2:
		return 8
	case 1:
		switch {
		case v >= 1:
			return 7
		case d >= 1:
			return 6
		}
		return 5
	}
	switch {
	case v == 2:
		return 4
	case v == 1:
		return 3
	case d >= 2:
		return 2
	case d == 1:
		return 1
	}
	return 0
}

// t1Block decodes the passes of a code-block into vals: magnitudes in
// half units (see below) with flagNeg in flags for negative values.
type t1Block struct {
	w, h  int
	band  int
	style int
	vals  []int32
	flags []uint8
	mq    mqDecoder
	raw   rawDecoder
	cx    mqContexts
}

func (b *t1Block) sig(x, y int) bool {
	if x < 0 || y < 0 || x >= b.w || y >= b.h {
		return false
	}
	return b.flags[y*b.w+x]&flagSig != 0
}

// neighbors counts the significant neighbors (horizontal, vertical,
// diagonal); with vertically causal contexts, the rows of the next stripe
// are ignored.
func (b *t1Block) neighbors(x, y int) (int, int, int) {
	below := y + 1
	causal := b.style&cbVCausal != 0 && below%4 == 0
	h, v, d := 0, 0, 0
	if b.sig(x-1, y) {
		h++
	}
	if b.sig(x+1, y) {
		h++
	}
	if b.sig(x, y-1) {
		v++
	}
	if !causal && b.sig(x, below) {
		v++
	}
	if b.sig(x-1, y-1) {
		d++
	}
	if b.sig(x+1, y-1) {
		d++
	}
	if !causal {
		if b.sig(x-1, below) {
			d++
		}
		if b.sig(x+1, below) {
			d++
		}
	}
	return h, v, d
}

func (b *t1Block) signContrib(x, y int, causal bool) int {
	if causal || !b.sig(x, y) {
		return 0
	}
	if b.flags[y*b.w+x]&flagNeg != 0 {
		return -1
	}
	return 1
}

func (b *t1Block) decodeSign(x, y int) {
	causal := b.style&cbVCausal != 0 && (y+1)%4 == 0
	h := b.signContrib(x-1, y, false) + b.signContrib(x+1, y, false)
	v := b.signContrib(x, y-1, false) + b.signContrib(x, y+1, causal)
	h, v = max(-1, min(1, h)), max(-1, min(1, v))
	var ctx, xor int
	switch {
	case h == 1:
		ctx = 12 + v
	case h == 0:
		switch v {
		case 1:
			ctx = 10
		case 0:
			ctx = 9
		default:
			ctx, xor = 10, 1
		}
	default:
		ctx, xor = 12-v, 1
	}
	if b.mq.decode(&b.cx, ctx)^xor != 0 {
		b.flags[y*b.w+x] |= flagNeg
	}
}

func (b *t1Block) sigProp(p int, raw bool) {
	for y0 := 0; y0 < b.h; y0 += 4 {
		for x := 0; x < b.w; x++ {
			for y := y0; y < min(y0+4, b.h); y++ {
				i := y*b.w + x
				if b.flags[i]&flagSig != 0 {
					continue
				}
				h, v, d := b.neighbors(x, y)
				if h+v+d == 0 {
					continue
				}
				var bit int
				if raw {
					bit = b.raw.bit()
				} else {
					bit = b.mq.decode(&b.cx, sigContext(b.band, h, v, d))
				}
				b.flags[i] |= flagVisit
				if bit != 0 {
					if raw {
						if b.raw.bit() != 0 {
							b.flags[i] |= flagNeg
						}
					} else {
						b.decodeSign(x, y)
					}
					b.flags[i] |= flagSig
					b.vals[i] = 3 << uint(p)
				}
			}
		}
	}
}

func (b *t1Block) magRef(p int, raw bool) {
	for y0 := 0; y0 < b.h; y0 += 4 {
		for x := 0; x < b.w; x++ {
			for y := y0; y < min(y0+4, b.h); y++ {
				i := y*b.w + x
				if b.flags[i]&(flagSig|flagVisit) != flagSig {
					continue
				}
				var bit int
				if raw {
					bit = b.raw.bit()
				} else {
					ctx := 16
					if b.flags[i]&flagRefine == 0 {
						h, v, d := b.neighbors(x, y)
						ctx = 14
						if h+v+d > 0 {
							ctx = 15
						}
					}
					bit = b.mq.decode(&b.cx, ctx)
				}
				if bit != 0 {
					b.vals[i] += 1 << uint(p)
				} else {
					b.vals[i] -= 1 << uint(p)
				}
				b.flags[i] |= flagRefine
			}
		}
	}
}

func (b *t1Block) cleanup(p int) {
	for y0 := 0; y0 < b.h; y0 += 4 {
		for x := 0; x < b.w; x++ {
			y := y0
			if y0+4 <= b.h {
				// Run-length mode for columns without significance.
				run := true
				for k := 0; k < 4 && run; k++ {
					i := (y0+k)*b.w + x
					if b.flags[i]&(flagSig|flagVisit) != 0 {
						run = false
						break
					}
					h, v, d := b.neighbors(x, y0+k)
					if h+v+d != 0 {
						run = false
					}
				}
				if run {
					if b.mq.decode(&b.cx, ctxRun) == 0 {
						continue
					}
					k := b.mq.decode(&b.cx, ctxUni) << 1
					k |= b.mq.decode(&b.cx, ctxUni)
					y = y0 + k
					i := y*b.w + x
					b.decodeSign(x, y)
					b.flags[i] |= flagSig
					b.vals[i] = 3 << uint(p)
					y++
				}
			}
			for ; y < min(y0+4, b.h); y++ {
				i := y*b.w + x
				if b.flags[i]&(flagSig|flagVisit) != 0 {
					continue
				}
				h, v, d := b.neighbors(x, y)
				if b.mq.decode(&b.cx, sigContext(b.band, h, v, d)) != 0 {
					b.decodeSign(x, y)
					b.flags[i] |= flagSig
					b.vals[i] = 3 << uint(p)
				}
			}
		}
	}
	if b.style&cbSegSym != 0 {
		for k := 0; k < 4; k++ {
			b.mq.decode(&b.cx, ctxUni)
		}
	}
}

// decodeBlock runs the coding passes of a code-block. Magnitudes are kept
// in half units: a coefficient becoming significant in bit-plane p gets
// 1.5×2^p, and refinements move it by half the plane, so that the value
// is reconstructed at the middle of its interval.
func decodeBlock(w, h, band, style, planes int, segs []*cbSegment, roiShift int) ([]int32, []uint8) {
	b := &t1Block{w: w, h: h, band: band, style: style, vals: make([]int32, w*h), flags: make([]uint8, w*h)}
	b.cx.reset()
	p := planes - 1
	kind := 2 // the first pass is a cleanup pass
	for _, s := range segs {
		if s.raw {
			b.raw.init(s.data)
		} else {
			b.mq.init(s.data)
		}
		for k := 0; k < s.passes; k++ {
			if p < 0 {
				break
			}
			if style&cbReset != 0 {
				b.cx.reset()
			}
			switch kind {
			case 0:
				b.sigProp(p, s.raw)
			case 1:
				b.magRef(p, s.raw)
			case 2:
				b.cleanup(p)
				for i := range b.flags {
					b.flags[i] &^= flagVisit
				}
				p--
			}
			kind = (kind + 1) % 3
		}
	}
	if roiShift > 0 {
		lim := int32(1) << uint(roiShift+1)
		for i, v := range b.vals {
			if v >= lim {
				b.vals[i] = v >> uint(roiShift)
			}
		}
	}
	return b.vals, b.flags
}

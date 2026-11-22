package fonts

import (
	"math"
	"sync"
)

// Glyph outlines: extents of TrueType and CFF glyphs.

type cffData struct {
	cff         []byte
	charStrings int // offset of the CharStrings INDEX
	gsubrs      int // offset of the global subrs INDEX
	// Local subrs INDEX offsets: one for name-keyed fonts, one per font
	// DICT for CID-keyed fonts (-1 when absent).
	lsubrs   []int
	fdSelect func(g int) int
}

type outlineState struct {
	once     sync.Once
	cff      *cffData
	kernOnce sync.Once
	kern     map[uint32]int16
}

var outlineStates sync.Map // *Font → *outlineState

func (f *Font) outlines() *outlineState {
	if v, ok := outlineStates.Load(f); ok {
		return v.(*outlineState)
	}
	v, _ := outlineStates.LoadOrStore(f, &outlineState{})
	return v.(*outlineState)
}

// GlyphExtents returns the bounding box of a glyph in font units (for
// CFF fonts, including curve control points, as HarfBuzz computes it).
func (f *Font) GlyphExtents(g uint16) (xMin, yMin, xMax, yMax int, ok bool) {
	defer func() {
		if recover() != nil {
			ok = false
		}
	}()
	if glyf := f.tables["glyf"]; glyf != nil {
		start, end := f.glyphRange(int(g))
		if end <= start {
			return 0, 0, 0, 0, true // empty glyph
		}
		if end > len(glyf) || end-start < 10 {
			return 0, 0, 0, 0, false
		}
		d := glyf[start:end]
		return int(i16(d, 2)), int(i16(d, 4)), int(i16(d, 6)), int(i16(d, 8)), true
	}
	st := f.outlines()
	st.once.Do(func() { st.cff = f.parseCFFOutlines() })
	if st.cff == nil {
		return 0, 0, 0, 0, false
	}
	b := newBounds()
	if !st.cff.run(int(g), b) {
		return 0, 0, 0, 0, false
	}
	if !b.any {
		return 0, 0, 0, 0, true
	}
	return int(math.Floor(b.x0)), int(math.Floor(b.y0)), int(math.Ceil(b.x1)), int(math.Ceil(b.y1)), true
}

func (f *Font) parseCFFOutlines() (c *cffData) {
	cff := f.tables["CFF "]
	if len(cff) < 4 {
		return nil
	}
	defer func() {
		if recover() != nil {
			c = nil
		}
	}()
	hdrSize := int(cff[2])
	off := skipIndex(cff, hdrSize) // Name INDEX
	topOff := off
	off = skipIndex(cff, off) // Top DICT INDEX
	off = skipIndex(cff, off) // String INDEX
	c = &cffData{cff: cff, gsubrs: off}
	top := parseDict(indexItem(cff, topOff, 0))
	cs, ok := top[17]
	if !ok || len(cs) == 0 {
		return nil
	}
	c.charStrings = int(cs[0])
	localSubrs := func(priv []float64) int {
		if len(priv) < 2 {
			return -1
		}
		size, po := int(priv[0]), int(priv[1])
		if po+size > len(cff) {
			return -1
		}
		pd := parseDict(cff[po : po+size])
		if s, ok := pd[19]; ok && len(s) > 0 {
			return po + int(s[0])
		}
		return -1
	}
	if fda, ok := top[1236]; ok && len(fda) > 0 {
		fdArray := int(fda[0])
		n, _, _ := indexInfo(cff, fdArray)
		for i := 0; i < n; i++ {
			fd := parseDict(indexItem(cff, fdArray, i))
			c.lsubrs = append(c.lsubrs, localSubrs(fd[18]))
		}
		if fds, ok := top[1237]; ok && len(fds) > 0 {
			p := int(fds[0])
			switch cff[p] {
			case 0:
				c.fdSelect = func(g int) int { return int(cff[p+1+g]) }
			case 3:
				nr := int(u16(cff, p+1))
				c.fdSelect = func(g int) int {
					for i := 0; i < nr; i++ {
						r := p + 3 + 3*i
						if g >= int(u16(cff, r)) && g < int(u16(cff, r+3)) {
							return int(cff[r+2])
						}
					}
					return 0
				}
			}
		}
	} else {
		c.lsubrs = []int{localSubrs(top[18])}
	}
	return c
}

func subrBias(n int) int {
	switch {
	case n < 1240:
		return 107
	case n < 33900:
		return 1131
	}
	return 32768
}

type bounds struct {
	x0, y0, x1, y1 float64
	any            bool
}

func newBounds() *bounds { return &bounds{} }

func (b *bounds) add(x, y float64) {
	if !b.any {
		b.x0, b.y0, b.x1, b.y1, b.any = x, y, x, y, true
		return
	}
	b.x0 = math.Min(b.x0, x)
	b.y0 = math.Min(b.y0, y)
	b.x1 = math.Max(b.x1, x)
	b.y1 = math.Max(b.y1, y)
}

// run interprets the Type 2 charstring of glyph g, adding its points to b.
func (c *cffData) run(g int, b *bounds) bool {
	cs := indexItem(c.cff, c.charStrings, g)
	if cs == nil {
		return false
	}
	lsubrs := -1
	fd := 0
	if c.fdSelect != nil {
		fd = c.fdSelect(g)
	}
	if fd < len(c.lsubrs) {
		lsubrs = c.lsubrs[fd]
	}
	in := &t2interp{c: c, lsubrs: lsubrs, b: b}
	if lsubrs >= 0 {
		n, _, _ := indexInfo(c.cff, lsubrs)
		in.lbias = subrBias(n)
	}
	n, _, _ := indexInfo(c.cff, c.gsubrs)
	in.gbias = subrBias(n)
	in.exec(cs, 0)
	return true
}

type t2interp struct {
	c            *cffData
	lsubrs       int
	lbias, gbias int
	b            *bounds
	stack        []float64
	x, y         float64
	nStems       int
	widthDone    bool
	ended        bool
	open         bool
	trans        [32]float64
}

func (t *t2interp) point(x, y float64) {
	t.b.add(x, y)
}

func (t *t2interp) moveTo(dx, dy float64) {
	t.x += dx
	t.y += dy
	t.open = true
}

func (t *t2interp) lineTo(dx, dy float64) {
	if t.open {
		t.point(t.x, t.y)
		t.open = false
	}
	t.x += dx
	t.y += dy
	t.point(t.x, t.y)
}

func (t *t2interp) curveTo(dx1, dy1, dx2, dy2, dx3, dy3 float64) {
	if t.open {
		t.point(t.x, t.y)
		t.open = false
	}
	x1, y1 := t.x+dx1, t.y+dy1
	x2, y2 := x1+dx2, y1+dy2
	t.x, t.y = x2+dx3, y2+dy3
	t.point(x1, y1)
	t.point(x2, y2)
	t.point(t.x, t.y)
}

func (t *t2interp) stems() {
	if !t.widthDone && len(t.stack)%2 == 1 {
		t.stack = t.stack[1:]
	}
	t.widthDone = true
	t.nStems += len(t.stack) / 2
	t.stack = t.stack[:0]
}

func (t *t2interp) width(n int) {
	if !t.widthDone && len(t.stack) > n {
		t.stack = t.stack[1:]
	}
	t.widthDone = true
}

func (t *t2interp) exec(cs []byte, depth int) {
	if depth > 10 {
		return
	}
	for i := 0; i < len(cs) && !t.ended; {
		b0 := int(cs[i])
		switch {
		case b0 == 28:
			t.stack = append(t.stack, float64(int16(u16(cs, i+1))))
			i += 3
			continue
		case b0 >= 32 && b0 <= 246:
			t.stack = append(t.stack, float64(b0-139))
			i++
			continue
		case b0 >= 247 && b0 <= 250:
			t.stack = append(t.stack, float64((b0-247)*256+int(cs[i+1])+108))
			i += 2
			continue
		case b0 >= 251 && b0 <= 254:
			t.stack = append(t.stack, float64(-(b0-251)*256-int(cs[i+1])-108))
			i += 2
			continue
		case b0 == 255:
			t.stack = append(t.stack, float64(int32(u32(cs, i+1)))/65536)
			i += 5
			continue
		}
		i++
		s := t.stack
		switch b0 {
		case 1, 3, 18, 23: // hstem, vstem, hstemhm, vstemhm
			t.stems()
		case 19, 20: // hintmask, cntrmask
			t.stems()
			i += (t.nStems + 7) / 8
		case 21: // rmoveto
			t.width(2)
			s = t.stack
			if len(s) >= 2 {
				t.moveTo(s[0], s[1])
			}
			t.stack = s[:0]
		case 22: // hmoveto
			t.width(1)
			s = t.stack
			if len(s) >= 1 {
				t.moveTo(s[0], 0)
			}
			t.stack = s[:0]
		case 4: // vmoveto
			t.width(1)
			s = t.stack
			if len(s) >= 1 {
				t.moveTo(0, s[0])
			}
			t.stack = s[:0]
		case 5: // rlineto
			for k := 0; k+1 < len(s); k += 2 {
				t.lineTo(s[k], s[k+1])
			}
			t.stack = s[:0]
		case 6, 7: // hlineto, vlineto
			h := b0 == 6
			for k := 0; k < len(s); k++ {
				if h {
					t.lineTo(s[k], 0)
				} else {
					t.lineTo(0, s[k])
				}
				h = !h
			}
			t.stack = s[:0]
		case 8: // rrcurveto
			for k := 0; k+5 < len(s); k += 6 {
				t.curveTo(s[k], s[k+1], s[k+2], s[k+3], s[k+4], s[k+5])
			}
			t.stack = s[:0]
		case 24: // rcurveline
			k := 0
			for ; k+5 < len(s)-2; k += 6 {
				t.curveTo(s[k], s[k+1], s[k+2], s[k+3], s[k+4], s[k+5])
			}
			if k+1 < len(s) {
				t.lineTo(s[k], s[k+1])
			}
			t.stack = s[:0]
		case 25: // rlinecurve
			k := 0
			for ; k+1 < len(s)-6; k += 2 {
				t.lineTo(s[k], s[k+1])
			}
			if k+5 < len(s) {
				t.curveTo(s[k], s[k+1], s[k+2], s[k+3], s[k+4], s[k+5])
			}
			t.stack = s[:0]
		case 26: // vvcurveto
			k := 0
			dx1 := 0.0
			if len(s)%2 == 1 {
				dx1 = s[0]
				k = 1
			}
			for ; k+3 < len(s); k += 4 {
				t.curveTo(dx1, s[k], s[k+1], s[k+2], 0, s[k+3])
				dx1 = 0
			}
			t.stack = s[:0]
		case 27: // hhcurveto
			k := 0
			dy1 := 0.0
			if len(s)%2 == 1 {
				dy1 = s[0]
				k = 1
			}
			for ; k+3 < len(s); k += 4 {
				t.curveTo(s[k], dy1, s[k+1], s[k+2], s[k+3], 0)
				dy1 = 0
			}
			t.stack = s[:0]
		case 30, 31: // vhcurveto, hvcurveto
			h := b0 == 31
			k := 0
			for ; k+3 < len(s); k += 4 {
				last := 0.0
				if k+5 == len(s) {
					last = s[k+4]
				}
				if h {
					t.curveTo(s[k], 0, s[k+1], s[k+2], last, s[k+3])
				} else {
					t.curveTo(0, s[k], s[k+1], s[k+2], s[k+3], last)
				}
				h = !h
			}
			t.stack = s[:0]
		case 10, 29: // callsubr, callgsubr
			if len(s) == 0 {
				return
			}
			n := int(s[len(s)-1])
			t.stack = s[:len(s)-1]
			idx := t.c.gsubrs
			bias := t.gbias
			if b0 == 10 {
				idx, bias = t.lsubrs, t.lbias
			}
			if idx < 0 {
				return
			}
			sub := indexItem(t.c.cff, idx, n+bias)
			if sub == nil {
				return
			}
			t.exec(sub, depth+1)
		case 11: // return
			return
		case 14: // endchar
			t.width(0)
			t.ended = true
			return
		case 12:
			op := int(cs[i])
			i++
			t.escape(op)
		default:
			t.stack = s[:0]
		}
	}
}

func (t *t2interp) escape(op int) {
	s := t.stack
	switch op {
	case 35: // flex
		if len(s) >= 13 {
			t.curveTo(s[0], s[1], s[2], s[3], s[4], s[5])
			t.curveTo(s[6], s[7], s[8], s[9], s[10], s[11])
		}
	case 34: // hflex
		if len(s) >= 7 {
			t.curveTo(s[0], 0, s[1], s[2], s[3], 0)
			t.curveTo(s[4], 0, s[5], -s[2], s[6], 0)
		}
	case 36: // hflex1
		if len(s) >= 9 {
			t.curveTo(s[0], s[1], s[2], s[3], s[4], 0)
			t.curveTo(s[5], 0, s[6], s[7], s[8], -(s[1] + s[3] + s[7]))
		}
	case 37: // flex1
		if len(s) >= 11 {
			dx := s[0] + s[2] + s[4] + s[6] + s[8]
			dy := s[1] + s[3] + s[5] + s[7] + s[9]
			t.curveTo(s[0], s[1], s[2], s[3], s[4], s[5])
			if math.Abs(dx) > math.Abs(dy) {
				t.curveTo(s[6], s[7], s[8], s[9], s[10], -dy)
			} else {
				t.curveTo(s[6], s[7], s[8], s[9], -dx, s[10])
			}
		}
	case 9: // abs
		if n := len(s); n > 0 {
			s[n-1] = math.Abs(s[n-1])
		}
		return
	case 10, 11, 24, 12: // add, sub, mul, div
		if n := len(s); n >= 2 {
			a, b := s[n-2], s[n-1]
			var v float64
			switch op {
			case 10:
				v = a + b
			case 11:
				v = a - b
			case 24:
				v = a * b
			case 12:
				if b != 0 {
					v = a / b
				}
			}
			t.stack = append(s[:n-2], v)
		}
		return
	case 14: // neg
		if n := len(s); n > 0 {
			s[n-1] = -s[n-1]
		}
		return
	case 18: // drop
		if n := len(s); n > 0 {
			t.stack = s[:n-1]
		}
		return
	case 27: // dup
		if n := len(s); n > 0 {
			t.stack = append(s, s[n-1])
		}
		return
	case 28: // exch
		if n := len(s); n >= 2 {
			s[n-1], s[n-2] = s[n-2], s[n-1]
		}
		return
	case 20: // put
		if n := len(s); n >= 2 {
			if k := int(s[n-1]); k >= 0 && k < len(t.trans) {
				t.trans[k] = s[n-2]
			}
			t.stack = s[:n-2]
		}
		return
	case 21: // get
		if n := len(s); n >= 1 {
			if k := int(s[n-1]); k >= 0 && k < len(t.trans) {
				s[n-1] = t.trans[k]
			}
		}
		return
	}
	t.stack = s[:0]
}

// KernTable returns the kerning of a glyph pair in the legacy kern table
// (horizontal format 0 subtables), in font units.
func (f *Font) KernTable(left, right uint16) int {
	st := f.outlines()
	st.kernOnce.Do(func() { st.kern = parseKernTable(f.tables["kern"]) })
	return int(st.kern[uint32(left)<<16|uint32(right)])
}

func parseKernTable(k []byte) (m map[uint32]int16) {
	m = map[uint32]int16{}
	defer func() { recover() }()
	if len(k) < 4 || u16(k, 0) != 0 {
		return m
	}
	n := int(u16(k, 2))
	off := 4
	for i := 0; i < n && off+6 <= len(k); i++ {
		length := int(u16(k, off+2))
		coverage := u16(k, off+4)
		if coverage>>8 == 0 && coverage&1 != 0 && coverage&4 == 0 {
			np := int(u16(k, off+6))
			for j := 0; j < np; j++ {
				p := off + 14 + 6*j
				if p+6 > len(k) {
					break
				}
				key := u32(k, p)
				if _, ok := m[key]; !ok {
					m[key] = i16(k, p+4)
				}
			}
		}
		if length == 0 {
			break
		}
		off += length
	}
	return m
}

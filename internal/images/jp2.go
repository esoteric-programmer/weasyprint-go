package images

import (
	"errors"
	"image"
	"image/color"
	"math"
)

// JPEG 2000 (ITU-T T.800, Part 1) decoder: JP2 files and raw codestreams.

func init() {
	registerBytes("jpeg2000", "\x00\x00\x00\x0cjP  \r\n\x87\n", decodeJP2)
	registerBytes("jpeg2000", "\xff\x4f\xff\x51", decodeJ2K)
}

var errJ2K = errors.New("invalid JPEG 2000 data")

// ---------------------------------------------------------------- JP2 boxes

type jp2Info struct {
	colorspace int // 16 sRGB, 17 gray, 18 sYCC
	palette    [][]int
	palDepth   []int
	cmap       [][3]int // component, type (0 direct, 1 palette), column
	cdef       [][3]int // channel, type (0 color, 1/2 opacity), association
}

func jp2Boxes(d []byte, fn func(typ string, body []byte) error) error {
	for p := 0; p+8 <= len(d); {
		l := int64(be32(d, p))
		typ := string(d[p+4 : p+8])
		h := 8
		switch l {
		case 0:
			l = int64(len(d) - p)
		case 1:
			if p+16 > len(d) {
				return errJ2K
			}
			l = int64(be32(d, p+8))<<32 | int64(be32(d, p+12))
			h = 16
		}
		if l < int64(h) || int64(p)+l > int64(len(d)) {
			return errJ2K
		}
		if err := fn(typ, d[p+h:p+int(l)]); err != nil {
			return err
		}
		p += int(l)
	}
	return nil
}

func decodeJP2(d []byte) (image.Image, error) {
	var info jp2Info
	var code []byte
	err := jp2Boxes(d, func(typ string, body []byte) error {
		switch typ {
		case "jp2c":
			if code == nil {
				code = body
			}
		case "jp2h":
			return jp2Boxes(body, func(typ string, b []byte) error {
				switch typ {
				case "colr":
					if len(b) >= 7 && b[0] == 1 {
						info.colorspace = be32(b, 3)
					}
				case "pclr":
					if len(b) < 3 {
						return errJ2K
					}
					ne, nc := be16(b, 0), int(b[2])
					if len(b) < 3+nc {
						return errJ2K
					}
					info.palDepth = make([]int, nc)
					size := 0
					for i := 0; i < nc; i++ {
						info.palDepth[i] = int(b[3+i]&0x7f) + 1
						size += (info.palDepth[i] + 7) / 8
					}
					p := 3 + nc
					if p+ne*size > len(b) {
						return errJ2K
					}
					for e := 0; e < ne; e++ {
						row := make([]int, nc)
						for i := 0; i < nc; i++ {
							n := (info.palDepth[i] + 7) / 8
							v := 0
							for k := 0; k < n; k++ {
								v = v<<8 | int(b[p])
								p++
							}
							row[i] = v
						}
						info.palette = append(info.palette, row)
					}
				case "cmap":
					for i := 0; i+4 <= len(b); i += 4 {
						info.cmap = append(info.cmap, [3]int{be16(b, i), int(b[i+2]), int(b[i+3])})
					}
				case "cdef":
					if len(b) >= 2 {
						for i := 0; i < be16(b, 0) && 2+6*i+6 <= len(b); i++ {
							info.cdef = append(info.cdef, [3]int{be16(b, 2+6*i), be16(b, 4+6*i), be16(b, 6+6*i)})
						}
					}
				}
				return nil
			})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if code == nil {
		return nil, errJ2K
	}
	return decodeJ2KInfo(code, &info)
}

func decodeJ2K(d []byte) (image.Image, error) { return decodeJ2KInfo(d, &jp2Info{}) }

// ---------------------------------------------------------------- codestream

type j2kComp struct {
	prec   int
	signed bool
	dx, dy int
}

type j2kCoding struct {
	nl, xcb, ycb, style, transform int
	precincts                      bool
	pp                             [33][2]int
}

type j2kQuant struct {
	guard, style int
	steps        [][2]int // exponent, mantissa
}

type j2kPOC struct{ rs, cs, lye, re, ce, order int }

type j2kDefaults struct {
	order, layers, mct int
	sop, eph           bool
	coding             []j2kCoding
	quant              []j2kQuant
	roi                []int
	pocs               []j2kPOC
}

type j2kStream struct {
	x0, y0, x1, y1      int
	tw, th, tx0, ty0    int
	comps               []j2kComp
	main                j2kDefaults
	ppm                 []byte
	tiles               map[int]*j2kTileData
	codingSet, quantSet []bool
}

type j2kTileData struct {
	def    j2kDefaults
	data   []byte
	ppt    []byte
	parts  int
	hasCOD bool
}

func (s *j2kStream) parseCOx(b []byte, c *j2kCoding, scod int) error {
	if len(b) < 5 {
		return errJ2K
	}
	c.nl, c.xcb, c.ycb, c.style, c.transform = int(b[0]), int(b[1])+2, int(b[2])+2, int(b[3]), int(b[4])
	if c.nl > 32 || c.xcb > 10 || c.ycb > 10 || c.xcb+c.ycb > 12 {
		return errJ2K
	}
	c.precincts = scod&1 != 0
	for r := 0; r <= c.nl; r++ {
		c.pp[r] = [2]int{15, 15}
		if c.precincts {
			if 5+r >= len(b) {
				return errJ2K
			}
			c.pp[r] = [2]int{int(b[5+r] & 15), int(b[5+r] >> 4)}
		}
	}
	return nil
}

func parseQCx(b []byte) (j2kQuant, error) {
	if len(b) < 1 {
		return j2kQuant{}, errJ2K
	}
	q := j2kQuant{guard: int(b[0] >> 5), style: int(b[0] & 31)}
	switch q.style {
	case 0:
		for _, v := range b[1:] {
			q.steps = append(q.steps, [2]int{int(v >> 3), 0})
		}
	case 1, 2:
		for i := 1; i+1 < len(b); i += 2 {
			v := be16(b, i)
			q.steps = append(q.steps, [2]int{v >> 11, v & 0x7ff})
		}
	default:
		return q, errJ2K
	}
	if len(q.steps) == 0 {
		return q, errJ2K
	}
	return q, nil
}

func (s *j2kStream) compIndex(b []byte) (int, int) {
	if len(s.comps) < 257 {
		return int(b[0]), 1
	}
	return be16(b, 0), 2
}

// marker applies a COD/COC/QCD/QCC/RGN/POC segment to the defaults.
func (s *j2kStream) marker(m int, b []byte, def *j2kDefaults, codingSet, quantSet []bool) error {
	nc := len(s.comps)
	switch m {
	case 0xff52: // COD
		if len(b) < 5 {
			return errJ2K
		}
		def.sop, def.eph = b[0]&2 != 0, b[0]&4 != 0
		def.order, def.layers, def.mct = int(b[1]), be16(b, 2), int(b[4])
		var c j2kCoding
		if err := s.parseCOx(b[5:], &c, int(b[0])); err != nil {
			return err
		}
		for i := 0; i < nc; i++ {
			if !codingSet[i] {
				def.coding[i] = c
			}
		}
	case 0xff53: // COC
		if len(b) < 2 {
			return errJ2K
		}
		ci, n := s.compIndex(b)
		if ci >= nc || len(b) < n+1 {
			return errJ2K
		}
		var c j2kCoding
		if err := s.parseCOx(b[n+1:], &c, int(b[n])); err != nil {
			return err
		}
		def.coding[ci] = c
		codingSet[ci] = true
	case 0xff5c: // QCD
		q, err := parseQCx(b)
		if err != nil {
			return err
		}
		for i := 0; i < nc; i++ {
			if !quantSet[i] {
				def.quant[i] = q
			}
		}
	case 0xff5d: // QCC
		if len(b) < 2 {
			return errJ2K
		}
		ci, n := s.compIndex(b)
		if ci >= nc {
			return errJ2K
		}
		q, err := parseQCx(b[n:])
		if err != nil {
			return err
		}
		def.quant[ci] = q
		quantSet[ci] = true
	case 0xff5e: // RGN
		ci, n := s.compIndex(b)
		if ci < nc && len(b) >= n+2 {
			def.roi[ci] = int(b[n+1])
		}
	case 0xff5f: // POC
		n := 1
		if nc >= 257 {
			n = 2
		}
		size := 5 + 2*n
		def.pocs = nil
		for i := 0; i+size <= len(b); i += size {
			p := j2kPOC{rs: int(b[i])}
			if n == 1 {
				p.cs, p.lye, p.re, p.ce, p.order = int(b[i+1]), be16(b, i+2), int(b[i+4]), int(b[i+5]), int(b[i+6])
			} else {
				p.cs, p.lye, p.re, p.ce, p.order = be16(b, i+1), be16(b, i+3), int(b[i+5]), be16(b, i+6), int(b[i+8])
			}
			if p.ce == 0 {
				p.ce = 256
			}
			def.pocs = append(def.pocs, p)
		}
	}
	return nil
}

func (d j2kDefaults) clone() j2kDefaults {
	d.coding = append([]j2kCoding(nil), d.coding...)
	d.quant = append([]j2kQuant(nil), d.quant...)
	d.roi = append([]int(nil), d.roi...)
	d.pocs = append([]j2kPOC(nil), d.pocs...)
	return d
}

func parseJ2K(d []byte) (*j2kStream, error) {
	if len(d) < 4 || be16(d, 0) != 0xff4f {
		return nil, errJ2K
	}
	s := &j2kStream{tiles: map[int]*j2kTileData{}}
	p := 2
	seg := func() (int, []byte, error) {
		if p+4 > len(d) {
			return 0, nil, errJ2K
		}
		m, l := be16(d, p), be16(d, p+2)
		if l < 2 || p+2+l > len(d) {
			return 0, nil, errJ2K
		}
		b := d[p+4 : p+2+l]
		p += 2 + l
		return m, b, nil
	}
	// SIZ.
	m, b, err := seg()
	if err != nil || m != 0xff51 || len(b) < 36 {
		return nil, errJ2K
	}
	s.x1, s.y1, s.x0, s.y0 = be32(b, 2), be32(b, 6), be32(b, 10), be32(b, 14)
	s.tw, s.th, s.tx0, s.ty0 = be32(b, 18), be32(b, 22), be32(b, 26), be32(b, 30)
	nc := be16(b, 34)
	if nc < 1 || nc > 16384 || len(b) < 36+3*nc || s.tw <= 0 || s.th <= 0 || s.x1 <= s.x0 || s.y1 <= s.y0 {
		return nil, errJ2K
	}
	for i := 0; i < nc; i++ {
		c := j2kComp{prec: int(b[36+3*i]&0x7f) + 1, signed: b[36+3*i]&0x80 != 0, dx: int(b[37+3*i]), dy: int(b[38+3*i])}
		if c.dx == 0 || c.dy == 0 || c.prec > 31 {
			return nil, errJ2K
		}
		s.comps = append(s.comps, c)
	}
	s.main = j2kDefaults{coding: make([]j2kCoding, nc), quant: make([]j2kQuant, nc), roi: make([]int, nc), layers: 1}
	s.codingSet, s.quantSet = make([]bool, nc), make([]bool, nc)
	// Main header.
	for {
		if p+2 > len(d) {
			return nil, errJ2K
		}
		if be16(d, p) == 0xff90 {
			break
		}
		m, b, err := seg()
		if err != nil {
			return nil, err
		}
		if m == 0xff60 && len(b) > 1 { // PPM
			s.ppm = append(s.ppm, b[1:]...)
			continue
		}
		if err := s.marker(m, b, &s.main, s.codingSet, s.quantSet); err != nil {
			return nil, err
		}
	}
	// Tile-parts.
	for p+2 <= len(d) && be16(d, p) == 0xff90 {
		start := p
		m, b, err := seg()
		if err != nil || m != 0xff90 || len(b) < 8 {
			return nil, errJ2K
		}
		ti, psot := be16(b, 0), be32(b, 2)
		t := s.tiles[ti]
		if t == nil {
			t = &j2kTileData{def: s.main.clone()}
			s.tiles[ti] = t
		}
		cs := append([]bool(nil), s.codingSet...)
		qs := append([]bool(nil), s.quantSet...)
		for p+2 <= len(d) && be16(d, p) != 0xff93 {
			m, b, err := seg()
			if err != nil {
				return nil, err
			}
			if m == 0xff61 && len(b) > 1 { // PPT
				t.ppt = append(t.ppt, b[1:]...)
				continue
			}
			if t.parts == 0 {
				if m == 0xff52 {
					// A tile COD overrides the main COC segments too.
					for i := range cs {
						cs[i] = false
					}
				}
				if m == 0xff5c {
					for i := range qs {
						qs[i] = false
					}
				}
			}
			if err := s.marker(m, b, &t.def, cs, qs); err != nil {
				return nil, err
			}
		}
		p += 2 // SOD
		end := start + psot
		if psot == 0 || end > len(d) {
			end = len(d)
			if end >= 2 && be16(d, end-2) == 0xffd9 {
				end -= 2
			}
		}
		if p > end {
			return nil, errJ2K
		}
		t.data = append(t.data, d[p:end]...)
		t.parts++
		p = end
	}
	return s, nil
}

// ---------------------------------------------------------------- tier 2

type tagNode struct{ value, low int }

// tagTree is a tag tree of code-block values (inclusion layers or zero
// bit-planes).
type tagTree struct {
	w, h  []int
	nodes [][]tagNode
}

func newTagTree(w, h int) *tagTree {
	t := &tagTree{}
	for {
		t.w = append(t.w, w)
		t.h = append(t.h, h)
		lvl := make([]tagNode, w*h)
		for i := range lvl {
			lvl[i].value = 1 << 30
		}
		t.nodes = append(t.nodes, lvl)
		if w <= 1 && h <= 1 {
			break
		}
		w, h = (w+1)/2, (h+1)/2
	}
	return t
}

// decode reads the bits needed to know whether the value of leaf (x, y)
// is below threshold, and returns the value if known.
func (t *tagTree) decode(br *pktReader, x, y, threshold int) (int, bool) {
	low := 0
	for l := len(t.w) - 1; l >= 0; l-- {
		n := &t.nodes[l][(y>>uint(l))*t.w[l]+x>>uint(l)]
		if low > n.low {
			n.low = low
		} else {
			low = n.low
		}
		for low < threshold && low < n.value {
			if br.bit() == 1 {
				n.value = low
			} else {
				low++
			}
			if br.err {
				return 0, false
			}
		}
		n.low = low
	}
	v := t.nodes[0][y*t.w[0]+x].value
	return v, v < threshold
}

type pktReader struct {
	d   []byte
	pos int
	buf int
	ct  int
	err bool
}

func (r *pktReader) bit() int {
	if r.ct == 0 {
		if r.pos >= len(r.d) {
			r.err = true
			return 0
		}
		if r.buf == 0xff {
			r.ct = 7
		} else {
			r.ct = 8
		}
		r.buf = int(r.d[r.pos])
		r.pos++
	}
	r.ct--
	return (r.buf >> uint(r.ct)) & 1
}

func (r *pktReader) bits(n int) int {
	v := 0
	for i := 0; i < n; i++ {
		v = v<<1 | r.bit()
	}
	return v
}

func (r *pktReader) align() {
	r.ct = 0
	if r.buf == 0xff {
		r.pos++
		r.buf = 0
	}
}

type j2kBlock struct {
	x0, y0, x1, y1 int // band coordinates
	included       bool
	lblock         int
	passes         int
	zbp            int
	segs           []*cbSegment
}

type j2kPrecBand struct {
	band       *j2kBand
	cw, ch     int
	blocks     []*j2kBlock
	incl, zero *tagTree
}

type j2kBand struct {
	kind           int
	x0, y0, x1, y1 int
	level          int
	coef           []float64
	step           float64
	planes, roi    int
}

type j2kRes struct {
	x0, y0, x1, y1 int
	ppx, ppy       int
	pw, ph         int
	bands          []*j2kBand
	precincts      [][]*j2kPrecBand
}

type j2kTileComp struct {
	x0, y0, x1, y1 int
	res            []*j2kRes
	coding         j2kCoding
	nextLayer      [][]int
}

func ceilDiv(a, b int) int {
	if b <= 0 {
		return 0
	}
	if a >= 0 {
		return (a + b - 1) / b
	}
	return -((-a) / b)
}

func floorDiv(a, b int) int {
	if a >= 0 {
		return a / b
	}
	return -((-a + b - 1) / b)
}

// segmentMax returns the number of passes of the segment starting at pass
// k.
func segmentMax(style, k int) (int, bool) {
	if style&cbTermAll != 0 {
		raw := style&cbBypass != 0 && k >= 10 && (k-1)%3 != 2
		return 1, raw
	}
	if style&cbBypass != 0 {
		if k < 10 {
			return 10 - k, false
		}
		if (k-1)%3 == 2 {
			return 1, false
		}
		return 2 - (k-1)%3, true
	}
	return 1 << 30, false
}

func (s *j2kStream) decodeTile(ti int, t *j2kTileData, img [][]float64) error {
	nx := ceilDiv(s.x1-s.tx0, s.tw)
	p, q := ti%nx, ti/nx
	tx0, ty0 := max(s.tx0+p*s.tw, s.x0), max(s.ty0+q*s.th, s.y0)
	tx1, ty1 := min(s.tx0+(p+1)*s.tw, s.x1), min(s.ty0+(q+1)*s.th, s.y1)
	def := &t.def
	tcs := make([]*j2kTileComp, len(s.comps))
	for c, comp := range s.comps {
		cod := def.coding[c]
		qnt := def.quant[c]
		tc := &j2kTileComp{x0: ceilDiv(tx0, comp.dx), y0: ceilDiv(ty0, comp.dy), x1: ceilDiv(tx1, comp.dx), y1: ceilDiv(ty1, comp.dy), coding: cod}
		for r := 0; r <= cod.nl; r++ {
			sc := 1 << uint(cod.nl-r)
			res := &j2kRes{x0: ceilDiv(tc.x0, sc), y0: ceilDiv(tc.y0, sc), x1: ceilDiv(tc.x1, sc), y1: ceilDiv(tc.y1, sc), ppx: cod.pp[r][0], ppy: cod.pp[r][1]}
			if res.x1 > res.x0 {
				res.pw = ceilDiv(res.x1, 1<<uint(res.ppx)) - floorDiv(res.x0, 1<<uint(res.ppx))
			}
			if res.y1 > res.y0 {
				res.ph = ceilDiv(res.y1, 1<<uint(res.ppy)) - floorDiv(res.y0, 1<<uint(res.ppy))
			}
			kinds := []int{bandLL}
			if r > 0 {
				kinds = []int{bandHL, bandLH, bandHH}
			}
			for bi, kind := range kinds {
				n := cod.nl - r + 1
				if r == 0 {
					n = cod.nl
				}
				xo, yo := 0, 0
				if kind == bandHL || kind == bandHH {
					xo = 1
				}
				if kind == bandLH || kind == bandHH {
					yo = 1
				}
				bsc := 1 << uint(n)
				half := 0
				if n > 0 {
					half = 1 << uint(n-1)
				}
				band := &j2kBand{kind: kind, level: n,
					x0: ceilDiv(tc.x0-half*xo, bsc), y0: ceilDiv(tc.y0-half*yo, bsc),
					x1: ceilDiv(tc.x1-half*xo, bsc), y1: ceilDiv(tc.y1-half*yo, bsc)}
				if band.x1 < band.x0 {
					band.x1 = band.x0
				}
				if band.y1 < band.y0 {
					band.y1 = band.y0
				}
				band.coef = make([]float64, (band.x1-band.x0)*(band.y1-band.y0))
				// Quantization.
				si := 0
				if r > 0 {
					si = 1 + 3*(r-1) + bi
				}
				var exp, mant int
				switch {
				case qnt.style == 1:
					exp, mant = qnt.steps[0][0]-cod.nl+n, qnt.steps[0][1]
					if r == 0 {
						exp = qnt.steps[0][0]
					}
				case si < len(qnt.steps):
					exp, mant = qnt.steps[si][0], qnt.steps[si][1]
				default:
					return errJ2K
				}
				gain := [4]int{0, 1, 1, 2}[kind]
				band.planes = qnt.guard + exp - 1
				band.roi = def.roi[c]
				band.step = math.Ldexp(1+float64(mant)/2048, comp.prec+gain-exp)
				res.bands = append(res.bands, band)
			}
			// Precincts and code-blocks.
			for py := 0; py < res.ph; py++ {
				for px := 0; px < res.pw; px++ {
					var pbs []*j2kPrecBand
					for _, band := range res.bands {
						ppx, ppy := res.ppx, res.ppy
						xcb, ycb := cod.xcb, cod.ycb
						if r > 0 {
							ppx--
							ppy--
						}
						xcb, ycb = min(xcb, ppx), min(ycb, ppy)
						if xcb < 0 || ycb < 0 {
							return errJ2K
						}
						bx0 := (floorDiv(res.x0, 1<<uint(res.ppx)) + px) << uint(ppx)
						by0 := (floorDiv(res.y0, 1<<uint(res.ppy)) + py) << uint(ppy)
						bx1, by1 := bx0+1<<uint(ppx), by0+1<<uint(ppy)
						bx0, by0 = max(bx0, band.x0), max(by0, band.y0)
						bx1, by1 = min(bx1, band.x1), min(by1, band.y1)
						pb := &j2kPrecBand{band: band}
						if bx1 > bx0 && by1 > by0 {
							cx0, cy0 := floorDiv(bx0, 1<<uint(xcb)), floorDiv(by0, 1<<uint(ycb))
							cx1, cy1 := ceilDiv(bx1, 1<<uint(xcb)), ceilDiv(by1, 1<<uint(ycb))
							pb.cw, pb.ch = cx1-cx0, cy1-cy0
							for cy := cy0; cy < cy1; cy++ {
								for cx := cx0; cx < cx1; cx++ {
									pb.blocks = append(pb.blocks, &j2kBlock{
										x0: max(cx<<uint(xcb), bx0), y0: max(cy<<uint(ycb), by0),
										x1: min((cx+1)<<uint(xcb), bx1), y1: min((cy+1)<<uint(ycb), by1),
										lblock: 3})
								}
							}
							pb.incl, pb.zero = newTagTree(pb.cw, pb.ch), newTagTree(pb.cw, pb.ch)
						}
						pbs = append(pbs, pb)
					}
					res.precincts = append(res.precincts, pbs)
				}
			}
			tc.res = append(tc.res, res)
		}
		tc.nextLayer = make([][]int, len(tc.res))
		for r := range tc.res {
			tc.nextLayer[r] = make([]int, len(tc.res[r].precincts))
		}
		tcs[c] = tc
	}
	// Packets.
	body := &pktReader{d: t.data}
	hdr := body
	if t.ppt != nil {
		hdr = &pktReader{d: t.ppt}
	} else if s.ppm != nil {
		hdr = &pktReader{d: s.ppmTile(ti)}
	}
	readPacket := func(c, r, k int) error {
		tc := tcs[c]
		cod := tc.coding
		if def.sop && body.pos+6 <= len(body.d) && body.d[body.pos] == 0xff && body.d[body.pos+1] == 0x91 {
			body.pos += 6
		}
		hdr.buf, hdr.ct = 0, 0
		type contrib struct {
			blk  *j2kBlock
			lens []int
			segs []int // passes per segment piece
		}
		var cs []contrib
		if hdr.bit() == 1 {
			layer := tc.nextLayer[r][k]
			for _, pb := range tc.res[r].precincts[k] {
				for bi, blk := range pb.blocks {
					bx, by := bi%pb.cw, bi/pb.cw
					var included bool
					if !blk.included {
						_, included = pb.incl.decode(hdr, bx, by, layer+1)
					} else {
						included = hdr.bit() == 1
					}
					if !included {
						continue
					}
					if !blk.included {
						// Zero bit-planes.
						th := 1
						for {
							v, ok := pb.zero.decode(hdr, bx, by, th)
							if ok {
								blk.zbp = v
								break
							}
							if hdr.err || th > 64 {
								return errJ2K
							}
							th++
						}
						blk.included = true
					}
					// Number of passes.
					n := 1
					if hdr.bit() == 1 {
						n = 2
						if hdr.bit() == 1 {
							v := hdr.bits(2)
							n = 3 + v
							if v == 3 {
								v = hdr.bits(5)
								n = 6 + v
								if v == 31 {
									n = 37 + hdr.bits(7)
								}
							}
						}
					}
					for hdr.bit() == 1 {
						blk.lblock++
					}
					ct := contrib{blk: blk}
					k := blk.passes
					left := n
					for left > 0 {
						segMax, _ := segmentMax(cod.style, k)
						inSeg := 0
						if len(blk.segs) > 0 && k > 0 {
							last := blk.segs[len(blk.segs)-1]
							if last.passes < last.max {
								inSeg = last.max - last.passes
							}
						}
						if inSeg == 0 {
							inSeg = segMax
						}
						take := min(left, inSeg)
						bits := blk.lblock
						for v := take; v > 1; v >>= 1 {
							bits++
						}
						ct.lens = append(ct.lens, hdr.bits(bits))
						ct.segs = append(ct.segs, take)
						k += take
						left -= take
					}
					cs = append(cs, ct)
					if hdr.err {
						return errJ2K
					}
				}
			}
		}
		hdr.align()
		if def.eph && hdr.pos+2 <= len(hdr.d) && hdr.d[hdr.pos] == 0xff && hdr.d[hdr.pos+1] == 0x92 {
			hdr.pos += 2
		}
		if hdr.err {
			return errJ2K
		}
		// Code-block data.
		for _, ct := range cs {
			blk := ct.blk
			for i, l := range ct.lens {
				if body.pos+l > len(body.d) {
					l = len(body.d) - body.pos
				}
				data := body.d[body.pos : body.pos+l]
				body.pos += l
				var seg *cbSegment
				if n := len(blk.segs); n > 0 && blk.segs[n-1].passes < blk.segs[n-1].max {
					seg = blk.segs[n-1]
				} else {
					m, raw := segmentMax(cod.style, blk.passes)
					seg = &cbSegment{max: m, raw: raw}
					blk.segs = append(blk.segs, seg)
				}
				seg.data = append(seg.data, data...)
				seg.passes += ct.segs[i]
				blk.passes += ct.segs[i]
			}
		}
		tc.nextLayer[r][k]++
		return nil
	}
	if err := s.iteratePackets(def, tcs, tx0, ty0, tx1, ty1, readPacket); err != nil {
		return err
	}
	// Tier 1, dequantization, inverse wavelet transform.
	planes := make([][]float64, len(s.comps))
	for c, tc := range tcs {
		reversible := tc.coding.transform == 1
		for _, res := range tc.res {
			for _, pbs := range res.precincts {
				for _, pb := range pbs {
					band := pb.band
					bw := band.x1 - band.x0
					for _, blk := range pb.blocks {
						if blk.passes == 0 {
							continue
						}
						w, h := blk.x1-blk.x0, blk.y1-blk.y0
						np := band.planes + band.roi - blk.zbp
						if np <= 0 || np > 31 {
							continue
						}
						vals, flags := decodeBlock(w, h, band.kind, tc.coding.style, np, blk.segs, band.roi)
						for y := 0; y < h; y++ {
							for x := 0; x < w; x++ {
								v := float64(vals[y*w+x])
								if reversible {
									v = math.Trunc(v / 2)
								} else {
									v = v / 2 * band.step
								}
								if flags[y*w+x]&flagNeg != 0 {
									v = -v
								}
								band.coef[(blk.y0-band.y0+y)*bw+blk.x0-band.x0+x] = v
							}
						}
					}
				}
			}
		}
		planes[c] = inverseDWT(tc, reversible)
	}
	// Multiple component transform.
	if def.mct == 1 && len(planes) >= 3 && len(planes[0]) == len(planes[1]) && len(planes[0]) == len(planes[2]) {
		if tcs[0].coding.transform == 1 {
			for i := range planes[0] {
				y, cb, cr := planes[0][i], planes[1][i], planes[2][i]
				g := y - math.Floor((cb+cr)/4)
				planes[0][i], planes[1][i], planes[2][i] = cr+g, g, cb+g
			}
		} else {
			for i := range planes[0] {
				y, cb, cr := planes[0][i], planes[1][i], planes[2][i]
				planes[0][i] = y + 1.402*cr
				planes[1][i] = y - 0.344136*cb - 0.714136*cr
				planes[2][i] = y + 1.772*cb
			}
		}
	}
	// Level shift and storage in the component planes.
	for c, tc := range tcs {
		comp := s.comps[c]
		cw := ceilDiv(s.x1, comp.dx) - ceilDiv(s.x0, comp.dx)
		ox, oy := ceilDiv(s.x0, comp.dx), ceilDiv(s.y0, comp.dy)
		shift := 0.0
		if !comp.signed {
			shift = float64(int(1) << uint(comp.prec-1))
		}
		w := tc.x1 - tc.x0
		for y := tc.y0; y < tc.y1; y++ {
			for x := tc.x0; x < tc.x1; x++ {
				img[c][(y-oy)*cw+x-ox] = planes[c][(y-tc.y0)*w+x-tc.x0] + shift
			}
		}
	}
	return nil
}

// ppmTile returns the packed packet headers of a tile from PPM segments
// (one chunk per tile-part, in order).
func (s *j2kStream) ppmTile(ti int) []byte {
	// The PPM data lists, for each tile-part in stream order, a length and
	// the headers; tile-parts are attributed in the order of the tiles.
	var out []byte
	p := 0
	idx := 0
	for _, k := range s.tileOrder() {
		for part := 0; part < s.tiles[k].parts; part++ {
			if p+4 > len(s.ppm) {
				return out
			}
			n := be32(s.ppm, p)
			p += 4
			if p+n > len(s.ppm) {
				n = len(s.ppm) - p
			}
			if k == ti {
				out = append(out, s.ppm[p:p+n]...)
			}
			p += n
			idx++
		}
	}
	return out
}

func (s *j2kStream) tileOrder() []int {
	var ks []int
	for k := range s.tiles {
		ks = append(ks, k)
	}
	for i := 1; i < len(ks); i++ {
		for j := i; j > 0 && ks[j] < ks[j-1]; j-- {
			ks[j], ks[j-1] = ks[j-1], ks[j]
		}
	}
	return ks
}

// iteratePackets calls fn for each packet in the progression order(s).
func (s *j2kStream) iteratePackets(def *j2kDefaults, tcs []*j2kTileComp, tx0, ty0, tx1, ty1 int, fn func(c, r, k int) error) error {
	maxRes := 0
	for _, tc := range tcs {
		maxRes = max(maxRes, len(tc.res))
	}
	pocs := def.pocs
	if len(pocs) == 0 {
		pocs = []j2kPOC{{0, 0, def.layers, maxRes, len(tcs), def.order}}
	}
	for _, poc := range pocs {
		ce, re := min(poc.ce, len(tcs)), min(poc.re, maxRes)
		lye := min(poc.lye, def.layers)
		try := func(l, r, c, k int) error {
			tc := tcs[c]
			if r >= len(tc.res) || k >= len(tc.res[r].precincts) || tc.nextLayer[r][k] != l || l >= lye {
				return nil
			}
			return fn(c, r, k)
		}
		switch poc.order {
		case 0, 1: // LRCP, RLCP
			for a := 0; a < max(lye, re); a++ {
				for b := 0; b < max(lye, re); b++ {
					l, r := a, b
					if poc.order == 1 {
						l, r = b, a
					}
					if l >= lye || r < poc.rs || r >= re {
						continue
					}
					for c := poc.cs; c < ce; c++ {
						if r >= len(tcs[c].res) {
							continue
						}
						for k := range tcs[c].res[r].precincts {
							if err := try(l, r, c, k); err != nil {
								return err
							}
						}
					}
				}
			}
		default: // RPCL, PCRL, CPRL: position-driven
			xstep, ystep := 0, 0
			for c := poc.cs; c < ce; c++ {
				comp := s.comps[c]
				tc := tcs[c]
				for r, res := range tc.res {
					sc := tc.coding.nl - r
					xs := comp.dx << uint(res.ppx+sc)
					ys := comp.dy << uint(res.ppy+sc)
					if xstep == 0 || xs < xstep {
						xstep = xs
					}
					if ystep == 0 || ys < ystep {
						ystep = ys
					}
				}
			}
			if xstep <= 0 || ystep <= 0 {
				continue
			}
			visit := func(r, c, x, y int) error {
				tc := tcs[c]
				if r >= len(tc.res) {
					return nil
				}
				comp := s.comps[c]
				res := tc.res[r]
				if res.pw == 0 || res.ph == 0 {
					return nil
				}
				sc := tc.coding.nl - r
				ys := comp.dy << uint(res.ppy+sc)
				xs := comp.dx << uint(res.ppx+sc)
				okY := y%ys == 0 || (y == ty0 && (res.y0<<uint(sc))%(1<<uint(res.ppy+sc)) != 0)
				okX := x%xs == 0 || (x == tx0 && (res.x0<<uint(sc))%(1<<uint(res.ppx+sc)) != 0)
				if !okX || !okY {
					return nil
				}
				kx := floorDiv(ceilDiv(x, comp.dx<<uint(sc)), 1<<uint(res.ppx)) - floorDiv(res.x0, 1<<uint(res.ppx))
				ky := floorDiv(ceilDiv(y, comp.dy<<uint(sc)), 1<<uint(res.ppy)) - floorDiv(res.y0, 1<<uint(res.ppy))
				if kx < 0 || ky < 0 || kx >= res.pw || ky >= res.ph {
					return nil
				}
				k := ky*res.pw + kx
				for l := tc.nextLayer[r][k]; l < lye; l++ {
					if err := try(l, r, c, k); err != nil {
						return err
					}
				}
				return nil
			}
			ystart, xstart := ty0-ty0%ystep, tx0-tx0%xstep
			pos := func(f func(x, y int) error) error {
				for y := ystart; y < ty1; y += ystep {
					for x := xstart; x < tx1; x += xstep {
						yy, xx := max(y, ty0), max(x, tx0)
						if err := f(xx, yy); err != nil {
							return err
						}
					}
				}
				return nil
			}
			var err error
			switch poc.order {
			case 2: // RPCL
				for r := poc.rs; r < re && err == nil; r++ {
					err = pos(func(x, y int) error {
						for c := poc.cs; c < ce; c++ {
							if e := visit(r, c, x, y); e != nil {
								return e
							}
						}
						return nil
					})
				}
			case 3: // PCRL
				err = pos(func(x, y int) error {
					for c := poc.cs; c < ce; c++ {
						for r := poc.rs; r < re; r++ {
							if e := visit(r, c, x, y); e != nil {
								return e
							}
						}
					}
					return nil
				})
			case 4: // CPRL
				for c := poc.cs; c < ce && err == nil; c++ {
					err = pos(func(x, y int) error {
						for r := poc.rs; r < re; r++ {
							if e := visit(r, c, x, y); e != nil {
								return e
							}
						}
						return nil
					})
				}
			}
			if err != nil {
				return err
			}
		}
	}
	return nil
}

// ---------------------------------------------------------------- wavelets

const (
	dwtAlpha = -1.586134342059924
	dwtBeta  = -0.052980118572961
	dwtGamma = 0.882911075530934
	dwtDelta = 0.443506852043971
	dwtK     = 1.230174104914001
)

// inverseDWT reconstructs a tile-component from its sub-bands.
func inverseDWT(tc *j2kTileComp, reversible bool) []float64 {
	ll := tc.res[0].bands[0]
	cur := append([]float64(nil), ll.coef...)
	for r := 1; r < len(tc.res); r++ {
		res := tc.res[r]
		w, h := res.x1-res.x0, res.y1-res.y0
		prev := tc.res[r-1]
		pw := prev.x1 - prev.x0
		out := make([]float64, w*h)
		bands := res.bands // HL, LH, HH
		for y := res.y0; y < res.y1; y++ {
			for x := res.x0; x < res.x1; x++ {
				var v float64
				hx, hy := x&1 == 1, y&1 == 1
				switch {
				case !hx && !hy:
					v = cur[(y/2-prev.y0)*pw+x/2-prev.x0]
				default:
					var b *j2kBand
					switch {
					case hx && !hy:
						b = bands[0]
					case !hx && hy:
						b = bands[1]
					default:
						b = bands[2]
					}
					bx, by := x/2, y/2
					if hx {
						bx = (x - 1) / 2
					}
					if hy {
						by = (y - 1) / 2
					}
					v = b.coef[(by-b.y0)*(b.x1-b.x0)+bx-b.x0]
				}
				out[(y-res.y0)*w+x-res.x0] = v
			}
		}
		line := make([]float64, max(w, h))
		for y := 0; y < h; y++ {
			copy(line, out[y*w:(y+1)*w])
			idwt1D(line[:w], res.x0, reversible)
			copy(out[y*w:(y+1)*w], line[:w])
		}
		for x := 0; x < w; x++ {
			for y := 0; y < h; y++ {
				line[y] = out[y*w+x]
			}
			idwt1D(line[:h], res.y0, reversible)
			for y := 0; y < h; y++ {
				out[y*w+x] = line[y]
			}
		}
		cur = out
	}
	return cur
}

// idwt1D runs the 1D inverse transform of a signal starting at absolute
// coordinate i0 (even positions are low-pass).
func idwt1D(x []float64, i0 int, reversible bool) {
	n := len(x)
	if n == 0 {
		return
	}
	if n == 1 {
		if i0&1 == 1 {
			if reversible {
				x[0] = math.Trunc(x[0] / 2)
			} else {
				x[0] /= 2
			}
		}
		return
	}
	// Whole-sample symmetric extension.
	at := func(i int) float64 {
		for i < 0 || i >= n {
			if i < 0 {
				i = -i
			}
			if i >= n {
				i = 2*(n-1) - i
			}
		}
		return x[i]
	}
	even := func(i int) bool { return (i0+i)&1 == 0 }
	lift := func(isEven bool, f func(i int)) {
		for i := 0; i < n; i++ {
			if even(i) == isEven {
				f(i)
			}
		}
	}
	if reversible {
		lift(true, func(i int) { x[i] -= math.Floor((at(i-1) + at(i+1) + 2) / 4) })
		lift(false, func(i int) { x[i] += math.Floor((at(i-1) + at(i+1)) / 2) })
		return
	}
	lift(true, func(i int) { x[i] *= dwtK })
	lift(false, func(i int) { x[i] /= dwtK })
	lift(true, func(i int) { x[i] -= dwtDelta * (at(i-1) + at(i+1)) })
	lift(false, func(i int) { x[i] -= dwtGamma * (at(i-1) + at(i+1)) })
	lift(true, func(i int) { x[i] -= dwtBeta * (at(i-1) + at(i+1)) })
	lift(false, func(i int) { x[i] -= dwtAlpha * (at(i-1) + at(i+1)) })
}

// ---------------------------------------------------------------- output

func decodeJ2KInfo(d []byte, info *jp2Info) (image.Image, error) {
	s, err := parseJ2K(d)
	if err != nil {
		return nil, err
	}
	w, h := s.x1-s.x0, s.y1-s.y0
	if err := checkSize(w, h); err != nil {
		return nil, err
	}
	planes := make([][]float64, len(s.comps))
	for c, comp := range s.comps {
		cw := ceilDiv(s.x1, comp.dx) - ceilDiv(s.x0, comp.dx)
		ch := ceilDiv(s.y1, comp.dy) - ceilDiv(s.y0, comp.dy)
		planes[c] = make([]float64, cw*ch)
	}
	for _, ti := range s.tileOrder() {
		if err := s.decodeTile(ti, s.tiles[ti], planes); err != nil {
			return nil, err
		}
	}
	// 8-bit samples of a component, upsampled to the image grid.
	sample := func(c, x, y int) int {
		comp := s.comps[c]
		cw := ceilDiv(s.x1, comp.dx) - ceilDiv(s.x0, comp.dx)
		ch := ceilDiv(s.y1, comp.dy) - ceilDiv(s.y0, comp.dy)
		cx := min(cw-1, ceilDiv(s.x0+x+1, comp.dx)-1-ceilDiv(s.x0, comp.dx))
		cy := min(ch-1, ceilDiv(s.y0+y+1, comp.dy)-1-ceilDiv(s.y0, comp.dy))
		v := math.Round(planes[c][max(cy, 0)*cw+max(cx, 0)])
		lo, hi := 0.0, float64(int(1)<<uint(comp.prec)-1)
		if comp.signed {
			lo, hi = -float64(int(1)<<uint(comp.prec-1)), float64(int(1)<<uint(comp.prec-1)-1)
			v -= lo
			hi -= lo
			lo = 0
		}
		return int(math.Max(lo, math.Min(hi, v)))
	}
	to8 := func(v, prec int) byte {
		switch {
		case prec > 8:
			return byte(v >> uint(prec-8))
		case prec < 8:
			return byte(v * 255 / (1<<uint(prec) - 1))
		}
		return byte(v)
	}
	// Channels: palette mapping, then color and opacity.
	type channel struct {
		get  func(x, y int) int
		prec int
	}
	var chans []channel
	if len(info.cmap) > 0 && len(info.palette) > 0 {
		for _, m := range info.cmap {
			m := m
			if m[0] >= len(s.comps) {
				return nil, errJ2K
			}
			if m[1] == 1 && m[2] < len(info.palDepth) {
				chans = append(chans, channel{func(x, y int) int {
					i := min(max(sample(m[0], x, y), 0), len(info.palette)-1)
					return info.palette[i][m[2]]
				}, info.palDepth[m[2]]})
			} else {
				chans = append(chans, channel{func(x, y int) int { return sample(m[0], x, y) }, s.comps[m[0]].prec})
			}
		}
	} else {
		for c := range s.comps {
			c := c
			chans = append(chans, channel{func(x, y int) int { return sample(c, x, y) }, s.comps[c].prec})
		}
	}
	alpha := -1
	for _, cd := range info.cdef {
		if (cd[1] == 1 || cd[1] == 2) && cd[0] < len(chans) {
			alpha = cd[0]
		}
	}
	ncolor := len(chans)
	if alpha < 0 && (ncolor == 2 || ncolor == 4) {
		alpha = ncolor - 1
	}
	if alpha >= 0 {
		ncolor--
	}
	colorIdx := []int{}
	for i := range chans {
		if i != alpha {
			colorIdx = append(colorIdx, i)
		}
	}
	gray := ncolor < 3 || info.colorspace == 17
	img := newNRGBA(w, h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			o := img.PixOffset(x, y)
			if gray {
				ch := chans[colorIdx[0]]
				v := to8(ch.get(x, y), ch.prec)
				img.Pix[o], img.Pix[o+1], img.Pix[o+2] = v, v, v
			} else {
				var c [3]byte
				for k := 0; k < 3; k++ {
					ch := chans[colorIdx[k]]
					c[k] = to8(ch.get(x, y), ch.prec)
				}
				if info.colorspace == 18 {
					c[0], c[1], c[2] = color.YCbCrToRGB(c[0], c[1], c[2])
				}
				img.Pix[o], img.Pix[o+1], img.Pix[o+2] = c[0], c[1], c[2]
			}
			img.Pix[o+3] = 255
			if alpha >= 0 {
				img.Pix[o+3] = to8(chans[alpha].get(x, y), chans[alpha].prec)
			}
		}
	}
	return img, nil
}

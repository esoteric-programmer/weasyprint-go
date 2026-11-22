package otl

import "math"

// GPOS lookup application.

func (s *shaper) applyGPOS(t *layoutTable, l *lookup, m *matcher, so, i int) (int, bool) {
	d := t.d
	g := s.buf[i].ID
	switch l.typ {
	case 1: // single
		ci := d.coverage(so+int(d.u16(so+2)), g)
		if ci < 0 {
			return 0, false
		}
		vf := d.u16(so + 4)
		switch d.u16(so) {
		case 1:
			s.applyValue(d, so, so+6, vf, i)
		case 2:
			if ci >= int(d.u16(so+6)) {
				return 0, false
			}
			s.applyValue(d, so, so+8+ci*valueSize(vf), vf, i)
		default:
			return 0, false
		}
		return i + 1, true
	case 2: // pair
		ci := d.coverage(so+int(d.u16(so+2)), g)
		if ci < 0 {
			return 0, false
		}
		j := m.next(i)
		if j < 0 {
			return 0, false
		}
		g2 := s.buf[j].ID
		vf1, vf2 := d.u16(so+4), d.u16(so+6)
		s1, s2 := valueSize(vf1), valueSize(vf2)
		switch d.u16(so) {
		case 1:
			if ci >= int(d.u16(so+8)) {
				return 0, false
			}
			ps := so + int(d.u16(so+10+2*ci))
			n := int(d.u16(ps))
			rec := 2 + s1 + s2
			lo, hi := 0, n
			for lo < hi {
				mid := (lo + hi) / 2
				r := ps + 2 + mid*rec
				v := d.u16(r)
				switch {
				case v == g2:
					s.applyValue(d, ps, r+2, vf1, i)
					s.applyValue(d, ps, r+2+s1, vf2, j)
					if s2 > 0 {
						j++
					}
					return j, true
				case v < g2:
					lo = mid + 1
				default:
					hi = mid
				}
			}
			return 0, false
		case 2:
			c2 := d.class(so+int(d.u16(so+10)), g2)
			if c2 == 0 {
				return 0, false
			}
			c1 := d.class(so+int(d.u16(so+8)), g)
			n1, n2 := int(d.u16(so+12)), int(d.u16(so+14))
			if c1 >= n1 || c2 >= n2 {
				return 0, false
			}
			r := so + 16 + (c1*n2+c2)*(s1+s2)
			s.applyValue(d, so, r, vf1, i)
			s.applyValue(d, so, r+s1, vf2, j)
			if s2 > 0 {
				j++
			}
			return j, true
		}
		return 0, false
	case 3:
		return s.cursive(d, l, m, so, i)
	case 4, 5, 6:
		return s.markAttach(d, l, m, so, i)
	case 7:
		return s.context(t, m, so, i)
	case 8:
		return s.chainContext(t, m, so, i)
	}
	return 0, false
}

func valueSize(vf uint16) int {
	n := 0
	for b := vf & 0xff; b != 0; b >>= 1 {
		n += int(b & 1)
	}
	return 2 * n
}

// applyValue adds a value record (with base the offset its device tables
// are relative to) to the position of glyph i.
func (s *shaper) applyValue(d data, base, off int, vf uint16, i int) {
	if vf == 0 {
		return
	}
	g := &s.buf[i]
	var v [4]int
	p := off
	for k := 0; k < 4; k++ {
		if vf&(1<<k) != 0 {
			v[k] = int(d.i16(p))
			p += 2
		}
	}
	for k := 0; k < 4; k++ {
		if vf&(0x10<<k) != 0 {
			if dev := d.u16(p); dev != 0 {
				v[k] += s.deviceDelta(d, base+int(dev))
			}
			p += 2
		}
	}
	g.XOffset += s.scale(v[0])
	g.YOffset += s.scale(v[1])
	g.XAdvance += s.scale(v[2])
	g.YAdvance += s.scale(v[3])
}

// deviceDelta returns the variation delta of a VariationIndex device
// table (hinting device tables are ignored, as by HarfBuzz at high
// resolutions).
func (s *shaper) deviceDelta(d data, off int) int {
	if d.u16(off+4) != 0x8000 || s.varDelta == nil {
		return 0
	}
	return int(math.Round(s.varDelta(int(d.u16(off)), int(d.u16(off+2)))))
}

// anchor returns the coordinates of an anchor in scaled (unrounded) units.
func (s *shaper) anchor(d data, off int) (float64, float64) {
	x, y := float64(d.i16(off+2)), float64(d.i16(off+4))
	if d.u16(off) == 3 && s.varDelta != nil {
		if dx := d.u16(off + 6); dx != 0 {
			x += float64(s.deviceDelta(d, off+int(dx)))
		}
		if dy := d.u16(off + 8); dy != 0 {
			y += float64(s.deviceDelta(d, off+int(dy)))
		}
	}
	return x * s.k, y * s.k
}

func (s *shaper) markAttach(d data, l *lookup, m *matcher, so, i int) (int, bool) {
	if d.u16(so) != 1 {
		return 0, false
	}
	mi := d.coverage(so+int(d.u16(so+2)), s.buf[i].ID)
	if mi < 0 {
		return 0, false
	}
	classCount := int(d.u16(so + 6))
	markArray := so + int(d.u16(so+8))
	baseArray := so + int(d.u16(so+10))
	var j int
	switch l.typ {
	case 4, 5:
		bm := &matcher{s: s, flag: 8}
		j = -1
		for k := i - 1; k >= 0; k-- {
			gk := &s.buf[k]
			if bm.skip(gk) {
				continue
			}
			// Only the first glyph of a multiple substitution takes marks.
			if gk.multiplied && gk.ligComp != 0 && !(k > 0 && s.buf[k-1].props&propMark != 0) {
				if d.coverage(so+int(d.u16(so+4)), gk.ID) < 0 {
					continue
				}
			}
			j = k
			break
		}
	case 6:
		mm := &matcher{s: s, flag: l.flag &^ 0x0e, filterSet: l.filterSet, ignoreZWNJ: true}
		j = mm.prev(i)
		if j >= 0 {
			if s.buf[j].props&propMark == 0 {
				return 0, false
			}
			id1, id2 := s.buf[i].ligID, s.buf[j].ligID
			c1, c2 := s.buf[i].ligComp, s.buf[j].ligComp
			good := false
			if id1 == id2 {
				good = id1 == 0 || c1 == c2
			} else {
				good = (id1 > 0 && c1 == 0) || (id2 > 0 && c2 == 0)
			}
			if !good {
				return 0, false
			}
		}
	}
	if j < 0 {
		return 0, false
	}
	bi := d.coverage(so+int(d.u16(so+4)), s.buf[j].ID)
	if bi < 0 {
		return 0, false
	}
	if mi >= int(d.u16(markArray)) {
		return 0, false
	}
	mr := markArray + 2 + 4*mi
	markClass := int(d.u16(mr))
	markAnchor := markArray + int(d.u16(mr+2))
	if markClass >= classCount {
		return 0, false
	}
	var baseAnchor int
	switch l.typ {
	case 4, 6:
		if bi >= int(d.u16(baseArray)) {
			return 0, false
		}
		a := d.u16(baseArray + 2 + 2*(bi*classCount+markClass))
		if a == 0 {
			return 0, false
		}
		baseAnchor = baseArray + int(a)
	case 5:
		if bi >= int(d.u16(baseArray)) {
			return 0, false
		}
		la := baseArray + int(d.u16(baseArray+2+2*bi))
		compCount := int(d.u16(la))
		if compCount == 0 {
			return 0, false
		}
		comp := compCount - 1
		if lid := s.buf[j].ligID; lid != 0 && lid == s.buf[i].ligID && s.buf[i].ligComp > 0 {
			comp = min(compCount, int(s.buf[i].ligComp)) - 1
		}
		a := d.u16(la + 2 + 2*(comp*classCount+markClass))
		if a == 0 {
			return 0, false
		}
		baseAnchor = la + int(a)
	}
	mx, my := s.anchor(d, markAnchor)
	bx, by := s.anchor(d, baseAnchor)
	g := &s.buf[i]
	g.XOffset = int32(math.Round(bx - mx))
	g.YOffset = int32(math.Round(by - my))
	g.attachType = attachMark
	g.attachChain = j - i
	return i + 1, true
}

func (s *shaper) cursive(d data, l *lookup, m *matcher, so, i int) (int, bool) {
	if d.u16(so) != 1 {
		return 0, false
	}
	cov := so + int(d.u16(so+2))
	ci := d.coverage(cov, s.buf[i].ID)
	if ci < 0 {
		return 0, false
	}
	entry := d.u16(so + 6 + 4*ci)
	if entry == 0 {
		return 0, false
	}
	j := m.prev(i)
	if j < 0 {
		return 0, false
	}
	cj := d.coverage(cov, s.buf[j].ID)
	if cj < 0 {
		return 0, false
	}
	exit := d.u16(so + 6 + 4*cj + 2)
	if exit == 0 {
		return 0, false
	}
	exitX, exitY := s.anchor(d, so+int(exit))
	entryX, entryY := s.anchor(d, so+int(entry))
	pj, pi := &s.buf[j], &s.buf[i]
	if !s.rtl {
		pj.XAdvance = int32(math.Round(exitX)) + pj.XOffset
		dd := int32(math.Round(entryX)) + pi.XOffset
		pi.XAdvance -= dd
		pi.XOffset -= dd
	} else {
		dd := int32(math.Round(exitX)) + pj.XOffset
		pj.XAdvance -= dd
		pj.XOffset -= dd
		pi.XAdvance = int32(math.Round(entryX)) + pi.XOffset
	}
	child, parent := j, i
	yOff := int32(math.Round(entryY - exitY))
	if l.flag&1 == 0 {
		child, parent = parent, child
		yOff = -yOff
	}
	s.buf[child].attachType = attachCursive
	s.buf[child].attachChain = parent - child
	s.buf[child].YOffset = yOff
	return i + 1, true
}

// propagateAttachments resolves attachment chains into offsets.
func (s *shaper) propagateAttachments() {
	for i := range s.buf {
		s.propagate(i, 32)
	}
}

func (s *shaper) propagate(i, depth int) {
	g := &s.buf[i]
	chain, typ := g.attachChain, g.attachType
	if chain == 0 || depth == 0 {
		return
	}
	g.attachChain = 0
	j := i + chain
	if j < 0 || j >= len(s.buf) {
		return
	}
	s.propagate(j, depth-1)
	p := &s.buf[j]
	if typ == attachCursive {
		g.YOffset += p.YOffset
		return
	}
	g.XOffset += p.XOffset
	g.YOffset += p.YOffset
	if j < i {
		if !s.rtl {
			for k := j; k < i; k++ {
				g.XOffset -= s.buf[k].XAdvance
				g.YOffset -= s.buf[k].YAdvance
			}
		} else {
			for k := j + 1; k <= i; k++ {
				g.XOffset += s.buf[k].XAdvance
				g.YOffset += s.buf[k].YAdvance
			}
		}
	}
}

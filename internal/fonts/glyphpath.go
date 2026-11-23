package fonts

// OutlineSeg is a segment of a glyph outline in font units: 'M' move,
// 'L' line, 'Q' quadratic curve (control, end), 'C' cubic curve, 'Z'
// close.
type OutlineSeg struct {
	Op byte
	P  [3][2]float64
}

// GlyphOutline returns the outline of a glyph (TrueType or CFF), in font
// units with y up.
func (f *Font) GlyphOutline(g uint16) (out []OutlineSeg, ok bool) {
	defer func() {
		if recover() != nil {
			out, ok = nil, false
		}
	}()
	if f.tables["glyf"] != nil {
		return f.glyfOutline(int(g), [6]float64{1, 0, 0, 1, 0, 0}, 0), true
	}
	st := f.outlines()
	st.once.Do(func() { st.cff = f.parseCFFOutlines() })
	if st.cff == nil {
		return nil, false
	}
	var path []OutlineSeg
	if !st.cff.runPath(int(g), newBounds(), &path) {
		return nil, false
	}
	if n := len(path); n > 0 && path[n-1].Op != 'Z' {
		path = append(path, OutlineSeg{Op: 'Z'})
	}
	return path, true
}

// glyfOutline decodes a TrueType glyph, transformed by m (a, b, c, d, e,
// f).
func (f *Font) glyfOutline(g int, m [6]float64, depth int) []OutlineSeg {
	if depth > 8 {
		return nil
	}
	glyf := f.tables["glyf"]
	start, end := f.glyphRange(g)
	if end <= start || end > len(glyf) || end-start < 10 {
		return nil
	}
	d := glyf[start:end]
	tr := func(x, y float64) [2]float64 {
		return [2]float64{m[0]*x + m[2]*y + m[4], m[1]*x + m[3]*y + m[5]}
	}
	if i16(d, 0) < 0 {
		var out []OutlineSeg
		for _, c := range decodeComposite(d) {
			t := transformOf(c)
			dx, dy := c.dx, c.dy
			if c.flags&2 == 0 {
				dx, dy = 0, 0 // point matching: not supported
			}
			// Component matrix, then the parent's.
			cm := [6]float64{t[0], t[1], t[2], t[3], dx, dy}
			if c.flags&0x800 != 0 { // SCALED_COMPONENT_OFFSET
				cm[4] = t[0]*dx + t[2]*dy
				cm[5] = t[1]*dx + t[3]*dy
			}
			nm := [6]float64{
				cm[0]*m[0] + cm[1]*m[2], cm[0]*m[1] + cm[1]*m[3],
				cm[2]*m[0] + cm[3]*m[2], cm[2]*m[1] + cm[3]*m[3],
				cm[4]*m[0] + cm[5]*m[2] + m[4], cm[4]*m[1] + cm[5]*m[3] + m[5],
			}
			out = append(out, f.glyfOutline(int(c.glyph), nm, depth+1)...)
		}
		return out
	}
	pts, ends, ok := decodeSimple(d)
	if !ok {
		return nil
	}
	var out []OutlineSeg
	first := 0
	for _, last := range ends {
		if last >= len(pts) || last < first {
			break
		}
		c := pts[first : last+1]
		first = last + 1
		n := len(c)
		if n == 0 {
			continue
		}
		// Start at an on-curve point (or the middle of two off-curve
		// points).
		k := -1
		for i := range c {
			if c[i].onCurve {
				k = i
				break
			}
		}
		var startPt point
		if k < 0 {
			startPt = point{(c[0].x + c[1%n].x) / 2, (c[0].y + c[1%n].y) / 2, true}
			k = 0
			// The rotation starts after the virtual point.
			c = append(c[1:], c[0])
			k = n - 1
		} else {
			startPt = c[k]
		}
		out = append(out, OutlineSeg{Op: 'M', P: [3][2]float64{tr(startPt.x, startPt.y)}})
		var ctrl *point
		for j := 1; j <= n; j++ {
			p := c[(k+j)%n]
			if j == n && k >= 0 && startPt.onCurve && p == startPt {
				p = startPt
			}
			if p.onCurve {
				if ctrl != nil {
					out = append(out, OutlineSeg{Op: 'Q', P: [3][2]float64{tr(ctrl.x, ctrl.y), tr(p.x, p.y)}})
					ctrl = nil
				} else {
					out = append(out, OutlineSeg{Op: 'L', P: [3][2]float64{tr(p.x, p.y)}})
				}
				continue
			}
			if ctrl != nil {
				mid := point{(ctrl.x + p.x) / 2, (ctrl.y + p.y) / 2, true}
				out = append(out, OutlineSeg{Op: 'Q', P: [3][2]float64{tr(ctrl.x, ctrl.y), tr(mid.x, mid.y)}})
			}
			pp := p
			ctrl = &pp
		}
		if ctrl != nil {
			out = append(out, OutlineSeg{Op: 'Q', P: [3][2]float64{tr(ctrl.x, ctrl.y), tr(startPt.x, startPt.y)}})
		}
		out = append(out, OutlineSeg{Op: 'Z'})
	}
	return out
}

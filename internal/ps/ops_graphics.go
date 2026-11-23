package ps

import "math"

// popMatrix pops a 6-element array.
func (in *Interp) popMatrix() (Object, Matrix) {
	a := in.popArray()
	return a, matrixOf(a)
}

func matrixOf(a Object) Matrix {
	if a.Kind != KArray || a.Len != 6 {
		rangecheck("matrix")
	}
	var m Matrix
	for i, e := range a.Elems() {
		m[i] = num(e)
	}
	return m
}

func storeMatrix(a Object, m Matrix) Object {
	if a.Kind != KArray || a.Len != 6 {
		rangecheck("matrix")
	}
	el := a.Elems()
	for i := range m {
		el[i] = Real(m[i])
	}
	return a
}

func matrixArray(m Matrix) Object {
	el := make([]Object, 6)
	for i := range m {
		el[i] = Real(m[i])
	}
	return Array(el)
}

// popXY pops two numbers, x below y.
func (in *Interp) popXY() (float64, float64) {
	y := in.popNum()
	x := in.popNum()
	return x, y
}

// transformOp implements transform, itransform, dtransform and
// idtransform with an optional matrix operand.
func (in *Interp) transformOp(inverse, delta bool) {
	m := in.gs.CTM
	if t := in.top(); t.Kind == KArray {
		_, m = in.popMatrix()
	}
	if inverse {
		var ok bool
		m, ok = m.Invert()
		if !ok {
			panic(psError{"undefinedresult", "itransform"})
		}
	}
	x, y := in.popXY()
	if delta {
		x, y = m.ApplyD(x, y)
	} else {
		x, y = m.Apply(x, y)
	}
	in.push(Real(x))
	in.push(Real(y))
}

// matrixOp applies a transformation to the CTM, or to a matrix operand.
func (in *Interp) matrixOp(pop func() Matrix) {
	if t := in.top(); t.Kind == KArray {
		a := in.pop()
		m := pop()
		in.push(storeMatrix(a, m))
		return
	}
	m := pop()
	in.gs.CTM = m.Mul(in.gs.CTM)
}

func (in *Interp) registerGraphics() {
	d := in.def
	// Graphics state.
	d("gsave", func(in *Interp) { in.gsave() })
	d("grestore", func(in *Interp) { in.grestore() })
	d("grestoreall", func(in *Interp) {
		for len(in.gstack) > 0 {
			in.grestore()
		}
	})
	d("initgraphics", func(in *Interp) {
		font := in.gs.Font
		in.gs.init()
		in.gs.Font = font
	})
	d("gstate", func(in *Interp) { in.push(Object{Kind: KGState, Ref: in.gs.clone()}) })
	d("currentgstate", func(in *Interp) {
		o := in.pop()
		if o.Kind != KGState {
			typecheck("currentgstate")
		}
		*o.Ref.(*GState) = *in.gs.clone()
		in.push(o)
	})
	d("setgstate", func(in *Interp) {
		o := in.pop()
		if o.Kind != KGState {
			typecheck("setgstate")
		}
		in.gs = o.Ref.(*GState).clone()
	})
	d("setlinewidth", func(in *Interp) { in.gs.Stroke.Width = math.Abs(in.popNum()) })
	d("currentlinewidth", func(in *Interp) { in.push(Real(in.gs.Stroke.Width)) })
	d("setlinecap", func(in *Interp) { in.gs.Stroke.Cap = in.popInt() })
	d("currentlinecap", func(in *Interp) { in.push(Int(in.gs.Stroke.Cap)) })
	d("setlinejoin", func(in *Interp) { in.gs.Stroke.Join = in.popInt() })
	d("currentlinejoin", func(in *Interp) { in.push(Int(in.gs.Stroke.Join)) })
	d("setmiterlimit", func(in *Interp) { in.gs.Stroke.Miter = math.Max(1, in.popNum()) })
	d("currentmiterlimit", func(in *Interp) { in.push(Real(in.gs.Stroke.Miter)) })
	d("setdash", func(in *Interp) {
		phase := in.popNum()
		a := in.popArray()
		var dash []float64
		total := 0.0
		for _, e := range a.Elems() {
			v := math.Abs(num(e))
			dash = append(dash, v)
			total += v
		}
		if total == 0 {
			dash = nil
		}
		in.gs.Stroke.Dash = dash
		in.gs.Stroke.Phase = phase
	})
	d("currentdash", func(in *Interp) {
		var el []Object
		for _, v := range in.gs.Stroke.Dash {
			el = append(el, Real(v))
		}
		in.push(Array(el))
		in.push(Real(in.gs.Stroke.Phase))
	})

	// Coordinates.
	d("matrix", func(in *Interp) { in.push(matrixArray(identity)) })
	d("initmatrix", func(in *Interp) { in.gs.CTM = identity })
	d("identmatrix", func(in *Interp) { a := in.popArray(); in.push(storeMatrix(a, identity)) })
	d("defaultmatrix", func(in *Interp) { a := in.popArray(); in.push(storeMatrix(a, identity)) })
	d("currentmatrix", func(in *Interp) { a := in.popArray(); in.push(storeMatrix(a, in.gs.CTM)) })
	d("setmatrix", func(in *Interp) { _, m := in.popMatrix(); in.gs.CTM = m })
	d("translate", func(in *Interp) {
		in.matrixOp(func() Matrix { x, y := in.popXY(); return Matrix{1, 0, 0, 1, x, y} })
	})
	d("scale", func(in *Interp) {
		in.matrixOp(func() Matrix { x, y := in.popXY(); return Matrix{x, 0, 0, y, 0, 0} })
	})
	d("rotate", func(in *Interp) {
		in.matrixOp(func() Matrix {
			a := in.popNum() * math.Pi / 180
			c, s := math.Cos(a), math.Sin(a)
			// Exact values for multiples of 90 degrees.
			if math.Abs(c) < 1e-15 {
				c = 0
			}
			if math.Abs(s) < 1e-15 {
				s = 0
			}
			return Matrix{c, s, -s, c, 0, 0}
		})
	})
	d("concat", func(in *Interp) { _, m := in.popMatrix(); in.gs.CTM = m.Mul(in.gs.CTM) })
	d("concatmatrix", func(in *Interp) {
		dst := in.popArray()
		_, m2 := in.popMatrix()
		_, m1 := in.popMatrix()
		in.push(storeMatrix(dst, m1.Mul(m2)))
	})
	d("invertmatrix", func(in *Interp) {
		dst := in.popArray()
		_, m := in.popMatrix()
		inv, ok := m.Invert()
		if !ok {
			panic(psError{"undefinedresult", "invertmatrix"})
		}
		in.push(storeMatrix(dst, inv))
	})
	d("transform", func(in *Interp) { in.transformOp(false, false) })
	d("itransform", func(in *Interp) { in.transformOp(true, false) })
	d("dtransform", func(in *Interp) { in.transformOp(false, true) })
	d("idtransform", func(in *Interp) { in.transformOp(true, true) })

	// Path construction.
	d("newpath", func(in *Interp) { in.newPath() })
	d("currentpoint", func(in *Interp) {
		x, y := in.userPoint()
		in.push(Real(x))
		in.push(Real(y))
	})
	d("moveto", func(in *Interp) { x, y := in.popXY(); in.moveTo(x, y) })
	d("rmoveto", func(in *Interp) {
		dx, dy := in.popXY()
		x, y := in.userPoint()
		in.moveTo(x+dx, y+dy)
	})
	d("lineto", func(in *Interp) { x, y := in.popXY(); in.lineTo(x, y) })
	d("rlineto", func(in *Interp) {
		dx, dy := in.popXY()
		x, y := in.userPoint()
		in.lineTo(x+dx, y+dy)
	})
	d("curveto", func(in *Interp) {
		x3, y3 := in.popXY()
		x2, y2 := in.popXY()
		x1, y1 := in.popXY()
		in.curveTo(x1, y1, x2, y2, x3, y3)
	})
	d("rcurveto", func(in *Interp) {
		x3, y3 := in.popXY()
		x2, y2 := in.popXY()
		x1, y1 := in.popXY()
		x, y := in.userPoint()
		in.curveTo(x+x1, y+y1, x+x2, y+y2, x+x3, y+y3)
	})
	arc := func(neg bool) func(*Interp) {
		return func(in *Interp) {
			a2, a1 := in.popNum(), in.popNum()
			r := in.popNum()
			x, y := in.popXY()
			in.arc(x, y, r, a1, a2, neg)
		}
	}
	d("arc", arc(false))
	d("arcn", arc(true))
	d("arct", func(in *Interp) {
		r := in.popNum()
		x2, y2 := in.popXY()
		x1, y1 := in.popXY()
		in.arct(x1, y1, x2, y2, r)
	})
	d("arcto", func(in *Interp) {
		r := in.popNum()
		x2, y2 := in.popXY()
		x1, y1 := in.popXY()
		a, b, c, e := in.arct(x1, y1, x2, y2, r)
		for _, v := range []float64{a, b, c, e} {
			in.push(Real(v))
		}
	})
	d("closepath", func(in *Interp) { in.closePath() })
	d("flattenpath", func(in *Interp) { in.gs.Path = flatten(in.gs.Path) })
	d("reversepath", func(in *Interp) { in.gs.Path = reversePath(in.gs.Path) })
	d("strokepath", func(in *Interp) {
		// Approximation: the stroked path is replaced by its outline's
		// path itself (a later fill paints the area it encloses).
		in.warn("ps: strokepath is approximated")
	})
	d("clippath", func(in *Interp) {
		in.newPath()
		c := in.gs.Clip
		if c != nil {
			in.gs.Path = append(Path(nil), c.Path...)
		} else {
			b := in.list.box
			in.gs.Path = Path{{Op: 'M', P: [3]Point{{b[0], b[1]}}}, {Op: 'L', P: [3]Point{{b[2], b[1]}}},
				{Op: 'L', P: [3]Point{{b[2], b[3]}}}, {Op: 'L', P: [3]Point{{b[0], b[3]}}}, {Op: 'Z'}}
		}
		if len(in.gs.Path) > 0 {
			in.gs.HasCP = true
			in.gs.CP = in.gs.Path[0].P[0]
		}
	})
	d("pathbbox", func(in *Interp) {
		g := in.gs
		if len(g.Path) == 0 {
			panic(psError{"nocurrentpoint", "pathbbox"})
		}
		inv, ok := g.CTM.Invert()
		if !ok {
			inv = identity
		}
		x0, y0, x1, y1, ok := g.Path.BBox()
		if !ok {
			x0, y0, x1, y1 = g.CP.X, g.CP.Y, g.CP.X, g.CP.Y
		}
		// Bounding box of the transformed corners.
		xs := [4]float64{}
		ys := [4]float64{}
		xs[0], ys[0] = inv.Apply(x0, y0)
		xs[1], ys[1] = inv.Apply(x1, y0)
		xs[2], ys[2] = inv.Apply(x1, y1)
		xs[3], ys[3] = inv.Apply(x0, y1)
		lx, ly, hx, hy := xs[0], ys[0], xs[0], ys[0]
		for i := 1; i < 4; i++ {
			lx, ly = math.Min(lx, xs[i]), math.Min(ly, ys[i])
			hx, hy = math.Max(hx, xs[i]), math.Max(hy, ys[i])
		}
		for _, v := range []float64{lx, ly, hx, hy} {
			in.push(Real(v))
		}
	})
	d("pathforall", func(in *Interp) {
		closeP := in.popProc()
		curveP := in.popProc()
		lineP := in.popProc()
		moveP := in.popProc()
		inv, ok := in.gs.CTM.Invert()
		if !ok {
			inv = identity
		}
		path := append(Path(nil), in.gs.Path...)
		for _, s := range path {
			switch s.Op {
			case 'M', 'L':
				x, y := inv.Apply(s.P[0].X, s.P[0].Y)
				in.push(Real(x))
				in.push(Real(y))
				if s.Op == 'M' {
					in.execObj(moveP)
				} else {
					in.execObj(lineP)
				}
			case 'C':
				for k := 0; k < 3; k++ {
					x, y := inv.Apply(s.P[k].X, s.P[k].Y)
					in.push(Real(x))
					in.push(Real(y))
				}
				in.execObj(curveP)
			case 'Z':
				in.execObj(closeP)
			}
		}
	})
	d("initclip", func(in *Interp) { in.gs.Clip = nil })
	d("clip", func(in *Interp) { in.clip(false) })
	d("eoclip", func(in *Interp) { in.clip(true) })
	d("rectclip", func(in *Interp) {
		path := in.rects()
		saved := in.gs.Path
		in.gs.Path = path
		in.clip(false)
		in.gs.Path = saved
		in.newPath()
	})
	d("setbbox", func(in *Interp) {
		for i := 0; i < 4; i++ {
			in.pop()
		}
	})
	d("uappend", func(in *Interp) { in.uappend(in.pop()) })
	d("upath", func(in *Interp) { in.pop(); in.push(procObj()) })
	d("ufill", func(in *Interp) { in.userPathOp(func() { in.fill(false) }) })
	d("ueofill", func(in *Interp) { in.userPathOp(func() { in.fill(true) }) })
	d("ustroke", func(in *Interp) {
		var m *Matrix
		if t := in.top(); t.Kind == KArray && !t.Exec && t.Len == 6 {
			_, mm := in.popMatrix()
			m = &mm
		}
		up := in.pop()
		in.gsave()
		in.newPath()
		in.uappend(up)
		if m != nil {
			in.gs.CTM = m.Mul(in.gs.CTM)
		}
		in.stroke()
		in.grestore()
	})
	d("ustrokepath", func(in *Interp) {
		if t := in.top(); t.Kind == KArray && !t.Exec && t.Len == 6 {
			in.pop()
		}
		in.uappend(in.pop())
	})
	d("charpath", func(in *Interp) {
		in.popBool()
		s := in.popString()
		in.showString(s.Bytes(), showOpts{charpath: true})
	})

	// Painting.
	d("fill", func(in *Interp) { in.fill(false) })
	d("eofill", func(in *Interp) { in.fill(true) })
	d("stroke", func(in *Interp) { in.stroke() })
	d("rectfill", func(in *Interp) {
		path := in.rects()
		saved := in.gs.Path
		in.gs.Path = path
		in.fill(false)
		in.gs.Path = saved
	})
	d("rectstroke", func(in *Interp) {
		var m *Matrix
		if t := in.top(); t.Kind == KArray && t.Len == 6 && len(in.ostack) >= 2 {
			// rectstroke with a matrix (x y w h matrix or array matrix).
			if u := in.ostack[len(in.ostack)-2]; u.IsNum() || u.Kind == KArray || u.Kind == KString {
				if len(in.ostack) >= 5 || u.Kind != KNull {
					_, mm := in.popMatrix()
					m = &mm
				}
			}
		}
		path := in.rects()
		in.gsave()
		in.gs.Path = path
		if m != nil {
			in.gs.CTM = m.Mul(in.gs.CTM)
		}
		in.stroke()
		in.grestore()
	})
	d("erasepage", func(in *Interp) {})
	d("shfill", func(in *Interp) {
		sh := in.parseShading(in.popDict())
		if sh != nil {
			in.emit(Op{Kind: OpShade, Shading: sh, CTM: in.gs.CTM})
		}
	})

	// Colors.
	d("setgray", func(in *Interp) {
		v := in.popNum()
		in.setSpace(deviceGray)
		in.setColor([]float64{v})
	})
	d("currentgray", func(in *Interp) { in.push(Real(in.gs.Paint.Color.gray())) })
	d("setrgbcolor", func(in *Interp) {
		b, g, r := in.popNum(), in.popNum(), in.popNum()
		in.setSpace(deviceRGB)
		in.setColor([]float64{r, g, b})
	})
	d("currentrgbcolor", func(in *Interp) {
		r, g, b := in.gs.Paint.Color.rgb()
		in.push(Real(r))
		in.push(Real(g))
		in.push(Real(b))
	})
	d("sethsbcolor", func(in *Interp) {
		b, s, h := in.popNum(), in.popNum(), in.popNum()
		r, g, bl := hsbToRGB(h, s, b)
		in.setSpace(deviceRGB)
		in.setColor([]float64{r, g, bl})
	})
	d("currenthsbcolor", func(in *Interp) {
		h, s, b := rgbToHSB(in.gs.Paint.Color.rgb())
		in.push(Real(h))
		in.push(Real(s))
		in.push(Real(b))
	})
	d("setcmykcolor", func(in *Interp) {
		k, y, m, c := in.popNum(), in.popNum(), in.popNum(), in.popNum()
		in.setSpace(deviceCMYK)
		in.setColor([]float64{c, m, y, k})
	})
	d("currentcmykcolor", func(in *Interp) {
		c, m, y, k := in.gs.Paint.Color.cmyk()
		for _, v := range []float64{c, m, y, k} {
			in.push(Real(v))
		}
	})
	d("setcolorspace", func(in *Interp) { in.setSpace(in.parseColorSpace(in.pop())) })
	d("currentcolorspace", func(in *Interp) {
		o := in.gs.Space.obj
		if o.Kind == KName {
			o = Array([]Object{o})
		}
		in.push(o)
	})
	d("setcolor", func(in *Interp) {
		cs := in.gs.Space
		if cs.family == "Pattern" {
			p := in.pop()
			var comps []float64
			if cs.base != nil {
				for i := 0; i < cs.base.n; i++ {
					comps = append([]float64{in.popNum()}, comps...)
				}
			}
			in.setPattern(p, cs, comps)
			return
		}
		c := make([]float64, cs.n)
		for i := cs.n - 1; i >= 0; i-- {
			c[i] = in.popNum()
		}
		in.setColor(c)
	})
	d("currentcolor", func(in *Interp) {
		for _, v := range in.gs.Comps {
			in.push(Real(v))
		}
	})
	d("setpattern", func(in *Interp) {
		p := in.top()
		if p.Kind == KDict {
			if pt, ok := p.DictV().GetS("PaintType"); ok && intOf(pt) == 2 {
				// Uncolored: the components of the current space.
				base := in.gs.Space
				in.pop()
				c := make([]float64, base.n)
				for i := base.n - 1; i >= 0; i-- {
					c[i] = in.popNum()
				}
				cs := &colorSpace{family: "Pattern", n: 1, base: base, obj: Array([]Object{Name("Pattern"), base.obj})}
				in.setSpace(cs)
				in.setPattern(p, cs, c)
				return
			}
		}
		in.pop()
		cs := &colorSpace{family: "Pattern", n: 1, obj: Name("Pattern")}
		in.setSpace(cs)
		in.setPattern(p, cs, nil)
	})
	d("makepattern", func(in *Interp) {
		_, m := in.popMatrix()
		p := in.popDict()
		in.push(DictObj(in.makePattern(p, m)))
	})
}

// rects pops rectangles (x y w h, or a number array) as a path.
func (in *Interp) rects() Path {
	var nums []float64
	t := in.top()
	switch t.Kind {
	case KArray:
		for _, e := range in.pop().Elems() {
			nums = append(nums, num(e))
		}
	case KString:
		nums = decodeNumString(in.pop().Bytes())
	default:
		h := in.popNum()
		w := in.popNum()
		x, y := in.popXY()
		nums = []float64{x, y, w, h}
	}
	g := in.gs
	var p Path
	for i := 0; i+3 < len(nums); i += 4 {
		x, y, w, h := nums[i], nums[i+1], nums[i+2], nums[i+3]
		pts := [4][2]float64{{x, y}, {x + w, y}, {x + w, y + h}, {x, y + h}}
		if w*h < 0 {
			pts[1], pts[3] = pts[3], pts[1]
		}
		for k, pt := range pts {
			dx, dy := g.CTM.Apply(pt[0], pt[1])
			op := byte('L')
			if k == 0 {
				op = 'M'
			}
			p = append(p, Seg{Op: op, P: [3]Point{{dx, dy}}})
		}
		p = append(p, Seg{Op: 'Z'})
	}
	return p
}

// decodeNumString decodes an encoded number string (binary token 149).
func decodeNumString(b []byte) []float64 {
	if len(b) < 4 || b[0] != 149 {
		return nil
	}
	rep := int(b[1])
	n := int(b[2])<<8 | int(b[3])
	if rep >= 128 {
		n = int(b[3])<<8 | int(b[2])
	}
	var out []float64
	p := 4
	be := rep < 128
	r := rep & 127
	for i := 0; i < n; i++ {
		switch {
		case r <= 31: // 32-bit fixed
			if p+4 > len(b) {
				return out
			}
			var v uint32
			if be {
				v = uint32(b[p])<<24 | uint32(b[p+1])<<16 | uint32(b[p+2])<<8 | uint32(b[p+3])
			} else {
				v = uint32(b[p+3])<<24 | uint32(b[p+2])<<16 | uint32(b[p+1])<<8 | uint32(b[p])
			}
			out = append(out, float64(int32(v))/float64(int(1)<<r))
			p += 4
		case r <= 47: // 16-bit fixed
			if p+2 > len(b) {
				return out
			}
			var v uint16
			if be {
				v = uint16(b[p])<<8 | uint16(b[p+1])
			} else {
				v = uint16(b[p+1])<<8 | uint16(b[p])
			}
			out = append(out, float64(int16(v))/float64(int(1)<<(r-32)))
			p += 2
		default: // 32-bit IEEE
			if p+4 > len(b) {
				return out
			}
			var v uint32
			if be {
				v = uint32(b[p])<<24 | uint32(b[p+1])<<16 | uint32(b[p+2])<<8 | uint32(b[p+3])
			} else {
				v = uint32(b[p+3])<<24 | uint32(b[p+2])<<16 | uint32(b[p+1])<<8 | uint32(b[p])
			}
			out = append(out, float64(math.Float32frombits(v)))
			p += 4
		}
	}
	return out
}

func (in *Interp) uappend(up Object) {
	if up.Kind != KArray {
		typecheck("uappend")
	}
	p := up
	p.Exec = true
	in.execObj(p)
}

func (in *Interp) userPathOp(paint func()) {
	up := in.pop()
	in.gsave()
	in.newPath()
	in.uappend(up)
	paint()
	in.grestore()
}

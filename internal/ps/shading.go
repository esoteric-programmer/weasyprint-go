package ps

import (
	"io"
	"math"
)

// function is a PostScript function (types 0, 2 and 3) or procedure.
type function struct {
	typ    int
	domain []float64
	rng    []float64
	// type 0
	size    []int
	bps     int
	encode  []float64
	decode  []float64
	samples []float64 // normalized 0..1, nOut per sample
	// type 2
	c0, c1 []float64
	n      float64
	// type 3
	fns    []*function
	bounds []float64
	// procedure
	proc Object
	in   *Interp
	nOut int
}

func numsOf(o Object) []float64 {
	var r []float64
	for _, e := range o.Elems() {
		if e.IsNum() {
			r = append(r, e.Num())
		}
	}
	return r
}

func (in *Interp) parseFunction(o Object) *function {
	if o.Kind == KArray && o.Exec {
		return &function{typ: -1, proc: o, in: in, nOut: 1}
	}
	if o.Kind != KDict {
		return nil
	}
	d := o.DictV()
	get := func(k string) (Object, bool) { return d.GetS(k) }
	f := &function{}
	if v, ok := get("FunctionType"); ok {
		f.typ = intOf(v)
	}
	if v, ok := get("Domain"); ok {
		f.domain = numsOf(v)
	}
	if v, ok := get("Range"); ok {
		f.rng = numsOf(v)
	}
	switch f.typ {
	case 0:
		if v, ok := get("Size"); ok {
			for _, s := range numsOf(v) {
				f.size = append(f.size, int(s))
			}
		}
		f.bps = 8
		if v, ok := get("BitsPerSample"); ok {
			f.bps = intOf(v)
		}
		if v, ok := get("Encode"); ok {
			f.encode = numsOf(v)
		}
		if v, ok := get("Decode"); ok {
			f.decode = numsOf(v)
		}
		f.nOut = len(f.rng) / 2
		if len(f.decode) == 0 {
			f.decode = f.rng
		}
		total := f.nOut
		for _, s := range f.size {
			total *= s
		}
		if total <= 0 || total > 1<<24 {
			return nil
		}
		var data []byte
		if v, ok := get("DataSource"); ok {
			switch v.Kind {
			case KString:
				data = v.Bytes()
			case KFile:
				data, _ = io.ReadAll(io.LimitReader(v.Ref.(*File), int64(total*f.bps/8+8)))
			}
		}
		br := bitReader{b: data}
		maxV := float64(uint64(1)<<f.bps - 1)
		f.samples = make([]float64, total)
		for i := range f.samples {
			f.samples[i] = float64(br.read(f.bps)) / maxV
		}
	case 2:
		f.c0 = []float64{0}
		f.c1 = []float64{1}
		if v, ok := get("C0"); ok {
			f.c0 = numsOf(v)
		}
		if v, ok := get("C1"); ok {
			f.c1 = numsOf(v)
		}
		f.n = 1
		if v, ok := get("N"); ok {
			f.n = v.Num()
		}
		f.nOut = len(f.c0)
	case 3:
		if v, ok := get("Functions"); ok {
			for _, e := range v.Elems() {
				sub := in.parseFunction(e)
				if sub == nil {
					return nil
				}
				f.fns = append(f.fns, sub)
			}
		}
		if v, ok := get("Bounds"); ok {
			f.bounds = numsOf(v)
		}
		if v, ok := get("Encode"); ok {
			f.encode = numsOf(v)
		}
		if len(f.fns) > 0 {
			f.nOut = f.fns[0].nOut
		}
	default:
		return nil
	}
	return f
}

// FuncDesc describes a function (types 0, 2 and 3).
type FuncDesc struct {
	Type          int
	Domain, Range []float64
	// Type 0: samples (0..1) and their layout.
	Size           []int
	Encode, Decode []float64
	Samples        []float64
	// Type 2.
	C0, C1 []float64
	N      float64
	// Type 3.
	Functions []*FuncDesc
	Bounds    []float64
}

func (f *function) desc() *FuncDesc {
	d := &FuncDesc{Type: f.typ, Domain: f.domain, Range: f.rng, Size: f.size, Encode: f.encode, Decode: f.decode,
		Samples: f.samples, C0: f.c0, C1: f.c1, N: f.n, Bounds: f.bounds}
	if d.Domain == nil {
		d.Domain = []float64{0, 1}
	}
	for _, sub := range f.fns {
		d.Functions = append(d.Functions, sub.desc())
	}
	return d
}

type bitReader struct {
	b   []byte
	pos int // bit position
}

func (r *bitReader) read(n int) uint64 {
	var v uint64
	for i := 0; i < n; i++ {
		byteI := r.pos >> 3
		var bit uint64
		if byteI < len(r.b) {
			bit = uint64(r.b[byteI]>>(7-uint(r.pos&7))) & 1
		}
		v = v<<1 | bit
		r.pos++
	}
	return v
}

func (r *bitReader) align() { r.pos = (r.pos + 7) &^ 7 }

func interp(x, x0, x1, y0, y1 float64) float64 {
	if x1 == x0 {
		return y0
	}
	return y0 + (x-x0)*(y1-y0)/(x1-x0)
}

func (f *function) eval(x []float64) []float64 {
	if f.typ == -1 {
		return f.in.callFunc(f.proc, x, f.nOut)
	}
	xs := make([]float64, len(x))
	copy(xs, x)
	for i := range xs {
		if 2*i+1 < len(f.domain) {
			xs[i] = math.Max(f.domain[2*i], math.Min(f.domain[2*i+1], xs[i]))
		}
	}
	var out []float64
	switch f.typ {
	case 0:
		out = f.evalSampled(xs)
	case 2:
		t := 0.0
		if len(xs) > 0 {
			t = xs[0]
		}
		out = make([]float64, len(f.c0))
		for i := range out {
			c1 := 0.0
			if i < len(f.c1) {
				c1 = f.c1[i]
			}
			out[i] = f.c0[i] + math.Pow(t, f.n)*(c1-f.c0[i])
		}
	case 3:
		t := 0.0
		if len(xs) > 0 {
			t = xs[0]
		}
		d0, d1 := 0.0, 1.0
		if len(f.domain) >= 2 {
			d0, d1 = f.domain[0], f.domain[1]
		}
		k := 0
		for k < len(f.bounds) && t >= f.bounds[k] {
			k++
		}
		if k >= len(f.fns) {
			k = len(f.fns) - 1
		}
		lo, hi := d0, d1
		if k > 0 {
			lo = f.bounds[k-1]
		}
		if k < len(f.bounds) {
			hi = f.bounds[k]
		}
		e0, e1 := 0.0, 1.0
		if 2*k+1 < len(f.encode) {
			e0, e1 = f.encode[2*k], f.encode[2*k+1]
		}
		out = f.fns[k].eval([]float64{interp(t, lo, hi, e0, e1)})
	}
	for i := range out {
		if 2*i+1 < len(f.rng) {
			out[i] = math.Max(f.rng[2*i], math.Min(f.rng[2*i+1], out[i]))
		}
	}
	return out
}

func (f *function) evalSampled(x []float64) []float64 {
	m := len(f.size)
	if m == 0 {
		return make([]float64, f.nOut)
	}
	// Encoded sample coordinates.
	e := make([]float64, m)
	for i := 0; i < m; i++ {
		d0, d1 := 0.0, 1.0
		if 2*i+1 < len(f.domain) {
			d0, d1 = f.domain[2*i], f.domain[2*i+1]
		}
		en0, en1 := 0.0, float64(f.size[i]-1)
		if 2*i+1 < len(f.encode) {
			en0, en1 = f.encode[2*i], f.encode[2*i+1]
		}
		xi := 0.0
		if i < len(x) {
			xi = x[i]
		}
		e[i] = math.Max(0, math.Min(float64(f.size[i]-1), interp(xi, d0, d1, en0, en1)))
	}
	out := make([]float64, f.nOut)
	// Multilinear interpolation over the 2^m corners.
	for corner := 0; corner < 1<<m; corner++ {
		w := 1.0
		idx := 0
		stride := 1
		for i := 0; i < m; i++ {
			lo := math.Floor(e[i])
			fr := e[i] - lo
			c := int(lo)
			if corner>>i&1 == 1 {
				c++
				w *= fr
			} else {
				w *= 1 - fr
			}
			if c >= f.size[i] {
				c = f.size[i] - 1
			}
			idx += c * stride
			stride *= f.size[i]
		}
		if w == 0 {
			continue
		}
		for j := 0; j < f.nOut; j++ {
			if k := idx*f.nOut + j; k < len(f.samples) {
				out[j] += w * f.samples[k]
			}
		}
	}
	for j := range out {
		d0, d1 := 0.0, 1.0
		if 2*j+1 < len(f.decode) {
			d0, d1 = f.decode[2*j], f.decode[2*j+1]
		}
		out[j] = d0 + out[j]*(d1-d0)
	}
	return out
}

// Shading is a smooth shading, with its colors converted to device
// colors.
type Shading struct {
	Type   int
	Coords []float64
	Domain [2]float64
	Extend [2]bool
	// Color returns the color at parameter t (types 2 and 3) or at
	// (x, y) (type 1, through Matrix).
	Color func(t ...float64) Color
	N     int // device components
	// Funcs are the functions in device colors (one, or one per
	// component), for exact output; nil when the colors are sampled.
	Funcs      []*FuncDesc
	Matrix     Matrix
	BBox       *[4]float64
	Background *Color
	// Mesh shadings (types 4 to 7): triangles or patches.
	Triangles [][3]Vertex
	Patches   []Patch
}

// Vertex is a mesh vertex.
type Vertex struct {
	X, Y float64
	C    Color
}

// Patch is a Coons or tensor patch: 16 points (Coons patches use the 12
// boundary ones) and 4 corner colors.
type Patch struct {
	P [16]Point
	C [4]Color
}

func (in *Interp) parseShading(d *Dict) *Shading {
	get := func(k string) (Object, bool) { return d.GetS(k) }
	sh := &Shading{Domain: [2]float64{0, 1}, Matrix: identity}
	v, ok := get("ShadingType")
	if !ok {
		return nil
	}
	sh.Type = intOf(v)
	csObj, ok := get("ColorSpace")
	if !ok {
		return nil
	}
	cs := in.parseColorSpace(csObj)
	var fn *function
	var fns []*function
	if v, ok := get("Function"); ok {
		if v.Kind == KArray && !v.Exec {
			for _, e := range v.Elems() {
				if f := in.parseFunction(e); f != nil {
					fns = append(fns, f)
				}
			}
		} else {
			fn = in.parseFunction(v)
		}
	}
	colorOf := func(args []float64) Color {
		var comps []float64
		switch {
		case fn != nil:
			fn.nOut = max(fn.nOut, cs.n)
			comps = fn.eval(args)
		case len(fns) > 0:
			for _, f := range fns {
				comps = append(comps, f.eval(args)...)
			}
		default:
			comps = args
		}
		return in.toDevice(cs, comps)
	}
	sh.N = in.toDevice(cs, cs.initial()).N
	if v, ok := get("BBox"); ok && v.Len == 4 {
		n := numsOf(v)
		b := [4]float64{n[0], n[1], n[2], n[3]}
		sh.BBox = &b
	}
	if v, ok := get("Background"); ok {
		c := in.toDevice(cs, numsOf(v))
		sh.Background = &c
	}
	if v, ok := get("Coords"); ok {
		sh.Coords = numsOf(v)
	}
	if v, ok := get("Domain"); ok {
		n := numsOf(v)
		if len(n) >= 2 {
			sh.Domain = [2]float64{n[0], n[1]}
		}
	}
	if v, ok := get("Extend"); ok {
		for i, e := range v.Elems() {
			if i < 2 && e.Kind == KBool {
				sh.Extend[i] = e.BoolV()
			}
		}
	}
	// Device color spaces keep their functions.
	if dev := cs.family == "DeviceGray" || cs.family == "DeviceRGB" || cs.family == "DeviceCMYK"; dev && cs.n == sh.N {
		if fn != nil && fn.typ >= 0 {
			sh.Funcs = []*FuncDesc{fn.desc()}
		} else if len(fns) == cs.n {
			for _, f := range fns {
				if f.typ < 0 {
					sh.Funcs = nil
					break
				}
				sh.Funcs = append(sh.Funcs, f.desc())
			}
		}
	}
	switch sh.Type {
	case 1:
		if v, ok := get("Matrix"); ok {
			sh.Matrix = matrixOf(v)
		}
		dom := [4]float64{0, 1, 0, 1}
		if v, ok := get("Domain"); ok {
			n := numsOf(v)
			if len(n) == 4 {
				dom = [4]float64{n[0], n[1], n[2], n[3]}
			}
		}
		sh.Coords = dom[:]
		sh.Color = func(t ...float64) Color { return colorOf(t) }
	case 2, 3:
		if len(sh.Coords) < 4 || sh.Type == 3 && len(sh.Coords) < 6 {
			return nil
		}
		sh.Color = func(t ...float64) Color { return colorOf(t) }
	case 4, 5, 6, 7:
		in.parseMesh(sh, d, cs, fn != nil || len(fns) > 0, colorOf)
	default:
		return nil
	}
	return sh
}

// parseMesh decodes the vertices of a mesh shading.
func (in *Interp) parseMesh(sh *Shading, d *Dict, cs *colorSpace, param bool, colorOf func([]float64) Color) {
	geti := func(k string, def int) int {
		if v, ok := d.GetS(k); ok && v.IsNum() {
			return int(v.Num())
		}
		return def
	}
	nc := cs.n
	if param {
		nc = 1
	}
	var decode []float64
	if v, ok := d.GetS("Decode"); ok {
		decode = numsOf(v)
	}
	bpc, bpcomp, bpf := geti("BitsPerCoordinate", 0), geti("BitsPerComponent", 0), geti("BitsPerFlag", 0)
	ds, _ := d.GetS("DataSource")
	// The values come from an array of numbers or from bits.
	var nums []float64
	var br *bitReader
	switch ds.Kind {
	case KArray:
		nums = numsOf(ds)
	case KString:
		br = &bitReader{b: ds.Bytes()}
	case KFile:
		data, _ := io.ReadAll(io.LimitReader(ds.Ref.(*File), 64<<20))
		br = &bitReader{b: data}
	default:
		return
	}
	pos := 0
	eof := false
	dec := func(k int, raw uint64, bits int) float64 {
		maxV := float64(uint64(1)<<bits - 1)
		if 2*k+1 < len(decode) && maxV > 0 {
			return decode[2*k] + float64(raw)*(decode[2*k+1]-decode[2*k])/maxV
		}
		return float64(raw)
	}
	readNum := func(k, bits int) float64 {
		if br == nil {
			if pos >= len(nums) {
				eof = true
				return 0
			}
			pos++
			return nums[pos-1]
		}
		if br.pos+bits > len(br.b)*8 {
			eof = true
			return 0
		}
		return dec(k, br.read(bits), bits)
	}
	readFlag := func() int {
		if br == nil {
			return int(readNum(0, 0))
		}
		if br.pos+bpf > len(br.b)*8 {
			eof = true
			return 0
		}
		return int(br.read(bpf))
	}
	readVertex := func() Vertex {
		x := readNum(0, bpc)
		y := readNum(1, bpc)
		c := make([]float64, nc)
		for i := range c {
			c[i] = readNum(2+i, bpcomp)
		}
		return Vertex{X: x, Y: y, C: colorOf(c)}
	}
	readColor := func() Color {
		c := make([]float64, nc)
		for i := range c {
			c[i] = readNum(2+i, bpcomp)
		}
		return colorOf(c)
	}
	switch sh.Type {
	case 4:
		var tri []Vertex
		var last [3]Vertex
		have := false
		for n := 0; n < 1<<22; n++ {
			f := readFlag()
			v := readVertex()
			if eof {
				break
			}
			if br != nil {
				br.align()
			}
			if len(tri) > 0 || f == 0 || !have {
				tri = append(tri, v)
				if len(tri) == 3 {
					last = [3]Vertex{tri[0], tri[1], tri[2]}
					sh.Triangles = append(sh.Triangles, last)
					have = true
					tri = nil
				}
				continue
			}
			if f == 1 {
				last = [3]Vertex{last[1], last[2], v}
			} else {
				last = [3]Vertex{last[0], last[2], v}
			}
			sh.Triangles = append(sh.Triangles, last)
		}
	case 5:
		perRow := geti("VerticesPerRow", 2)
		var rows [][]Vertex
		for n := 0; n < 1<<22; n++ {
			row := make([]Vertex, perRow)
			for i := range row {
				row[i] = readVertex()
				if br != nil {
					br.align()
				}
			}
			if eof {
				break
			}
			rows = append(rows, row)
		}
		for r := 1; r < len(rows); r++ {
			for i := 1; i < perRow; i++ {
				p, q := rows[r-1], rows[r]
				sh.Triangles = append(sh.Triangles, [3]Vertex{p[i-1], p[i], q[i-1]}, [3]Vertex{p[i], q[i], q[i-1]})
			}
		}
	case 6, 7:
		np := 12
		if sh.Type == 7 {
			np = 16
		}
		var prev *Patch
		for n := 0; n < 1<<20; n++ {
			f := readFlag()
			if eof {
				break
			}
			var pt Patch
			var pts []Point
			var cols []Color
			nNew, cNew := np, 4
			if f != 0 {
				nNew, cNew = np-4, 2
			}
			for i := 0; i < nNew; i++ {
				x := readNum(0, bpc)
				y := readNum(1, bpc)
				pts = append(pts, Point{x, y})
			}
			for i := 0; i < cNew; i++ {
				cols = append(cols, readColor())
			}
			if eof {
				break
			}
			if f != 0 && prev == nil {
				break
			}
			// Boundary order: the 12 points go around the patch.
			if f == 0 {
				copy(pt.P[:np], pts)
				copy(pt.C[:], cols)
			} else {
				// Shared edge of the previous patch.
				var e [4]Point
				var c [2]Color
				p := prev.P
				switch f {
				case 1:
					e = [4]Point{p[3], p[4], p[5], p[6]}
					c = [2]Color{prev.C[1], prev.C[2]}
				case 2:
					e = [4]Point{p[6], p[7], p[8], p[9]}
					c = [2]Color{prev.C[2], prev.C[3]}
				case 3:
					e = [4]Point{p[9], p[10], p[11], p[0]}
					c = [2]Color{prev.C[3], prev.C[0]}
				}
				pt.P[0], pt.P[1], pt.P[2], pt.P[3] = e[0], e[1], e[2], e[3]
				copy(pt.P[4:np], pts)
				pt.C[0], pt.C[1], pt.C[2], pt.C[3] = c[0], c[1], cols[0], cols[1]
			}
			sh.Patches = append(sh.Patches, pt)
			prev = &sh.Patches[len(sh.Patches)-1]
			if br != nil {
				br.align()
			}
		}
	}
}

// Pattern is a tiling (type 1) or shading (type 2) pattern.
type Pattern struct {
	Type int
	// Matrix maps pattern space to the default user space.
	Matrix    Matrix
	BBox      [4]float64
	XStep     float64
	YStep     float64
	PaintType int
	Ops       []Op // tiling: the cell, in pattern space
	Shading   *Shading
}

func (in *Interp) makePattern(d *Dict, m Matrix) *Dict {
	c := d.Copy()
	p := &Pattern{Matrix: m.Mul(in.gs.CTM)}
	if v, ok := d.GetS("PatternType"); ok {
		p.Type = intOf(v)
	}
	switch p.Type {
	case 1:
		if v, ok := d.GetS("BBox"); ok {
			n := numsOf(v)
			if len(n) == 4 {
				p.BBox = [4]float64{n[0], n[1], n[2], n[3]}
			}
		}
		if v, ok := d.GetS("XStep"); ok {
			p.XStep = v.Num()
		}
		if v, ok := d.GetS("YStep"); ok {
			p.YStep = v.Num()
		}
		p.PaintType = 1
		if v, ok := d.GetS("PaintType"); ok {
			p.PaintType = intOf(v)
		}
		if proc, ok := d.GetS("PaintProc"); ok {
			// Run the paint procedure in pattern space, into a list of
			// its own.
			saved, savedStack, savedList := in.gs, in.gstack, in.list
			in.gs = newGState()
			in.gs.Font = saved.Font
			in.gs.noColor = p.PaintType == 2
			in.gstack = nil
			in.list = &List{box: p.BBox}
			func() {
				defer func() {
					in.gs, in.gstack = saved, savedStack
					p.Ops = in.list.Ops
					in.list = savedList
				}()
				in.push(DictObj(c))
				st, err := in.stopped(func() { in.execObj(proc) })
				if st && err != nil {
					in.warn("ps: error in PaintProc: %s", err.Error())
				}
			}()
		}
	case 2:
		if v, ok := d.GetS("Shading"); ok && v.Kind == KDict {
			p.Shading = in.parseShading(v.DictV())
		}
	}
	c.PutS("Implementation", internal(p))
	return c
}

func (in *Interp) setPattern(o Object, cs *colorSpace, comps []float64) {
	if in.gs.noColor {
		return
	}
	var p *Pattern
	if o.Kind == KDict {
		if v, ok := o.DictV().GetS("Implementation"); ok {
			p, _ = v.Ref.(*Pattern)
		}
	}
	in.gs.Comps = comps
	paint := Paint{Pattern: p, Color: Color{N: 1}}
	if cs.base != nil && comps != nil {
		paint.Color = in.toDevice(cs.base, comps)
	}
	in.gs.Paint = paint
}

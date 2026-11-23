package ps

import (
	"math"
)

// Matrix is a PostScript matrix [a b c d e f].
type Matrix [6]float64

var identity = Matrix{1, 0, 0, 1, 0, 0}

// Mul returns m × n (m applied first).
func (m Matrix) Mul(n Matrix) Matrix {
	return Matrix{
		m[0]*n[0] + m[1]*n[2], m[0]*n[1] + m[1]*n[3],
		m[2]*n[0] + m[3]*n[2], m[2]*n[1] + m[3]*n[3],
		m[4]*n[0] + m[5]*n[2] + n[4], m[4]*n[1] + m[5]*n[3] + n[5],
	}
}

// Apply transforms a point.
func (m Matrix) Apply(x, y float64) (float64, float64) {
	return m[0]*x + m[2]*y + m[4], m[1]*x + m[3]*y + m[5]
}

// ApplyD transforms a distance.
func (m Matrix) ApplyD(x, y float64) (float64, float64) {
	return m[0]*x + m[2]*y, m[1]*x + m[3]*y
}

// Invert returns the inverse matrix.
func (m Matrix) Invert() (Matrix, bool) {
	det := m[0]*m[3] - m[1]*m[2]
	if det == 0 || math.IsNaN(det) {
		return Matrix{}, false
	}
	return Matrix{
		m[3] / det, -m[1] / det, -m[2] / det, m[0] / det,
		(m[2]*m[5] - m[3]*m[4]) / det, (m[1]*m[4] - m[0]*m[5]) / det,
	}, true
}

// Point is a point.
type Point struct{ X, Y float64 }

// Seg is a path segment: 'M' move, 'L' line, 'C' curve (three points),
// 'Z' close.
type Seg struct {
	Op byte
	P  [3]Point
}

// Path is a path.
type Path []Seg

// Transform returns the path transformed by m.
func (p Path) Transform(m Matrix) Path {
	out := make(Path, len(p))
	for i, s := range p {
		out[i].Op = s.Op
		for k := range s.P {
			out[i].P[k].X, out[i].P[k].Y = m.Apply(s.P[k].X, s.P[k].Y)
		}
	}
	return out
}

// BBox returns the bounding box of the points (control points included).
func (p Path) BBox() (x0, y0, x1, y1 float64, ok bool) {
	x0, y0 = math.Inf(1), math.Inf(1)
	x1, y1 = math.Inf(-1), math.Inf(-1)
	for _, s := range p {
		n := 0
		switch s.Op {
		case 'M', 'L':
			n = 1
		case 'C':
			n = 3
		}
		for k := 0; k < n; k++ {
			x0, y0 = math.Min(x0, s.P[k].X), math.Min(y0, s.P[k].Y)
			x1, y1 = math.Max(x1, s.P[k].X), math.Max(y1, s.P[k].Y)
			ok = true
		}
	}
	return
}

// Color is a device color.
type Color struct {
	N int // 1 gray, 3 RGB, 4 CMYK
	V [4]float64
}

// Paint is a color or a pattern.
type Paint struct {
	Color   Color
	Pattern *Pattern
}

// Clip is a clipping path, intersected with its parent.
type Clip struct {
	Parent  *Clip
	Path    Path // device space
	EvenOdd bool
}

// StrokeStyle holds the stroke parameters (in user space).
type StrokeStyle struct {
	Width, Miter float64
	Cap, Join    int
	Dash         []float64
	Phase        float64
}

// OpKind is the kind of a display list item.
type OpKind uint8

const (
	OpFill OpKind = iota
	OpStroke
	OpImage
	OpShade
)

// Op is a painting operation.
type Op struct {
	Kind    OpKind
	Path    Path // device space
	EvenOdd bool
	// CTM: the user space of strokes, the unit square of images (row 0
	// at the top, like PDF), the shading space of shadings.
	CTM     Matrix
	Paint   Paint
	Stroke  StrokeStyle
	Clip    *Clip
	Image   *Image
	Shading *Shading
}

// List is a display list.
type List struct {
	Ops       []Op
	showpages int
	box       [4]float64 // the page (bounding box), for clippath
}

// GState is the graphics state.
type GState struct {
	CTM    Matrix
	Path   Path
	CP     Point // current point (device space)
	HasCP  bool
	Clip   *Clip
	Space  *colorSpace
	Comps  []float64
	Paint  Paint
	Stroke StrokeStyle
	Font   Object
	// noColor: in a Type 3 glyph with setcachedevice, color operators
	// are ignored.
	noColor bool
}

func newGState() *GState {
	g := &GState{}
	g.init()
	return g
}

func (g *GState) init() {
	g.CTM = identity
	g.Path = nil
	g.HasCP = false
	g.Clip = nil
	g.Space = deviceGray
	g.Comps = []float64{0}
	g.Paint = Paint{Color: Color{N: 1}}
	g.Stroke = StrokeStyle{Width: 1, Miter: 10}
}

func (g *GState) clone() *GState {
	c := *g
	c.Path = append(Path(nil), g.Path...)
	c.Comps = append([]float64(nil), g.Comps...)
	c.Stroke.Dash = append([]float64(nil), g.Stroke.Dash...)
	return &c
}

func (in *Interp) gsave() {
	if len(in.gstack) > 1000 {
		panic(psError{"limitcheck", "gsave"})
	}
	in.gstack = append(in.gstack, in.gs.clone())
}

func (in *Interp) grestore() {
	if len(in.gstack) == 0 {
		return
	}
	in.gs = in.gstack[len(in.gstack)-1]
	in.gstack = in.gstack[:len(in.gstack)-1]
}

// ---------------------------------------------------------------- paths

func (in *Interp) userPoint() (float64, float64) {
	if !in.gs.HasCP {
		panic(psError{"nocurrentpoint", ""})
	}
	inv, ok := in.gs.CTM.Invert()
	if !ok {
		panic(psError{"undefinedresult", ""})
	}
	return inv.Apply(in.gs.CP.X, in.gs.CP.Y)
}

func (in *Interp) moveTo(x, y float64) {
	g := in.gs
	dx, dy := g.CTM.Apply(x, y)
	if n := len(g.Path); n > 0 && g.Path[n-1].Op == 'M' {
		g.Path = g.Path[:n-1]
	}
	g.Path = append(g.Path, Seg{Op: 'M', P: [3]Point{{dx, dy}}})
	g.CP = Point{dx, dy}
	g.HasCP = true
}

func (in *Interp) lineTo(x, y float64) {
	g := in.gs
	if !g.HasCP {
		panic(psError{"nocurrentpoint", "lineto"})
	}
	dx, dy := g.CTM.Apply(x, y)
	g.Path = append(g.Path, Seg{Op: 'L', P: [3]Point{{dx, dy}}})
	g.CP = Point{dx, dy}
}

func (in *Interp) curveTo(x1, y1, x2, y2, x3, y3 float64) {
	g := in.gs
	if !g.HasCP {
		panic(psError{"nocurrentpoint", "curveto"})
	}
	ax, ay := g.CTM.Apply(x1, y1)
	bx, by := g.CTM.Apply(x2, y2)
	cx, cy := g.CTM.Apply(x3, y3)
	g.Path = append(g.Path, Seg{Op: 'C', P: [3]Point{{ax, ay}, {bx, by}, {cx, cy}}})
	g.CP = Point{cx, cy}
}

func (in *Interp) closePath() {
	g := in.gs
	if !g.HasCP || len(g.Path) == 0 {
		return
	}
	if g.Path[len(g.Path)-1].Op == 'Z' {
		return
	}
	// The current point goes back to the start of the subpath.
	for i := len(g.Path) - 1; i >= 0; i-- {
		if g.Path[i].Op == 'M' {
			g.CP = g.Path[i].P[0]
			break
		}
	}
	g.Path = append(g.Path, Seg{Op: 'Z'})
}

func (in *Interp) newPath() {
	in.gs.Path = nil
	in.gs.HasCP = false
}

// arc appends an arc (counterclockwise if !neg), with a line from the
// current point.
func (in *Interp) arc(x, y, r, a1, a2 float64, neg bool) {
	if neg {
		for a2 > a1 {
			a2 -= 360
		}
	} else {
		for a2 < a1 {
			a2 += 360
		}
	}
	t1 := a1 * math.Pi / 180
	t2 := a2 * math.Pi / 180
	sx, sy := x+r*math.Cos(t1), y+r*math.Sin(t1)
	if in.gs.HasCP {
		in.lineTo(sx, sy)
	} else {
		in.moveTo(sx, sy)
	}
	n := int(math.Ceil(math.Abs(t2-t1) / (math.Pi / 2)))
	if n == 0 {
		return
	}
	d := (t2 - t1) / float64(n)
	k := 4.0 / 3 * math.Tan(d/4)
	for i := 0; i < n; i++ {
		a := t1 + float64(i)*d
		b := a + d
		ca, sa := math.Cos(a), math.Sin(a)
		cb, sb := math.Cos(b), math.Sin(b)
		in.curveTo(x+r*(ca-k*sa), y+r*(sa+k*ca), x+r*(cb+k*sb), y+r*(sb-k*cb), x+r*cb, y+r*sb)
	}
}

// arct appends the arc tangent to two lines, returning the tangent points.
func (in *Interp) arct(x1, y1, x2, y2, r float64) (float64, float64, float64, float64) {
	x0, y0 := in.userPoint()
	// Unit vectors from the corner.
	ux, uy := x0-x1, y0-y1
	vx, vy := x2-x1, y2-y1
	lu, lv := math.Hypot(ux, uy), math.Hypot(vx, vy)
	if lu == 0 || lv == 0 || r == 0 {
		in.lineTo(x1, y1)
		return x1, y1, x1, y1
	}
	ux, uy, vx, vy = ux/lu, uy/lu, vx/lv, vy/lv
	cosT := ux*vx + uy*vy
	theta := math.Acos(math.Max(-1, math.Min(1, cosT)))
	if math.Abs(math.Sin(theta)) < 1e-9 {
		in.lineTo(x1, y1)
		return x1, y1, x1, y1
	}
	dist := r / math.Tan(theta/2)
	t1x, t1y := x1+ux*dist, y1+uy*dist
	t2x, t2y := x1+vx*dist, y1+vy*dist
	// Center along the bisector.
	bx, by := ux+vx, uy+vy
	bl := math.Hypot(bx, by)
	cd := r / math.Sin(theta/2)
	cx, cy := x1+bx/bl*cd, y1+by/bl*cd
	a1 := math.Atan2(t1y-cy, t1x-cx) * 180 / math.Pi
	a2 := math.Atan2(t2y-cy, t2x-cx) * 180 / math.Pi
	cross := ux*vy - uy*vx
	in.lineTo(t1x, t1y)
	// Draw the short way around.
	if cross > 0 {
		for a2 > a1 {
			a2 -= 360
		}
		if a1-a2 > 180 {
			a2 += 360
		}
		in.arcSegment(cx, cy, r, a1, a2)
	} else {
		for a2 < a1 {
			a2 += 360
		}
		if a2-a1 > 180 {
			a2 -= 360
		}
		in.arcSegment(cx, cy, r, a1, a2)
	}
	return t1x, t1y, t2x, t2y
}

func (in *Interp) arcSegment(x, y, r, a1, a2 float64) {
	t1, t2 := a1*math.Pi/180, a2*math.Pi/180
	n := int(math.Ceil(math.Abs(t2-t1) / (math.Pi / 2)))
	if n == 0 {
		return
	}
	d := (t2 - t1) / float64(n)
	k := 4.0 / 3 * math.Tan(d/4)
	for i := 0; i < n; i++ {
		a := t1 + float64(i)*d
		b := a + d
		ca, sa := math.Cos(a), math.Sin(a)
		cb, sb := math.Cos(b), math.Sin(b)
		in.curveTo(x+r*(ca-k*sa), y+r*(sa+k*ca), x+r*(cb+k*sb), y+r*(sb-k*cb), x+r*cb, y+r*sb)
	}
}

// ---------------------------------------------------------------- painting

func (in *Interp) emit(op Op) {
	if b := in.currentBuild(); b != nil && b.capture != nil {
		// charpath of a Type 3 glyph: paths are added to the path.
		if op.Kind == OpFill || op.Kind == OpStroke {
			*b.capture = append(*b.capture, op.Path...)
		}
		return
	}
	op.Clip = in.gs.Clip
	in.list.Ops = append(in.list.Ops, op)
}

func (in *Interp) fill(evenOdd bool) {
	g := in.gs
	if len(g.Path) > 0 {
		in.emit(Op{Kind: OpFill, Path: g.Path, EvenOdd: evenOdd, Paint: g.Paint, CTM: g.CTM})
	}
	in.newPath()
}

func (in *Interp) stroke() {
	g := in.gs
	if len(g.Path) > 0 {
		st := g.Stroke
		st.Dash = append([]float64(nil), st.Dash...)
		in.emit(Op{Kind: OpStroke, Path: g.Path, Paint: g.Paint, CTM: g.CTM, Stroke: st})
	}
	in.newPath()
}

func (in *Interp) clip(evenOdd bool) {
	g := in.gs
	p := g.Path
	if len(p) == 0 {
		p = Path{} // empty clip: nothing is painted
	}
	g.Clip = &Clip{Parent: g.Clip, Path: append(Path(nil), p...), EvenOdd: evenOdd}
}

// reversePath reverses the direction of each subpath.
func reversePath(p Path) Path {
	var out Path
	var sub Path
	flush := func() {
		if len(sub) == 0 {
			return
		}
		closed := sub[len(sub)-1].Op == 'Z'
		if closed {
			sub = sub[:len(sub)-1]
		}
		// Points of the subpath, then rebuild backwards.
		start := sub[0].P[0]
		end := start
		type seg struct {
			op   byte
			from Point
			p    [3]Point
		}
		var segs []seg
		for _, s := range sub[1:] {
			switch s.Op {
			case 'L':
				segs = append(segs, seg{'L', end, s.P})
				end = s.P[0]
			case 'C':
				segs = append(segs, seg{'C', end, s.P})
				end = s.P[2]
			}
		}
		out = append(out, Seg{Op: 'M', P: [3]Point{end}})
		for i := len(segs) - 1; i >= 0; i-- {
			s := segs[i]
			if s.op == 'L' {
				out = append(out, Seg{Op: 'L', P: [3]Point{s.from}})
			} else {
				out = append(out, Seg{Op: 'C', P: [3]Point{s.p[1], s.p[0], s.from}})
			}
		}
		if closed {
			out = append(out, Seg{Op: 'Z'})
		}
		sub = nil
	}
	for _, s := range p {
		if s.Op == 'M' {
			flush()
		}
		sub = append(sub, s)
	}
	flush()
	return out
}

// flatten replaces curves by lines.
func flatten(p Path) Path {
	var out Path
	var cur Point
	for _, s := range p {
		switch s.Op {
		case 'M', 'L':
			cur = s.P[0]
			out = append(out, s)
		case 'C':
			n := 8
			for i := 1; i <= n; i++ {
				t := float64(i) / float64(n)
				u := 1 - t
				x := u*u*u*cur.X + 3*u*u*t*s.P[0].X + 3*u*t*t*s.P[1].X + t*t*t*s.P[2].X
				y := u*u*u*cur.Y + 3*u*u*t*s.P[0].Y + 3*u*t*t*s.P[1].Y + t*t*t*s.P[2].Y
				out = append(out, Seg{Op: 'L', P: [3]Point{{x, y}}})
			}
			cur = s.P[2]
		default:
			out = append(out, s)
		}
	}
	return out
}

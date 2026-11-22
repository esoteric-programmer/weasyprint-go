package draw

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/esoteric-programmer/weasyprint-go/internal/html"
	"github.com/esoteric-programmer/weasyprint-go/internal/images"
	"github.com/esoteric-programmer/weasyprint-go/internal/pdf"
)

// SVG paint servers and effects referenced by URL: patterns, masks,
// filters (feOffset and feBlend, like WeasyPrint) and markers.

// styleOf returns the style of an element computed from its ancestors,
// used for the content of referenced elements (patterns, masks, markers).
func (sr *svgRenderer) styleOf(n *html.Node) svgStyle {
	var chain []*html.Node
	for e := n; e != nil && e.Type == html.ElementNode; e = e.Parent {
		chain = append(chain, e)
		if e == sr.svg.Root {
			break
		}
	}
	st := sr.initial
	for i := len(chain) - 1; i >= 0; i-- {
		st = sr.style(chain[i], st)
	}
	return st
}

// defChildren returns the element following href links whose children
// define the content of a pattern.
func (sr *svgRenderer) defChildren(n *html.Node) *html.Node {
	for i := 0; i < 5 && n != nil; i++ {
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if c.Type == html.ElementNode {
				return n
			}
		}
		href := images.Attr(n, "href")
		if href == "" {
			href = images.Attr(n, "xlink:href")
		}
		if href == "" {
			break
		}
		n = sr.ref(href)
	}
	return nil
}

// bbox returns the bounding box (x0, y0, x1, y1) of an element in its user
// space, ignoring strokes and text.
func (sr *svgRenderer) bbox(n *html.Node, st svgStyle) ([4]float64, bool) {
	if sr.depth > 64 {
		return [4]float64{}, false
	}
	tag := strings.ToLower(n.Tag)
	switch tag {
	case "g", "a", "switch", "svg", "use":
		var out [4]float64
		ok := false
		add := func(b [4]float64, m []float64) {
			if m != nil {
				var pts [8]float64
				pts = [8]float64{b[0], b[1], b[2], b[1], b[0], b[3], b[2], b[3]}
				b = [4]float64{math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)}
				for i := 0; i < 8; i += 2 {
					x := m[0]*pts[i] + m[2]*pts[i+1] + m[4]
					y := m[1]*pts[i] + m[3]*pts[i+1] + m[5]
					b = [4]float64{math.Min(b[0], x), math.Min(b[1], y), math.Max(b[2], x), math.Max(b[3], y)}
				}
			}
			if !ok {
				out, ok = b, true
				return
			}
			out = [4]float64{math.Min(out[0], b[0]), math.Min(out[1], b[1]), math.Max(out[2], b[2]), math.Max(out[3], b[3])}
		}
		sr.depth++
		defer func() { sr.depth-- }()
		if tag == "use" {
			href := images.Attr(n, "href")
			if href == "" {
				href = images.Attr(n, "xlink:href")
			}
			if t := sr.ref(href); t != nil {
				if b, has := sr.bbox(t, sr.style(t, st)); has {
					x, y := sr.attrLen(n, st, "x", sr.vw), sr.attrLen(n, st, "y", sr.vh)
					add(b, []float64{1, 0, 0, 1, x, y})
				}
			}
			return out, ok
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if c.Type != html.ElementNode {
				continue
			}
			cst := sr.style(c, st)
			if cst["display"] == "none" {
				continue
			}
			if b, has := sr.bbox(c, cst); has {
				var m []float64
				if t := images.Attr(c, "transform"); t != "" {
					m = parseTransform(t)
				}
				add(b, m)
			}
		}
		return out, ok
	case "image":
		x, y := sr.attrLen(n, st, "x", sr.vw), sr.attrLen(n, st, "y", sr.vh)
		w, h := sr.attrLen(n, st, "width", sr.vw), sr.attrLen(n, st, "height", sr.vh)
		return [4]float64{x, y, x + w, y + h}, w > 0 && h > 0
	}
	if segs := sr.shape(n, st); len(segs) > 0 {
		return pathBBox(segs), true
	}
	return [4]float64{}, false
}

// fraction parses an objectBoundingBox value (number or percentage).
func fraction(s string, def float64) float64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return def
	}
	if strings.HasSuffix(s, "%") {
		if f, err := strconv.ParseFloat(strings.TrimSuffix(s, "%"), 64); err == nil {
			return f / 100
		}
		return def
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return f
	}
	return def
}

// region returns the rectangle of a mask or pattern, given its units.
func (sr *svgRenderer) region(attr func(string) string, units string, st svgStyle, bb [4]float64, defs [4]string) (x, y, w, h float64) {
	val := func(name string, i int, ref float64) float64 {
		v := attr(name)
		if v == "" {
			v = defs[i]
		}
		if units == "userSpaceOnUse" {
			return sr.length(st, v, ref)
		}
		f := fraction(v, 0)
		return f * ref
	}
	if units == "userSpaceOnUse" {
		return val("x", 0, sr.vw), val("y", 1, sr.vh), val("width", 2, sr.vw), val("height", 3, sr.vh)
	}
	bw, bh := bb[2]-bb[0], bb[3]-bb[1]
	return bb[0] + val("x", 0, bw), bb[1] + val("y", 1, bh), val("width", 2, bw), val("height", 3, bh)
}

// popContent ends a content stream started with pushGroup and returns it.
func (cv *canvas) popContent() *pdf.Content {
	c := cv.c
	cv.c = cv.stack[len(cv.stack)-1]
	cv.stack = cv.stack[:len(cv.stack)-1]
	return c
}

// applyMask sets a soft mask painting the content of a <mask> element.
func (sr *svgRenderer) applyMask(v string, n *html.Node, st svgStyle) {
	m := sr.ref(v)
	if m == nil || strings.ToLower(m.Tag) != "mask" || sr.depth > 32 {
		return
	}
	attr := func(name string) string { return images.Attr(m, name) }
	units := attr("maskUnits")
	bb, ok := sr.bbox(n, st)
	if units != "userSpaceOnUse" && !ok {
		return
	}
	x, y, w, h := sr.region(attr, units, st, bb, [4]string{"-10%", "-10%", "120%", "120%"})
	cv := sr.cv
	cv.pushGroup()
	if w > 0 && h > 0 {
		cv.c.Rect(x, y, w, h)
		cv.c.Clip()
		if attr("maskContentUnits") == "objectBoundingBox" {
			cv.c.Transform(bb[2]-bb[0], 0, 0, bb[3]-bb[1], bb[0], bb[1])
		}
		mst := sr.styleOf(m)
		sr.depth++
		sr.children(m, mst)
		sr.depth--
	}
	group := cv.popContent()
	group.Dict["Type"] = pdf.Name("XObject")
	group.Dict["Subtype"] = pdf.Name("Form")
	group.Dict["BBox"] = []float64{-1e5, -1e5, 1e5, 1e5}
	group.Dict["Group"] = pdf.Dict{"Type": pdf.Name("Group"), "S": pdf.Name("Transparency"), "CS": pdf.Name("DeviceRGB")}
	group.Dict["Resources"] = cv.resources()
	mode := pdf.Name("Luminosity")
	if sr.styleOf(m)["mask-type"] == "alpha" {
		mode = pdf.Name("Alpha")
	}
	state := pdf.Dict{"Type": pdf.Name("ExtGState"), "SMask": pdf.Dict{"Type": pdf.Name("Mask"), "S": mode, "G": cv.r.doc.Add(group)}, "ca": 1, "AIS": false}
	name := fmt.Sprintf("SM%d", cv.r.nextID())
	cv.states[name] = cv.r.doc.Add(state)
	cv.c.SetState(name)
}

// applyFilter applies the feOffset and feBlend primitives of a <filter>
// element, the ones WeasyPrint supports.
func (sr *svgRenderer) applyFilter(v string, n *html.Node, st svgStyle) {
	f := sr.ref(v)
	if f == nil || strings.ToLower(f.Tag) != "filter" {
		return
	}
	for c := f.FirstChild; c != nil; c = c.NextSibling {
		if c.Type != html.ElementNode {
			continue
		}
		switch strings.ToLower(c.Tag) {
		case "feoffset":
			var dx, dy float64
			if images.Attr(f, "primitiveUnits") == "objectBoundingBox" {
				if bb, ok := sr.bbox(n, st); ok {
					dx = fraction(images.Attr(c, "dx"), 0) * (bb[2] - bb[0])
					dy = fraction(images.Attr(c, "dy"), 0) * (bb[3] - bb[1])
				}
			} else {
				dx = sr.length(st, images.Attr(c, "dx"), sr.vw)
				dy = sr.length(st, images.Attr(c, "dy"), sr.vh)
			}
			sr.cv.c.Transform(1, 0, 0, 1, dx, dy)
		case "feblend":
			mode := images.Attr(c, "mode")
			if mode == "" || mode == "normal" {
				continue
			}
			var b strings.Builder
			for _, part := range strings.Split(mode, "-") {
				if part != "" {
					b.WriteString(strings.ToUpper(part[:1]) + part[1:])
				}
			}
			name := fmt.Sprintf("BM%d", sr.cv.r.nextID())
			sr.cv.states[name] = pdf.Dict{"Type": pdf.Name("ExtGState"), "BM": pdf.Name(b.String())}
			sr.cv.c.SetState(name)
		}
	}
}

// paintPattern fills or strokes a path with a <pattern> element, as a PDF
// tiling pattern.
func (sr *svgRenderer) paintPattern(p *html.Node, segs []images.PathSeg, st svgStyle, stroke bool) {
	if sr.depth > 32 {
		return
	}
	attr := func(name string) string { return sr.gradAttr(p, name) }
	bb := pathBBox(segs)
	bw, bh := bb[2]-bb[0], bb[3]-bb[1]
	units := attr("patternUnits")
	if units != "userSpaceOnUse" && (bw <= 0 || bh <= 0) {
		return
	}
	x, y, w, h := sr.region(attr, units, st, bb, [4]string{"0", "0", "0", "0"})
	if w <= 0 || h <= 0 {
		return
	}
	cv := sr.cv
	// Tile content, in pattern space (the tile is at the origin).
	cv.pushGroup()
	savedVW, savedVH := sr.vw, sr.vh
	if vb := images.ParseNumbers(attr("viewBox")); len(vb) == 4 && vb[2] > 0 && vb[3] > 0 {
		sr.viewBoxTransform([4]float64{vb[0], vb[1], vb[2], vb[3]}, w, h, attr("preserveAspectRatio"))
		sr.vw, sr.vh = vb[2], vb[3]
	} else if attr("patternContentUnits") == "objectBoundingBox" {
		// Like browsers, the content is placed relative to each tile.
		cv.c.Transform(bw, 0, 0, bh, 0, 0)
	}
	if content := sr.defChildren(p); content != nil {
		sr.depth++
		sr.children(content, sr.styleOf(content))
		sr.depth--
	}
	sr.vw, sr.vh = savedVW, savedVH
	tile := cv.popContent()
	m := []float64{1, 0, 0, 1, x, y}
	if t := attr("patternTransform"); t != "" {
		pt := parseTransform(t)
		m = []float64{
			pt[0], pt[1], pt[2], pt[3],
			pt[0]*x + pt[2]*y + pt[4], pt[1]*x + pt[3]*y + pt[5],
		}
	}
	tile.Dict["Type"] = pdf.Name("Pattern")
	tile.Dict["PatternType"] = 1
	tile.Dict["PaintType"] = 1
	tile.Dict["TilingType"] = 1
	tile.Dict["BBox"] = []float64{0, 0, w, h}
	tile.Dict["XStep"] = w
	tile.Dict["YStep"] = h
	tile.Dict["Matrix"] = m
	tile.Dict["Resources"] = cv.resources()
	pname := fmt.Sprintf("P%d", cv.r.nextID())
	cv.patterns[pname] = cv.r.doc.Add(tile)
	// The pattern matrix maps pattern space to the space the form using it
	// is painted in: paint in a form to use the current user space.
	cv.pushGroup()
	g := cv.c
	opacity := st.num("fill-opacity", 1)
	if stroke {
		opacity = st.num("stroke-opacity", 1)
		g.Op("/Pattern CS /" + pname + " SCN")
		sr.setStrokeParams(st)
		addPath(g, segs)
		g.Stroke()
	} else {
		g.Op("/Pattern cs /" + pname + " scn")
		addPath(g, segs)
		if st["fill-rule"] == "evenodd" {
			g.FillEvenOdd()
		} else {
			g.Fill()
		}
	}
	name := cv.popGroup([4]float64{-1e5, -1e5, 1e5, 1e5})
	c := cv.c
	c.Save()
	if opacity < 1 {
		c.SetState(cv.alphaState(opacity, opacity))
	}
	c.DrawXObject(name)
	c.Restore()
}

// markerVertex is a vertex of a path, with the directions of the segments
// arriving and leaving.
type markerVertex struct {
	x, y          float64
	in, out       float64
	hasIn, hasOut bool
}

// pathVertices returns the vertices of a path for markers.
func pathVertices(segs []images.PathSeg) []markerVertex {
	var vs []markerVertex
	var cx, cy, sx, sy float64
	dir := func(x0, y0, x1, y1 float64) (float64, bool) {
		if x0 == x1 && y0 == y1 {
			return 0, false
		}
		return math.Atan2(y1-y0, x1-x0), true
	}
	setOut := func(a float64, ok bool) {
		if len(vs) > 0 && ok && !vs[len(vs)-1].hasOut {
			vs[len(vs)-1].out, vs[len(vs)-1].hasOut = a, true
		}
	}
	for _, s := range segs {
		switch s.Op {
		case 'M':
			cx, cy = s.Pts[0], s.Pts[1]
			sx, sy = cx, cy
			vs = append(vs, markerVertex{x: cx, y: cy})
		case 'L':
			x, y := s.Pts[0], s.Pts[1]
			a, ok := dir(cx, cy, x, y)
			setOut(a, ok)
			vs = append(vs, markerVertex{x: x, y: y, in: a, hasIn: ok})
			cx, cy = x, y
		case 'C':
			p := s.Pts
			a0, ok0 := dir(cx, cy, p[0], p[1])
			if !ok0 {
				a0, ok0 = dir(cx, cy, p[2], p[3])
			}
			if !ok0 {
				a0, ok0 = dir(cx, cy, p[4], p[5])
			}
			a1, ok1 := dir(p[2], p[3], p[4], p[5])
			if !ok1 {
				a1, ok1 = dir(p[0], p[1], p[4], p[5])
			}
			if !ok1 {
				a1, ok1 = dir(cx, cy, p[4], p[5])
			}
			if s.Join && len(vs) > 0 {
				// Continuation of an arc: move the vertex ending it.
				v := &vs[len(vs)-1]
				v.x, v.y, v.in, v.hasIn = p[4], p[5], a1, ok1
			} else {
				setOut(a0, ok0)
				vs = append(vs, markerVertex{x: p[4], y: p[5], in: a1, hasIn: ok1})
			}
			cx, cy = p[4], p[5]
		case 'Z':
			a, ok := dir(cx, cy, sx, sy)
			if ok {
				setOut(a, ok)
				vs = append(vs, markerVertex{x: sx, y: sy, in: a, hasIn: ok})
			}
			cx, cy = sx, sy
		}
	}
	return vs
}

// drawMarkers draws the markers of a path.
func (sr *svgRenderer) drawMarkers(segs []images.PathSeg, st svgStyle) {
	refs := [3]string{st["marker-start"], st["marker-mid"], st["marker-end"]}
	if all := st["marker"]; all != "" {
		for i := range refs {
			if refs[i] == "" {
				refs[i] = all
			}
		}
	}
	var markers [3]*html.Node
	any := false
	for i, r := range refs {
		if strings.HasPrefix(r, "url(") {
			if m := sr.ref(r); m != nil && strings.ToLower(m.Tag) == "marker" {
				markers[i], any = m, true
			}
		}
	}
	if !any || sr.depth > 32 {
		return
	}
	vs := pathVertices(segs)
	for i, v := range vs {
		kind := 1
		switch i {
		case 0:
			kind = 0
		case len(vs) - 1:
			kind = 2
		}
		m := markers[kind]
		if m == nil {
			continue
		}
		var angle float64
		switch {
		case kind == 0 && v.hasOut:
			angle = v.out
		case kind == 2 && v.hasIn:
			angle = v.in
		case v.hasIn && v.hasOut:
			// Bisector of the incoming and outgoing directions.
			angle = math.Atan2(math.Sin(v.in)+math.Sin(v.out), math.Cos(v.in)+math.Cos(v.out))
		case v.hasIn:
			angle = v.in
		case v.hasOut:
			angle = v.out
		}
		sr.drawMarker(m, v.x, v.y, angle, kind == 0, st)
	}
}

// drawMarker draws a <marker> element at a vertex.
func (sr *svgRenderer) drawMarker(m *html.Node, x, y, angle float64, start bool, st svgStyle) {
	mst := sr.styleOf(m)
	attr := func(name string) string { return images.Attr(m, name) }
	mw, mh := 3.0, 3.0
	if v := attr("markerWidth"); v != "" {
		mw = sr.length(mst, v, sr.vw)
	}
	if v := attr("markerHeight"); v != "" {
		mh = sr.length(mst, v, sr.vh)
	}
	if mw <= 0 || mh <= 0 {
		return
	}
	switch o := strings.TrimSpace(attr("orient")); o {
	case "auto":
	case "auto-start-reverse":
		if start {
			angle += math.Pi
		}
	default:
		o = strings.TrimSuffix(o, "deg")
		a, _ := strconv.ParseFloat(o, 64)
		angle = a * math.Pi / 180
	}
	scale := 1.0
	if attr("markerUnits") != "userSpaceOnUse" {
		scale = sr.strokeWidth(st)
	}
	vbm := []float64{1, 0, 0, 1, 0, 0}
	vw, vh := mw, mh
	if vb := images.ParseNumbers(attr("viewBox")); len(vb) == 4 && vb[2] > 0 && vb[3] > 0 {
		vbm = viewBoxMatrix([4]float64{vb[0], vb[1], vb[2], vb[3]}, mw, mh, attr("preserveAspectRatio"))
		vw, vh = vb[2], vb[3]
	}
	rx, ry := sr.length(mst, attr("refX"), vw), sr.length(mst, attr("refY"), vh)
	rx, ry = vbm[0]*rx+vbm[4], vbm[3]*ry+vbm[5]
	c := sr.cv.c
	c.Save()
	defer c.Restore()
	cos, sin := math.Cos(angle), math.Sin(angle)
	c.Transform(scale*cos, scale*sin, -scale*sin, scale*cos, x, y)
	c.Transform(1, 0, 0, 1, -rx, -ry)
	if ov := mst["overflow"]; ov != "visible" && ov != "auto" {
		c.Rect(0, 0, mw, mh)
		c.Clip()
	}
	c.Transform(vbm[0], vbm[1], vbm[2], vbm[3], vbm[4], vbm[5])
	saved := [2]float64{sr.vw, sr.vh}
	sr.vw, sr.vh = vw, vh
	sr.depth++
	sr.children(m, mst)
	sr.depth--
	sr.vw, sr.vh = saved[0], saved[1]
}

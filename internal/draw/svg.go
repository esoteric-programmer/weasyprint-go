package draw

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/esoteric-programmer/weasyprint-go/internal/css"
	"github.com/esoteric-programmer/weasyprint-go/internal/fetch"
	"github.com/esoteric-programmer/weasyprint-go/internal/html"
	"github.com/esoteric-programmer/weasyprint-go/internal/images"
	"github.com/esoteric-programmer/weasyprint-go/internal/pdf"
	"github.com/esoteric-programmer/weasyprint-go/internal/text"
)

var svgInherited = map[string]bool{
	"fill": true, "fill-opacity": true, "fill-rule": true, "stroke": true,
	"stroke-width": true, "stroke-opacity": true, "stroke-linecap": true,
	"stroke-linejoin": true, "stroke-miterlimit": true, "stroke-dasharray": true,
	"stroke-dashoffset": true, "font-family": true, "font-size": true,
	"font-weight": true, "font-style": true, "text-anchor": true, "color": true,
	"visibility": true, "letter-spacing": true, "word-spacing": true,
	"dominant-baseline": true, "font": true, "text-decoration": true, "clip-rule": true,
}

var svgProps = map[string]bool{
	"fill": true, "fill-opacity": true, "fill-rule": true, "stroke": true,
	"stroke-width": true, "stroke-opacity": true, "stroke-linecap": true,
	"stroke-linejoin": true, "stroke-miterlimit": true, "stroke-dasharray": true,
	"stroke-dashoffset": true, "font-family": true, "font-size": true,
	"font-weight": true, "font-style": true, "text-anchor": true, "color": true,
	"visibility": true, "display": true, "opacity": true, "stop-color": true,
	"stop-opacity": true, "clip-path": true, "letter-spacing": true,
	"dominant-baseline": true, "transform": false, "clip-rule": true,
}

type svgRenderer struct {
	cv    *canvas
	svg   *images.SVG
	depth int
	vw    float64 // viewport width/height for percentages
	vh    float64
}

type svgStyle map[string]string

func (sr *svgRenderer) style(n *html.Node, parent svgStyle) svgStyle {
	st := svgStyle{}
	for k, v := range parent {
		if svgInherited[k] {
			st[k] = v
		}
	}
	for _, a := range n.Attrs {
		name := strings.ToLower(a.Name)
		if svgProps[name] {
			st[name] = strings.TrimSpace(a.Value)
		}
	}
	// <style> rules, by specificity then order.
	type match struct {
		spec  css.Specificity
		order int
		decls []css.Declaration
	}
	var matches []match
	for _, r := range sr.svg.Rules {
		for _, sel := range r.Selectors {
			if sel.PseudoElement == "" && sel.Match(n) {
				matches = append(matches, match{sel.Specificity, r.Order, r.Decls})
			}
		}
	}
	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].spec != matches[j].spec {
			return matches[i].spec.Less(matches[j].spec)
		}
		return matches[i].order < matches[j].order
	})
	apply := func(decls []css.Declaration) {
		for _, d := range decls {
			v := strings.TrimSpace(css.Serialize(d.Value))
			if d.Name == "font" {
				sr.expandFont(st, v)
				continue
			}
			st[d.Name] = v
		}
	}
	for _, m := range matches {
		apply(m.decls)
	}
	if s, ok := n.Attr("style"); ok {
		decls, _ := css.ParseDeclarations(css.Tokenize(s))
		apply(decls)
	}
	for k, v := range st {
		if v == "inherit" {
			st[k] = parent[k]
		}
	}
	return st
}

func (sr *svgRenderer) expandFont(st svgStyle, v string) {
	parts := strings.Fields(v)
	for i, p := range parts {
		if l, ok := images.ParseLength(strings.Split(p, "/")[0], 0, 16); ok && (strings.ContainsAny(p, "0123456789")) {
			st["font-size"] = fmt.Sprint(l)
			st["font-family"] = strings.Join(parts[i+1:], " ")
			return
		}
		switch p {
		case "bold", "bolder":
			st["font-weight"] = "bold"
		case "italic", "oblique":
			st["font-style"] = "italic"
		}
	}
}

func (st svgStyle) num(name string, def float64) float64 {
	v, ok := st[name]
	if !ok || v == "" {
		return def
	}
	if strings.HasSuffix(v, "%") {
		f, err := strconv.ParseFloat(strings.TrimSuffix(v, "%"), 64)
		if err == nil {
			return f / 100
		}
		return def
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return def
	}
	return f
}

func (sr *svgRenderer) fontSize(st svgStyle) float64 {
	if v, ok := st["font-size"]; ok {
		if l, ok := images.ParseLength(v, 16, 16); ok {
			return l
		}
	}
	return 16
}

func (sr *svgRenderer) length(st svgStyle, v string, ref float64) float64 {
	l, _ := images.ParseLength(v, ref, sr.fontSize(st))
	return l
}

func (sr *svgRenderer) color(st svgStyle, v string) (css.Color, bool) {
	v = strings.TrimSpace(v)
	if v == "" || v == "none" || v == "transparent" {
		return css.Color{}, false
	}
	c, cur, ok := css.ParseColor(v)
	if cur {
		if col, ok := st["color"]; ok && !strings.EqualFold(col, "currentcolor") {
			return sr.color(st, col)
		}
		return css.Color{A: 1}, true
	}
	return c, ok
}

// ---------------------------------------------------------------- entry

func (cv *canvas) drawSVG(s *images.SVG, x, y, w, h float64) {
	sr := &svgRenderer{cv: cv, svg: s}
	cv.c.Save()
	cv.c.Rect(x, y, w, h)
	cv.c.Clip()
	cv.c.Transform(1, 0, 0, 1, x, y)
	sr.vw, sr.vh = w, h
	if s.HasViewBox && s.ViewBox[2] > 0 && s.ViewBox[3] > 0 {
		sr.viewBoxTransform(s.ViewBox, w, h, images.Attr(s.Root, "preserveAspectRatio"))
		sr.vw, sr.vh = s.ViewBox[2], s.ViewBox[3]
	} else if s.Width > 0 && s.Height > 0 {
		cv.c.Transform(w/s.Width, 0, 0, h/s.Height, 0, 0)
		sr.vw, sr.vh = s.Width, s.Height
	}
	root := svgStyle{"fill": "black", "stroke": "none", "font-size": "16", "font-family": "sans-serif"}
	st := sr.style(s.Root, root)
	sr.children(s.Root, st)
	cv.c.Restore()
}

func (sr *svgRenderer) viewBoxTransform(vb [4]float64, w, h float64, par string) {
	par = strings.TrimSpace(par)
	sx, sy := w/vb[2], h/vb[3]
	align := "xmidymid"
	slice := false
	if par != "" {
		f := strings.Fields(strings.ToLower(par))
		align = f[0]
		if len(f) > 1 && f[1] == "slice" {
			slice = true
		}
	}
	tx, ty := -vb[0]*sx, -vb[1]*sy
	if align != "none" {
		s := math.Min(sx, sy)
		if slice {
			s = math.Max(sx, sy)
		}
		sx, sy = s, s
		tx, ty = -vb[0]*s, -vb[1]*s
		ex, ey := w-vb[2]*s, h-vb[3]*s
		if strings.Contains(align, "xmid") {
			tx += ex / 2
		} else if strings.Contains(align, "xmax") {
			tx += ex
		}
		if strings.Contains(align, "ymid") {
			ty += ey / 2
		} else if strings.Contains(align, "ymax") {
			ty += ey
		}
	}
	sr.cv.c.Transform(sx, 0, 0, sy, tx, ty)
}

func (sr *svgRenderer) children(n *html.Node, st svgStyle) {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode {
			sr.node(c, st)
		}
	}
}

// parseTransform parses an SVG transform attribute into a matrix.
func parseTransform(s string) []float64 {
	m := []float64{1, 0, 0, 1, 0, 0}
	mul := func(t []float64) {
		a := m
		m = []float64{
			t[0]*a[0] + t[1]*a[2], t[0]*a[1] + t[1]*a[3],
			t[2]*a[0] + t[3]*a[2], t[2]*a[1] + t[3]*a[3],
			t[4]*a[0] + t[5]*a[2] + a[4], t[4]*a[1] + t[5]*a[3] + a[5],
		}
	}
	for {
		s = strings.TrimLeft(s, " ,\t\n")
		open := strings.IndexByte(s, '(')
		if open < 0 {
			break
		}
		close := strings.IndexByte(s, ')')
		if close < open {
			break
		}
		name := strings.ToLower(strings.TrimSpace(s[:open]))
		args := images.ParseNumbers(s[open+1 : close])
		s = s[close+1:]
		arg := func(i int, d float64) float64 {
			if i < len(args) {
				return args[i]
			}
			return d
		}
		switch name {
		case "matrix":
			if len(args) == 6 {
				mul(args)
			}
		case "translate":
			mul([]float64{1, 0, 0, 1, arg(0, 0), arg(1, 0)})
		case "scale":
			sx := arg(0, 1)
			mul([]float64{sx, 0, 0, arg(1, sx), 0, 0})
		case "rotate":
			a := arg(0, 0) * math.Pi / 180
			cx, cy := arg(1, 0), arg(2, 0)
			mul([]float64{1, 0, 0, 1, cx, cy})
			mul([]float64{math.Cos(a), math.Sin(a), -math.Sin(a), math.Cos(a), 0, 0})
			mul([]float64{1, 0, 0, 1, -cx, -cy})
		case "skewx":
			mul([]float64{1, 0, math.Tan(arg(0, 0) * math.Pi / 180), 1, 0, 0})
		case "skewy":
			mul([]float64{1, math.Tan(arg(0, 0) * math.Pi / 180), 0, 1, 0, 0})
		}
	}
	return m
}

func (sr *svgRenderer) node(n *html.Node, parent svgStyle) {
	tag := strings.ToLower(n.Tag)
	switch tag {
	case "defs", "symbol", "clippath", "lineargradient", "radialgradient", "style", "title",
		"desc", "metadata", "mask", "pattern", "marker", "script", "filter":
		return
	}
	st := sr.style(n, parent)
	if st["display"] == "none" {
		return
	}
	sr.depth++
	defer func() { sr.depth-- }()
	if sr.depth > 64 {
		return
	}
	c := sr.cv.c
	c.Save()
	defer c.Restore()
	if t := images.Attr(n, "transform"); t != "" {
		m := parseTransform(t)
		c.Transform(m[0], m[1], m[2], m[3], m[4], m[5])
	}
	op := st.num("opacity", 1)
	if op <= 0 {
		return
	}
	grouped := op < 1
	if grouped {
		sr.cv.pushGroup()
	}
	if cp := st["clip-path"]; strings.HasPrefix(cp, "url(") {
		sr.applyClip(cp, n, st)
	}
	switch tag {
	case "g", "a", "switch":
		sr.children(n, st)
	case "svg":
		sr.nestedSVG(n, st)
	case "use":
		sr.use(n, st)
	case "image":
		sr.image(n, st)
	case "text":
		sr.text(n, st)
	default:
		if segs := sr.shape(n, st); segs != nil {
			sr.paintPath(segs, st, tag != "line" && tag != "polyline" || true)
		}
	}
	if grouped {
		name := sr.cv.popGroup([4]float64{-1e5, -1e5, 1e5, 1e5})
		c.SetState(sr.cv.alphaState(op, op))
		c.DrawXObject(name)
	}
}

func (sr *svgRenderer) attrLen(n *html.Node, st svgStyle, name string, ref float64) float64 {
	return sr.length(st, images.Attr(n, name), ref)
}

// shape returns the path of a basic shape element.
func (sr *svgRenderer) shape(n *html.Node, st svgStyle) []images.PathSeg {
	tag := strings.ToLower(n.Tag)
	W, H := sr.vw, sr.vh
	switch tag {
	case "path":
		return images.ParsePath(images.Attr(n, "d"))
	case "rect":
		x, y := sr.attrLen(n, st, "x", W), sr.attrLen(n, st, "y", H)
		w, h := sr.attrLen(n, st, "width", W), sr.attrLen(n, st, "height", H)
		if w <= 0 || h <= 0 {
			return nil
		}
		rxs, rys := images.Attr(n, "rx"), images.Attr(n, "ry")
		rx, ry := sr.length(st, rxs, W), sr.length(st, rys, H)
		if rxs == "" {
			rx = ry
		}
		if rys == "" {
			ry = rx
		}
		rx, ry = math.Min(rx, w/2), math.Min(ry, h/2)
		if rx <= 0 || ry <= 0 {
			return []images.PathSeg{{Op: 'M', Pts: []float64{x, y}}, {Op: 'L', Pts: []float64{x + w, y}},
				{Op: 'L', Pts: []float64{x + w, y + h}}, {Op: 'L', Pts: []float64{x, y + h}}, {Op: 'Z'}}
		}
		k := kappa
		return []images.PathSeg{
			{Op: 'M', Pts: []float64{x + rx, y}},
			{Op: 'L', Pts: []float64{x + w - rx, y}},
			{Op: 'C', Pts: []float64{x + w - rx + rx*k, y, x + w, y + ry - ry*k, x + w, y + ry}},
			{Op: 'L', Pts: []float64{x + w, y + h - ry}},
			{Op: 'C', Pts: []float64{x + w, y + h - ry + ry*k, x + w - rx + rx*k, y + h, x + w - rx, y + h}},
			{Op: 'L', Pts: []float64{x + rx, y + h}},
			{Op: 'C', Pts: []float64{x + rx - rx*k, y + h, x, y + h - ry + ry*k, x, y + h - ry}},
			{Op: 'L', Pts: []float64{x, y + ry}},
			{Op: 'C', Pts: []float64{x, y + ry - ry*k, x + rx - rx*k, y, x + rx, y}},
			{Op: 'Z'},
		}
	case "circle", "ellipse":
		cx, cy := sr.attrLen(n, st, "cx", W), sr.attrLen(n, st, "cy", H)
		var rx, ry float64
		if tag == "circle" {
			r := sr.attrLen(n, st, "r", math.Hypot(W, H)/math.Sqrt2)
			rx, ry = r, r
		} else {
			rx, ry = sr.attrLen(n, st, "rx", W), sr.attrLen(n, st, "ry", H)
		}
		if rx <= 0 || ry <= 0 {
			return nil
		}
		k := kappa
		return []images.PathSeg{
			{Op: 'M', Pts: []float64{cx + rx, cy}},
			{Op: 'C', Pts: []float64{cx + rx, cy + ry*k, cx + rx*k, cy + ry, cx, cy + ry}},
			{Op: 'C', Pts: []float64{cx - rx*k, cy + ry, cx - rx, cy + ry*k, cx - rx, cy}},
			{Op: 'C', Pts: []float64{cx - rx, cy - ry*k, cx - rx*k, cy - ry, cx, cy - ry}},
			{Op: 'C', Pts: []float64{cx + rx*k, cy - ry, cx + rx, cy - ry*k, cx + rx, cy}},
			{Op: 'Z'},
		}
	case "line":
		return []images.PathSeg{
			{Op: 'M', Pts: []float64{sr.attrLen(n, st, "x1", W), sr.attrLen(n, st, "y1", H)}},
			{Op: 'L', Pts: []float64{sr.attrLen(n, st, "x2", W), sr.attrLen(n, st, "y2", H)}},
		}
	case "polyline", "polygon":
		pts := images.ParseNumbers(images.Attr(n, "points"))
		if len(pts) < 4 {
			return nil
		}
		segs := []images.PathSeg{{Op: 'M', Pts: pts[:2]}}
		for i := 2; i+1 < len(pts); i += 2 {
			segs = append(segs, images.PathSeg{Op: 'L', Pts: pts[i : i+2]})
		}
		if tag == "polygon" {
			segs = append(segs, images.PathSeg{Op: 'Z'})
		}
		return segs
	}
	return nil
}

func addPath(c *pdf.Content, segs []images.PathSeg) {
	for _, s := range segs {
		switch s.Op {
		case 'M':
			c.MoveTo(s.Pts[0], s.Pts[1])
		case 'L':
			c.LineTo(s.Pts[0], s.Pts[1])
		case 'C':
			c.CurveTo(s.Pts[0], s.Pts[1], s.Pts[2], s.Pts[3], s.Pts[4], s.Pts[5])
		case 'Z':
			c.ClosePath()
		}
	}
}

func pathBBox(segs []images.PathSeg) [4]float64 {
	bb := [4]float64{math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)}
	for _, s := range segs {
		for i := 0; i+1 < len(s.Pts); i += 2 {
			bb[0] = math.Min(bb[0], s.Pts[i])
			bb[1] = math.Min(bb[1], s.Pts[i+1])
			bb[2] = math.Max(bb[2], s.Pts[i])
			bb[3] = math.Max(bb[3], s.Pts[i+1])
		}
	}
	if math.IsInf(bb[0], 1) {
		return [4]float64{}
	}
	return bb
}

func (sr *svgRenderer) paintPath(segs []images.PathSeg, st svgStyle, fillable bool) {
	if len(segs) == 0 || st["visibility"] == "hidden" {
		return
	}
	c := sr.cv.c
	fill := st["fill"]
	evenOdd := st["fill-rule"] == "evenodd"
	if fill != "none" && fillable {
		if strings.HasPrefix(fill, "url(") {
			sr.paintGradient(fill, segs, st, evenOdd, st.num("fill-opacity", 1))
		} else if col, ok := sr.color(st, fill); ok {
			col.A *= st.num("fill-opacity", 1)
			c.Save()
			sr.cv.setFill(col)
			addPath(c, segs)
			if evenOdd {
				c.FillEvenOdd()
			} else {
				c.Fill()
			}
			c.Restore()
		}
	}
	stroke := st["stroke"]
	if stroke != "" && stroke != "none" {
		width := 1.0
		if v, ok := st["stroke-width"]; ok {
			width = sr.length(st, v, math.Hypot(sr.vw, sr.vh)/math.Sqrt2)
		}
		if width <= 0 {
			return
		}
		col, ok := sr.color(st, stroke)
		if strings.HasPrefix(stroke, "url(") {
			// Gradient strokes are approximated by their first stop.
			col, ok = sr.gradientFirstColor(stroke, st)
		}
		if !ok {
			return
		}
		col.A *= st.num("stroke-opacity", 1)
		c.Save()
		sr.cv.setStroke(col)
		c.SetLineWidth(width)
		switch st["stroke-linecap"] {
		case "round":
			c.Op("1 J")
		case "square":
			c.Op("2 J")
		}
		switch st["stroke-linejoin"] {
		case "round":
			c.Op("1 j")
		case "bevel":
			c.Op("2 j")
		}
		if ml, ok := st["stroke-miterlimit"]; ok {
			if v, err := strconv.ParseFloat(ml, 64); err == nil && v >= 1 {
				c.Op(pdf.FormatNumber(v) + " M")
			}
		}
		if da := st["stroke-dasharray"]; da != "" && da != "none" {
			var dashes []float64
			for _, p := range strings.FieldsFunc(da, func(r rune) bool { return r == ',' || r == ' ' }) {
				dashes = append(dashes, sr.length(st, p, sr.vw))
			}
			if len(dashes)%2 == 1 {
				dashes = append(dashes, dashes...)
			}
			sum := 0.0
			for _, d := range dashes {
				sum += d
			}
			if sum > 0 {
				c.SetDash(dashes, st.num("stroke-dashoffset", 0))
			}
		}
		addPath(c, segs)
		c.Stroke()
		c.Restore()
	}
}

func (sr *svgRenderer) ref(v string) *html.Node {
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(v, "url(")
	v = strings.TrimSuffix(v, ")")
	v = strings.Trim(v, "\"' ")
	if i := strings.IndexByte(v, '#'); i >= 0 {
		v = v[i+1:]
	}
	return sr.svg.IDs[v]
}

type gradStop struct {
	off   float64
	color css.Color
}

func (sr *svgRenderer) gradientStops(g *html.Node, depth int) []gradStop {
	var stops []gradStop
	for c := g.FirstChild; c != nil; c = c.NextSibling {
		if c.Type != html.ElementNode || strings.ToLower(c.Tag) != "stop" {
			continue
		}
		st := sr.style(c, svgStyle{})
		off := 0.0
		if o := images.Attr(c, "offset"); o != "" {
			if strings.HasSuffix(o, "%") {
				v, _ := strconv.ParseFloat(strings.TrimSuffix(o, "%"), 64)
				off = v / 100
			} else {
				off, _ = strconv.ParseFloat(o, 64)
			}
		}
		col, ok := sr.color(st, st["stop-color"])
		if _, has := st["stop-color"]; !has {
			col, ok = css.Color{A: 1}, true
		}
		if !ok {
			col = css.Color{}
		}
		col.A *= st.num("stop-opacity", 1)
		off = math.Max(0, math.Min(1, off))
		if len(stops) > 0 && off < stops[len(stops)-1].off {
			off = stops[len(stops)-1].off
		}
		stops = append(stops, gradStop{off, col})
	}
	if len(stops) == 0 && depth < 5 {
		if href := images.Attr(g, "href"); href != "" {
			if ref := sr.ref(href); ref != nil {
				return sr.gradientStops(ref, depth+1)
			}
		}
		if href := images.Attr(g, "xlink:href"); href != "" {
			if ref := sr.ref(href); ref != nil {
				return sr.gradientStops(ref, depth+1)
			}
		}
	}
	return stops
}

func (sr *svgRenderer) gradientFirstColor(v string, st svgStyle) (css.Color, bool) {
	g := sr.ref(v)
	if g == nil {
		return css.Color{}, false
	}
	stops := sr.gradientStops(g, 0)
	if len(stops) == 0 {
		return css.Color{}, false
	}
	return stops[0].color, true
}

func (sr *svgRenderer) gradAttr(g *html.Node, name string) string {
	for i := 0; i < 5 && g != nil; i++ {
		if v := images.Attr(g, name); v != "" {
			return v
		}
		href := images.Attr(g, "href")
		if href == "" {
			href = images.Attr(g, "xlink:href")
		}
		if href == "" {
			break
		}
		g = sr.ref(href)
	}
	return ""
}

func (sr *svgRenderer) paintGradient(v string, segs []images.PathSeg, st svgStyle, evenOdd bool, opacity float64) {
	g := sr.ref(v)
	if g == nil {
		// Fallback color after the URL.
		if i := strings.IndexByte(v, ')'); i >= 0 {
			if col, ok := sr.color(st, v[i+1:]); ok {
				sr.cv.c.Save()
				sr.cv.setFill(col)
				addPath(sr.cv.c, segs)
				sr.cv.c.Fill()
				sr.cv.c.Restore()
			}
		}
		return
	}
	stops := sr.gradientStops(g, 0)
	if len(stops) == 0 {
		return
	}
	c := sr.cv.c
	c.Save()
	defer c.Restore()
	addPath(c, segs)
	if evenOdd {
		c.ClipEvenOdd()
	} else {
		c.Clip()
	}
	if len(stops) == 1 {
		col := stops[0].color
		col.A *= opacity
		sr.cv.setFill(col)
		bb := pathBBox(segs)
		c.Rect(bb[0], bb[1], bb[2]-bb[0], bb[3]-bb[1])
		c.Fill()
		return
	}
	userSpace := sr.gradAttr(g, "gradientUnits") == "userSpaceOnUse"
	if !userSpace {
		bb := pathBBox(segs)
		w, h := bb[2]-bb[0], bb[3]-bb[1]
		if w <= 0 || h <= 0 {
			return
		}
		c.Transform(w, 0, 0, h, bb[0], bb[1])
	}
	if t := sr.gradAttr(g, "gradientTransform"); t != "" {
		m := parseTransform(t)
		c.Transform(m[0], m[1], m[2], m[3], m[4], m[5])
	}
	val := func(name string, def string, ref float64) float64 {
		s := sr.gradAttr(g, name)
		if s == "" {
			s = def
		}
		if !userSpace {
			if strings.HasSuffix(s, "%") {
				f, _ := strconv.ParseFloat(strings.TrimSuffix(s, "%"), 64)
				return f / 100
			}
			f, _ := strconv.ParseFloat(s, 64)
			return f
		}
		return sr.length(st, s, ref)
	}
	var coords []float64
	shType := 2
	if strings.ToLower(g.Tag) == "radialgradient" {
		shType = 3
		cx, cy := val("cx", "50%", sr.vw), val("cy", "50%", sr.vh)
		r := val("r", "50%", math.Hypot(sr.vw, sr.vh)/math.Sqrt2)
		fx, fy := cx, cy
		if sr.gradAttr(g, "fx") != "" {
			fx = val("fx", "50%", sr.vw)
		}
		if sr.gradAttr(g, "fy") != "" {
			fy = val("fy", "50%", sr.vh)
		}
		coords = []float64{fx, fy, 0, cx, cy, r}
	} else {
		coords = []float64{val("x1", "0%", sr.vw), val("y1", "0%", sr.vh), val("x2", "100%", sr.vw), val("y2", "0%", sr.vh)}
	}
	var fns pdf.Array
	var bounds, encode []float64
	alpha := 0.0
	for i := 0; i+1 < len(stops); i++ {
		a, b := stops[i].color, stops[i+1].color
		fns = append(fns, pdf.Dict{"FunctionType": 2, "Domain": []float64{0, 1}, "C0": []float64{a.R, a.G, a.B}, "C1": []float64{b.R, b.G, b.B}, "N": 1})
		if i > 0 {
			bounds = append(bounds, stops[i].off)
		}
		encode = append(encode, 0, 1)
	}
	for _, s := range stops {
		alpha += s.color.A
	}
	alpha /= float64(len(stops))
	var fn any = fns[0]
	domain := []float64{stops[0].off, stops[len(stops)-1].off}
	if domain[1]-domain[0] < 1e-9 {
		domain = []float64{0, 1}
	}
	if len(fns) > 1 {
		fn = pdf.Dict{"FunctionType": 3, "Domain": domain, "Functions": fns, "Bounds": bounds, "Encode": encode}
	} else {
		fns[0].(pdf.Dict)["Domain"] = domain
	}
	sh := pdf.Dict{"ShadingType": shType, "ColorSpace": pdf.Name("DeviceRGB"), "Coords": coords,
		"Function": fn, "Extend": pdf.Array{true, true}, "Domain": domain}
	name := fmt.Sprintf("Sh%d", sr.cv.r.nextID())
	sr.cv.shadings[name] = sr.cv.r.doc.Add(sh)
	if a := alpha * opacity; a < 1 {
		c.SetState(sr.cv.alphaState(a, 1))
	}
	c.ShFill(name)
}

func (sr *svgRenderer) applyClip(v string, n *html.Node, st svgStyle) {
	cp := sr.ref(v)
	if cp == nil {
		return
	}
	c := sr.cv.c
	any := false
	var bb [4]float64
	for ch := cp.FirstChild; ch != nil; ch = ch.NextSibling {
		if ch.Type != html.ElementNode {
			continue
		}
		cst := sr.style(ch, st)
		segs := sr.shape(ch, cst)
		if segs == nil {
			continue
		}
		if t := images.Attr(ch, "transform"); t != "" {
			segs = transformSegs(segs, parseTransform(t))
		}
		addPath(c, segs)
		b := pathBBox(segs)
		if !any {
			bb = b
		}
		any = true
	}
	_ = bb
	if any {
		c.Clip()
	}
}

func transformSegs(segs []images.PathSeg, m []float64) []images.PathSeg {
	out := make([]images.PathSeg, len(segs))
	for i, s := range segs {
		pts := make([]float64, len(s.Pts))
		for j := 0; j+1 < len(s.Pts); j += 2 {
			x, y := s.Pts[j], s.Pts[j+1]
			pts[j] = m[0]*x + m[2]*y + m[4]
			pts[j+1] = m[1]*x + m[3]*y + m[5]
		}
		out[i] = images.PathSeg{Op: s.Op, Pts: pts}
	}
	return out
}

func (sr *svgRenderer) nestedSVG(n *html.Node, st svgStyle) {
	x, y := sr.attrLen(n, st, "x", sr.vw), sr.attrLen(n, st, "y", sr.vh)
	w, h := sr.vw, sr.vh
	if v := images.Attr(n, "width"); v != "" {
		w = sr.length(st, v, sr.vw)
	}
	if v := images.Attr(n, "height"); v != "" {
		h = sr.length(st, v, sr.vh)
	}
	c := sr.cv.c
	c.Transform(1, 0, 0, 1, x, y)
	c.Rect(0, 0, w, h)
	c.Clip()
	saved := [2]float64{sr.vw, sr.vh}
	sr.vw, sr.vh = w, h
	if vb := images.ParseNumbers(images.Attr(n, "viewBox")); len(vb) == 4 && vb[2] > 0 && vb[3] > 0 {
		sr.viewBoxTransform([4]float64{vb[0], vb[1], vb[2], vb[3]}, w, h, images.Attr(n, "preserveAspectRatio"))
		sr.vw, sr.vh = vb[2], vb[3]
	}
	sr.children(n, st)
	sr.vw, sr.vh = saved[0], saved[1]
}

func (sr *svgRenderer) use(n *html.Node, st svgStyle) {
	href := images.Attr(n, "href")
	if href == "" {
		href = images.Attr(n, "xlink:href")
	}
	target := sr.ref(href)
	if target == nil {
		return
	}
	x, y := sr.attrLen(n, st, "x", sr.vw), sr.attrLen(n, st, "y", sr.vh)
	sr.cv.c.Transform(1, 0, 0, 1, x, y)
	if strings.ToLower(target.Tag) == "symbol" {
		w, h := sr.vw, sr.vh
		if v := images.Attr(n, "width"); v != "" {
			w = sr.length(st, v, sr.vw)
		}
		if v := images.Attr(n, "height"); v != "" {
			h = sr.length(st, v, sr.vh)
		}
		tst := sr.style(target, st)
		saved := [2]float64{sr.vw, sr.vh}
		if vb := images.ParseNumbers(images.Attr(target, "viewBox")); len(vb) == 4 && vb[2] > 0 && vb[3] > 0 {
			sr.viewBoxTransform([4]float64{vb[0], vb[1], vb[2], vb[3]}, w, h, images.Attr(target, "preserveAspectRatio"))
			sr.vw, sr.vh = vb[2], vb[3]
		}
		sr.children(target, tst)
		sr.vw, sr.vh = saved[0], saved[1]
		return
	}
	sr.node(target, st)
}

func (sr *svgRenderer) image(n *html.Node, st svgStyle) {
	href := images.Attr(n, "href")
	if href == "" {
		href = images.Attr(n, "xlink:href")
	}
	if href == "" {
		return
	}
	url := fetch.Resolve(sr.svg.URL, href)
	im := sr.svg.Fetch(url)
	if im == nil {
		return
	}
	x, y := sr.attrLen(n, st, "x", sr.vw), sr.attrLen(n, st, "y", sr.vh)
	iw, ih, _ := im.IntrinsicSize()
	w, h := iw, ih
	if v := images.Attr(n, "width"); v != "" {
		w = sr.length(st, v, sr.vw)
	}
	if v := images.Attr(n, "height"); v != "" {
		h = sr.length(st, v, sr.vh)
	}
	if w <= 0 || h <= 0 {
		return
	}
	// preserveAspectRatio (xMidYMid meet by default).
	dw, dh := w, h
	if iw > 0 && ih > 0 && !strings.HasPrefix(strings.ToLower(images.Attr(n, "preserveAspectRatio")), "none") {
		s := math.Min(w/iw, h/ih)
		dw, dh = iw*s, ih*s
	}
	sr.cv.paintImage(im, x+(w-dw)/2, y+(h-dh)/2, dw, dh)
}

func (sr *svgRenderer) text(n *html.Node, st svgStyle) {
	x := sr.attrLen(n, st, "x", sr.vw)
	y := sr.attrLen(n, st, "y", sr.vh)
	if dx := images.Attr(n, "dx"); dx != "" {
		x += sr.length(st, dx, sr.vw)
	}
	if dy := images.Attr(n, "dy"); dy != "" {
		y += sr.length(st, dy, sr.vh)
	}
	type run struct {
		text string
		st   svgStyle
		x, y *float64
	}
	var runs []run
	var collect func(m *html.Node, ms svgStyle)
	collect = func(m *html.Node, ms svgStyle) {
		for c := m.FirstChild; c != nil; c = c.NextSibling {
			switch c.Type {
			case html.TextNode:
				t := strings.Join(strings.Fields(strings.ReplaceAll(c.Data, "\n", " ")), " ")
				if strings.HasPrefix(c.Data, " ") && len(runs) > 0 {
					t = " " + t
				}
				if strings.HasSuffix(c.Data, " ") && t != "" {
					t += " "
				}
				if t != "" {
					runs = append(runs, run{text: t, st: ms})
				}
			case html.ElementNode:
				if strings.ToLower(c.Tag) == "tspan" {
					cs := sr.style(c, ms)
					if cs["display"] == "none" {
						continue
					}
					start := len(runs)
					collect(c, cs)
					if start < len(runs) {
						if v := images.Attr(c, "x"); v != "" {
							xv := sr.length(cs, v, sr.vw)
							runs[start].x = &xv
						}
						if v := images.Attr(c, "y"); v != "" {
							yv := sr.length(cs, v, sr.vh)
							runs[start].y = &yv
						}
						if v := images.Attr(c, "dy"); v != "" && runs[start].y == nil {
							yv := math.NaN()
							runs[start].y = &yv
							_ = v
						}
					}
				}
			}
		}
	}
	collect(n, st)
	if len(runs) == 0 || st["visibility"] == "hidden" {
		return
	}
	// Shape everything to compute the total width for text-anchor.
	type shapedRun struct {
		sh  *text.Shaped
		col css.Color
		ok  bool
		r   run
	}
	var shaped []shapedRun
	total := 0.0
	for _, r := range runs {
		spec := text.Spec{Families: splitFamilies(r.st["font-family"]), Weight: 400, Size: sr.fontSize(r.st), Stretch: 5}
		if fw := r.st["font-weight"]; fw == "bold" || fw == "bolder" {
			spec.Weight = 700
		} else if v, err := strconv.Atoi(fw); err == nil {
			spec.Weight = v
		}
		if fs := r.st["font-style"]; fs == "italic" || fs == "oblique" {
			spec.Italic = true
		}
		sh := sr.cv.r.shaper.Shape(r.text, spec)
		col, ok := sr.color(r.st, r.st["fill"])
		col.A *= r.st.num("fill-opacity", 1)
		shaped = append(shaped, shapedRun{sh, col, ok, r})
		total += sh.Pos[len(sh.Runes)]
	}
	switch st["text-anchor"] {
	case "middle":
		x -= total / 2
	case "end":
		x -= total
	}
	for _, s := range shaped {
		if s.r.x != nil {
			x = *s.r.x
		}
		if s.r.y != nil && !math.IsNaN(*s.r.y) {
			y = *s.r.y
		}
		if s.ok {
			sr.cv.drawGlyphs(s.sh.Runes, s.sh.Glyphs, nil, x, y, s.col)
		}
		x += s.sh.Pos[len(s.sh.Runes)]
	}
}

func splitFamilies(s string) []string {
	var out []string
	for _, f := range strings.Split(s, ",") {
		f = strings.Trim(strings.TrimSpace(f), "\"'")
		if f != "" {
			out = append(out, f)
		}
	}
	if len(out) == 0 {
		out = []string{"sans-serif"}
	}
	return out
}

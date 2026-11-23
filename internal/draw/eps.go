package draw

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"strings"

	"github.com/esoteric-programmer/weasyprint-go/internal/fonts"
	"github.com/esoteric-programmer/weasyprint-go/internal/images"
	"github.com/esoteric-programmer/weasyprint-go/internal/pdf"
	"github.com/esoteric-programmer/weasyprint-go/internal/ps"
)

// EPS images are run by the PostScript interpreter and drawn as a form
// XObject of vector graphics (WeasyPrint embeds Ghostscript's 72 dpi
// rendering).

type epsForm struct {
	ref    pdf.Ref
	images []epsImageUse
}

type epsImageUse struct {
	im *images.Image
	m  ps.Matrix // unit square to form space
}

// drawEPS paints an EPS image into the rectangle.
func (cv *canvas) drawEPS(im *images.Image, x, y, w, h float64) {
	f := cv.r.epsForm(im)
	if f == nil {
		return
	}
	b := im.EPS.BBox
	bw, bh := b[2]-b[0], b[3]-b[1]
	if bw <= 0 || bh <= 0 {
		return
	}
	cv.c.Save()
	sx, sy := w/bw, h/bh
	cv.c.Transform(sx, 0, 0, -sy, x-b[0]*sx, y+h+b[1]*sy)
	ctm := cv.c.CTM()
	page := ps.Matrix{ctm[0], ctm[1], ctm[2], ctm[3], ctm[4], ctm[5]}
	for _, u := range f.images {
		m := u.m.Mul(page)
		cv.r.useImageAt(u.im, 1, 1, [6]float64(m))
	}
	name := fmt.Sprintf("Fm%d", int(f.ref))
	cv.xobjects[name] = f.ref
	cv.c.DrawXObject(name)
	cv.c.Restore()
}

// epsForm runs an EPS program and writes its form XObject (once per
// document).
func (r *Renderer) epsForm(im *images.Image) *epsForm {
	if r.epsForms == nil {
		r.epsForms = map[*images.Image]*epsForm{}
	}
	if f, ok := r.epsForms[im]; ok {
		return f
	}
	r.epsForms[im] = nil
	warn := func(s string) {
		if r.opts.Warn != nil {
			r.opts.Warn(s)
		}
	}
	list, err := ps.RenderEPS(im.EPS.Program, im.EPS.BBox, ps.Options{Font: r.epsFont, Warn: warn})
	if err != nil {
		warn("Error in EPS image " + shortURL(im.URL) + ": " + err.Error())
	}
	if list == nil {
		return nil
	}
	f := &epsForm{}
	cv := &canvas{r: r, c: pdf.NewContent(r.opts.Compress), fonts: pdf.Dict{}, xobjects: pdf.Dict{},
		states: pdf.Dict{}, shadings: pdf.Dict{}, patterns: pdf.Dict{}, stateIDs: map[string]string{}}
	e := &epsWriter{cv: cv, form: f, patterns: map[*ps.Pattern]string{}, imgs: map[*ps.Image]*images.Image{}}
	e.ops(list.Ops, ps.Matrix{1, 0, 0, 1, 0, 0})
	c := cv.c
	b := im.EPS.BBox
	c.Dict["Type"] = pdf.Name("XObject")
	c.Dict["Subtype"] = pdf.Name("Form")
	c.Dict["BBox"] = []float64{b[0], b[1], b[2], b[3]}
	c.Dict["Resources"] = cv.resources()
	f.ref = r.doc.Add(c)
	r.epsForms[im] = f
	return f
}

func shortURL(s string) string {
	if len(s) > 80 {
		return s[:77] + "..."
	}
	return s
}

// epsFont finds a system font for a PostScript font name.
func (r *Renderer) epsFont(name string) *fonts.Font {
	if r.shaper == nil || r.shaper.Lib == nil {
		return nil
	}
	lib := r.shaper.Lib
	if f := lib.FindLocal(name); f != nil {
		return f
	}
	if alt, ok := urwNames[name]; ok {
		if f := lib.FindLocal(alt); f != nil {
			return f
		}
	}
	family, req := psFontRequest(name)
	families := []string{family}
	if fam, ok := psFamilies[family]; ok {
		families = append(fam, family)
	}
	if ordered, ok := lib.OrderedFamilies(families); ok {
		families = ordered
	}
	for _, fam := range families {
		if face := lib.Resolve(fam, req); face != nil {
			return face.Font
		}
	}
	return nil
}

// urwNames are the URW base35 fonts (Ghostscript's) for the standard
// PostScript fonts.
var urwNames = map[string]string{}

func init() {
	add := func(ps, urw string, styles ...[2]string) {
		for _, s := range styles {
			n := ps
			if s[0] != "" {
				n += "-" + s[0]
			}
			urwNames[n] = urw + "-" + s[1]
		}
	}
	add("Helvetica", "NimbusSans", [2]string{"", "Regular"}, [2]string{"Bold", "Bold"}, [2]string{"Oblique", "Italic"}, [2]string{"BoldOblique", "BoldItalic"})
	add("Helvetica-Narrow", "NimbusSansNarrow", [2]string{"", "Regular"}, [2]string{"Bold", "Bold"}, [2]string{"Oblique", "Oblique"}, [2]string{"BoldOblique", "BoldOblique"})
	add("Times", "NimbusRoman", [2]string{"Roman", "Regular"}, [2]string{"Bold", "Bold"}, [2]string{"Italic", "Italic"}, [2]string{"BoldItalic", "BoldItalic"})
	add("Courier", "NimbusMonoPS", [2]string{"", "Regular"}, [2]string{"Bold", "Bold"}, [2]string{"Oblique", "Italic"}, [2]string{"BoldOblique", "BoldItalic"})
	add("AvantGarde", "URWGothic", [2]string{"Book", "Book"}, [2]string{"Demi", "Demi"}, [2]string{"BookOblique", "BookOblique"}, [2]string{"DemiOblique", "DemiOblique"})
	add("Bookman", "URWBookman", [2]string{"Light", "Light"}, [2]string{"Demi", "Demi"}, [2]string{"LightItalic", "LightItalic"}, [2]string{"DemiItalic", "DemiItalic"})
	add("NewCenturySchlbk", "C059", [2]string{"Roman", "Roman"}, [2]string{"Bold", "Bold"}, [2]string{"Italic", "Italic"}, [2]string{"BoldItalic", "BdIta"})
	add("Palatino", "P052", [2]string{"Roman", "Roman"}, [2]string{"Bold", "Bold"}, [2]string{"Italic", "Italic"}, [2]string{"BoldItalic", "BoldItalic"})
	urwNames["Symbol"] = "StandardSymbolsPS"
	urwNames["ZapfDingbats"] = "D050000L"
	urwNames["ZapfChancery-MediumItalic"] = "Z003-MediumItalic"
}

// psFamilies are the families tried for the standard font names.
var psFamilies = map[string][]string{
	"Helvetica":        {"Nimbus Sans", "Nimbus Sans L", "Liberation Sans", "Arial"},
	"Helvetica-Narrow": {"Nimbus Sans Narrow", "Liberation Sans Narrow", "Arial Narrow"},
	"Times":            {"Nimbus Roman", "Nimbus Roman No9 L", "Liberation Serif", "Times New Roman"},
	"Courier":          {"Nimbus Mono PS", "Nimbus Mono L", "Liberation Mono", "Courier New"},
	"Symbol":           {"Standard Symbols PS", "Standard Symbols L"},
	"AvantGarde":       {"URW Gothic", "URW Gothic L"},
	"Bookman":          {"URW Bookman", "URW Bookman L"},
	"NewCenturySchlbk": {"C059", "Century Schoolbook L"},
	"Palatino":         {"P052", "URW Palladio L", "TeX Gyre Pagella"},
	"ZapfChancery":     {"Z003", "URW Chancery L"},
	"ZapfDingbats":     {"D050000L", "Dingbats"},
}

// psFontRequest splits a PostScript font name into a family and a style.
func psFontRequest(name string) (string, fonts.Request) {
	family, style, _ := strings.Cut(name, "-")
	if strings.HasPrefix(name, "Helvetica-Narrow") {
		family, style = "Helvetica-Narrow", strings.TrimPrefix(strings.TrimPrefix(name, "Helvetica-Narrow"), "-")
	}
	req := fonts.Request{Weight: 400, Stretch: 5}
	l := strings.ToLower(style)
	switch {
	case strings.Contains(l, "black") || strings.Contains(l, "heavy"):
		req.Weight = 900
	case strings.Contains(l, "bold") || strings.Contains(l, "demi") || strings.Contains(l, "bd"):
		req.Weight = 700
	case strings.Contains(l, "light"):
		req.Weight = 300
	case strings.Contains(l, "medium"):
		req.Weight = 500
	}
	req.Italic = strings.Contains(l, "italic") || strings.Contains(l, "oblique") || strings.Contains(l, "ita")
	if strings.Contains(l, "cond") || strings.Contains(l, "narrow") {
		req.Stretch = 3
	}
	return family, req
}

// epsWriter writes a display list into a content stream.
type epsWriter struct {
	cv       *canvas
	form     *epsForm
	patterns map[*ps.Pattern]string
	imgs     map[*ps.Image]*images.Image
	depth    int
}

func addPSPath(c *pdf.Content, p ps.Path) {
	for _, s := range p {
		switch s.Op {
		case 'M':
			c.MoveTo(s.P[0].X, s.P[0].Y)
		case 'L':
			c.LineTo(s.P[0].X, s.P[0].Y)
		case 'C':
			c.CurveTo(s.P[0].X, s.P[0].Y, s.P[1].X, s.P[1].Y, s.P[2].X, s.P[2].Y)
		case 'Z':
			c.ClosePath()
		}
	}
}

func colorOp(col ps.Color, stroke bool) string {
	n := pdf.FormatNumber
	var s string
	switch col.N {
	case 3:
		s = fmt.Sprintf("%s %s %s rg", n(col.V[0]), n(col.V[1]), n(col.V[2]))
	case 4:
		s = fmt.Sprintf("%s %s %s %s k", n(col.V[0]), n(col.V[1]), n(col.V[2]), n(col.V[3]))
	default:
		s = n(col.V[0]) + " g"
	}
	if stroke {
		i := strings.LastIndexByte(s, ' ')
		s = s[:i] + strings.ToUpper(s[i:])
	}
	return s
}

// setPaint sets the fill or stroke paint. It returns false when nothing
// is to be painted.
func (e *epsWriter) setPaint(p ps.Paint, stroke bool) bool {
	c := e.cv.c
	if p.Pattern == nil {
		if p.Color.N == 0 {
			return false
		}
		c.Op(colorOp(p.Color, stroke))
		return true
	}
	name := e.pattern(p.Pattern)
	if name == "" {
		return false
	}
	cs, sc := "cs", "scn"
	if stroke {
		cs, sc = "CS", "SCN"
	}
	if p.Pattern.Type == 1 && p.Pattern.PaintType == 2 {
		base := map[int]string{1: "DeviceGray", 3: "DeviceRGB", 4: "DeviceCMYK"}[p.Color.N]
		if base == "" {
			base = "DeviceGray"
		}
		csName := "PS" + base
		if e.cv.colorSpaces == nil {
			e.cv.colorSpaces = pdf.Dict{}
		}
		e.cv.colorSpaces[csName] = pdf.Array{pdf.Name("Pattern"), pdf.Name(base)}
		var comps []string
		for i := 0; i < max(1, p.Color.N); i++ {
			comps = append(comps, pdf.FormatNumber(p.Color.V[i]))
		}
		c.Op(fmt.Sprintf("/%s %s %s /%s %s", csName, cs, strings.Join(comps, " "), name, sc))
		return true
	}
	c.Op(fmt.Sprintf("/Pattern %s /%s %s", cs, name, sc))
	return true
}

// ops writes the operations; base maps the form space of nested lists
// (patterns) to the form.
func (e *epsWriter) ops(ops []ps.Op, base ps.Matrix) {
	c := e.cv.c
	var clip *ps.Clip
	open := false
	for i := range ops {
		op := &ops[i]
		if !open || op.Clip != clip {
			if open {
				c.Restore()
			}
			c.Save()
			open = true
			clip = op.Clip
			e.applyClip(clip)
		}
		switch op.Kind {
		case ps.OpFill:
			if !e.setPaint(op.Paint, false) {
				continue
			}
			addPSPath(c, op.Path)
			if op.EvenOdd {
				c.FillEvenOdd()
			} else {
				c.Fill()
			}
		case ps.OpStroke:
			inv, ok := op.CTM.Invert()
			if !ok {
				continue
			}
			c.Save()
			if !e.setPaint(op.Paint, true) {
				c.Restore()
				continue
			}
			m := op.CTM
			c.Transform(m[0], m[1], m[2], m[3], m[4], m[5])
			st := op.Stroke
			c.SetLineWidth(st.Width)
			c.SetLineCap(st.Cap)
			c.Op(fmt.Sprintf("%d j", st.Join))
			c.Op(pdf.FormatNumber(st.Miter) + " M")
			if len(st.Dash) > 0 {
				c.SetDash(st.Dash, st.Phase)
			}
			addPSPath(c, op.Path.Transform(inv))
			c.Stroke()
			c.Restore()
		case ps.OpImage:
			im := e.image(op.Image)
			if im == nil {
				continue
			}
			c.Save()
			if op.Image.Mask && !e.setPaint(op.Paint, false) {
				c.Restore()
				continue
			}
			m := op.CTM
			c.Transform(m[0], m[1], m[2], m[3], m[4], m[5])
			ref := e.cv.r.imageRef(im, op.Image.Interpolate)
			name := fmt.Sprintf("Im%d", int(ref))
			e.cv.xobjects[name] = ref
			c.DrawXObject(name)
			c.Restore()
			e.form.images = append(e.form.images, epsImageUse{im: im, m: m.Mul(base)})
		case ps.OpShade:
			ref := e.cv.r.psShading(op.Shading)
			if ref == 0 {
				continue
			}
			name := fmt.Sprintf("Sh%d", int(ref))
			e.cv.shadings[name] = ref
			c.Save()
			m := op.CTM
			c.Transform(m[0], m[1], m[2], m[3], m[4], m[5])
			c.ShFill(name)
			c.Restore()
		}
	}
	if open {
		c.Restore()
	}
}

func (e *epsWriter) applyClip(cl *ps.Clip) {
	var chain []*ps.Clip
	for ; cl != nil; cl = cl.Parent {
		chain = append(chain, cl)
	}
	c := e.cv.c
	for i := len(chain) - 1; i >= 0; i-- {
		cl := chain[i]
		if len(cl.Path) == 0 {
			// Nothing is painted.
			c.Rect(0, 0, 0, 0)
		} else {
			addPSPath(c, cl.Path)
		}
		if cl.EvenOdd {
			c.ClipEvenOdd()
		} else {
			c.Clip()
		}
	}
}

// image converts an image of the display list.
func (e *epsWriter) image(pi *ps.Image) *images.Image {
	if im, ok := e.imgs[pi]; ok {
		return im
	}
	im := &images.Image{Width: pi.W, Height: pi.H, Components: pi.N, Pixels: pi.Data, Alpha: pi.Alpha}
	if pi.Mask {
		im.Stencil = true
		im.Components = 1
	} else if pi.JPEG != nil {
		im.JPEG = pi.JPEG
		im.Pixels = nil
		im.InvertCMYK = pi.JPEGInverted
	}
	e.imgs[pi] = im
	return im
}

// pattern writes a pattern and returns its resource name.
func (e *epsWriter) pattern(p *ps.Pattern) string {
	if n, ok := e.patterns[p]; ok {
		return n
	}
	e.patterns[p] = ""
	m := p.Matrix
	matrix := []float64{m[0], m[1], m[2], m[3], m[4], m[5]}
	var ref pdf.Ref
	switch p.Type {
	case 1:
		if e.depth > 8 || p.XStep == 0 || p.YStep == 0 {
			return ""
		}
		cv := e.cv
		saved := cv.c
		cv.c = pdf.NewContent(cv.r.opts.Compress)
		e.depth++
		e.ops(p.Ops, m)
		e.depth--
		tile := cv.c
		cv.c = saved
		tile.Dict["Type"] = pdf.Name("Pattern")
		tile.Dict["PatternType"] = 1
		tile.Dict["PaintType"] = p.PaintType
		tile.Dict["TilingType"] = 1
		tile.Dict["BBox"] = []float64{p.BBox[0], p.BBox[1], p.BBox[2], p.BBox[3]}
		tile.Dict["XStep"] = p.XStep
		tile.Dict["YStep"] = p.YStep
		tile.Dict["Matrix"] = matrix
		tile.Dict["Resources"] = cv.resources()
		ref = cv.r.doc.Add(tile)
	case 2:
		if p.Shading == nil {
			return ""
		}
		sh := e.cv.r.psShading(p.Shading)
		if sh == 0 {
			return ""
		}
		ref = e.cv.r.doc.Add(pdf.Dict{"Type": pdf.Name("Pattern"), "PatternType": 2, "Shading": sh, "Matrix": matrix})
	default:
		return ""
	}
	name := fmt.Sprintf("P%d", int(ref))
	e.cv.patterns[name] = ref
	e.patterns[p] = name
	return name
}

// psShading writes a shading of the display list: the colors are
// sampled into a function of device colors.
func (r *Renderer) psShading(sh *ps.Shading) pdf.Ref {
	if r.psShadings == nil {
		r.psShadings = map[*ps.Shading]pdf.Ref{}
	}
	if ref, ok := r.psShadings[sh]; ok {
		return ref
	}
	n := sh.N
	cs := map[int]pdf.Name{1: "DeviceGray", 3: "DeviceRGB", 4: "DeviceCMYK"}[n]
	if cs == "" {
		return 0
	}
	d := pdf.Dict{"ShadingType": sh.Type, "ColorSpace": cs}
	if sh.BBox != nil {
		d["BBox"] = sh.BBox[:]
	}
	if sh.Background != nil {
		d["Background"] = sh.Background.V[:n]
	}
	rng := make([]float64, 0, 2*n)
	for i := 0; i < n; i++ {
		rng = append(rng, 0, 1)
	}
	put16 := func(b *bytes.Buffer, v float64) {
		u := uint16(math.Round(math.Max(0, math.Min(1, v)) * 65535))
		b.WriteByte(byte(u >> 8))
		b.WriteByte(byte(u))
	}
	var ref pdf.Ref
	switch sh.Type {
	case 1:
		const k = 32
		var buf bytes.Buffer
		x0, x1, y0, y1 := sh.Coords[0], sh.Coords[1], sh.Coords[2], sh.Coords[3]
		for j := 0; j < k; j++ {
			for i := 0; i < k; i++ {
				x := x0 + (x1-x0)*float64(i)/(k-1)
				y := y0 + (y1-y0)*float64(j)/(k-1)
				col := sh.Color(x, y)
				for c := 0; c < n; c++ {
					put16(&buf, col.V[c])
				}
			}
		}
		fn := r.doc.Add(&pdf.Stream{Dict: pdf.Dict{"FunctionType": 0, "Domain": []float64{x0, x1, y0, y1},
			"Range": rng, "Size": []float64{k, k}, "BitsPerSample": 16}, Data: buf.Bytes(), Compress: true})
		m := sh.Matrix
		d["Domain"] = []float64{x0, x1, y0, y1}
		d["Matrix"] = []float64{m[0], m[1], m[2], m[3], m[4], m[5]}
		d["Function"] = fn
		ref = r.doc.Add(d)
	case 2, 3:
		if sh.Funcs != nil {
			var fns pdf.Array
			for _, f := range sh.Funcs {
				fns = append(fns, r.psFunction(f))
			}
			d["Coords"] = sh.Coords
			d["Domain"] = []float64{sh.Domain[0], sh.Domain[1]}
			d["Extend"] = pdf.Array{sh.Extend[0], sh.Extend[1]}
			if len(fns) == 1 {
				d["Function"] = fns[0]
			} else {
				d["Function"] = fns
			}
			ref = r.doc.Add(d)
			break
		}
		const k = 1024
		var buf bytes.Buffer
		t0, t1 := sh.Domain[0], sh.Domain[1]
		for i := 0; i < k; i++ {
			col := sh.Color(t0 + (t1-t0)*float64(i)/(k-1))
			for c := 0; c < n; c++ {
				put16(&buf, col.V[c])
			}
		}
		fn := r.doc.Add(&pdf.Stream{Dict: pdf.Dict{"FunctionType": 0, "Domain": []float64{t0, t1},
			"Range": rng, "Size": []float64{k}, "BitsPerSample": 16}, Data: buf.Bytes(), Compress: true})
		d["Coords"] = sh.Coords
		d["Domain"] = []float64{t0, t1}
		d["Extend"] = pdf.Array{sh.Extend[0], sh.Extend[1]}
		d["Function"] = fn
		ref = r.doc.Add(d)
	case 4, 5, 6, 7:
		// Re-encoded as free-form triangles (4) or patches (6, 7) with
		// 32-bit coordinates.
		var pts []ps.Point
		for _, t := range sh.Triangles {
			for _, v := range t {
				pts = append(pts, ps.Point{X: v.X, Y: v.Y})
			}
		}
		for _, p := range sh.Patches {
			pts = append(pts, p.P[:]...)
		}
		if len(pts) == 0 {
			return 0
		}
		minX, minY, maxX, maxY := pts[0].X, pts[0].Y, pts[0].X, pts[0].Y
		for _, p := range pts {
			minX, minY = math.Min(minX, p.X), math.Min(minY, p.Y)
			maxX, maxY = math.Max(maxX, p.X), math.Max(maxY, p.Y)
		}
		if maxX == minX {
			maxX++
		}
		if maxY == minY {
			maxY++
		}
		var buf bytes.Buffer
		coord := func(v, lo, hi float64) {
			var b [4]byte
			binary.BigEndian.PutUint32(b[:], uint32(math.Round((v-lo)/(hi-lo)*4294967295)))
			buf.Write(b[:])
		}
		color := func(c ps.Color) {
			for i := 0; i < n; i++ {
				put16(&buf, c.V[i])
			}
		}
		typ := 4
		if len(sh.Patches) > 0 {
			typ = sh.Type
		}
		if typ == 4 {
			for _, t := range sh.Triangles {
				for _, v := range t {
					buf.WriteByte(0)
					coord(v.X, minX, maxX)
					coord(v.Y, minY, maxY)
					color(v.C)
				}
			}
		} else {
			np := 12
			if typ == 7 {
				np = 16
			}
			for _, p := range sh.Patches {
				buf.WriteByte(0)
				for _, q := range p.P[:np] {
					coord(q.X, minX, maxX)
					coord(q.Y, minY, maxY)
				}
				for _, c := range p.C {
					color(c)
				}
			}
		}
		decode := []float64{minX, maxX, minY, maxY}
		decode = append(decode, rng...)
		d["ShadingType"] = typ
		ref = r.doc.Add(&pdf.Stream{Dict: mergeDict(d, pdf.Dict{"BitsPerCoordinate": 32, "BitsPerComponent": 16,
			"BitsPerFlag": 8, "Decode": decode}), Data: buf.Bytes(), Compress: true})
	default:
		return 0
	}
	r.psShadings[sh] = ref
	return ref
}

// psFunction writes a function of a shading.
func (r *Renderer) psFunction(f *ps.FuncDesc) any {
	d := pdf.Dict{"FunctionType": f.Type, "Domain": f.Domain}
	if f.Range != nil {
		d["Range"] = f.Range
	}
	switch f.Type {
	case 0:
		var buf bytes.Buffer
		for _, v := range f.Samples {
			u := uint16(math.Round(math.Max(0, math.Min(1, v)) * 65535))
			buf.WriteByte(byte(u >> 8))
			buf.WriteByte(byte(u))
		}
		size := make([]float64, len(f.Size))
		for i, n := range f.Size {
			size[i] = float64(n)
		}
		d["Size"] = size
		d["BitsPerSample"] = 16
		if f.Encode != nil {
			d["Encode"] = f.Encode
		}
		if f.Decode != nil {
			d["Decode"] = f.Decode
		}
		return r.doc.Add(&pdf.Stream{Dict: d, Data: buf.Bytes(), Compress: true})
	case 2:
		d["C0"] = f.C0
		d["C1"] = f.C1
		d["N"] = f.N
	case 3:
		var fns pdf.Array
		for _, sub := range f.Functions {
			fns = append(fns, r.psFunction(sub))
		}
		d["Functions"] = fns
		d["Bounds"] = f.Bounds
		enc := f.Encode
		if enc == nil {
			for range f.Functions {
				enc = append(enc, 0, 1)
			}
		}
		d["Encode"] = enc
	}
	return r.doc.Add(d)
}

func mergeDict(a, b pdf.Dict) pdf.Dict {
	for k, v := range b {
		a[k] = v
	}
	return a
}

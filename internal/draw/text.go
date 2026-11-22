package draw

import (
	"github.com/esoteric-programmer/weasyprint-go/internal/bidi"
	"math"

	"github.com/esoteric-programmer/weasyprint-go/internal/css"
	"github.com/esoteric-programmer/weasyprint-go/internal/fonts"
	"github.com/esoteric-programmer/weasyprint-go/internal/images"
	"github.com/esoteric-programmer/weasyprint-go/internal/layout"
	"github.com/esoteric-programmer/weasyprint-go/internal/text"
)

type glyphRun struct {
	face  *fonts.Face
	size  float64
	x     float64
	items []any
	text  []string
}

func (cv *canvas) fontName(f *fonts.Font) (string, *fontEntry) {
	e := cv.r.fm.get(f)
	cv.fonts[e.name] = e.ref
	return e.name, e
}

// drawGlyphs draws glyphs starting at x on baseline y. advances gives the
// desired advance of each glyph (or nil to use the natural ones).
func (cv *canvas) drawGlyphs(runes []rune, glyphs []text.Glyph, advances []float64, x, y float64, color css.Color) float64 {
	var runs []*glyphRun
	var cur *glyphRun
	type colorGlyph struct {
		im      *images.Image
		x, size float64
		font    *fonts.Font
		id      uint16
	}
	var colored []colorGlyph
	for i, g := range glyphs {
		if g.Face == nil || (g.ID == 0 && isIgnorable(runes[g.Start])) {
			if advances != nil {
				x += advances[i]
			} else {
				x += g.Advance
			}
			continue
		}
		if cur == nil || cur.face != g.Face || cur.size != g.Size {
			cur = &glyphRun{face: g.Face, size: g.Size, x: x}
			runs = append(runs, cur)
		}
		natural := g.Face.Font.Advance(g.ID) * g.Size / g.Face.Font.UnitsPerEm
		want := g.Advance
		if advances != nil {
			want = advances[i]
		}
		if g.Face.Font.HasColorBitmaps() {
			if im := cv.r.colorGlyph(g.Face.Font, g.ID); im != nil {
				colored = append(colored, colorGlyph{im, x, g.Size, g.Face.Font, g.ID})
			}
		}
		_, e := cv.fontName(g.Face.Font)
		cur.items = append(cur.items, e.code(g.ID, string(runes[g.Start:g.End])))
		if d := want - natural; math.Abs(d) > 1e-6 {
			cur.items = append(cur.items, -d*1000/g.Size)
		}
		x += want
	}
	if len(runs) == 0 {
		return x
	}
	cv.c.Save()
	cv.setFill(color)
	cv.c.BeginText()
	for _, r := range runs {
		name, _ := cv.fontName(r.face.Font)
		cv.c.SetFont(name, r.size)
		skew := 0.0
		if r.face.SynthItalic {
			skew = -0.2
		}
		cv.c.SetTextMatrix(1, 0, skew, -1, r.x, y)
		if r.face.SynthBold {
			cv.c.SetTextRender(2)
			cv.c.SetStrokeRGB(color.R, color.G, color.B)
			cv.c.SetLineWidth(r.size * 0.035)
		} else {
			cv.c.SetTextRender(0)
		}
		cv.c.ShowGlyphs(r.items)
	}
	cv.c.EndText()
	cv.c.Restore()
	// Color bitmap glyphs are drawn as images, placed as WeasyPrint does:
	// the height is the advance of the glyph and the top is at the
	// baseline plus the ascent (in ems) minus the font size.
	for _, cg := range colored {
		f := cg.font
		d := f.Advance(cg.id) / f.UnitsPerEm
		if d <= 0 || cg.im.Height == 0 {
			continue
		}
		a := float64(cg.im.Width) / float64(cg.im.Height) * d
		top := y + f.Ascent/f.UnitsPerEm - cg.size
		cv.paintImage(cg.im, cg.x, top, a*cg.size, d*cg.size)
	}
	return x
}

func (cv *canvas) drawText(b *layout.Box, decos []decoration) {
	if b.Shaped == nil || b.Style.Kw(css.PVisibility) != "visible" {
		return
	}
	color := b.Style.Color(css.PColor)
	sh := b.Shaped
	glyphs := sh.GlyphsInRange(b.Start, b.End)
	advances := make([]float64, len(glyphs))
	total := 0.0
	tabs := 0
	for i, g := range glyphs {
		advances[i] = g.Advance
		for k := g.Start; k < g.End; k++ {
			switch sh.Runes[k] {
			case ' ', 0xa0:
				advances[i] += b.JustifySpacing
			case '\t':
				tabs++
			case '\n':
				advances[i] = 0
			}
		}
		total += advances[i]
	}
	var hsh *text.Shaped
	if b.Hyphen != "" {
		hsh = cv.r.shaper.Shape(b.Hyphen, b.Spec)
	}
	if tabs > 0 {
		tw := layout.TabWidths(b)
		k := 0
		for i, g := range glyphs {
			if sh.Runes[g.Start] == '\t' && k < len(tw) {
				advances[i] = tw[k]
				k++
			}
		}
	}
	runes := sh.Runes
	if cv.ellipsisRight != 0 && b.X+total > cv.ellipsisRight+1e-6 && !b.Spec.RTL {
		glyphs, advances, runes = cv.ellipsize(b, glyphs, advances, cv.ellipsisRight-b.X)
	}
	glyphs, advances = visualOrder(b, glyphs, advances)
	baseline := b.Y + b.Baseline
	// Text shadows.
	if shadows, ok := b.Style.Get(css.PTextShadow).([]css.Shadow); ok {
		for i := len(shadows) - 1; i >= 0; i-- {
			s := shadows[i]
			col := b.Style.ResolveColor(s.Color)
			cv.drawGlyphs(runes, glyphs, advances, b.X+s.X.V, baseline+s.Y.V, col)
		}
	}
	// Underlines and overlines are drawn below the text, line-through above.
	cv.drawDecorations(b, decos, baseline, false)
	x := cv.drawGlyphs(runes, glyphs, advances, b.X, baseline, color)
	if hsh != nil {
		cv.drawGlyphs(hsh.Runes, hsh.Glyphs, nil, x, baseline, color)
	}
	cv.drawDecorations(b, decos, baseline, true)
}

func (cv *canvas) drawDecorations(b *layout.Box, decos []decoration, baseline float64, through bool) {
	for _, d := range decos {
		if (d.line == "line-through") != through || d.line == "blink" {
			continue
		}
		m := b.Metrics
		if m.UnderlineThick == 0 {
			m = cv.r.shaper.PrimaryMetrics(b.Spec)
		}
		thick := m.UnderlineThick
		if l, ok := d.thickness.(css.Length); ok {
			thick = l.Resolve(b.Spec.Size)
		}
		if thick <= 0 {
			thick = b.Spec.Size / 16
		}
		var y float64
		switch d.line {
		case "underline":
			y = baseline + m.UnderlinePos + thick/2
			if l, ok := d.src.Get(css.PTextUnderlineOffset).(css.Length); ok {
				y = baseline + l.Resolve(b.Spec.Size) + thick/2
			}
		case "overline":
			y = baseline - m.Ascent + thick/2
		case "line-through":
			y = baseline - m.StrikeoutPos
			if m.StrikeoutThick > 0 && d.thickness == css.Keyword("auto") {
				thick = m.StrikeoutThick
			}
		}
		cv.drawDecorationLine(b.X, b.X+b.W, y, thick, d.style, d.color, b.X-cv.lineX)
	}
}

// drawDecorationLine draws a text decoration like WeasyPrint's draw_line;
// offset is the distance from the start of the line box, so that patterns
// continue between boxes.
func (cv *canvas) drawDecorationLine(x0, x1, y, thick float64, style string, color css.Color, offset float64) {
	if x1 <= x0 {
		return
	}
	c := cv.c
	c.Save()
	cv.setStroke(color)
	switch style {
	case "dashed":
		c.SetDash([]float64{5 * thick}, offset)
	case "dotted":
		c.SetLineCap(1)
		c.SetDash([]float64{0, 2 * thick}, offset)
	}
	switch style {
	case "double":
		c.SetLineWidth(thick / 3)
		c.MoveTo(x0, y-thick/3)
		c.LineTo(x1, y-thick/3)
		c.MoveTo(x0, y+thick/3)
		c.LineTo(x1, y+thick/3)
	case "wavy":
		r := 0.75 * thick
		c.Rect(x0, y-2*r, x1-x0, 4*r)
		c.Clip()
		c.SetLineWidth(thick)
		x := x0 - offset
		c.MoveTo(x, y)
		up := 1.0
		for x < x1 {
			c.CurveTo(x+r/2, y+up*r, x+3*r/2, y+up*r, x+2*r, y)
			x += 2 * r
			up = -up
		}
	default:
		c.SetLineWidth(thick)
		c.MoveTo(x0, y)
		c.LineTo(x1, y)
	}
	c.Stroke()
	c.Restore()
}

func (cv *canvas) drawLeader(b *layout.Box) {
	if b.W <= 0 || b.Leader == "" || b.Style.Kw(css.PVisibility) != "visible" {
		return
	}
	spec := layout.SpecFor(b.Style)
	sh := cv.r.shaper.Shape(b.Leader, spec)
	w := sh.Pos[len(sh.Runes)]
	if w <= 0 {
		return
	}
	// Leader patterns are aligned on the end of the line, and only whole
	// patterns are drawn.
	anchor := b.X + b.W + b.LeaderAnchor
	start := anchor - math.Floor((anchor-b.X)/w+1e-9)*w
	color := b.Style.Color(css.PColor)
	m := cv.r.shaper.PrimaryMetrics(spec)
	lh := b.Style.LineHeight(m.Ascent + m.Descent)
	baseline := b.Y + b.MT + (b.H+(lh-(m.Ascent+m.Descent)))/2
	baseline = b.Y + b.Baseline
	for x := start; x+w <= b.X+b.W+1e-6; x += w {
		cv.drawGlyphs(sh.Runes, sh.Glyphs, nil, x, baseline, color)
	}
}

func isIgnorable(r rune) bool {
	return r == '\n' || r == '\r' || r == 0xad || (r >= 0x200b && r <= 0x200f) || r == 0x2060 || r == 0xfeff
}

// visualOrder reorders the glyphs of a text box for display with the
// Unicode bidirectional algorithm, the base direction being the direction
// of the box (as Pango does for each WeasyPrint text box), and mirrors
// glyphs on right-to-left levels.
func visualOrder(b *layout.Box, glyphs []text.Glyph, advances []float64) ([]text.Glyph, []float64) {
	if len(glyphs) == 0 {
		return glyphs, advances
	}
	runes := b.Shaped.Runes[b.Start:b.End]
	base := 0
	if b.Style.Kw(css.PDirection) == "rtl" {
		base = 1
	}
	if base == 0 && !bidi.HasRTL(runes) {
		return glyphs, advances
	}
	levels := bidi.Levels(runes, base)
	gl := make([]uint8, len(glyphs))
	for i, g := range glyphs {
		if k := g.Start - b.Start; k >= 0 && k < len(levels) {
			gl[i] = levels[k]
		}
	}
	order := bidi.Reorder(gl)
	og := make([]text.Glyph, len(glyphs))
	oa := make([]float64, len(glyphs))
	for v, l := range order {
		g := glyphs[l]
		if gl[l]%2 == 1 && g.Face != nil && g.End-g.Start == 1 {
			if m, ok := bidi.Mirror(b.Shaped.Runes[g.Start]); ok {
				if id := g.Face.Font.GlyphIndex(m); id != 0 {
					g.ID = id
				}
			}
		}
		og[v] = g
		oa[v] = advances[l]
	}
	return og, oa
}

type colorGlyphKey struct {
	font *fonts.Font
	id   uint16
}

// colorGlyph returns the decoded color bitmap of a glyph, or nil.
func (r *Renderer) colorGlyph(f *fonts.Font, id uint16) *images.Image {
	k := colorGlyphKey{f, id}
	if im, ok := r.colorGlyphs[k]; ok {
		return im
	}
	var im *images.Image
	if data := f.GlyphPNG(id); data != nil {
		im, _ = images.Decode(data, "image/png", "")
	}
	r.colorGlyphs[k] = im
	return im
}

// ellipsize cuts the end of the text so that it and a "…" fit in width, like
// Pango's end ellipsization used by WeasyPrint for text-overflow: ellipsis.
func (cv *canvas) ellipsize(b *layout.Box, glyphs []text.Glyph, advances []float64, width float64) ([]text.Glyph, []float64, []rune) {
	ell := cv.r.shaper.Shape("\u2026", b.Spec)
	ew := ell.Pos[len(ell.Runes)]
	used := 0.0
	n := 0
	for n < len(glyphs) && used+advances[n]+ew <= width+1e-6 {
		used += advances[n]
		n++
	}
	// Keep whole clusters.
	for n > 0 && n < len(glyphs) && glyphs[n].Start == glyphs[n-1].Start {
		n--
	}
	out := append([]text.Glyph{}, glyphs[:n]...)
	outAdv := append([]float64{}, advances[:n]...)
	end := b.End
	runes := append(append([]rune{}, b.Shaped.Runes[:end]...), '\u2026')
	for _, g := range ell.Glyphs {
		g.Start, g.End = end, end+1
		out = append(out, g)
		outAdv = append(outAdv, g.Advance)
	}
	return out, outAdv, runes
}

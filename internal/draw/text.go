package draw

import (
	"math"

	"github.com/esoteric-programmer/weasyprint-go/internal/css"
	"github.com/esoteric-programmer/weasyprint-go/internal/fonts"
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
	hyphenW := 0.0
	var hsh *text.Shaped
	if b.Hyphen != "" {
		hsh = cv.r.shaper.Shape(b.Hyphen, b.Spec)
		hyphenW = hsh.Pos[len(hsh.Runes)]
	}
	if tabs > 0 {
		extra := (b.W - hyphenW - total) / float64(tabs)
		for i, g := range glyphs {
			if sh.Runes[g.Start] == '\t' {
				advances[i] += extra
			}
		}
	}
	baseline := b.Y + b.Baseline
	// Text shadows.
	if shadows, ok := b.Style.Get(css.PTextShadow).([]css.Shadow); ok {
		for i := len(shadows) - 1; i >= 0; i-- {
			s := shadows[i]
			col, ok := s.Color.(css.Color)
			if !ok {
				col = color
			}
			cv.drawGlyphs(sh.Runes, glyphs, advances, b.X+s.X.V, baseline+s.Y.V, col)
		}
	}
	// Underlines and overlines are drawn below the text, line-through above.
	cv.drawDecorations(b, decos, baseline, false)
	x := cv.drawGlyphs(sh.Runes, glyphs, advances, b.X, baseline, color)
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
		cv.drawDecorationLine(b.X, b.X+b.W, y, thick, d.style, d.color)
	}
}

func (cv *canvas) drawDecorationLine(x0, x1, y, thick float64, style string, color css.Color) {
	if x1 <= x0 {
		return
	}
	c := cv.c
	c.Save()
	cv.setStroke(color)
	c.SetLineWidth(thick)
	switch style {
	case "double":
		c.MoveTo(x0, y-thick)
		c.LineTo(x1, y-thick)
		c.MoveTo(x0, y+thick)
		c.LineTo(x1, y+thick)
		c.SetLineWidth(thick * 0.75)
	case "dotted":
		c.SetLineCap(1)
		c.SetDash([]float64{0, 2 * thick}, 0)
		c.MoveTo(x0+thick/2, y)
		c.LineTo(x1, y)
	case "dashed":
		c.SetDash([]float64{3 * thick, 2 * thick}, 0)
		c.MoveTo(x0, y)
		c.LineTo(x1, y)
	case "wavy":
		amp := thick * 1.5
		step := thick * 3
		c.MoveTo(x0, y)
		up := true
		for x := x0; x < x1; x += step {
			nx := math.Min(x+step, x1)
			dy := amp
			if up {
				dy = -amp
			}
			c.CurveTo(x+step/3, y+dy, nx-step/3, y+dy, nx, y)
			up = !up
		}
	default:
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
	// Align leader glyphs on a global grid so that leaders line up.
	start := math.Ceil(b.X/w) * w
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

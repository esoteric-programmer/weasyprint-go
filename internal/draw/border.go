package draw

import (
	"math"

	"github.com/esoteric-programmer/weasyprint-go/internal/css"
	"github.com/esoteric-programmer/weasyprint-go/internal/layout"
)

func rgbToHSV(c css.Color) (float64, float64, float64) {
	mx := math.Max(c.R, math.Max(c.G, c.B))
	mn := math.Min(c.R, math.Min(c.G, c.B))
	v := mx
	if mx == mn {
		return 0, 0, v
	}
	s := (mx - mn) / mx
	rc, gc, bc := (mx-c.R)/(mx-mn), (mx-c.G)/(mx-mn), (mx-c.B)/(mx-mn)
	var h float64
	switch {
	case c.R == mx:
		h = bc - gc
	case c.G == mx:
		h = 2 + rc - bc
	default:
		h = 4 + gc - rc
	}
	h = math.Mod(h/6, 1)
	if h < 0 {
		h++
	}
	return h, s, v
}

func hsvToRGB(h, s, v float64, a float64) css.Color {
	if s == 0 {
		return css.Color{R: v, G: v, B: v, A: a}
	}
	i := math.Floor(h * 6)
	f := h*6 - i
	p, q, t := v*(1-s), v*(1-s*f), v*(1-s*(1-f))
	switch int(i) % 6 {
	case 0:
		return css.Color{R: v, G: t, B: p, A: a}
	case 1:
		return css.Color{R: q, G: v, B: p, A: a}
	case 2:
		return css.Color{R: p, G: v, B: t, A: a}
	case 3:
		return css.Color{R: p, G: q, B: v, A: a}
	case 4:
		return css.Color{R: t, G: p, B: v, A: a}
	}
	return css.Color{R: v, G: p, B: q, A: a}
}

// darken and lighten follow WeasyPrint's HSV-based 3D border colors.
func darken(c css.Color) css.Color {
	h, s, v := rgbToHSV(c)
	return hsvToRGB(h, s/1.25, v/1.5, c.A)
}

func lighten(c css.Color) css.Color {
	h, s, v := rgbToHSV(c)
	v = 1 - (1-v)/1.5
	if s != 0 {
		s = 1 - (1-s)/1.25
	}
	return hsvToRGB(h, s, v, c.A)
}

type sideInfo struct {
	width float64
	style string
	color css.Color
}

func (cv *canvas) fillRing(outer rect, or [4][2]float64, w [4]float64) {
	inner := rect{outer.x + w[3], outer.y + w[0], outer.w - w[1] - w[3], outer.h - w[0] - w[2]}
	pathRounded(cv.c, outer, or)
	if inner.w > 0 && inner.h > 0 {
		pathRounded(cv.c, inner, shrinkRadii(or, w[0], w[1], w[2], w[3]))
	}
	cv.c.FillEvenOdd()
}

func (cv *canvas) drawBorders(b *layout.Box) {
	if b.Style == nil {
		return
	}
	widths := [4]float64{b.BT, b.BR, b.BB, b.BL}
	if widths == [4]float64{} {
		return
	}
	if b.Style.Kw(css.PVisibility) != "visible" {
		return
	}
	var sides [4]sideInfo
	for i := 0; i < 4; i++ {
		sides[i] = sideInfo{widths[i], b.Style.BorderStyle(i), b.Style.BorderColor(i)}
	}
	cv.drawBorderSides(borderRect(b), outerRadii(b), sides)
}

// drawBorderSides paints the borders of a box like WeasyPrint's
// draw_border: rounded rings for simple uniform borders, stroked lines
// for uniform dotted and dashed borders without radii, and otherwise each
// side clipped to its segment.
func (cv *canvas) drawBorderSides(outer rect, or [4][2]float64, sides [4]sideInfo) {
	var widths [4]float64
	styles := [4]string{}
	any := false
	for i, s := range sides {
		widths[i] = s.width
		if s.style == "none" || s.style == "hidden" {
			widths[i] = 0
		}
		if widths[i] > 0 {
			any = true
		}
		styles[i] = s.style
		if s.color.IsTransparent() {
			styles[i] = ""
		}
	}
	if !any {
		return
	}
	sameStyle, sameColor, sameWidth, four := true, true, true, true
	for i := 0; i < 4; i++ {
		if styles[i] != styles[0] {
			sameStyle = false
		}
		if sides[i].color != sides[0].color {
			sameColor = false
		}
		if widths[i] != widths[0] {
			sameWidth = false
		}
		if widths[i] == 0 {
			four = false
		}
	}
	if sameStyle && sameColor && four && (styles[0] == "solid" || styles[0] == "double") {
		cv.c.Save()
		cv.drawRoundedBorder(outer, or, widths, styles[0], [2]css.Color{sides[0].color})
		cv.c.Restore()
		return
	}
	if sameStyle && sameColor && four && sameWidth && !hasRadii(or) && (styles[0] == "dotted" || styles[0] == "dashed") {
		cv.c.Save()
		cv.drawDottedDashed(outer, styles[0], widths[0], sides[0].color)
		cv.c.Restore()
		return
	}
	// Sides are drawn one by one, in WeasyPrint's order.
	for _, i := range []int{2, 3, 1, 0} {
		if widths[i] <= 0 || styles[i] == "" {
			continue
		}
		cv.c.Save()
		clipBorderSegment(cv.c, styles[i], widths[i], i, outer, widths, or)
		cv.drawRoundedBorder(outer, or, widths, styles[i], styledColor(styles[i], sides[i].color, i))
		cv.c.Restore()
	}
}

// styledColor returns the colors of inset, outset, ridge and groove
// borders (two colors, inner half first, for ridge and groove).
func styledColor(style string, c css.Color, side int) [2]css.Color {
	topLeft := side == 0 || side == 3
	switch style {
	case "inset", "outset":
		if topLeft != (style == "inset") {
			return [2]css.Color{lighten(c)}
		}
		return [2]css.Color{darken(c)}
	case "ridge", "groove":
		if topLeft != (style == "ridge") {
			return [2]css.Color{lighten(c), darken(c)}
		}
		return [2]css.Color{darken(c), lighten(c)}
	}
	return [2]css.Color{c}
}

// drawRoundedBorder fills the border ring (WeasyPrint's draw_rounded_box).
func (cv *canvas) drawRoundedBorder(outer rect, or [4][2]float64, w [4]float64, style string, colors [2]css.Color) {
	ratio := func(f float64) (rect, [4][2]float64) {
		t, r, b, l := w[0]*f, w[1]*f, w[2]*f, w[3]*f
		return rect{outer.x + l, outer.y + t, outer.w - l - r, outer.h - t - b}, shrinkRadii(or, t, r, b, l)
	}
	padding, pr := ratio(1)
	if style == "ridge" || style == "groove" {
		half, hr := ratio(0.5)
		cv.setFill(colors[0])
		pathRounded(cv.c, padding, pr)
		pathRounded(cv.c, half, hr)
		cv.c.FillEvenOdd()
		cv.setFill(colors[1])
		pathRounded(cv.c, half, hr)
		pathRounded(cv.c, outer, or)
		cv.c.FillEvenOdd()
		return
	}
	cv.setFill(colors[0])
	pathRounded(cv.c, padding, pr)
	if style == "double" {
		a, ar := ratio(1.0 / 3)
		b, br := ratio(2.0 / 3)
		pathRounded(cv.c, a, ar)
		pathRounded(cv.c, b, br)
	}
	pathRounded(cv.c, outer, or)
	cv.c.FillEvenOdd()
}

// drawDottedDashed strokes a uniform dotted or dashed border without
// rounded corners, with dots or dashes on each corner.
func (cv *canvas) drawDottedDashed(outer rect, style string, width float64, col css.Color) {
	half := width / 2
	inset := 0.0
	if style == "dotted" {
		inset = half
	}
	dash := func(length float64) []float64 {
		if style == "dotted" {
			count := math.Max(2, math.Floor(length/(2*width))+1)
			return []float64{0, (length - width) / (count - 1)}
		}
		count := math.Floor(length/(6*width)) + 1
		d := length / (2*count - 1)
		return []float64{d, d}
	}
	cv.setStroke(col)
	cv.c.SetLineWidth(width)
	if style == "dotted" {
		cv.c.SetLineCap(1)
	} else {
		cv.c.SetLineCap(0)
	}
	x, y, w, h := outer.x, outer.y, outer.w, outer.h
	for _, s := range [][5]float64{
		{x + inset, y + half, x + w - inset, y + half, w},
		{x + w - half, y + inset, x + w - half, y + h - inset, h},
		{x + inset, y + h - half, x + w - inset, y + h - half, w},
		{x + half, y + inset, x + half, y + h - inset, h},
	} {
		cv.c.SetDash(dash(s[4]), 0)
		cv.c.MoveTo(s[0], s[1])
		cv.c.LineTo(s[2], s[3])
		cv.c.Stroke()
	}
}

func (cv *canvas) drawOutline(b *layout.Box) {
	s := b.Style
	st := s.Kw(css.POutlineStyle)
	w := s.Px(css.POutlineWidth, 0)
	if st == "none" || st == "hidden" || w <= 0 || s.Kw(css.PVisibility) != "visible" {
		return
	}
	if st == "auto" {
		st = "solid"
	}
	off := s.Px(css.POutlineOffset, 0)
	br := borderRect(b)
	outer := rect{br.x - off - w, br.y - off - w, br.w + 2*(off+w), br.h + 2*(off+w)}
	col := s.Color(css.POutlineColor)
	var sides [4]sideInfo
	for i := range sides {
		sides[i] = sideInfo{w, st, col}
	}
	cv.drawBorderSides(outer, [4][2]float64{}, sides)
}

func (cv *canvas) drawBoxShadows(b *layout.Box, inset bool) {
	shadows, ok := b.Style.Get(css.PBoxShadow).([]css.Shadow)
	if !ok {
		return
	}
	br := borderRect(b)
	radii := outerRadii(b)
	for i := len(shadows) - 1; i >= 0; i-- {
		sh := shadows[i]
		if sh.Inset != inset {
			continue
		}
		col, ok := sh.Color.(css.Color)
		if !ok || col.IsTransparent() {
			continue
		}
		dx, dy, blur, spread := sh.X.V, sh.Y.V, sh.Blur.V, sh.Spread.V
		cv.c.Save()
		if !inset {
			// Clip out the border box.
			cv.c.Rect(-1e5, -1e5, 2e5, 2e5)
			pathRounded(cv.c, br, radii)
			cv.c.ClipEvenOdd()
			steps := 1
			if blur > 0 {
				steps = 6
			}
			for k := 0; k < steps; k++ {
				grow := spread
				a := col.A
				if steps > 1 {
					t := (float64(k) + 0.5) / float64(steps)
					grow = spread + blur*(t-0.5)
					a = col.A / float64(steps) * 1.6
					if a > col.A {
						a = col.A
					}
				}
				rc := rect{br.x + dx - grow, br.y + dy - grow, br.w + 2*grow, br.h + 2*grow}
				if rc.w <= 0 || rc.h <= 0 {
					continue
				}
				r := radii
				for j := range r {
					r[j][0] = math.Max(0, r[j][0]+grow)
					r[j][1] = math.Max(0, r[j][1]+grow)
				}
				cv.setFill(css.Color{R: col.R, G: col.G, B: col.B, A: a})
				pathRounded(cv.c, rc, r)
				cv.c.Fill()
			}
		} else {
			pr := paddingRect(b)
			pathRounded(cv.c, pr, innerRadii(b, radii))
			cv.c.Clip()
			inner := rect{pr.x + dx + spread, pr.y + dy + spread, pr.w - 2*spread, pr.h - 2*spread}
			cv.setFill(col)
			cv.c.Rect(pr.x-1e4, pr.y-1e4, pr.w+2e4, pr.h+2e4)
			if inner.w > 0 && inner.h > 0 {
				cv.c.Rect(inner.x, inner.y, inner.w, inner.h)
			}
			cv.c.FillEvenOdd()
		}
		cv.c.Restore()
	}
}

// drawCollapsedBorders draws the borders of a table in the collapsing
// border model.
func (cv *canvas) drawCollapsedBorders(t *layout.Box) {
	if t.Style.Kw(css.PVisibility) != "visible" {
		return
	}
	for _, g := range t.Children {
		if g.Kind != layout.KRowGroup {
			continue
		}
		for _, row := range g.Children {
			for _, cell := range row.Children {
				edges := t.Collapse.CellEdges(cell)
				br := borderRect(cell)
				// Cell border boxes start on grid lines: center the borders.
				outer := rect{br.x - edges[3].Width/2, br.y - edges[0].Width/2,
					br.w + (edges[3].Width+edges[1].Width)/2, br.h + (edges[0].Width+edges[2].Width)/2}
				var sides [4]sideInfo
				for i, e := range edges {
					sides[i] = sideInfo{e.Width, e.Style, e.Color}
				}
				// Each shared edge would be drawn twice; draw top/left always
				// and bottom/right only on the table edge or when it differs.
				cv.drawBorderSides(outer, [4][2]float64{}, sides)
			}
		}
	}
}

// drawColumnRules draws column rules between the columns of a multi-column
// container.
func (cv *canvas) drawColumnRules(b *layout.Box) {
	s := b.Style
	st := s.Kw(css.PColumnRuleStyle)
	w := s.Px(css.PColumnRuleWidth, 0)
	if st == "none" || st == "hidden" || w <= 0 || len(b.Children) < 2 {
		return
	}
	col := s.Color(css.PColumnRuleColor)
	for i := 1; i < len(b.Children); i++ {
		prev, cur := b.Children[i-1], b.Children[i]
		if !prev.IsColumn || !cur.IsColumn {
			continue
		}
		x := (prev.X + prev.MarginWidth() + cur.X) / 2
		top, h := b.ContentY(), b.H
		var sides [4]sideInfo
		sides[3] = sideInfo{w, st, col}
		cv.drawBorderSides(rect{x - w/2, top, w, h}, [4][2]float64{}, sides)
	}
}

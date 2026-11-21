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

// clipSide clips to the trapezoid of a border side (0 top, 1 right, 2
// bottom, 3 left).
func (cv *canvas) clipSide(outer rect, w [4]float64, side int) {
	x0, y0, x1, y1 := outer.x, outer.y, outer.x+outer.w, outer.y+outer.h
	ix0, iy0, ix1, iy1 := x0+w[3], y0+w[0], x1-w[1], y1-w[2]
	c := cv.c
	switch side {
	case 0:
		c.MoveTo(x0, y0)
		c.LineTo(x1, y0)
		c.LineTo(ix1, iy0)
		c.LineTo(ix0, iy0)
	case 1:
		c.MoveTo(x1, y0)
		c.LineTo(x1, y1)
		c.LineTo(ix1, iy1)
		c.LineTo(ix1, iy0)
	case 2:
		c.MoveTo(x1, y1)
		c.LineTo(x0, y1)
		c.LineTo(ix0, iy1)
		c.LineTo(ix1, iy1)
	case 3:
		c.MoveTo(x0, y1)
		c.LineTo(x0, y0)
		c.LineTo(ix0, iy0)
		c.LineTo(ix0, iy1)
	}
	c.ClosePath()
	c.Clip()
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

func (cv *canvas) drawBorderSides(outer rect, or [4][2]float64, sides [4]sideInfo) {
	var widths [4]float64
	for i, s := range sides {
		if s.style != "none" && s.style != "hidden" && !s.color.IsTransparent() {
			widths[i] = s.width
		}
	}
	allWidths := [4]float64{sides[0].width, sides[1].width, sides[2].width, sides[3].width}
	// Fast path: identical solid sides.
	same := true
	for i := 1; i < 4; i++ {
		if sides[i].style != sides[0].style || sides[i].color != sides[0].color || widths[i] == 0 {
			same = false
		}
	}
	if same && sides[0].style == "solid" && widths[0] > 0 {
		cv.c.Save()
		cv.setFill(sides[0].color)
		cv.fillRing(outer, or, allWidths)
		cv.c.Restore()
		return
	}
	for i := 0; i < 4; i++ {
		s := sides[i]
		if widths[i] <= 0 {
			continue
		}
		cv.c.Save()
		cv.clipSide(outer, allWidths, i)
		cv.drawSide(outer, or, allWidths, i, s)
		cv.c.Restore()
	}
}

func (cv *canvas) drawSide(outer rect, or [4][2]float64, w [4]float64, side int, s sideInfo) {
	switch s.style {
	case "solid":
		cv.setFill(s.color)
		cv.fillRing(outer, or, w)
	case "double":
		cv.setFill(s.color)
		third := [4]float64{w[0] / 3, w[1] / 3, w[2] / 3, w[3] / 3}
		cv.fillRing(outer, or, third)
		inner := rect{outer.x + 2*third[3], outer.y + 2*third[0], outer.w - 2*third[1] - 2*third[3], outer.h - 2*third[0] - 2*third[2]}
		cv.fillRing(inner, shrinkRadii(or, 2*third[0], 2*third[1], 2*third[2], 2*third[3]), third)
	case "groove", "ridge":
		half := [4]float64{w[0] / 2, w[1] / 2, w[2] / 2, w[3] / 2}
		inner, outerCol := darken(s.color), lighten(s.color)
		if (side == 0 || side == 3) != (s.style == "ridge") {
			inner, outerCol = lighten(s.color), darken(s.color)
		}
		cv.setFill(outerCol)
		cv.fillRing(outer, or, half)
		in := rect{outer.x + half[3], outer.y + half[0], outer.w - half[1] - half[3], outer.h - half[0] - half[2]}
		cv.setFill(inner)
		cv.fillRing(in, shrinkRadii(or, half[0], half[1], half[2], half[3]), half)
	case "inset", "outset":
		col := darken(s.color)
		if (side == 0 || side == 3) != (s.style == "inset") {
			col = lighten(s.color)
		}
		cv.setFill(col)
		cv.fillRing(outer, or, w)
	case "dashed", "dotted":
		width := w[side]
		cv.setStroke(s.color)
		cv.c.SetLineWidth(width)
		half := width / 2
		inset := 0.0
		if s.style == "dotted" {
			inset = half
			cv.c.SetLineCap(1)
		}
		x0, y0, x1, y1 := outer.x, outer.y, outer.x+outer.w, outer.y+outer.h
		var ax, ay, bx, by, length float64
		switch side {
		case 0:
			ax, ay, bx, by, length = x0+inset, y0+half, x1-inset, y0+half, outer.w
		case 1:
			ax, ay, bx, by, length = x1-half, y0+inset, x1-half, y1-inset, outer.h
		case 2:
			ax, ay, bx, by, length = x0+inset, y1-half, x1-inset, y1-half, outer.w
		case 3:
			ax, ay, bx, by, length = x0+half, y0+inset, x0+half, y1-inset, outer.h
		}
		if s.style == "dotted" {
			count := math.Max(2, math.Floor(length/(2*width))+1)
			cv.c.SetDash([]float64{0, (length - width) / (count - 1)}, 0)
		} else {
			count := math.Floor(length/(6*width)) + 1
			d := length / (2*count - 1)
			cv.c.SetDash([]float64{d, d}, 0)
		}
		if hasRadii(or) {
			half4 := [4]float64{w[0] / 2, w[1] / 2, w[2] / 2, w[3] / 2}
			mid := rect{outer.x + half4[3], outer.y + half4[0], outer.w - half4[1] - half4[3], outer.h - half4[0] - half4[2]}
			pathRounded(cv.c, mid, shrinkRadii(or, half4[0], half4[1], half4[2], half4[3]))
		} else {
			cv.c.MoveTo(ax, ay)
			cv.c.LineTo(bx, by)
		}
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

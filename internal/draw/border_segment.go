package draw

import (
	"math"

	"github.com/esoteric-programmer/weasyprint-go/internal/pdf"
)

// clipBorderSegment clips to one side of a border, and for dotted and
// dashed borders to the dots or dashes of that side. It is a port of
// WeasyPrint's clip_border_segment.
//
// side is 0 top, 1 right, 2 bottom, 3 left; widths are the border widths
// (top, right, bottom, left) and radii the outer corner radii (top-left,
// top-right, bottom-right, bottom-left).
func clipBorderSegment(c *pdf.Content, style string, width float64, side int, bb rect, widths [4]float64, radii [4][2]float64) {
	bbx, bby, bbw, bbh := bb.x, bb.y, bb.w, bb.h
	tlh, tlv := radii[0][0], radii[0][1]
	trh, trv := radii[1][0], radii[1][1]
	brh, brv := radii[2][0], radii[2][1]
	blh, blv := radii[3][0], radii[3][1]
	bt, br, bbw_, bl := widths[0], widths[1], widths[2], widths[3]

	// transitionPoint returns the point used for the transition between
	// two sides, and whether it is inside the rounded padding box.
	transitionPoint := func(x1, y1, x2, y2 float64) (float64, float64, bool) {
		if math.Abs(x1) > math.Abs(x2) && math.Abs(y1) > math.Abs(y2) {
			return x1, y1, true
		}
		return x2, y2, false
	}
	// cornerHalfLength approximates the length of half an elliptic corner
	// (Ramanujan).
	cornerHalfLength := func(a, b float64) float64 {
		if a+b == 0 {
			return 0
		}
		x := (a - b) / (a + b)
		return math.Pi / 8 * (a + b) * (1 + 3*x*x/(10+math.Sqrt(4-3*x*x)))
	}
	drawDash := func(cx, cy, w, h, r float64) {
		if style == "dotted" {
			ratio := r / math.Sqrt(math.Pi)
			c.MoveTo(cx+r, cy)
			c.CurveTo(cx+r, cy+ratio, cx+ratio, cy+r, cx, cy+r)
			c.CurveTo(cx-ratio, cy+r, cx-r, cy+ratio, cx-r, cy)
			c.CurveTo(cx-r, cy-ratio, cx-ratio, cy-r, cx, cy-r)
			c.CurveTo(cx+ratio, cy-r, cx+r, cy-ratio, cx+r, cy)
			c.ClosePath()
		} else if style == "dashed" {
			c.Rect(cx-w/2, cy-h/2, w, h)
		}
	}

	var px1, py1, px2, py2 float64
	var rounded1, rounded2 bool
	var way, angle, mainOffset float64
	switch side {
	case 0:
		px1, py1, rounded1 = transitionPoint(tlh, tlv, bl, bt)
		px2, py2, rounded2 = transitionPoint(-trh, trv, -br, bt)
		width, way, angle, mainOffset = bt, 1, 1, bby
	case 1:
		px1, py1, rounded1 = transitionPoint(-trh, trv, -br, bt)
		px2, py2, rounded2 = transitionPoint(-brh, -brv, -br, -bbw_)
		width, way, angle, mainOffset = br, 1, 2, bbx+bbw
	case 2:
		px1, py1, rounded1 = transitionPoint(blh, -blv, bl, -bbw_)
		px2, py2, rounded2 = transitionPoint(-brh, -brv, -br, -bbw_)
		width, way, angle, mainOffset = bbw_, -1, 3, bby+bbh
	default:
		px1, py1, rounded1 = transitionPoint(tlh, tlv, bl, bt)
		px2, py2, rounded2 = transitionPoint(blh, -blv, bl, -bbw_)
		width, way, angle, mainOffset = bl, -1, 4, bbx
	}
	horizontal := side == 0 || side == 2

	var a1, b1, a2, b2, lineLength, length float64
	if horizontal {
		a1, b1 = px1-bl/2, way*py1-width/2
		a2, b2 = -px2-br/2, way*py2-width/2
		lineLength = bbw - px1 + px2
		length = bbw
		c.MoveTo(bbx+bbw, mainOffset)
		c.LineTo(bbx, mainOffset)
		c.LineTo(bbx+px1, mainOffset+py1)
		c.LineTo(bbx+bbw+px2, mainOffset+py2)
	} else {
		a1, b1 = -way*px1-width/2, py1-bt/2
		a2, b2 = -way*px2-width/2, -py2-bbw_/2
		lineLength = bbh - py1 + py2
		length = bbh
		c.MoveTo(mainOffset, bby+bbh)
		c.LineTo(mainOffset, bby)
		c.LineTo(mainOffset+px1, bby+py1)
		c.LineTo(mainOffset+px2, bby+bbh+py2)
	}
	c.ClosePath()

	if style != "dotted" && style != "dashed" {
		c.ClipEvenOdd()
		return
	}
	dash := width
	if style == "dashed" {
		dash = 3 * width
	}
	c.ClipEvenOdd()
	if dash <= 0 {
		return
	}

	if rounded1 || rounded2 {
		// At least one of the two corners is rounded.
		chl1 := cornerHalfLength(a1, b1)
		chl2 := cornerHalfLength(a2, b2)
		length = lineLength + chl1 + chl2
		dashLength := math.RoundToEven(length / dash)
		dl := dashLength + math.Mod(dashLength, 2)
		if rounded1 && rounded2 {
			// 2x dashes.
			if dl == 0 {
				return
			}
			dash = length / dl
		} else {
			// 2x - 1/2 dashes.
			if dl-0.5 == 0 {
				return
			}
			dash = length / (dl - 0.5)
		}
		dashes1 := int(math.Ceil((chl1 - dash/2) / dash))
		dashes2 := int(math.Ceil((chl2 - dash/2) / dash))
		line := int(math.Floor(lineLength / dash))

		drawDashes := func(dashes, line int, way, x, y, px, py, chl float64) (int, float64) {
			if style == "dotted" {
				if dashes <= 0 {
					return line + 1, -1
				} else if dashes == 1 {
					return line + 1, -0.5
				}
				i := 1
				for ; i < dashes; i += 2 {
					a := ((2*angle - way) + float64(i)*way*dash/chl) / 4 * math.Pi
					cx, cy := x, y
					if horizontal {
						cy = mainOffset
					} else {
						cx = mainOffset
					}
					drawDash(cx+px-(math.Abs(px)-dash/2)*math.Cos(a), cy+py-(math.Abs(py)-dash/2)*math.Sin(a), 0, 0, dash/2)
				}
				i -= 2 // last value taken by the loop
				nextA := ((2*angle - way) + float64(i+2)*way*dash/chl) / 4 * math.Pi
				offset := nextA/math.Pi*2 - angle
				if dashes%2 == 1 {
					line++
				}
				return line, offset
			}
			if dashes <= 0 {
				return line + 1, -1.0 / 3
			}
			var angle1, angle2 float64
			for k := 0; k < dashes; k += 2 {
				i := float64(k) + 0.5 // half dash
				angle1 = ((2*angle - way) + i*way*dash/chl) / 4 * math.Pi
				raw2 := ((2*angle - way) + (i+1)*way*dash/chl) / 4 * math.Pi
				limit := angle * math.Pi / 2
				if way > 0 {
					angle2 = math.Min(raw2, limit)
				} else {
					angle2 = math.Max(raw2, limit)
				}
				if horizontal {
					c.MoveTo(x+px, mainOffset+py)
					c.LineTo(x+px-way*px/math.Tan(angle2), mainOffset)
					c.LineTo(x+px-way*px/math.Tan(angle1), mainOffset)
				} else {
					c.MoveTo(mainOffset+px, y+py)
					c.LineTo(mainOffset, y+py+way*py*math.Tan(angle2))
					c.LineTo(mainOffset, y+py+way*py*math.Tan(angle1))
				}
				c.ClosePath()
				if angle2 == limit {
					offset := (angle1 - angle2) / (raw2 - angle1)
					return line + 1, offset
				}
			}
			return line, 1 - ((angle*math.Pi/2 - angle2) / (angle2 - angle1))
		}

		var offset float64
		line, offset = drawDashes(dashes1, line, way, bbx, bby, px1, py1, chl1)
		line, _ = drawDashes(dashes2, line, -way, bbx+bbw, bby+bbh, px2, py2, chl2)

		if lineLength > 1e-6 {
			for k := 0; k < line; k += 2 {
				i := float64(k) + offset
				var x1, x2, y1, y2 float64
				if horizontal {
					x1 = bbx + px1 + i*dash
					x2 = bbx + px1 + (i+1)*dash
					y1 = mainOffset
					if way < 0 {
						y1 -= width
					}
					y2 = y1 + width
				} else {
					y1 = bby + py1 + i*dash
					y2 = bby + py1 + (i+1)*dash
					x1 = mainOffset
					if way > 0 {
						x1 -= width
					}
					x2 = x1 + width
				}
				drawDash(x1+(x2-x1)/2, y1+(y2-y1)/2, x2-x1, y2-y1, width/2)
			}
		}
	} else {
		// No rounded corner: dashes on corners and evenly spaced between.
		spaces := math.Floor(length / dash / 2)
		n := spaces + 1
		var space float64
		if style == "dotted" {
			dash = width
			if spaces > 0 {
				space = (length - n*dash) / spaces
			}
		} else {
			dash = length / (spaces + n)
			if dash == 0 {
				dash = 1
			}
			space = dash
		}
		for i := 0; i <= int(n); i++ {
			advance := float64(i) * (space + dash)
			var cx, cy, dw, dh float64
			switch side {
			case 0:
				cx, cy, dw, dh = bbx+advance+dash/2, bby+width/2, dash, width
			case 1:
				cx, cy, dw, dh = bbx+bbw-width/2, bby+advance+dash/2, width, dash
			case 2:
				cx, cy, dw, dh = bbx+advance+dash/2, bby+bbh-width/2, dash, width
			default:
				cx, cy, dw, dh = bbx+width/2, bby+advance+dash/2, width, dash
			}
			drawDash(cx, cy, dw, dh, dash/2)
		}
	}
	c.ClipEvenOdd()
}

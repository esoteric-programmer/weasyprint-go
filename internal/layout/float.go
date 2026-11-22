package layout

import (
	"math"

	"github.com/esoteric-programmer/weasyprint-go/internal/css"
)

func clearMatches(clear, float string) bool {
	return clear == float || clear == "both"
}

// getClearance returns the clearance of a box, if any.
func (c *Context) getClearance(box *Box, collapsedMargin float64) (float64, bool) {
	if box.Style == nil || box.Kind == KLine || box.Kind == KText {
		return 0, false
	}
	clear := box.Style.Kw(css.PClear)
	if clear == "none" || clear == "" {
		return 0, false
	}
	found := false
	clearance := 0.0
	hyp := box.Y + collapsedMargin
	for _, s := range c.excluded {
		if clearMatches(clear, s.Style.Kw(css.PFloat)) {
			y, h := s.Y, s.MarginHeight()
			if hyp < y+h {
				clearance = math.Max(clearance, y+h-hyp)
				found = true
			}
		}
	}
	return clearance, found
}

// avoidCollisions finds a position for box avoiding floats.
func (c *Context) avoidCollisions(box *Box, cbi cbInfo, cb *Box, outer bool) (float64, float64, float64) {
	posY := box.Y
	if !outer {
		posY = box.BorderBoxY()
	}
	boxW, boxH := box.BorderWidth(), box.BorderHeight()
	if outer {
		boxW, boxH = box.MarginWidth(), box.MarginHeight()
	}
	cbX := 0.0
	if cb != nil {
		cbX = cb.ContentX()
	}
	if box.BorderHeight() == 0 && box.IsFloated() {
		return 0, posY, cbi.W
	}
	var maxLeft, maxRight float64
	for {
		var colliding []*Box
		for _, s := range c.excluded {
			sy, sh := s.Y, s.MarginHeight()
			if (sy < posY && posY < sy+sh) || (sy < posY+boxH && posY+boxH < sy+sh) || (sy >= posY && sy+sh <= posY+boxH) {
				colliding = append(colliding, s)
			}
		}
		maxLeft = cbX
		maxRight = cbX + cbi.W
		if !outer {
			maxLeft += nanTo0(box.ML)
			maxRight -= nanTo0(box.MR)
		}
		hasBounds := false
		for _, s := range colliding {
			switch s.Style.Kw(css.PFloat) {
			case "left":
				maxLeft = math.Max(maxLeft, s.X+s.MarginWidth())
				hasBounds = true
			case "right":
				maxRight = math.Min(maxRight, s.X)
				hasBounds = true
			}
		}
		if hasBounds && boxW > maxRight-maxLeft+1e-9 {
			newY := math.Inf(1)
			for _, s := range colliding {
				newY = math.Min(newY, s.Y+s.MarginHeight())
			}
			if newY > posY {
				posY = newY
				continue
			}
		}
		break
	}
	posX := maxLeft
	if box.Style != nil && box.Kind != KLine && !box.IsFloated() && cbi.Style != nil && cbi.Style.Kw(css.PDirection) == "rtl" {
		posX = maxRight - boxW
	}
	avail := maxRight - maxLeft
	if !outer {
		posX -= nanTo0(box.ML)
		posY -= nanTo0(box.MT)
	}
	return posX, posY, avail
}

// floatLayout lays out a floating box and adds it to the exclusions.
func (c *Context) floatLayout(box *Box, cb *Box, cbi cbInfo, abs, fixed *absList, bottomSpace float64) *Box {
	defer c.lineBarrier()()
	resolvePercentages(box, cbi.W)
	box.ML, box.MR, box.MT, box.MB = nanTo0(box.ML), nanTo0(box.MR), nanTo0(box.MT), nanTo0(box.MB)
	if clr, ok := c.getClearance(box, 0); ok {
		box.Y += clr
	}
	box.W = specifiedWidth(box, cbi.W)
	box.H = specifiedHeight(box, cbi)
	var nb *Box
	switch {
	case box.Kind == KReplaced:
		c.replacedWidthHeight(box, cbi)
		nb = box
	default:
		if math.IsNaN(box.W) && box.Kind != KTableWrapper {
			box.W = c.shrinkToFit(box, cbi.W-box.ML-box.MR-box.PL-box.PR-box.BL-box.BR)
		}
		if box.Kind != KTableWrapper {
			mn, mx := minMaxWidth(box, cbi.W)
			box.W = math.Max(mn, math.Min(mx, box.W))
		}
		if box.Kind == KTableWrapper {
			c.tableWrapperWidth(box, cbi)
		}
		if box.Kind == KFlex {
			res := c.flexLayout(box, bottomSpace, nil, cb, cbi, true, abs, fixed)
			nb = res.box
		} else {
			res := c.blockContainerLayout(box, bottomSpace, nil, true, abs, fixed, nil, false, cbi)
			nb = res.box
		}
		if nb == nil {
			nb = box
		}
	}
	nb = c.findFloatPosition(nb, cbi, cb)
	c.excluded = append(c.excluded, nb)
	return nb
}

func (c *Context) findFloatPosition(box *Box, cbi cbInfo, cb *Box) *Box {
	if n := len(c.excluded); n > 0 {
		highest := c.excluded[n-1].Y
		if box.Y < highest {
			box.Translate(0, highest-box.Y)
		}
	}
	x, y, avail := c.avoidCollisions(box, cbi, cb, true)
	if box.Style.Kw(css.PFloat) == "right" {
		x += avail - box.MarginWidth()
	}
	box.Translate(x-box.X, y-box.Y)
	return box
}

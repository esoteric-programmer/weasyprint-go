package layout

import (
	"math"

	"github.com/esoteric-programmer/weasyprint-go/internal/css"
)

// absoluteLayout lays out an absolutely positioned placeholder whose
// containing block is cb (a positioned box, or a page).
func (c *Context) absoluteLayout(ph *Box, cb *Box, fixed *absList) {
	defer c.lineBarrier()()
	if !ph.IsAbsPlaceholder {
		return
	}
	var cbX, cbY, cbW, cbH float64
	if cb.Kind == KPage {
		cbX, cbY, cbW, cbH = cb.ContentX(), cb.ContentY(), cb.W, cb.H
	} else {
		cbX, cbY, cbW, cbH = cb.PaddingBoxX(), cb.PaddingBoxY(), cb.PaddingWidth(), cb.PaddingHeight()
	}
	box := ph.Copy()
	box.IsAbsPlaceholder = false
	staticX, staticY := ph.X, ph.Y
	resolvePercentages(box, cbW)
	s := box.Style
	left, lok := lengthOrAuto(s, css.PLeft, cbW)
	right, rok := lengthOrAuto(s, css.PRight, cbW)
	top, tok := lengthOrAuto(s, css.PTop, cbH)
	bottom, bok := lengthOrAuto(s, css.PBottom, cbH)
	box.W = specifiedWidth(box, cbW)
	box.H = specifiedHeight(box, cbInfo{W: cbW, H: cbH})
	cbi := cbInfo{W: cbW, H: cbH, Style: cb.Style}
	isReplaced := box.Kind == KReplaced
	if isReplaced {
		c.replacedWidthHeight(box, cbi)
	}
	pbW := box.PL + box.PR + box.BL + box.BR
	pbH := box.PT + box.PB + box.BT + box.BB
	wAuto := math.IsNaN(box.W)

	// Horizontal.
	var x float64
	xFromRight := false
	switch {
	case !lok && !rok && wAuto:
		box.ML, box.MR = nanTo0(box.ML), nanTo0(box.MR)
		box.W = c.absShrink(box, cbW-(pbW+box.ML+box.MR))
		x = staticX
		if box.IsOutsideMarker {
			xFromRight = true
		}
	case lok && rok && !wAuto:
		avail := cbW - (right + left + box.W + pbW)
		switch {
		case math.IsNaN(box.ML) && math.IsNaN(box.MR):
			if avail >= 0 {
				box.ML, box.MR = avail/2, avail/2
			} else {
				box.ML, box.MR = 0, avail
			}
		case math.IsNaN(box.ML):
			box.ML = avail - box.MR
		case math.IsNaN(box.MR):
			box.MR = avail - box.ML
		default:
			box.MR = avail + box.MR - box.MR
		}
		x = cbX + left
	default:
		box.ML, box.MR = nanTo0(box.ML), nanTo0(box.MR)
		spacing := pbW + box.ML + box.MR
		switch {
		case !lok && wAuto:
			box.W = c.absShrink(box, cbW-spacing-right)
			x = cbX + cbW - right - spacing - box.W
		case !lok && !rok:
			x = staticX
		case wAuto && !rok:
			box.W = c.absShrink(box, cbW-spacing-left)
			x = cbX + left
		case !lok:
			x = cbX + cbW - right - spacing - box.W
		case wAuto:
			box.W = math.Max(0, cbW-right-left-spacing)
			x = cbX + left
		default:
			x = cbX + left
		}
	}
	if !isReplaced && box.Kind != KTableWrapper {
		mn, mx := minMaxWidth(box, cbW)
		box.W = math.Max(mn, math.Min(mx, box.W))
	}
	if box.Kind == KTableWrapper {
		c.tableWrapperWidth(box, cbi)
	}
	if xFromRight {
		x -= box.MarginWidth()
	}

	// Vertical.
	hAuto := math.IsNaN(box.H)
	box.MT, box.MB = nanTo0(box.MT), nanTo0(box.MB)
	var y float64
	yMode := "top"
	switch {
	case !tok && !bok:
		y = staticY
	case tok && bok && !hAuto:
		avail := cbH - (top + bottom + box.H + pbH + box.MT + box.MB)
		_ = avail
		y = cbY + top
	case tok && bok && hAuto:
		box.H = math.Max(0, cbH-top-bottom-pbH-box.MT-box.MB)
		y = cbY + top
	case tok:
		y = cbY + top
	default:
		// Only bottom: position after layout.
		y = cbY
		yMode = "bottom"
	}

	box.X, box.Y = x, y
	var nb *Box
	switch {
	case isReplaced:
		nb = box
	case box.Kind == KFlex:
		res := c.flexLayout(box, math.Inf(-1), nil, cb, cbi, true, &absList{}, fixed)
		nb = res.box
	default:
		saved := c.saveBottom()
		c.PageBottom = math.Inf(1)
		c.createBFC()
		res := c.blockContainerLayout(box, math.Inf(-1), nil, true, &absList{}, fixed, nil, false, cbi)
		c.finishBFC(res.box)
		c.restoreBottom(saved)
		nb = res.box
	}
	if nb == nil {
		nb = box
	}
	if yMode == "bottom" {
		nb.Translate(0, cbY+cbH-bottom-nb.MarginHeight()-nb.Y)
	}
	*ph = *nb
	ph.IsAbsPlaceholder = false
	ph.AbsSource = ph.Orig
}

func (c *Context) absShrink(box *Box, avail float64) float64 {
	if box.Kind == KReplaced {
		return box.W
	}
	if box.Kind == KTableWrapper {
		return math.NaN()
	}
	return c.shrinkToFit(box, avail)
}

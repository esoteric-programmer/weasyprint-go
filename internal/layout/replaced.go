package layout

import (
	"math"

	"github.com/esoteric-programmer/weasyprint-go/internal/css"
)

// replacedWidthHeight sets the used width and height of a replaced box.
// box.W / box.H hold the specified values (NaN for auto).
func (c *Context) replacedWidthHeight(box *Box, cbi cbInfo) {
	iw, ih, hasSize := box.Image.IntrinsicSize()
	if res, ok := box.Style.Get(css.PImageResolution).(css.Number); ok && res > 0 && box.Image.SVG == nil {
		iw, ih = iw/float64(res), ih/float64(res)
	}
	ratio := box.Image.Ratio()
	w, h := box.W, box.H
	wAuto, hAuto := math.IsNaN(w), math.IsNaN(h)
	switch {
	case wAuto && hAuto:
		if hasSize && iw > 0 {
			w, h = iw, ih
		} else if ratio > 0 {
			w = math.Min(cbi.W, 300)
			h = w / ratio
		} else {
			w, h = 300, 150
		}
		if !hasSize && ratio > 0 && iw > 0 {
			w, h = iw, iw/ratio
		}
	case wAuto:
		if ratio > 0 {
			w = h * ratio
		} else if hasSize {
			w = iw
		} else {
			w = 300
		}
	case hAuto:
		if ratio > 0 {
			h = w / ratio
		} else if hasSize {
			h = ih
		} else {
			h = 150
		}
	}
	// Min/max constraints (CSS 2.1 §10.4).
	minW, maxW := minMaxWidth(box, cbi.W)
	minH, maxH := minMaxHeight(box, cbi)
	if wAuto && hAuto && ratio > 0 {
		w, h = constrainRatio(w, h, minW, maxW, minH, maxH)
	} else {
		w = math.Max(minW, math.Min(maxW, w))
		h = math.Max(minH, math.Min(maxH, h))
		if wAuto && !hAuto && ratio > 0 {
			w = math.Max(minW, math.Min(maxW, h*ratio))
		}
		if hAuto && !wAuto && ratio > 0 {
			h = math.Max(minH, math.Min(maxH, w/ratio))
		}
	}
	box.W, box.H = w, h
}

func constrainRatio(w, h, minW, maxW, minH, maxH float64) (float64, float64) {
	if math.IsInf(maxW, 1) {
		maxW = math.Max(w, minW)
	}
	if math.IsInf(maxH, 1) {
		maxH = math.Max(h, minH)
	}
	maxW = math.Max(minW, maxW)
	maxH = math.Max(minH, maxH)
	switch {
	case w > maxW && h > maxH:
		if maxW/w <= maxH/h {
			return maxW, math.Max(minH, maxW*h/w)
		}
		return math.Max(minW, maxH*w/h), maxH
	case w < minW && h < minH:
		if minW/w <= minH/h {
			return math.Min(maxW, minH*w/h), minH
		}
		return minW, math.Min(maxH, minW*h/w)
	case w < minW && h > maxH:
		return minW, maxH
	case w > maxW && h < minH:
		return maxW, minH
	case w > maxW:
		return maxW, math.Max(maxW*h/w, minH)
	case w < minW:
		return minW, math.Min(minW*h/w, maxH)
	case h > maxH:
		return math.Max(maxH*w/h, minW), maxH
	case h < minH:
		return math.Min(minH*w/h, maxW), minH
	}
	return w, h
}

func (c *Context) blockReplacedLayout(box *Box, cbi cbInfo) *Box {
	box.W = specifiedWidth(box, cbi.W)
	box.H = specifiedHeight(box, cbi)
	c.replacedWidthHeight(box, cbi)
	// Margins (CSS 2.1 §10.3.4).
	w := box.W
	blockLevelWidth(box, cbi.W, false, cbi.Style)
	box.W = w
	box.Baseline = box.MarginHeight()
	return box
}

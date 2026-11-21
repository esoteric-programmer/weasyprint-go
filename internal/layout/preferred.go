package layout

import (
	"math"

	"github.com/esoteric-programmer/weasyprint-go/internal/css"
	"github.com/esoteric-programmer/weasyprint-go/internal/text"
)

// shrinkToFit returns the shrink-to-fit content width.
func (c *Context) shrinkToFit(box *Box, available float64) float64 {
	mn, mx := c.preferredWidths(box)
	return math.Min(math.Max(mn, available), mx)
}

// preferredWidths returns the min-content and max-content widths of the
// content box.
func (c *Context) preferredWidths(box *Box) (float64, float64) {
	return c.minContent(box, false), c.maxContent(box, false)
}

func (c *Context) minContent(box *Box, outer bool) float64 {
	return c.contentWidth(box, outer, true)
}

func (c *Context) maxContent(box *Box, outer bool) float64 {
	return c.contentWidth(box, outer, false)
}

func (c *Context) contentWidth(box *Box, outer, minimum bool) float64 {
	switch box.Kind {
	case KTableWrapper:
		mn, mx := c.tablePreferredWidths(box, outer)
		if minimum {
			return mn
		}
		return mx
	case KReplaced:
		w := c.replacedIntrinsicWidth(box)
		return adjustWidth(box, outer, w)
	case KLine:
		return c.inlineWidth(box, minimum)
	case KInline:
		lw := &Box{Kind: KLine, Style: box.Style, Children: []*Box{box}}
		return c.inlineWidth(lw, minimum)
	case KFlex:
		return c.flexContentWidth(box, outer, minimum)
	case KText:
		lw := &Box{Kind: KLine, Style: box.Style, Children: []*Box{box}}
		return c.inlineWidth(lw, minimum)
	case KCell:
		var w float64
		for _, ch := range box.Children {
			if ch.IsAbsolutelyPositioned() {
				continue
			}
			w = math.Max(w, c.contentWidth(ch, true, minimum))
		}
		if !minimum {
			mn := c.contentWidth(box, outer, true)
			return math.Max(mn, adjustWidth(box, outer, math.Max(w, specifiedPx(box))))
		}
		return adjustWidth(box, outer, math.Max(w, 0))
	}
	// Block containers.
	var width float64
	if l, ok := box.Style.Get(css.PWidth).(css.Length); ok && !l.IsPercent() {
		width = l.V
		switch box.Style.Kw(css.PBoxSizing) {
		case "border-box":
			width -= box.Style.BorderWidth(1) + box.Style.BorderWidth(3) + box.Style.Px(css.PPaddingLeft, 0) + box.Style.Px(css.PPaddingRight, 0)
		case "padding-box":
			width -= box.Style.Px(css.PPaddingLeft, 0) + box.Style.Px(css.PPaddingRight, 0)
		}
		width = math.Max(0, width)
	} else {
		for _, ch := range box.Children {
			if ch.IsAbsolutelyPositioned() || ch.RunningName != "" {
				continue
			}
			width = math.Max(width, c.contentWidth(ch, true, minimum))
		}
	}
	return adjustWidth(box, outer, width)
}

func specifiedPx(box *Box) float64 {
	if l, ok := box.Style.Get(css.PWidth).(css.Length); ok && !l.IsPercent() {
		return l.V
	}
	return 0
}

func minMaxClamp(box *Box, w float64) float64 {
	mn := 0.0
	if l, ok := box.Style.Get(css.PMinWidth).(css.Length); ok && !l.IsPercent() {
		mn = l.V
	}
	mx := math.Inf(1)
	if l, ok := box.Style.Get(css.PMaxWidth).(css.Length); ok && !l.IsPercent() {
		mx = l.V
	}
	return math.Max(mn, math.Min(w, mx))
}

// adjustWidth applies min/max and adds horizontal edges if outer.
func adjustWidth(box *Box, outer bool, w float64) float64 {
	if box.Style == nil {
		return w
	}
	w = minMaxClamp(box, w)
	if !outer {
		return w
	}
	return marginWidthOf(box, w, true, true)
}

func marginWidthOf(box *Box, w float64, left, right bool) float64 {
	s := box.Style
	pct := 0.0
	add := func(id css.PropID) {
		if l, ok := s.Get(id).(css.Length); ok {
			if l.Unit == "%" {
				pct += l.V
			} else if l.Unit == "calc" {
				w += l.V
				pct += l.Pct
			} else {
				w += l.V
			}
		}
	}
	if left {
		add(css.PMarginLeft)
		add(css.PPaddingLeft)
		if box.Collapse != nil && box.Kind == KCell {
			_, _, _, l := box.Collapse.cellBorders(box)
			w += l
		} else {
			w += s.BorderWidth(3)
		}
	}
	if right {
		add(css.PMarginRight)
		add(css.PPaddingRight)
		if box.Collapse != nil && box.Kind == KCell {
			_, r, _, _ := box.Collapse.cellBorders(box)
			w += r
		} else {
			w += s.BorderWidth(1)
		}
	}
	if pct < 100 {
		return w / (1 - pct/100)
	}
	return 0
}

// inlineWidth computes the min- or max-content width of inline content.
func (c *Context) inlineWidth(line *Box, minimum bool) float64 {
	fl := c.flatten(line)
	indent := 0.0
	if l, ok := line.Style.Get(css.PTextIndent).(css.Length); ok && !l.IsPercent() {
		indent = l.V
	}
	best := 0.0
	cur := indent
	trailing := 0.0 // width of trailing collapsible spaces in cur
	finish := func() {
		w := cur - trailing
		if w > best {
			best = w
		}
		cur, trailing = 0, 0
	}
	for i := range fl.items {
		item := &fl.items[i]
		switch item.kind {
		case itOpen:
			cur += marginWidthOf(item.box, 0, true, false)
		case itClose:
			cur += marginWidthOf(item.box, 0, false, true)
		case itText:
			runes := item.shaped.Runes
			for j := 0; j < len(runes); j++ {
				b := item.breaks[j]
				if b == text.BreakMandatory || (minimum && (b == text.BreakAllowed || b == text.BreakHyphen) && (j > 0 || i > 0)) {
					finish()
				}
				r := runes[j]
				w := item.shaped.Pos[j+1] - item.shaped.Pos[j]
				if r == '\t' && !isCollapsibleSpace(item, r) {
					w = tabWidth(item, cur, 0)
				}
				if isCollapsibleSpace(item, r) {
					if cur == 0 && minimum {
						continue
					}
					trailing += w
				} else if r != '\n' {
					trailing = 0
				}
				cur += w
			}
			if n := len(runes); n > 0 && item.breaks[n] == text.BreakMandatory && runes[n-1] == '\n' {
				finish()
			}
		case itAtomic:
			if minimum && item.breaks[0] != text.BreakNone {
				finish()
			}
			cur += c.contentWidth(item.box, true, minimum)
			trailing = 0
			if minimum && item.breaks[1] != text.BreakNone {
				finish()
			}
		case itFloat:
			w := c.contentWidth(item.box, true, minimum)
			if minimum {
				best = math.Max(best, w)
			} else {
				cur += w
			}
		}
	}
	finish()
	return best
}

// replacedIntrinsicWidth returns the used width of a replaced box for
// preferred width computation.
func (c *Context) replacedIntrinsicWidth(box *Box) float64 {
	if l, ok := box.Style.Get(css.PWidth).(css.Length); ok && !l.IsPercent() {
		return l.V - boxSizingWidthStyle(box.Style)
	}
	w, h, _ := box.Image.IntrinsicSize()
	if l, ok := box.Style.Get(css.PHeight).(css.Length); ok && !l.IsPercent() {
		if r := box.Image.Ratio(); r > 0 {
			return (l.V - boxSizingHeightStyle(box.Style)) * r
		}
	}
	if w == 0 && h == 0 {
		return 300
	}
	return w
}

func boxSizingWidthStyle(s *css.Style) float64 {
	switch s.Kw(css.PBoxSizing) {
	case "border-box":
		return s.Px(css.PPaddingLeft, 0) + s.Px(css.PPaddingRight, 0) + s.BorderWidth(1) + s.BorderWidth(3)
	case "padding-box":
		return s.Px(css.PPaddingLeft, 0) + s.Px(css.PPaddingRight, 0)
	}
	return 0
}

func boxSizingHeightStyle(s *css.Style) float64 {
	switch s.Kw(css.PBoxSizing) {
	case "border-box":
		return s.Px(css.PPaddingTop, 0) + s.Px(css.PPaddingBottom, 0) + s.BorderWidth(0) + s.BorderWidth(2)
	case "padding-box":
		return s.Px(css.PPaddingTop, 0) + s.Px(css.PPaddingBottom, 0)
	}
	return 0
}

package layout

import (
	"math"

	"github.com/esoteric-programmer/weasyprint-go/internal/css"
)

// Footnotes (GCPM float: footnote), ported from WeasyPrint's
// LayoutContext footnote methods.
//
// A footnote is pending until the line holding its call is laid out. It is
// then laid out in the footnote area of the current page, which reduces the
// space left for the page content, or reported to the next page when it
// does not fit.

type fnState uint8

const (
	fnPending fnState = iota
	fnCurrent
	fnReported
	fnDone
)

type footnotes struct {
	enabled  bool
	state    map[*Box]fnState
	claim    map[*Box]int // page where the footnote's call was laid out
	current  []*Box       // footnotes of the current page
	reported []*Box       // footnotes moved to the next page
	inArea   bool         // laying out the footnote area itself

	// Footnote area of the current page.
	areaStyle  *css.Style
	page       *Box
	pageCBI    cbInfo
	areaActive bool    // area height is not auto (WeasyPrint)
	areaMH     float64 // margin height subtracted from the page bottom
	delta      float64 // total adjustment of PageBottom on this page
	inColumn   int     // laying out multi-column content
	colDelta   float64 // page bottom change pending until columns end
}

// bottomSave is a saved PageBottom, restored keeping footnote changes made
// meanwhile.
type bottomSave struct{ v, d float64 }

func (c *Context) saveBottom() bottomSave { return bottomSave{c.PageBottom, c.fn.delta} }

func (c *Context) restoreBottom(s bottomSave) {
	c.PageBottom = s.v + (c.fn.delta - s.d)
}

func (c *Context) adjustBottom(d float64) {
	if c.fn.inColumn > 0 {
		// Columns have a fixed height: the change is applied to the page
		// once the columns are laid out (as in WeasyPrint).
		c.fn.colDelta += d
		return
	}
	c.PageBottom += d
	c.fn.delta += d
}

// beginColumns and endColumns bracket the layout of multi-column content.
func (c *Context) beginColumns() { c.fn.inColumn++ }

func (c *Context) endColumns() {
	c.fn.inColumn--
	if c.fn.inColumn == 0 && c.fn.colDelta != 0 {
		d := c.fn.colDelta
		c.fn.colDelta = 0
		c.adjustBottom(d)
	}
}

// startPageFootnotes prepares the footnote area of a new page and lays out
// the footnotes reported from the previous page.
func (c *Context) startPageFootnotes(pt css.PageType, pageStyle *css.Style, page *Box) {
	if !c.fn.enabled {
		return
	}
	st := c.R.MarginBoxStyle(pt, pageStyle, "footnote")
	if st == nil {
		st = c.R.AnonymousStyle(pageStyle)
	}
	c.fn.areaStyle = st
	c.fn.page = page
	c.fn.pageCBI = cbInfo{W: page.W, H: page.H, Style: pageStyle}
	c.fn.areaActive = false
	c.fn.areaMH = 0
	c.fn.delta = 0
	c.fn.current = nil
	reported := c.fn.reported
	c.fn.reported = nil
	for i, f := range reported {
		c.fn.state[f] = fnPending
		overflow := c.layoutFootnote(f)
		if overflow && i != 0 {
			c.reportFootnote(f)
			for _, r := range reported[i+1:] {
				c.fn.reported = append(c.fn.reported, r)
				c.fn.state[r] = fnReported
			}
			break
		}
	}
}

// layoutFootnote adds a footnote to the current page and reports whether
// the footnote area overflows.
func (c *Context) layoutFootnote(f *Box) bool {
	c.fn.state[f] = fnCurrent
	if _, ok := c.fn.claim[f]; !ok {
		c.fn.claim[f] = c.PageIndex
	}
	c.fn.current = append(c.fn.current, f)
	// Footnotes called inside this footnote follow it.
	for _, n := range nestedFootnotes(f) {
		if c.fn.state[n] == fnPending {
			c.fn.state[n] = fnCurrent
			if _, ok := c.fn.claim[n]; !ok {
				c.fn.claim[n] = c.PageIndex
			}
			c.fn.current = append(c.fn.current, n)
		}
	}
	return c.updateFootnoteArea()
}

// nestedFootnotes returns the footnotes called inside a footnote.
func nestedFootnotes(f *Box) []*Box {
	var out []*Box
	for _, ch := range f.Children {
		ch.Descendants(func(d *Box) {
			if d.Footnote != nil {
				out = append(out, d.Footnote)
				out = append(out, nestedFootnotes(d.Footnote)...)
			}
		})
	}
	return out
}

// unlayoutFootnote returns a footnote of the current page to the pending
// list.
func (c *Context) unlayoutFootnote(f *Box) {
	st := c.fn.state[f]
	if st != fnCurrent && st != fnReported {
		return
	}
	if p, ok := c.fn.claim[f]; !ok || p != c.PageIndex {
		return
	}
	for _, g := range append([]*Box{f}, nestedFootnotes(f)...) {
		if p, ok := c.fn.claim[g]; !ok || p != c.PageIndex {
			continue
		}
		delete(c.fn.claim, g)
		c.fn.state[g] = fnPending
		c.fn.current = removeBox(c.fn.current, g)
		c.fn.reported = removeBox(c.fn.reported, g)
	}
	c.updateFootnoteArea()
}

// reportFootnote moves a footnote of the current page to the next page.
func (c *Context) reportFootnote(f *Box) {
	c.fn.current = removeBox(c.fn.current, f)
	c.fn.reported = append(c.fn.reported, f)
	c.fn.state[f] = fnReported
	c.updateFootnoteArea()
}

func removeBox(list []*Box, b *Box) []*Box {
	for i, x := range list {
		if x == b {
			return append(list[:i:i], list[i+1:]...)
		}
	}
	return list
}

// updateFootnoteArea lays out the footnote area again, updates the page
// bottom and reports whether the footnotes overflow the area.
func (c *Context) updateFootnoteArea() bool {
	if c.fn.areaActive {
		c.adjustBottom(c.fn.areaMH)
	}
	c.fn.areaActive = true
	if len(c.fn.current) > 0 {
		area := c.layoutFootnoteArea(0)
		c.fn.areaMH = area.MarginHeight()
		c.adjustBottom(-c.fn.areaMH)
		if len(area.Children) == 0 {
			return false
		}
		last := area.Children[len(area.Children)-1]
		lastBottom := last.Y + last.MarginHeight() - last.MB
		areaBottom := area.Y + area.MarginHeight() - area.MB
		return lastBottom > areaBottom+1e-6
	}
	st := c.fn.areaStyle
	w := c.fn.pageCBI.W
	mh := 0.0
	for _, id := range []css.PropID{css.PMarginTop, css.PMarginBottom} {
		if v, ok := lengthOrAuto(st, id, w); ok {
			mh += v
		}
	}
	mh += st.Px(css.PPaddingTop, w) + st.Px(css.PPaddingBottom, w) + st.BorderWidth(0) + st.BorderWidth(2)
	c.fn.areaMH = mh
	c.adjustBottom(-mh)
	return false
}

// layoutFootnoteArea lays out the footnote area of the current page with
// its top margin edge at y.
func (c *Context) layoutFootnoteArea(y float64) *Box {
	area := &Box{Kind: KBlock, Style: c.fn.areaStyle, Children: append([]*Box(nil), c.fn.current...)}
	fixupBlockContainer(c.Builder, area)
	area.X = c.fn.page.ContentX()
	area.Y = y
	savedExcluded, savedStack := c.excluded, c.bfcStack
	savedBottom := c.PageBottom
	savedIn := c.fn.inArea
	c.excluded, c.bfcStack = nil, nil
	c.fn.inArea = true
	c.PageBottom = math.Inf(1)
	c.createBFC()
	res := c.blockLevelLayout(area, math.Inf(-1), nil, c.fn.page, c.fn.pageCBI, true, &absList{}, &absList{}, nil, false)
	c.finishBFC(res.box)
	c.PageBottom = savedBottom
	c.fn.inArea = savedIn
	c.excluded, c.bfcStack = savedExcluded, savedStack
	if res.box == nil {
		return area
	}
	return res.box
}

// finishPageFootnotes returns the laid out footnote area of the page (or
// nil) and marks its footnotes as done.
func (c *Context) finishPageFootnotes(page *Box) *Box {
	if !c.fn.enabled || len(c.fn.current) == 0 {
		return nil
	}
	area := c.layoutFootnoteArea(0)
	bottom := page.ContentY() + page.H
	area.Translate(0, bottom-area.MarginHeight()-area.Y)
	for _, f := range c.fn.current {
		c.fn.state[f] = fnDone
	}
	c.fn.current = nil
	return area
}

// footnoteCalls returns the pending footnotes called in a laid-out line.
func (c *Context) footnoteCalls(line *Box) []*Box {
	var out []*Box
	line.Descendants(func(d *Box) {
		if d.Footnote != nil && c.fn.state[d.Footnote] == fnPending {
			out = append(out, d.Footnote)
		}
	})
	return out
}

// unlayoutFootnotes is called for boxes removed from the layout.
func (c *Context) unlayoutFootnotes(boxes []*Box) {
	if !c.fn.enabled || c.fn.inArea {
		return
	}
	for _, b := range boxes {
		b.Descendants(func(d *Box) {
			if d.Footnote != nil {
				c.unlayoutFootnote(d.Footnote)
			}
		})
	}
}

func (c *Context) removePlaceholders(boxes []*Box, abs, fixed *absList) {
	abs.remove(boxes)
	fixed.remove(boxes)
	c.unlayoutFootnotes(boxes)
}

// fnSnapshot is a copy of the footnote state, used to undo trial layouts.
type fnSnapshot struct {
	f     footnotes
	state map[*Box]fnState
	claim map[*Box]int
}

func (c *Context) footnoteSnapshot() *fnSnapshot {
	if !c.fn.enabled {
		return nil
	}
	s := &fnSnapshot{f: c.fn, state: make(map[*Box]fnState, len(c.fn.state)), claim: make(map[*Box]int, len(c.fn.claim))}
	for k, v := range c.fn.state {
		s.state[k] = v
	}
	for k, v := range c.fn.claim {
		s.claim[k] = v
	}
	s.f.current = append([]*Box(nil), c.fn.current...)
	s.f.reported = append([]*Box(nil), c.fn.reported...)
	return s
}

// restoreFootnotes restores a footnote snapshot, including its effect on
// the page bottom.
func (c *Context) restoreFootnotes(s *fnSnapshot) {
	if s == nil {
		return
	}
	c.PageBottom -= c.fn.delta - s.f.delta
	c.fn = s.f
	c.fn.state, c.fn.claim = s.state, s.claim
	c.fn.current = append([]*Box(nil), s.f.current...)
	c.fn.reported = append([]*Box(nil), s.f.reported...)
	// Keep the snapshot reusable.
	s.state, s.claim = copyStates(s.state), copyClaims(s.claim)
}

func copyStates(m map[*Box]fnState) map[*Box]fnState {
	out := make(map[*Box]fnState, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func copyClaims(m map[*Box]int) map[*Box]int {
	out := make(map[*Box]int, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

package layout

import (
	"math"

	"github.com/esoteric-programmer/weasyprint-go/internal/css"
)

func isMulticol(b *Box) bool {
	if b.Kind != KBlock || b.Style == nil {
		return false
	}
	return b.Style.Kw(css.PColumnCount) != "auto" || b.Style.Kw(css.PColumnWidth) != "auto"
}

// columnsLayout lays out a multi-column container (CSS Multi-column 1).
func (c *Context) columnsLayout(box *Box, bottomSpace float64, skip Skip, cbi cbInfo, pageIsEmpty bool,
	abs, fixed *absList, adjoining []float64) blockResult {
	s := box.Style
	avail := box.W
	gap := s.FontSize()
	if l, ok := s.Get(css.PColumnGap).(css.Length); ok {
		gap = l.Resolve(avail)
	}
	count := 0
	if n, ok := s.Get(css.PColumnCount).(css.Number); ok {
		count = int(n)
	}
	cw := 0.0
	if l, ok := s.Get(css.PColumnWidth).(css.Length); ok {
		cw = l.V
	}
	n := count
	switch {
	case cw <= 0 && count > 0:
	case count <= 0 && cw > 0:
		n = int(math.Max(1, math.Floor((avail+gap)/(cw+gap))))
	case count > 0 && cw > 0:
		n = int(math.Min(float64(count), math.Max(1, math.Floor((avail+gap)/(cw+gap)))))
	default:
		n = 1
	}
	if n < 1 {
		n = 1
	}
	colW := math.Max(0, (avail-float64(n-1)*gap)/float64(n))

	// Position the container (it establishes a BFC: no collapsing with
	// its children).
	isStart := skip == nil
	if !isStart {
		box.MT, box.PT, box.BT = 0, 0, 0
	}
	box.Y += collapseMargins(append(append([]float64{}, adjoining...), box.MT)) - box.MT
	top := box.ContentY()
	if hasSpanningChild(box) {
		return c.spanningColumnsLayout(box, bottomSpace, skip, cbi, pageIsEmpty, abs, fixed, n, colW, gap, top)
	}
	maxH := math.Inf(1)
	if !math.IsInf(c.PageBottom, 1) {
		maxH = c.PageBottom - bottomSpace - box.PB - box.BB - top
	}
	// A height constraint smaller than the page makes the content overflow
	// in additional columns in the inline direction instead of continuing
	// on the next page.
	inlineOverflow := false
	if specH := specifiedHeight(box, cbi); !math.IsNaN(specH) {
		if specH <= maxH {
			inlineOverflow = true
		}
		maxH = math.Min(maxH, specH)
	}

	kids := box.Children
	column := func(x float64) *Box {
		col := box.CopyWith(kids)
		col.Orig = box.Original()
		col.Style = c.R.AnonymousStyleWith(box.Style, map[css.PropID]css.Value{
			css.PColumnCount: css.Keyword("auto"), css.PColumnWidth: css.Keyword("auto"),
		})
		col.Anonymous = true
		col.X, col.Y = x, top
		col.W = colW
		col.H = math.NaN()
		col.MT, col.MR, col.MB, col.ML = 0, 0, 0, 0
		col.PT, col.PR, col.PB, col.PL = 0, 0, 0, 0
		col.BT, col.BR, col.BB, col.BL = 0, 0, 0, 0
		col.Marker = nil
		col.Anchor, col.BookmarkLevel, col.StringSets = "", 0, nil
		return col
	}
	// layoutColumns tries to fit content in n columns of height h.
	layoutColumns := func(h float64, commit bool) ([]*Box, Skip, float64) {
		savedExcl := c.excluded
		var snap *fnSnapshot
		if !commit {
			// Trial layouts must not keep their footnotes.
			snap = c.footnoteSnapshot()
		}
		savedBottom := c.saveBottom()
		c.PageBottom = top + h
		defer func() {
			c.restoreFootnotes(snap)
			c.restoreBottom(savedBottom)
			c.excluded = savedExcl
		}()
		var cols []*Box
		rs := skip
		used := 0.0
		for i := 0; i < n || (commit && inlineOverflow && i < 1000); i++ {
			x := box.ContentX() + float64(i)*(colW+gap)
			if s.Kw(css.PDirection) == "rtl" {
				x = box.ContentX() + avail - float64(i+1)*colW - float64(i)*gap
			}
			col := column(x)
			c.createBFC()
			a, f := abs, fixed
			if !commit {
				a, f = &absList{}, &absList{}
			}
			res := c.blockContainerLayout(col, 0, rs, pageIsEmpty || i > 0, a, f, nil, false, cbInfo{W: colW, HAuto: true, Style: box.Style})
			c.finishBFC(nil)
			if res.box == nil {
				// Nothing fits in this column: the content is not done.
				if rs == nil {
					rs = Skip{}
				}
				break
			}
			res.box.NoStartDecor, res.box.NoEndDecor = false, false
			if res.resume == nil {
				// Columns are formatting contexts: the bottom margin of
				// their last child is inside them.
				res.box.H += math.Max(0, collapseMargins(res.adjoining))
			}
			cols = append(cols, res.box)
			used = math.Max(used, res.box.H)
			rs = res.resume
			if rs == nil {
				break
			}
		}
		return cols, rs, used
	}

	var cols []*Box
	var resume Skip
	balance := s.Kw(css.PColumnFill) != "auto" || math.IsInf(maxH, 1)
	for attempt := 0; ; attempt++ {
		snap := c.footnoteSnapshot()
		c.beginColumns()
		if balance {
			// Total height in a single column.
			_, _, total := func() ([]*Box, Skip, float64) {
				saved := n
				n = 1
				defer func() { n = saved }()
				return layoutColumns(math.Inf(1), false)
			}()
			lo := total / float64(n)
			hi := math.Min(total, maxH)
			if lo > hi {
				lo = hi
			}
			if _, rs, _ := layoutColumns(lo, false); rs == nil {
				hi = lo
			} else {
				for i := 0; i < 16 && hi-lo > 0.5; i++ {
					mid := (lo + hi) / 2
					if _, rs, _ := layoutColumns(mid, false); rs == nil {
						hi = mid
					} else {
						lo = mid
					}
				}
			}
			cols, resume, _ = layoutColumns(hi, true)
		} else {
			cols, resume, _ = layoutColumns(maxH, true)
		}
		c.endColumns()
		// Footnotes called in the columns reduce the page height: lay the
		// columns out again if they now overlap the footnote area.
		if snap == nil || attempt >= 4 || math.IsInf(c.PageBottom, 1) {
			break
		}
		used := 0.0
		for _, col := range cols {
			used = math.Max(used, col.H)
		}
		limit := c.PageBottom - bottomSpace - box.PB - box.BB - top
		if used <= limit+1e-6 || c.fn.delta == snap.f.delta {
			break
		}
		c.removePlaceholders(cols, abs, fixed)
		c.restoreFootnotes(snap)
		maxH = math.Min(maxH, limit)
	}
	colH := 0.0
	for _, col := range cols {
		col.IsColumn = true
		colH = math.Max(colH, col.H)
	}
	for _, col := range cols {
		col.H = colH
	}
	nb := box.CopyWith(cols)
	nb.NoStartDecor = !isStart || box.NoStartDecor
	nb.H = colH
	if specH := specifiedHeight(box, cbi); !math.IsNaN(specH) {
		nb.H = specH
	}
	if resume != nil {
		nb.NoEndDecor = true
		nb.MB, nb.PB, nb.BB = 0, 0, 0
	}
	if len(cols) == 0 && !pageIsEmpty {
		return blockResult{next: nextPage{Break: "any"}}
	}
	np := nextPage{Break: "any", set: true}
	_, np.Page = pageValues(nb)
	return blockResult{box: nb, resume: resume, next: np}
}

func hasSpanningChild(box *Box) bool {
	for _, ch := range box.Children {
		if isSpanning(ch) {
			return true
		}
	}
	return false
}

func isSpanning(b *Box) bool {
	return b.Style != nil && b.Style.Kw(css.PColumnSpan) == "all" && !b.IsAbsPlaceholder
}

// spanningColumnsLayout lays out a multi-column container with
// column-span: all children like WeasyPrint's columns_layout: runs of
// other children are laid out in balanced columns, spanning children
// as blocks across the whole container.
func (c *Context) spanningColumnsLayout(box *Box, bottomSpace float64, skip Skip, cbi cbInfo, pageIsEmpty bool,
	abs, fixed *absList, n int, colW, gap, top float64) blockResult {
	type group struct {
		start int
		kids  []*Box
		span  bool
	}
	start, sub := 0, Skip(nil)
	if skip != nil {
		start, sub = skipFirst(skip)
	}
	var groups []group
	for i := start; i < len(box.Children); i++ {
		ch := box.Children[i]
		if isSpanning(ch) {
			groups = append(groups, group{start: i, kids: []*Box{ch}, span: true})
			continue
		}
		if len(groups) == 0 || groups[len(groups)-1].span {
			groups = append(groups, group{start: i})
		}
		g := &groups[len(groups)-1]
		g.kids = append(g.kids, ch)
	}
	isStart := skip == nil
	y := top
	var adjoining []float64
	var children []*Box
	var resume Skip
	next := nextPage{Break: "any"}
	inner := cbInfo{W: box.W, HAuto: true, Style: box.Style}
	for gi, g := range groups {
		gskip := Skip(nil)
		if gi == 0 && sub != nil {
			gskip = Skip{0: sub}
		}
		if g.span {
			child := g.kids[0].Copy()
			child.X, child.Y = box.ContentX(), y
			res := c.blockLevelLayout(child, bottomSpace, gskip, box, inner, pageIsEmpty, abs, fixed, adjoining, false)
			if res.box == nil {
				resume = Skip{g.start: nil}
				break
			}
			children = append(children, res.box)
			y = res.box.Y + res.box.MT + res.box.BorderHeight()
			adjoining = append([]float64{}, res.adjoining...)
			if len(adjoining) == 0 {
				adjoining = []float64{res.box.MB}
			}
			if res.resume != nil {
				resume = Skip{g.start: res.resume}
				next = res.next
				break
			}
			pageIsEmpty = false
			continue
		}
		y += collapseMargins(adjoining)
		adjoining = nil
		// Lay the run out as a multi-column container of its own.
		mc := box.CopyWith(g.kids)
		ov := map[css.PropID]css.Value{}
		for _, id := range []css.PropID{css.PColumnCount, css.PColumnWidth, css.PColumnGap, css.PColumnFill} {
			ov[id] = box.Style.Get(id)
		}
		mc.Style = c.R.AnonymousStyleWith(box.Style, ov)
		mc.Anonymous = true
		mc.X, mc.Y = box.X, y
		mc.ML, mc.MR, mc.BL, mc.BR, mc.PL, mc.PR = 0, 0, 0, 0, 0, 0
		mc.X = box.ContentX()
		mc.W = box.W
		mc.MT, mc.MB, mc.PT, mc.PB, mc.BT, mc.BB = 0, 0, 0, 0, 0, 0
		mc.Marker = nil
		mc.Anchor, mc.BookmarkLevel, mc.StringSets = "", 0, nil
		res := c.columnsLayout(mc, bottomSpace, gskip, inner, pageIsEmpty, abs, fixed, nil)
		if res.box == nil {
			resume = Skip{g.start: nil}
			if gskip != nil {
				resume = skip
			}
			break
		}
		for _, col := range res.box.Children {
			children = append(children, col)
		}
		y += res.box.H
		pageIsEmpty = false
		if res.resume != nil {
			k, v := skipFirst(res.resume)
			resume = Skip{g.start + k: v}
			next = res.next
			break
		}
	}
	if len(children) == 0 && !pageIsEmpty {
		return blockResult{next: nextPage{Break: "any"}}
	}
	nb := box.CopyWith(children)
	nb.NoStartDecor = !isStart || box.NoStartDecor
	if resume == nil {
		y += collapseMargins(adjoining)
	}
	nb.H = y - top
	if specH := specifiedHeight(box, cbi); !math.IsNaN(specH) {
		nb.H = specH
	}
	if resume != nil {
		nb.NoEndDecor = true
		nb.MB, nb.PB, nb.BB = 0, 0, 0
	}
	np := next
	np.set = true
	_, np.Page = pageValues(nb)
	return blockResult{box: nb, resume: resume, next: np}
}

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
	maxH := math.Inf(1)
	if !math.IsInf(c.PageBottom, 1) {
		maxH = c.PageBottom - bottomSpace - box.PB - box.BB - top
	}
	if specH := specifiedHeight(box, cbi); !math.IsNaN(specH) {
		maxH = math.Min(maxH, specH)
	}

	column := func(x float64) *Box {
		col := box.CopyWith(box.Children)
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
		savedBottom := c.PageBottom
		c.PageBottom = top + h
		defer func() {
			c.PageBottom = savedBottom
			c.excluded = savedExcl
		}()
		var cols []*Box
		rs := skip
		used := 0.0
		for i := 0; i < n; i++ {
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
				break
			}
			res.box.NoStartDecor, res.box.NoEndDecor = false, false
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

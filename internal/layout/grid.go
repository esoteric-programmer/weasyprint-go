package layout

import (
	"math"
	"sort"

	"github.com/esoteric-programmer/weasyprint-go/internal/css"
)

type gridItem struct {
	box              *Box
	row, col         int // 0-based start
	rowSpan, colSpan int
	laid             *Box
	minW, maxW       float64 // outer content contributions
	outerH           float64
}

type track struct {
	size      css.TrackSize
	base      float64
	limit     float64
	isFlex    bool
	fr        float64
	pos       float64
	autoGrows bool
}

func expandTracks(v css.Value, avail float64, gap float64) []css.TrackSize {
	list, ok := v.(css.TrackList)
	if !ok {
		return nil
	}
	var out []css.TrackSize
	for _, it := range list {
		switch t := it.(type) {
		case css.TrackSize:
			out = append(out, t)
		case css.TrackRepeat:
			n := t.Count
			if t.Auto != "" {
				// Repeat as many times as fit (using fixed sizes).
				size := 0.0
				for _, s := range t.Sizes {
					b := s.Max
					if b.Kind != "length" {
						b = s.Min
					}
					if b.Kind == "length" {
						size += b.L.Resolve(avail)
					}
				}
				size += gap * float64(len(t.Sizes))
				fixed := 0.0
				for _, o := range out {
					if o.Max.Kind == "length" {
						fixed += o.Max.L.Resolve(avail) + gap
					}
				}
				n = 1
				if size > 0 && !math.IsInf(avail, 1) {
					n = int(math.Max(1, math.Floor((avail-fixed+gap)/size)))
				}
			}
			for i := 0; i < n && i < 1000; i++ {
				out = append(out, t.Sizes...)
			}
		}
	}
	return out
}

func autoTrack(v css.Value, i int) css.TrackSize {
	if list, ok := v.(css.TrackList); ok && len(list) > 0 {
		if t, ok := list[i%len(list)].(css.TrackSize); ok {
			return t
		}
	}
	return css.TrackSize{Min: css.TrackBreadth{Kind: "auto"}, Max: css.TrackBreadth{Kind: "auto"}}
}

func lineOf(s *css.Style, id css.PropID) css.GridLine {
	if g, ok := s.Get(id).(css.GridLine); ok {
		return g
	}
	return css.GridLine{Auto: true}
}

// resolveLine resolves a start/end pair on one axis. It returns start
// (0-based) and span, and whether the start is definite.
func resolveLine(start, end css.GridLine, explicit int, areas *css.GridAreas, isRow bool, isStartName bool) (int, int, bool) {
	toIndex := func(g css.GridLine, isEnd bool) (int, bool) {
		if g.Name != "" && g.Span == 0 && g.Line == 0 && areas != nil {
			if a, ok := areas.Areas[g.Name]; ok {
				var v int
				switch {
				case isRow && !isEnd:
					v = a[0]
				case isRow && isEnd:
					v = a[2]
				case !isRow && !isEnd:
					v = a[1]
				default:
					v = a[3]
				}
				return v, true
			}
			return 0, false
		}
		if g.Line > 0 {
			return g.Line - 1, true
		}
		if g.Line < 0 {
			return explicit + 1 + g.Line, true
		}
		return 0, false
	}
	s, sok := 0, false
	e, eok := 0, false
	if !start.Auto && start.Span == 0 {
		s, sok = toIndex(start, false)
	}
	if !end.Auto && end.Span == 0 {
		e, eok = toIndex(end, true)
	}
	switch {
	case sok && eok:
		if e < s {
			s, e = e, s
		}
		if e == s {
			e = s + 1
		}
		return s, e - s, true
	case sok:
		span := 1
		if end.Span > 0 {
			span = end.Span
		}
		return s, span, true
	case eok:
		span := 1
		if start.Span > 0 {
			span = start.Span
		}
		return max(0, e-span), span, true
	}
	span := 1
	if start.Span > 0 {
		span = start.Span
	} else if end.Span > 0 {
		span = end.Span
	}
	return 0, span, false
}

func (c *Context) gridLayout(box *Box, bottomSpace float64, skip Skip, cb *Box, cbi cbInfo, pageIsEmpty bool, abs, fixed *absList) blockResult {
	c.createBFC()
	resolvePercentages(box, cbi.W)
	if math.IsNaN(box.W) || box.Style.IsAuto(css.PWidth) && box.W == 0 {
		box.W = specifiedWidth(box, cbi.W)
	}
	if box.IsInline || box.IsFloated() || box.IsAbsolutelyPositioned() {
		box.ML, box.MR = nanTo0(box.ML), nanTo0(box.MR)
		if math.IsNaN(box.W) {
			box.W = c.shrinkToFit(box, cbi.W-box.ML-box.MR-box.PL-box.PR-box.BL-box.BR)
		}
	} else {
		blockLevelWidth(box, cbi.W, true, cbi.Style)
	}
	box.MT, box.MB = nanTo0(box.MT), nanTo0(box.MB)
	s := box.Style
	specH := specifiedHeight(box, cbi)
	colGap, rowGap := 0.0, 0.0
	if l, ok := s.Get(css.PColumnGap).(css.Length); ok {
		colGap = l.Resolve(box.W)
	}
	if l, ok := s.Get(css.PRowGap).(css.Length); ok {
		rowGap = l.Resolve(box.W)
	}
	var areas *css.GridAreas
	if a, ok := s.Get(css.PGridTemplateAreas).(css.GridAreas); ok {
		areas = &a
	}
	colTemplate := expandTracks(s.Get(css.PGridTemplateColumns), box.W, colGap)
	rowTemplate := expandTracks(s.Get(css.PGridTemplateRows), specH, rowGap)
	nExplicitCols := len(colTemplate)
	nExplicitRows := len(rowTemplate)
	if areas != nil {
		nExplicitCols = max(nExplicitCols, areas.Cols)
		nExplicitRows = max(nExplicitRows, areas.Rows)
	}
	if box.Style.Kw(css.PPosition) != "static" {
		abs = &absList{}
	}

	// Items.
	var children []*Box
	var items []*gridItem
	for i, ch := range box.Children {
		if ch.IsAbsolutelyPositioned() {
			ph := ch.Copy()
			ph.IsAbsPlaceholder = true
			ph.X, ph.Y = box.ContentX(), box.ContentY()
			ph.index = i
			if ch.Style.Kw(css.PPosition) == "fixed" {
				fixed.add(ph)
			} else {
				abs.add(ph)
			}
			children = append(children, ph)
			continue
		}
		if ch.RunningName != "" {
			c.addRunning(ch)
			continue
		}
		items = append(items, &gridItem{box: ch})
	}
	sort.SliceStable(items, func(i, j int) bool {
		return items[i].box.Style.Num(css.POrder) < items[j].box.Style.Num(css.POrder)
	})
	// Placement.
	flow := s.Kw(css.PGridAutoFlow)
	columnFlow := len(flow) >= 6 && flow[:6] == "column"
	occupied := map[[2]int]bool{}
	nRows, nCols := nExplicitRows, nExplicitCols
	if nCols == 0 {
		nCols = 1
	}
	mark := func(it *gridItem) {
		for r := it.row; r < it.row+it.rowSpan; r++ {
			for cc := it.col; cc < it.col+it.colSpan; cc++ {
				occupied[[2]int{r, cc}] = true
			}
		}
		nRows = max(nRows, it.row+it.rowSpan)
		nCols = max(nCols, it.col+it.colSpan)
	}
	free := func(r, cc, rs, cs int) bool {
		for y := r; y < r+rs; y++ {
			for x := cc; x < cc+cs; x++ {
				if occupied[[2]int{y, x}] {
					return false
				}
			}
		}
		return true
	}
	var autoItems []*gridItem
	for _, it := range items {
		st := it.box.Style
		r, rs, rdef := resolveLine(lineOf(st, css.PGridRowStart), lineOf(st, css.PGridRowEnd), nExplicitRows, areas, true, false)
		cc, cs, cdef := resolveLine(lineOf(st, css.PGridColumnStart), lineOf(st, css.PGridColumnEnd), nExplicitCols, areas, false, false)
		it.row, it.rowSpan, it.col, it.colSpan = r, rs, cc, cs
		switch {
		case rdef && cdef:
			mark(it)
		default:
			it.row, it.col = -1, -1
			if rdef {
				it.row = r
			}
			if cdef {
				it.col = cc
			}
			autoItems = append(autoItems, it)
		}
	}
	cursorR, cursorC := 0, 0
	for _, it := range autoItems {
		if !columnFlow {
			if it.row >= 0 {
				// Definite row: find a column.
				cc := 0
				for !free(it.row, cc, it.rowSpan, it.colSpan) {
					cc++
				}
				it.col = cc
				mark(it)
				continue
			}
			for {
				if it.col >= 0 {
					cursorC = it.col
				}
				if cursorC+it.colSpan > nCols && it.col < 0 {
					cursorR++
					cursorC = 0
					continue
				}
				if free(cursorR, cursorC, it.rowSpan, it.colSpan) {
					it.row, it.col = cursorR, cursorC
					mark(it)
					cursorC += it.colSpan
					break
				}
				cursorC++
				if it.col >= 0 {
					cursorR++
				}
			}
		} else {
			if it.col >= 0 {
				r := 0
				for !free(r, it.col, it.rowSpan, it.colSpan) {
					r++
				}
				it.row = r
				mark(it)
				continue
			}
			rowsLimit := max(nExplicitRows, 1)
			for {
				if it.row >= 0 {
					cursorR = it.row
				}
				if cursorR+it.rowSpan > rowsLimit && it.row < 0 {
					cursorC++
					cursorR = 0
					continue
				}
				if free(cursorR, cursorC, it.rowSpan, it.colSpan) {
					it.row, it.col = cursorR, cursorC
					mark(it)
					cursorR += it.rowSpan
					break
				}
				cursorR++
			}
		}
	}

	// Column tracks.
	cols := make([]*track, nCols)
	for i := range cols {
		var ts css.TrackSize
		if i < len(colTemplate) {
			ts = colTemplate[i]
		} else {
			ts = autoTrack(s.Get(css.PGridAutoColumns), i-len(colTemplate))
		}
		cols[i] = &track{size: ts}
	}
	for _, it := range items {
		b := it.box.Copy()
		resolvePercentages(b, box.W)
		b.ML, b.MR, b.MT, b.MB = nanTo0(b.ML), nanTo0(b.MR), nanTo0(b.MT), nanTo0(b.MB)
		it.box = b
		it.minW = c.minContent(b, true)
		it.maxW = c.maxContent(b, true)
	}
	sizeTracks(cols, items, box.W, colGap, true, s.Kw(css.PJustifyContent))
	pos := box.ContentX()
	for i, t := range cols {
		t.pos = pos
		pos += t.base
		if i < len(cols)-1 {
			pos += colGap
		}
	}
	// Lay out items with their column widths.
	itemCB := cbInfo{W: box.W, H: specH, HAuto: math.IsNaN(specH), Style: s}
	widths := map[*gridItem]float64{}
	for _, it := range items {
		w := areaSize(cols, it.col, it.colSpan, colGap)
		b := it.box
		js := b.Style.Kw(css.PJustifySelf)
		if js == "auto" || js == "normal" {
			js = s.Kw(css.PJustifyItems)
		}
		cw := w - (b.ML + b.MR + b.PL + b.PR + b.BL + b.BR)
		if sw := specifiedWidth(b, w); !math.IsNaN(sw) {
			cw = sw
		} else if js != "normal" && js != "stretch" && js != "legacy" || b.Kind == KReplaced {
			cw = math.Min(cw, c.maxContent(b, false))
		}
		widths[it] = math.Max(0, cw)
		it.laid = c.layoutFlexItem(b, math.Max(0, cw), math.NaN(), itemCB)
		it.outerH = it.laid.MarginHeight()
	}
	// Row tracks.
	rows := make([]*track, nRows)
	for i := range rows {
		var ts css.TrackSize
		if i < len(rowTemplate) {
			ts = rowTemplate[i]
		} else {
			ts = autoTrack(s.Get(css.PGridAutoRows), i-len(rowTemplate))
		}
		rows[i] = &track{size: ts}
	}
	rowItems := make([]*gridItem, len(items))
	for i, it := range items {
		cp := *it
		cp.row, cp.col = it.col, it.row
		cp.rowSpan, cp.colSpan = it.colSpan, it.rowSpan
		cp.minW, cp.maxW = it.outerH, it.outerH
		rowItems[i] = &cp
	}
	// Transposed: sizeTracks works on the "col" fields.
	for i, it := range rowItems {
		it.col, it.colSpan = items[i].row, items[i].rowSpan
	}
	availH := specH
	if math.IsNaN(availH) {
		availH = math.Inf(1)
	}
	sizeTracks(rows, rowItems, availH, rowGap, false, s.Kw(css.PAlignContent))
	pos = box.ContentY()
	for i, t := range rows {
		t.pos = pos
		pos += t.base
		if i < len(rows)-1 {
			pos += rowGap
		}
	}
	total := pos - box.ContentY()

	// Pagination: break between rows, or split a row whose items are
	// taller than the page.
	var resume Skip
	paged := !math.IsInf(c.PageBottom, 1) && !box.IsInline && !math.IsInf(bottomSpace, -1) &&
		!box.IsFloated() && !box.IsAbsolutelyPositioned() && len(rows) > 0
	limit := c.PageBottom - bottomSpace - box.PB - box.BB
	if paged && (skip != nil || box.ContentY()+total > limit+1e-6) {
		index := map[*gridItem]int{}
		for i, it := range items {
			index[it] = i
		}
		firstRow := 0
		var itemSkips Skip
		if skip != nil {
			firstRow, itemSkips = skipFirst(skip)
		}
		y0 := box.ContentY()
		cont := map[*gridItem]bool{}
		var kept []*gridItem
		contH := 0.0
		for _, it := range items {
			if itemSkips != nil {
				if sk, has := itemSkips[index[it]]; has {
					if it.laid != nil {
						c.unlayoutFootnotes([]*Box{it.laid})
					}
					it.laid, _ = c.layoutFlexItemPaged(it.box, widths[it], itemCB, sk, false, false, y0, 0)
					it.outerH = it.laid.MarginHeight()
					end := it.row + it.rowSpan
					it.row, it.rowSpan = firstRow, max(1, end-firstRow)
					cont[it] = true
					contH = math.Max(contH, it.outerH)
					kept = append(kept, it)
					continue
				}
				if it.row <= firstRow {
					continue
				}
			} else if it.row < firstRow {
				continue
			}
			kept = append(kept, it)
		}
		if firstRow >= len(rows) {
			firstRow = len(rows) - 1
		}
		pos := y0
		for r := firstRow; r < len(rows); r++ {
			if r == firstRow && itemSkips != nil {
				rows[r].base = contH
			}
			rows[r].pos = pos
			pos += rows[r].base + rowGap
		}
		lastRow := len(rows) - 1
		for r := firstRow; r < len(rows); r++ {
			if rows[r].pos+rows[r].base <= limit+1e-6 {
				continue
			}
			isCont := r == firstRow && itemSkips != nil
			canMove := r > firstRow || !pageIsEmpty
			if canMove && !isCont && rows[r].base <= c.PageBottom-c.PageTop-bottomSpace {
				resume = Skip{r: nil}
				lastRow = r - 1
				break
			}
			sks := Skip{}
			for _, it := range kept {
				if it.row > r || it.row+it.rowSpan <= r {
					continue
				}
				y := rows[it.row].pos
				if y+it.outerH <= limit+1e-6 {
					continue
				}
				var sk Skip
				if cont[it] {
					sk = itemSkips[index[it]]
				}
				var rs Skip
				if it.laid != nil {
					c.unlayoutFootnotes([]*Box{it.laid})
				}
				it.laid, rs = c.layoutFlexItemPaged(it.box, widths[it], itemCB, sk, false, true, y, bottomSpace+box.PB+box.BB)
				it.outerH = it.laid.MarginHeight()
				if rs != nil {
					sks[index[it]] = rs
				}
			}
			if len(sks) == 0 {
				continue
			}
			rows[r].base = math.Max(0, limit-rows[r].pos)
			resume = Skip{r: sks}
			lastRow = r
			break
		}
		items = items[:0]
		for _, it := range kept {
			if it.row > lastRow {
				continue
			}
			if it.row+it.rowSpan > lastRow+1 {
				it.rowSpan = lastRow + 1 - it.row
			}
			items = append(items, it)
		}
		if lastRow >= firstRow {
			total = rows[lastRow].pos + rows[lastRow].base - y0
		} else {
			total = 0
		}
		specH = math.NaN()
		if skip == nil && resume == nil {
			paged = false
		}
	} else {
		paged = false
	}
	// Position items.
	for _, it := range items {
		b := it.laid
		x := cols[it.col].pos
		y := rows[it.row].pos
		w := areaSize(cols, it.col, it.colSpan, colGap)
		h := areaSize(rows, it.row, it.rowSpan, rowGap)
		js := b.Style.Kw(css.PJustifySelf)
		if js == "auto" || js == "normal" {
			js = s.Kw(css.PJustifyItems)
		}
		as := b.Style.Kw(css.PAlignSelf)
		if as == "auto" || as == "normal" {
			as = s.Kw(css.PAlignItems)
		}
		dx, dy := 0.0, 0.0
		switch js {
		case "center":
			dx = (w - b.MarginWidth()) / 2
		case "end", "flex-end", "right", "self-end":
			dx = w - b.MarginWidth()
		}
		switch as {
		case "center":
			dy = (h - b.MarginHeight()) / 2
		case "end", "flex-end", "self-end":
			dy = h - b.MarginHeight()
		case "normal", "stretch":
			if b.Style.IsAuto(css.PHeight) && b.Kind != KReplaced {
				nh := h - (b.MT + b.MB + b.PT + b.PB + b.BT + b.BB)
				if nh > b.H {
					b.H = nh
				}
			}
		}
		b.Translate(x+dx-b.X, y+dy-b.Y)
		children = append(children, b)
	}
	nb := box.CopyWith(children)
	nb.H = total
	if !math.IsNaN(specH) {
		nb.H = specH
	}
	if !paged {
		mn, mx := minMaxHeight(nb, cbi)
		nb.H = math.Max(mn, math.Min(mx, nb.H))
	}
	if skip != nil {
		nb.NoStartDecor = true
	}
	if resume != nil {
		nb.NoEndDecor = true
		nb.MB, nb.PB, nb.BB = 0, 0, 0
	}
	if box.Style.Kw(css.PPosition) != "static" {
		for _, a := range abs.boxes {
			c.absoluteLayout(a, nb, fixed)
		}
	}
	for _, ch := range nb.Children {
		relativePositioning(ch, nb.W, nb.H)
	}
	c.finishBFC(nil)
	np := nextPage{Break: "any", set: true}
	_, np.Page = pageValues(nb)
	return blockResult{box: nb, next: np, resume: resume}
}

func areaSize(tracks []*track, start, span int, gap float64) float64 {
	w := 0.0
	for i := start; i < start+span && i < len(tracks); i++ {
		w += tracks[i].base
		if i > start {
			w += gap
		}
	}
	return w
}

// sizeTracks implements a simplified grid track sizing algorithm.
func sizeTracks(tracks []*track, items []*gridItem, avail, gap float64, inline bool, distribution string) {
	n := len(tracks)
	if n == 0 {
		return
	}
	definite := !math.IsInf(avail, 1) && !math.IsNaN(avail)
	resolve := func(b css.TrackBreadth) (float64, bool) {
		if b.Kind == "length" {
			if b.L.IsPercent() && !definite {
				return 0, false
			}
			return b.L.Resolve(avail), true
		}
		return 0, false
	}
	for _, t := range tracks {
		if v, ok := resolve(t.size.Min); ok {
			t.base = v
		}
		if t.size.Max.Kind == "fr" {
			t.isFlex = true
			t.fr = t.size.Max.Fr
			t.limit = math.Inf(1)
		} else if v, ok := resolve(t.size.Max); ok {
			t.limit = math.Max(v, t.base)
		} else {
			t.limit = math.Inf(1)
			t.autoGrows = true
		}
	}
	// Content contributions of items spanning one track, then spanning
	// items.
	sort.SliceStable(items, func(i, j int) bool { return items[i].colSpan < items[j].colSpan })
	contentLimit := make([]float64, n)
	hasContent := make([]bool, n)
	for _, it := range items {
		if it.col < 0 || it.col >= n {
			continue
		}
		if it.colSpan == 1 {
			t := tracks[it.col]
			minKind := t.size.Min.Kind
			if minKind == "auto" || minKind == "min-content" {
				t.base = math.Max(t.base, it.minW)
			} else if minKind == "max-content" {
				t.base = math.Max(t.base, it.maxW)
			}
			if t.size.Max.Kind == "auto" || t.size.Max.Kind == "max-content" {
				contentLimit[it.col] = math.Max(contentLimit[it.col], it.maxW)
				hasContent[it.col] = true
			} else if t.size.Max.Kind == "min-content" {
				contentLimit[it.col] = math.Max(contentLimit[it.col], it.minW)
				hasContent[it.col] = true
			}
			continue
		}
		// Spanning items: distribute extra minimum over non-flex tracks.
		end := min(n, it.col+it.colSpan)
		sum := gap * float64(end-it.col-1)
		var growable []*track
		for i := it.col; i < end; i++ {
			sum += tracks[i].base
			if !tracks[i].isFlex && (tracks[i].size.Min.Kind != "length") {
				growable = append(growable, tracks[i])
			}
		}
		if extra := it.minW - sum; extra > 0 && len(growable) > 0 {
			for _, t := range growable {
				t.base += extra / float64(len(growable))
			}
		}
	}
	for i, t := range tracks {
		if t.autoGrows {
			if hasContent[i] {
				t.limit = math.Max(t.base, contentLimit[i])
			} else {
				t.limit = t.base
			}
		}
		if t.size.FitContent != nil {
			t.limit = math.Max(t.base, math.Min(t.limit, t.size.FitContent.Resolve(avail)))
		}
		if t.limit < t.base {
			t.limit = t.base
		}
	}
	gaps := gap * float64(n-1)
	used := func() float64 {
		s := gaps
		for _, t := range tracks {
			s += t.base
		}
		return s
	}
	// Maximize tracks.
	if definite {
		freeSpace := avail - used()
		for iter := 0; iter < 10 && freeSpace > 1e-6; iter++ {
			var growable []*track
			for _, t := range tracks {
				if !t.isFlex && t.base < t.limit {
					growable = append(growable, t)
				}
			}
			if len(growable) == 0 {
				break
			}
			share := freeSpace / float64(len(growable))
			for _, t := range growable {
				d := math.Min(share, t.limit-t.base)
				t.base += d
				freeSpace -= d
			}
		}
	} else {
		for _, t := range tracks {
			if !t.isFlex && !math.IsInf(t.limit, 1) {
				t.base = t.limit
			}
		}
	}
	// Flexible tracks.
	frSum := 0.0
	for _, t := range tracks {
		if t.isFlex {
			frSum += t.fr
		}
	}
	if frSum > 0 {
		if definite {
			leftover := avail - gaps
			for _, t := range tracks {
				if !t.isFlex {
					leftover -= t.base
				}
			}
			// Tracks whose base exceeds their share are treated as inflexible.
			flexible := map[*track]bool{}
			for _, t := range tracks {
				if t.isFlex {
					flexible[t] = true
				}
			}
			for iter := 0; iter < 10; iter++ {
				fs := 0.0
				space := leftover
				for t := range flexible {
					fs += t.fr
				}
				for _, t := range tracks {
					if t.isFlex && !flexible[t] {
						space -= t.base
					}
				}
				if fs <= 0 {
					break
				}
				hyp := space / math.Max(fs, 1)
				changed := false
				for t := range flexible {
					if t.fr*hyp < t.base {
						delete(flexible, t)
						changed = true
					}
				}
				if !changed {
					for t := range flexible {
						t.base = t.fr * hyp
					}
					break
				}
			}
		} else {
			// Indefinite: size flex tracks from their content.
			flexFraction := 0.0
			for i, t := range tracks {
				if t.isFlex {
					ff := math.Max(t.base, contentLimit[i])
					if t.fr > 1 {
						ff /= t.fr
					}
					flexFraction = math.Max(flexFraction, ff)
				}
			}
			for _, t := range tracks {
				if t.isFlex {
					t.base = math.Max(t.base, flexFraction*t.fr)
				}
			}
		}
	} else if definite && (distribution == "normal" || distribution == "stretch") {
		// Stretch auto tracks.
		freeSpace := avail - used()
		var autos []*track
		for _, t := range tracks {
			if t.size.Max.Kind == "auto" {
				autos = append(autos, t)
			}
		}
		if freeSpace > 0 && len(autos) > 0 {
			for _, t := range autos {
				t.base += freeSpace / float64(len(autos))
			}
		}
	}
}

func (c *Context) gridContentWidth(box *Box, outer, minimum bool) float64 {
	s := box.Style
	if l, ok := s.Get(css.PWidth).(css.Length); ok && !l.IsPercent() {
		return adjustWidth(box, outer, l.V-boxSizingWidthStyle(s))
	}
	cols := expandTracks(s.Get(css.PGridTemplateColumns), math.Inf(1), 0)
	n := len(cols)
	if n == 0 {
		n = 1
	}
	gap := 0.0
	if l, ok := s.Get(css.PColumnGap).(css.Length); ok && !l.IsPercent() {
		gap = l.V
	}
	widths := make([]float64, n)
	i := 0
	for _, ch := range box.Children {
		if ch.IsAbsolutelyPositioned() {
			continue
		}
		w := c.contentWidth(ch, true, minimum)
		col := i % n
		if col < len(cols) && cols[col].Max.Kind == "length" && !cols[col].Max.L.IsPercent() {
			w = cols[col].Max.L.V
		}
		widths[col] = math.Max(widths[col], w)
		i++
	}
	total := gap * float64(n-1)
	for _, w := range widths {
		total += w
	}
	return adjustWidth(box, outer, total)
}

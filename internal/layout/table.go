package layout

import (
	"math"

	"github.com/esoteric-programmer/weasyprint-go/internal/css"
)

type tableInfo struct {
	ncols        int
	colWidths    []float64
	spacingH     float64
	spacingV     float64
	contentWidth float64 // table content width
	minWidth     float64
	maxWidth     float64
	collapse     *collapsedBorders
	widthCB      float64
}

func tableBox(wrapper *Box) *Box {
	for _, ch := range wrapper.Children {
		if ch.Kind == KTable {
			return ch
		}
	}
	return nil
}

func isCollapsed(t *Box) bool { return t.Style.Kw(css.PBorderCollapse) == "collapse" }

// assignGrid computes GridX for cells and returns the number of columns.
func assignGrid(table *Box) int {
	ncols := 0
	for _, g := range table.Children {
		if g.Kind == KColGroup {
			n := 0
			for _, col := range g.Children {
				span := col.Span
				if span < 1 {
					span = 1
				}
				n += span
			}
			ncols += n
		}
	}
	colsFromCols := ncols
	ncols = 0
	for _, g := range table.Children {
		if g.Kind != KRowGroup {
			continue
		}
		occupied := map[[2]int]bool{}
		nrows := len(g.Children)
		for ri, row := range g.Children {
			x := 0
			for _, cell := range row.Children {
				for occupied[[2]int{ri, x}] {
					x++
				}
				cell.GridX = x
				rs := cell.Rowspan
				if rs == 0 {
					rs = nrows - ri
				}
				if rs < 1 {
					rs = 1
				}
				if ri+rs > nrows {
					rs = nrows - ri
				}
				cell.Rowspan = rs
				cs := cell.Colspan
				if cs < 1 {
					cs = 1
				}
				for dy := 0; dy < rs; dy++ {
					for dx := 0; dx < cs; dx++ {
						occupied[[2]int{ri + dy, x + dx}] = true
					}
				}
				x += cs
				if x > ncols {
					ncols = x
				}
			}
		}
	}
	if colsFromCols > ncols {
		ncols = colsFromCols
	}
	return ncols
}

func colElements(table *Box) []*Box {
	var cols []*Box
	for _, g := range table.Children {
		if g.Kind != KColGroup {
			continue
		}
		for _, col := range g.Children {
			span := col.Span
			if span < 1 {
				span = 1
			}
			for i := 0; i < span; i++ {
				cols = append(cols, col)
			}
		}
	}
	return cols
}

func (c *Context) tableInfoFor(table *Box) *tableInfo {
	orig := table.Original()
	if ti, ok := c.tableCache[orig]; ok {
		return ti
	}
	ti := &tableInfo{}
	ti.ncols = assignGrid(table)
	if isCollapsed(table) {
		ti.collapse = computeCollapsedBorders(table, ti.ncols)
		markCollapse(table, ti.collapse)
	} else if sp, ok := table.Style.Get(css.PBorderSpacing).([2]css.Length); ok {
		ti.spacingH, ti.spacingV = sp[0].V, sp[1].V
	}
	c.tableCache[orig] = ti
	return ti
}

func markCollapse(table *Box, cb *collapsedBorders) {
	table.Collapse = cb
	for _, g := range table.Children {
		for _, row := range g.Children {
			if g.Kind != KRowGroup {
				continue
			}
			for _, cell := range row.Children {
				cell.Collapse = cb
			}
		}
	}
}

// columnPreferred returns min and max widths per column.
func (c *Context) columnPreferred(table *Box, ti *tableInfo) ([]float64, []float64, []float64) {
	n := ti.ncols
	mins := make([]float64, n)
	maxs := make([]float64, n)
	pcts := make([]float64, n)
	// Column element widths.
	for i, col := range colElements(table) {
		if i >= n {
			break
		}
		if l, ok := col.Style.Get(css.PWidth).(css.Length); ok {
			if l.Unit == "%" {
				pcts[i] = l.V
			} else {
				mins[i] = math.Max(mins[i], l.V)
				maxs[i] = math.Max(maxs[i], l.V)
			}
		}
	}
	type spanCell struct {
		cell     *Box
		min, max float64
	}
	var spans []spanCell
	for _, g := range table.Children {
		if g.Kind != KRowGroup {
			continue
		}
		for _, row := range g.Children {
			for _, cell := range row.Children {
				mn := c.minContent(cell, true)
				mx := c.maxContent(cell, true)
				if l, ok := cell.Style.Get(css.PWidth).(css.Length); ok && !l.IsPercent() {
					w := marginWidthOf(cell, l.V-boxSizingWidthStyle(cell.Style), true, true)
					mx = math.Max(mn, w)
					mn = math.Max(mn, 0)
				}
				if mx < mn {
					mx = mn
				}
				if cell.Colspan <= 1 {
					x := cell.GridX
					if x < n {
						mins[x] = math.Max(mins[x], mn)
						maxs[x] = math.Max(maxs[x], mx)
						if l, ok := cell.Style.Get(css.PWidth).(css.Length); ok && l.Unit == "%" {
							pcts[x] = math.Max(pcts[x], l.V)
						}
					}
				} else {
					spans = append(spans, spanCell{cell, mn, mx})
				}
			}
		}
	}
	// Distribute spanning cells.
	for _, sc := range spans {
		x0 := sc.cell.GridX
		x1 := x0 + sc.cell.Colspan
		if x1 > n {
			x1 = n
		}
		spacing := float64(x1-x0-1) * ti.spacingH
		curMin, curMax := 0.0, 0.0
		for i := x0; i < x1; i++ {
			curMin += mins[i]
			curMax += maxs[i]
		}
		if extra := sc.min - spacing - curMin; extra > 0 {
			for i := x0; i < x1; i++ {
				share := extra / float64(x1-x0)
				if curMax > 0 {
					share = extra * maxs[i] / curMax
				}
				mins[i] += share
			}
		}
		if extra := sc.max - spacing - curMax; extra > 0 {
			for i := x0; i < x1; i++ {
				share := extra / float64(x1-x0)
				if curMax > 0 {
					share = extra * maxs[i] / curMax
				}
				maxs[i] += share
			}
		}
	}
	for i := range maxs {
		if maxs[i] < mins[i] {
			maxs[i] = mins[i]
		}
	}
	return mins, maxs, pcts
}

func tableEdges(table *Box, cbW float64) float64 {
	s := table.Style
	w := s.Px(css.PPaddingLeft, cbW) + s.Px(css.PPaddingRight, cbW)
	if table.Collapse != nil {
		_, r, _, l := table.Collapse.tableBorders()
		return w + r + l
	}
	return w + s.BorderWidth(1) + s.BorderWidth(3)
}

// tablePreferredWidths returns the min- and max-content widths of a table
// wrapper.
func (c *Context) tablePreferredWidths(wrapper *Box, outer bool) (float64, float64) {
	table := tableBox(wrapper)
	if table == nil {
		return 0, 0
	}
	ti := c.tableInfoFor(table)
	mins, maxs, _ := c.columnPreferred(table, ti)
	spacing := float64(ti.ncols+1) * ti.spacingH
	if ti.ncols == 0 {
		spacing = 0
	}
	mn, mx := spacing, spacing
	for i := range mins {
		mn += mins[i]
		mx += maxs[i]
	}
	edges := tableEdges(table, 0)
	mn += edges
	mx += edges
	if l, ok := table.Style.Get(css.PWidth).(css.Length); ok && !l.IsPercent() {
		w := l.V
		if table.Style.Kw(css.PBoxSizing) != "border-box" {
			w += edges
		}
		mn = math.Max(mn, w)
		mx = math.Max(mn, w)
	}
	for _, ch := range wrapper.Children {
		if ch.Kind == KCaption {
			mn = math.Max(mn, c.minContent(ch, true))
		}
	}
	if outer {
		mn = marginWidthOf(wrapper, mn, true, true)
		mx = marginWidthOf(wrapper, mx, true, true)
	}
	return mn, mx
}

// tableWrapperWidth sets the wrapper width from the table width.
func (c *Context) tableWrapperWidth(wrapper *Box, cbi cbInfo) {
	table := tableBox(wrapper)
	if table == nil {
		return
	}
	ti := c.tableInfoFor(table)
	resolvePercentages(table, cbi.W)
	edges := table.PL + table.PR + table.BL + table.BR
	mins, maxs, pcts := c.columnPreferred(table, ti)
	n := ti.ncols
	spacing := 0.0
	if n > 0 {
		spacing = float64(n+1) * ti.spacingH
	}
	sumMin, sumMax := 0.0, 0.0
	for i := range mins {
		sumMin += mins[i]
		sumMax += maxs[i]
	}
	tableMin := sumMin + spacing
	tableMax := sumMax + spacing
	ml := nanTo0(wrapper.ML)
	mr := nanTo0(wrapper.MR)
	if wrapper.Style != nil {
		if v, ok := lengthOrAuto(wrapper.Style, css.PMarginLeft, cbi.W); ok {
			ml = v
		} else {
			ml = 0
		}
		if v, ok := lengthOrAuto(wrapper.Style, css.PMarginRight, cbi.W); ok {
			mr = v
		} else {
			mr = 0
		}
	}
	avail := cbi.W - ml - mr - edges
	var width float64
	specified, isSpecified := lengthOrAuto(table.Style, css.PWidth, cbi.W)
	if isSpecified && table.Style.Kw(css.PBoxSizing) == "border-box" {
		specified -= edges
	}
	fixedLayout := table.Style.Kw(css.PTableLayout) == "fixed" && isSpecified
	if fixedLayout {
		width = math.Max(specified, 0)
		ti.colWidths = c.fixedColumns(table, ti, width-spacing)
		sum := 0.0
		for _, w := range ti.colWidths {
			sum += w
		}
		width = math.Max(width, sum+spacing)
	} else {
		if isSpecified {
			width = math.Max(specified, tableMin)
		} else {
			width = math.Max(tableMin, math.Min(avail, tableMax))
			// Percentage columns may require a larger table.
			totalPct := 0.0
			for _, p := range pcts {
				totalPct += p
			}
			if totalPct > 0 && totalPct < 100 {
				nonPct := 0.0
				for i := range maxs {
					if pcts[i] == 0 {
						nonPct += maxs[i]
					}
				}
				need := nonPct / (1 - totalPct/100)
				for i := range maxs {
					if pcts[i] > 0 {
						need = math.Max(need, maxs[i]*100/pcts[i])
					}
				}
				width = math.Max(width, math.Min(avail, need+spacing))
			}
		}
		ti.colWidths = distributeColumns(width-spacing, mins, maxs, pcts)
	}
	ti.contentWidth = width
	ti.widthCB = cbi.W
	ti.minWidth, ti.maxWidth = tableMin, tableMax
	wrapper.W = width + edges
}

func (c *Context) fixedColumns(table *Box, ti *tableInfo, avail float64) []float64 {
	n := ti.ncols
	widths := make([]float64, n)
	set := make([]bool, n)
	for i, col := range colElements(table) {
		if i >= n {
			break
		}
		if v, ok := lengthOrAuto(col.Style, css.PWidth, avail); ok {
			widths[i] = v
			set[i] = true
		}
	}
	// First row cells.
	for _, g := range table.Children {
		if g.Kind != KRowGroup || len(g.Children) == 0 {
			continue
		}
		for _, cell := range g.Children[0].Children {
			if v, ok := lengthOrAuto(cell.Style, css.PWidth, avail); ok {
				w := marginWidthOf(cell, v-boxSizingWidthStyle(cell.Style), true, true)
				cs := cell.Colspan
				if cs < 1 {
					cs = 1
				}
				for i := cell.GridX; i < cell.GridX+cs && i < n; i++ {
					if !set[i] {
						widths[i] = w / float64(cs)
						set[i] = true
					}
				}
			}
		}
		break
	}
	used := 0.0
	unset := 0
	for i := range widths {
		if set[i] {
			used += widths[i]
		} else {
			unset++
		}
	}
	if unset > 0 {
		each := math.Max(0, avail-used) / float64(unset)
		for i := range widths {
			if !set[i] {
				widths[i] = each
			}
		}
	} else if used < avail && used > 0 {
		for i := range widths {
			widths[i] += (avail - used) * widths[i] / used
		}
	}
	return widths
}

// distributeColumns distributes width among columns.
func distributeColumns(width float64, mins, maxs, pcts []float64) []float64 {
	n := len(mins)
	out := make([]float64, n)
	if n == 0 {
		return out
	}
	sumMin, sumMax := 0.0, 0.0
	for i := range mins {
		sumMin += mins[i]
		sumMax += maxs[i]
	}
	// Percentage columns first.
	pctUsed := 0.0
	hasPct := false
	for i := range pcts {
		if pcts[i] > 0 {
			hasPct = true
		}
	}
	if hasPct {
		rest := width
		for i := range out {
			if pcts[i] > 0 {
				out[i] = math.Max(mins[i], pcts[i]*width/100)
				pctUsed += out[i]
			}
		}
		rest = width - pctUsed
		var amins, amaxs []float64
		var idx []int
		for i := range out {
			if pcts[i] == 0 {
				amins = append(amins, mins[i])
				amaxs = append(amaxs, maxs[i])
				idx = append(idx, i)
			}
		}
		if len(idx) == 0 {
			// Scale percentage columns to fill the width.
			if pctUsed > 0 && pctUsed != width {
				for i := range out {
					out[i] = out[i] * width / pctUsed
				}
			}
			return out
		}
		sub := distributeColumns(math.Max(rest, 0), amins, amaxs, make([]float64, len(idx)))
		for j, i := range idx {
			out[i] = sub[j]
		}
		return out
	}
	switch {
	case width >= sumMax:
		extra := width - sumMax
		if sumMax > 0 {
			for i := range out {
				out[i] = maxs[i] + extra*maxs[i]/sumMax
			}
		} else {
			for i := range out {
				out[i] = width / float64(n)
			}
		}
	case width <= sumMin:
		copy(out, mins)
	default:
		d := sumMax - sumMin
		for i := range out {
			if d > 0 {
				out[i] = mins[i] + (width-sumMin)*(maxs[i]-mins[i])/d
			} else {
				out[i] = mins[i]
			}
		}
	}
	return out
}

// tableLayout lays out a table box (inside its wrapper).
func (c *Context) tableLayout(table *Box, bottomSpace float64, skip Skip, wrapper *Box, cbi cbInfo, pageIsEmpty bool, abs, fixed *absList) blockResult {
	ti := c.tableInfoFor(table)
	resolvePercentages(table, cbi.W)
	table.ML, table.MR, table.MT, table.MB = 0, 0, 0, 0
	if ti.colWidths == nil {
		c.tableWrapperWidth(wrapper, cbi)
	}
	table.W = ti.contentWidth
	table.ColumnWidths = ti.colWidths
	// Column positions.
	positions := make([]float64, ti.ncols)
	x := table.ContentX() + ti.spacingH
	rtl := table.Style.Kw(css.PDirection) == "rtl"
	if rtl {
		x = table.ContentX() + table.W - ti.spacingH
		for i, w := range ti.colWidths {
			x -= w
			positions[i] = x
			x -= ti.spacingH
		}
	} else {
		for i, w := range ti.colWidths {
			positions[i] = x
			x += w + ti.spacingH
		}
	}
	table.ColumnPositions = positions
	isStart := skip == nil
	if !isStart {
		table.BT, table.PT = 0, 0
	}
	posY := table.ContentY()
	if ti.spacingV > 0 && (isStart || true) {
		posY += ti.spacingV
	}
	startGroup, groupSkip := 0, Skip(nil)
	if !isStart {
		startGroup, groupSkip = skipFirst(skip)
	}
	var header, footer *Box
	for _, g := range table.Children {
		if g.Kind == KRowGroup && g.IsHeader {
			header = g
		}
		if g.Kind == KRowGroup && g.IsFooter {
			footer = g
		}
	}
	var newChildren []*Box
	// Column groups are kept for backgrounds.
	for _, g := range table.Children {
		if g.Kind == KColGroup {
			ng := g.CopyWith(nil)
			newChildren = append(newChildren, ng)
		}
	}
	// Repeated header.
	if header != nil && !isStart && table.Children[startGroup] != header {
		hg, h := c.layoutRowGroup(header, table, ti, posY, nil, math.Inf(-1), true, abs, fixed)
		if hg != nil {
			newChildren = append(newChildren, hg)
			posY += h
		}
	}
	// Footer height to reserve.
	footerH := 0.0
	var footerBox *Box
	if footer != nil {
		fb, h := c.layoutRowGroup(footer, table, ti, 0, nil, math.Inf(-1), true, abs, fixed)
		footerBox = fb
		footerH = h
		if fb != nil {
			c.removePlaceholders([]*Box{fb}, abs, fixed)
		}
	}
	bottomPad := table.PB + table.BB + ti.spacingV
	var resume Skip
	rowsOnPage := 0
	for gi := startGroup; gi < len(table.Children); gi++ {
		g := table.Children[gi]
		if g.Kind != KRowGroup || g == footer {
			continue
		}
		var gs Skip
		if gi == startGroup {
			gs = groupSkip
		}
		bs := bottomSpace + bottomPad
		if g != footer && footerH > 0 {
			bs += footerH
		}
		ng, h, rs, nrows := c.layoutRowGroupPaged(g, table, ti, posY, gs, bs, pageIsEmpty && rowsOnPage == 0, abs, fixed)
		if ng != nil {
			newChildren = append(newChildren, ng)
			posY += h
			rowsOnPage += nrows
		}
		if rs != nil || ng == nil {
			if ng == nil && rowsOnPage == 0 && !pageIsEmpty {
				// Nothing fits: move the whole table.
				c.removePlaceholders(newChildren, abs, fixed)
				return blockResult{next: nextPage{Break: "any"}}
			}
			if rs != nil {
				resume = Skip{gi: rs}
			} else {
				resume = Skip{gi: nil}
			}
			break
		}
	}
	if footerBox != nil {
		fb, h := c.layoutRowGroup(footer, table, ti, posY, nil, math.Inf(-1), true, abs, fixed)
		if fb != nil {
			newChildren = append(newChildren, fb)
			posY += h
		}
	}
	nt := table.CopyWith(newChildren)
	nt.NoStartDecor = !isStart || table.NoStartDecor
	nt.Collapse = ti.collapse
	if resume != nil {
		nt.NoEndDecor = true
		nt.PB, nt.BB = 0, 0
	}
	contentH := posY - nt.ContentY()
	if h, ok := lengthOrAuto(table.Style, css.PHeight, cbi.H); ok && !(cbi.HAuto && isPercentStyle(table.Style, css.PHeight)) {
		h -= boxSizingHeight(nt)
		contentH = math.Max(contentH, h)
	}
	nt.H = contentH
	np := nextPage{Break: "any"}
	_, np.Page = pageValues(nt)
	np.set = true
	return blockResult{box: nt, resume: resume, next: np}
}

func isPercentStyle(s *css.Style, id css.PropID) bool {
	l, ok := s.Get(id).(css.Length)
	return ok && l.IsPercent()
}

// layoutRowGroup lays out a whole row group without page breaks.
func (c *Context) layoutRowGroup(g, table *Box, ti *tableInfo, y float64, skip Skip, bottomSpace float64, empty bool, abs, fixed *absList) (*Box, float64) {
	saved := c.saveBottom()
	c.PageBottom = math.Inf(1)
	ng, h, _, _ := c.layoutRowGroupPaged(g, table, ti, y, skip, math.Inf(-1), true, abs, fixed)
	c.restoreBottom(saved)
	return ng, h
}

// layoutRowGroupPaged lays out rows of a group until the page is full.
// It returns the new group, its height (including vertical spacing after
// each row), the resume skip and the number of rows laid out.
func (c *Context) layoutRowGroupPaged(g, table *Box, ti *tableInfo, y float64, skip Skip, bottomSpace float64, pageIsEmpty bool, abs, fixed *absList) (*Box, float64, Skip, int) {
	ng := g.CopyWith(nil)
	resolvePercentages(ng, table.W)
	ng.MT, ng.MB, ng.ML, ng.MR = 0, 0, 0, 0
	ng.X = table.ContentX()
	ng.Y = y
	ng.W = table.W
	start := 0
	var rowSkip Skip
	if skip != nil {
		start, rowSkip = skipFirst(skip)
		ng.NoStartDecor = true
	}
	rows := g.Children
	n := len(rows)
	heights := make([]float64, n)
	type laid struct {
		row   *Box
		cells []*Box
	}
	var out []laid
	posY := y
	var resume Skip
	// Avoid breaking inside groups with break-inside: avoid.
	for ri := start; ri < n; ri++ {
		row := rows[ri]
		nr := row.CopyWith(nil)
		resolvePercentages(nr, table.W)
		nr.MT, nr.MB, nr.ML, nr.MR = 0, 0, 0, 0
		nr.X = table.ContentX()
		nr.Y = posY
		nr.W = table.W
		var cells []*Box
		rowH := 0.0
		if v, ok := lengthOrAuto(row.Style, css.PHeight, 0); ok && !isPercentStyle(row.Style, css.PHeight) {
			rowH = v
		}
		continuing := ri == start && rowSkip != nil
		for ci, cell := range row.Children {
			var nc *Box
			if continuing {
				cs, has := rowSkip[ci]
				nc, _ = c.layoutCellPagedInf(cell, table, ti, posY, cs, !has)
			} else {
				nc = c.layoutCell(cell, table, ti, posY)
			}
			cells = append(cells, nc)
			if cell.Rowspan <= 1 {
				rowH = math.Max(rowH, nc.BorderHeight())
			}
		}
		if continuing {
			nr.NoStartDecor = true
		}
		heights[ri] = rowH
		// Rowspan cells ending in this row.
		for _, l := range out {
			for _, cl := range l.cells {
				startRow := indexOfRow(rows, l.row.Original())
				if startRow+cl.Rowspan-1 == ri {
					sum := 0.0
					for k := startRow; k <= ri; k++ {
						if k == ri {
							sum += rowH
						} else {
							sum += heights[k] + ti.spacingV
						}
					}
					if cl.BorderHeight() > sum {
						heights[ri] += cl.BorderHeight() - sum
					}
				}
			}
		}
		for _, cl := range cells {
			if cl.Rowspan > 1 && cl.Rowspan == 1+0 {
			}
		}
		rowH = heights[ri]
		// Page break check.
		if c.overflowsPage(bottomSpace, posY+rowH) {
			canMove := len(out) > 0 || !pageIsEmpty
			fitsFreshPage := rowH <= c.PageBottom-c.PageTop-bottomSpace
			avoid := row.Style.Kw(css.PBreakInside) != "auto"
			if canMove && (avoid || (fitsFreshPage && !continuing)) {
				c.removePlaceholders(cells, abs, fixed)
				if len(out) == 0 {
					return nil, 0, nil, 0
				}
				if continuing {
					resume = Skip{ri: rowSkip}
				} else {
					resume = Skip{ri: nil}
				}
				break
			}
			if !math.IsInf(c.PageBottom, 1) {
				// Split the row: fragment each cell at the page bottom.
				cellSkips := Skip{}
				c.unlayoutFootnotes(cells)
				cells = cells[:0]
				for ci, cell := range row.Children {
					var cs Skip
					has := true
					if continuing {
						cs, has = rowSkip[ci]
					}
					nc, rs := c.layoutCellPaged(cell, table, ti, posY, cs, true, !has, bottomSpace)
					cells = append(cells, nc)
					if rs != nil {
						cellSkips[ci] = rs
					}
				}
				if len(cellSkips) > 0 {
					rowH = math.Max(0, c.PageBottom-bottomSpace-posY)
					heights[ri] = rowH
					nr.H = rowH
					nr.NoEndDecor = true
					out = append(out, laid{nr, cells})
					posY += rowH + ti.spacingV
					resume = Skip{ri: cellSkips}
					break
				}
				rowH = 0
				for _, nc := range cells {
					rowH = math.Max(rowH, nc.BorderHeight())
				}
				heights[ri] = rowH
			}
		}
		nr.H = rowH
		out = append(out, laid{nr, cells})
		posY += rowH + ti.spacingV
		if ri+1 < n && forcePageBreak(blockLevelPageBreak(row, rows[ri+1])) {
			resume = Skip{ri + 1: nil}
			break
		}
	}
	// Final heights and vertical alignment of cells.
	for li, l := range out {
		ri := indexOfRow(rows, l.row.Original())
		l.row.H = heights[ri]
		for _, cl := range l.cells {
			span := cl.Rowspan
			if span < 1 {
				span = 1
			}
			h := 0.0
			for k := 0; k < span && li+k < len(out); k++ {
				rk := indexOfRow(rows, out[li+k].row.Original())
				h += heights[rk]
				if k > 0 {
					h += ti.spacingV
				}
			}
			c.finishCell(cl, h, l.cells)
		}
		l.row.Children = l.cells
		ng.Children = append(ng.Children, l.row)
	}
	// Baseline alignment of cells in each row.
	for _, l := range out {
		alignBaselines(l.cells)
	}
	total := posY - y
	ng.H = math.Max(0, total-ti.spacingV)
	if resume != nil {
		ng.NoEndDecor = true
	}
	return ng, total, resume, len(out)
}

func indexOfRow(rows []*Box, r *Box) int {
	for i, x := range rows {
		if x == r || x.Original() == r {
			return i
		}
	}
	return 0
}

func (c *Context) layoutCell(cell, table *Box, ti *tableInfo, y float64) *Box {
	nc, _ := c.layoutCellPaged(cell, table, ti, y, nil, false, false, 0)
	return nc
}

// layoutCellPaged lays out a cell. If constrained, the cell content is
// fragmented at the page bottom and the resume skip is returned. If empty,
// the cell is a continuation whose content is already finished.
func (c *Context) layoutCellPaged(cell, table *Box, ti *tableInfo, y float64, skip Skip, constrained, empty bool, bottomSpace float64) (*Box, Skip) {
	nc := cell.Copy()
	resolvePercentages(nc, table.W)
	nc.MT, nc.MB, nc.ML, nc.MR = 0, 0, 0, 0
	x0 := cell.GridX
	cs := cell.Colspan
	if cs < 1 {
		cs = 1
	}
	w := 0.0
	for i := x0; i < x0+cs && i < len(ti.colWidths); i++ {
		w += ti.colWidths[i]
	}
	w += float64(cs-1) * ti.spacingH
	x := table.ContentX()
	if x0 < len(table.ColumnPositions) {
		x = table.ColumnPositions[x0]
		if table.Style.Kw(css.PDirection) == "rtl" {
			last := x0 + cs - 1
			if last < len(table.ColumnPositions) {
				x = table.ColumnPositions[last]
			}
		}
	}
	nc.X, nc.Y = x, y
	nc.W = math.Max(0, w-nc.PL-nc.PR-nc.BL-nc.BR)
	nc.H = math.NaN()
	if empty {
		nc.Children = nil
		nc.NoStartDecor = true
		nc.H = 0
		nc.CellEmpty = true
		return nc, nil
	}
	saved := c.saveBottom()
	bs := math.Inf(-1)
	if constrained {
		bs = bottomSpace + nc.PB + nc.BB
	} else {
		c.PageBottom = math.Inf(1)
	}
	c.createBFC()
	res := c.blockContainerLayout(nc, bs, skip, true, &absList{}, &absList{}, nil, false, cbInfo{W: table.W, HAuto: true, Style: table.Style})
	c.finishBFC(res.box)
	c.restoreBottom(saved)
	out := res.box
	resume := res.resume
	if out == nil {
		out = nc
		out.Children = nil
		out.H = 0
		resume = skip
		if resume == nil {
			resume = Skip{0: nil}
		}
	}
	if skip != nil {
		out.NoStartDecor = true
	}
	if !constrained || resume == nil {
		if v, ok := lengthOrAuto(cell.Style, css.PHeight, 0); ok && !isPercentStyle(cell.Style, css.PHeight) {
			v -= boxSizingHeight(out)
			if v > out.H {
				out.H = v
			}
		}
	}
	if resume != nil {
		out.NoEndDecor = true
	}
	out.CellEmpty = cellIsEmpty(out)
	return out, resume
}

func cellIsEmpty(b *Box) bool {
	for _, ch := range b.Children {
		if ch.IsAbsPlaceholder {
			continue
		}
		if ch.Kind == KLine {
			if !isPhantomLine(ch) {
				return false
			}
			continue
		}
		return false
	}
	return true
}

// finishCell sets the cell height to fill its rows and applies
// vertical-align.
func (c *Context) finishCell(cell *Box, rowsHeight float64, siblings []*Box) {
	contentH := 0.0
	for _, ch := range cell.Children {
		if ch.IsAbsPlaceholder || ch.IsFloated() {
			continue
		}
		if b := ch.Y + ch.MarginHeight() - cell.ContentY(); b > contentH {
			contentH = b
		}
	}
	newH := rowsHeight - cell.PT - cell.PB - cell.BT - cell.BB
	extra := newH - contentH
	cell.H = math.Max(newH, cell.H)
	if extra <= 0 {
		return
	}
	var dy float64
	switch cell.Style.Kw(css.PVerticalAlign) {
	case "middle":
		dy = extra / 2
	case "bottom":
		dy = extra
	default:
		return
	}
	for _, ch := range cell.Children {
		ch.Translate(0, dy)
	}
}

func alignBaselines(cells []*Box) {
	var baseline float64
	var aligned []*Box
	var bls []float64
	for _, cl := range cells {
		va := cl.Style.Kw(css.PVerticalAlign)
		if va != "baseline" && va != "" && va != "sub" && va != "super" && va != "text-top" && va != "text-bottom" {
			if _, isLen := cl.Style.Get(css.PVerticalAlign).(css.Length); !isLen {
				continue
			}
		}
		if cl.Rowspan > 1 {
			continue
		}
		bl, ok := firstBaseline(cl)
		if !ok {
			bl = cl.ContentY() + cl.H
		}
		aligned = append(aligned, cl)
		bls = append(bls, bl)
		if bl > baseline {
			baseline = bl
		}
	}
	for i, cl := range aligned {
		dy := baseline - bls[i]
		if dy <= 0 {
			continue
		}
		for _, ch := range cl.Children {
			ch.Translate(0, dy)
		}
		// Grow the cell content if needed (the row height already covers it
		// in common cases).
		bottom := 0.0
		for _, ch := range cl.Children {
			if b := ch.Y + ch.MarginHeight(); b > bottom {
				bottom = b
			}
		}
		if bottom > cl.ContentY()+cl.H {
			cl.H = bottom - cl.ContentY()
		}
	}
}

// ---------------------------------------------------------------- collapsed borders

type borderSpec struct {
	width float64
	style string
	color css.Color
	prio  int // source priority: cell 4, row 3, row group 2, column 1, table 0
}

var styleScore = map[string]int{
	"hidden": 10, "double": 9, "solid": 8, "dashed": 7, "dotted": 6,
	"ridge": 5, "outset": 4, "groove": 3, "inset": 2, "none": 0,
}

func better(a, b borderSpec) bool {
	if b.style == "hidden" {
		return false
	}
	if a.style == "hidden" {
		return true
	}
	if a.style == "none" {
		return false
	}
	if b.style == "none" {
		return true
	}
	if a.width != b.width {
		return a.width > b.width
	}
	if styleScore[a.style] != styleScore[b.style] {
		return styleScore[a.style] > styleScore[b.style]
	}
	return a.prio > b.prio
}

type collapsedBorders struct {
	nrows, ncols int
	// horizontal[r][c]: border above row r (r in 0..nrows) for column c.
	horizontal [][]borderSpec
	// vertical[r][c]: border left of column c (c in 0..ncols) for row r.
	vertical [][]borderSpec
	cellPos  map[*Box][2]int // cell → (global row, col)
	rowIndex map[*Box]int
}

func sideSpec(s *css.Style, side int, prio int) borderSpec {
	st := s.BorderStyle(side)
	w := s.BorderWidth(side)
	if st == "none" || st == "hidden" {
		w = 0
	}
	return borderSpec{width: w, style: st, color: s.BorderColor(side), prio: prio}
}

func computeCollapsedBorders(table *Box, ncols int) *collapsedBorders {
	cb := &collapsedBorders{ncols: ncols, cellPos: map[*Box][2]int{}, rowIndex: map[*Box]int{}}
	type rowInfo struct {
		row   *Box
		group *Box
	}
	var rows []rowInfo
	for _, g := range table.Children {
		if g.Kind != KRowGroup {
			continue
		}
		for _, r := range g.Children {
			rows = append(rows, rowInfo{r, g})
		}
	}
	nr := len(rows)
	cb.nrows = nr
	cb.horizontal = make([][]borderSpec, nr+1)
	for i := range cb.horizontal {
		cb.horizontal[i] = make([]borderSpec, ncols)
	}
	cb.vertical = make([][]borderSpec, nr)
	for i := range cb.vertical {
		cb.vertical[i] = make([]borderSpec, ncols+1)
	}
	set := func(cur *borderSpec, b borderSpec) {
		if better(b, *cur) || (cur.style == "" && b.style != "") {
			*cur = b
		}
	}
	// Table borders on the outside.
	ts := table.Style
	for c := 0; c < ncols; c++ {
		set(&cb.horizontal[0][c], sideSpec(ts, 0, 0))
		set(&cb.horizontal[nr][c], sideSpec(ts, 2, 0))
	}
	for r := 0; r < nr; r++ {
		set(&cb.vertical[r][0], sideSpec(ts, 3, 0))
		set(&cb.vertical[r][ncols], sideSpec(ts, 1, 0))
	}
	// Columns.
	for ci, col := range colElements(table) {
		if ci >= ncols {
			break
		}
		for r := 0; r < nr; r++ {
			set(&cb.vertical[r][ci], sideSpec(col.Style, 3, 1))
			set(&cb.vertical[r][ci+1], sideSpec(col.Style, 1, 1))
		}
	}
	// Row groups and rows.
	ri := 0
	for _, g := range table.Children {
		if g.Kind != KRowGroup || len(g.Children) == 0 {
			continue
		}
		first, last := ri, ri+len(g.Children)-1
		for c := 0; c < ncols; c++ {
			set(&cb.horizontal[first][c], sideSpec(g.Style, 0, 2))
			set(&cb.horizontal[last+1][c], sideSpec(g.Style, 2, 2))
		}
		for r := first; r <= last; r++ {
			set(&cb.vertical[r][0], sideSpec(g.Style, 3, 2))
			set(&cb.vertical[r][ncols], sideSpec(g.Style, 1, 2))
		}
		for _, row := range g.Children {
			cb.rowIndex[row] = ri
			for c := 0; c < ncols; c++ {
				set(&cb.horizontal[ri][c], sideSpec(row.Style, 0, 3))
				set(&cb.horizontal[ri+1][c], sideSpec(row.Style, 2, 3))
			}
			set(&cb.vertical[ri][0], sideSpec(row.Style, 3, 3))
			set(&cb.vertical[ri][ncols], sideSpec(row.Style, 1, 3))
			ri++
		}
	}
	// Cells.
	for r, info := range rows {
		for _, cell := range info.row.Children {
			cb.cellPos[cell] = [2]int{r, cell.GridX}
			cs, rs := cell.Colspan, cell.Rowspan
			if cs < 1 {
				cs = 1
			}
			if rs < 1 {
				rs = 1
			}
			for c := cell.GridX; c < cell.GridX+cs && c < ncols; c++ {
				set(&cb.horizontal[r][c], sideSpec(cell.Style, 0, 4))
				if r+rs <= nr {
					set(&cb.horizontal[r+rs][c], sideSpec(cell.Style, 2, 4))
				}
			}
			for rr := r; rr < r+rs && rr < nr; rr++ {
				if cell.GridX < ncols+1 {
					set(&cb.vertical[rr][cell.GridX], sideSpec(cell.Style, 3, 4))
				}
				if cell.GridX+cs <= ncols {
					set(&cb.vertical[rr][cell.GridX+cs], sideSpec(cell.Style, 1, 4))
				}
			}
		}
	}
	return cb
}

func (cb *collapsedBorders) width(b borderSpec) float64 {
	if b.style == "none" || b.style == "hidden" {
		return 0
	}
	return b.width
}

func (cb *collapsedBorders) tableBorders() (float64, float64, float64, float64) {
	var t, r, b, l float64
	for c := 0; c < cb.ncols; c++ {
		t = math.Max(t, cb.width(cb.horizontal[0][c]))
		b = math.Max(b, cb.width(cb.horizontal[cb.nrows][c]))
	}
	if cb.nrows > 0 {
		l = cb.width(cb.vertical[0][0])
		r = cb.width(cb.vertical[0][cb.ncols])
	}
	return t / 2, r / 2, b / 2, l / 2
}

func (cb *collapsedBorders) cellBorders(cell *Box) (float64, float64, float64, float64) {
	pos, ok := cb.cellPos[cell.Original()]
	if !ok {
		pos, ok = cb.cellPos[cell]
		if !ok {
			return 0, 0, 0, 0
		}
	}
	r, c := pos[0], pos[1]
	cs, rs := cell.Colspan, cell.Rowspan
	if cs < 1 {
		cs = 1
	}
	if rs < 1 {
		rs = 1
	}
	var t, rt, b, l float64
	for x := c; x < c+cs && x < cb.ncols; x++ {
		t = math.Max(t, cb.width(cb.horizontal[r][x]))
		if r+rs <= cb.nrows {
			b = math.Max(b, cb.width(cb.horizontal[r+rs][x]))
		}
	}
	for y := r; y < r+rs && y < cb.nrows; y++ {
		l = math.Max(l, cb.width(cb.vertical[y][c]))
		if c+cs <= cb.ncols {
			rt = math.Max(rt, cb.width(cb.vertical[y][c+cs]))
		}
	}
	return t / 2, rt / 2, b / 2, l / 2
}

// CollapsedEdges returns, for drawing, the borders around a cell:
// top, right, bottom, left specs (each possibly spanning several grid
// segments, approximated by the first one).
func (cb *collapsedBorders) CellEdges(cell *Box) [4]BorderEdge {
	var out [4]BorderEdge
	pos, ok := cb.cellPos[cell.Original()]
	if !ok {
		return out
	}
	r, c := pos[0], pos[1]
	cs, rs := cell.Colspan, cell.Rowspan
	if cs < 1 {
		cs = 1
	}
	if rs < 1 {
		rs = 1
	}
	conv := func(b borderSpec) BorderEdge {
		return BorderEdge{Width: cb.width(b), Style: b.style, Color: b.color}
	}
	if c < cb.ncols {
		out[0] = conv(cb.horizontal[r][c])
		if r+rs <= cb.nrows {
			out[2] = conv(cb.horizontal[r+rs][c])
		}
	}
	if r < cb.nrows {
		out[3] = conv(cb.vertical[r][c])
		if c+cs <= cb.ncols {
			out[1] = conv(cb.vertical[r][c+cs])
		}
	}
	return out
}

// BorderEdge is a resolved border edge for drawing.
type BorderEdge struct {
	Width float64
	Style string
	Color css.Color
}

// layoutCellPagedInf lays out a continued cell without page constraint.
func (c *Context) layoutCellPagedInf(cell, table *Box, ti *tableInfo, y float64, skip Skip, empty bool) (*Box, Skip) {
	return c.layoutCellPaged(cell, table, ti, y, skip, false, empty, 0)
}

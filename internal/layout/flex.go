package layout

import (
	"math"
	"sort"

	"github.com/esoteric-programmer/weasyprint-go/internal/css"
)

type flexItem struct {
	box              *Box
	base, hypo       float64 // main size of content box
	minMain, maxMain float64
	outerExtra       float64 // margins+borders+paddings on the main axis
	target           float64
	frozen           bool
	laid             *Box
	cross            float64 // outer cross size
	grow, shrink     float64
}

func (c *Context) flexContentWidth(box *Box, outer, minimum bool) float64 {
	row := !isColumn(box.Style)
	var w float64
	for _, ch := range box.Children {
		if ch.IsAbsolutelyPositioned() {
			continue
		}
		cw := c.contentWidth(ch, true, minimum)
		if row && !minimum {
			w += cw
		} else {
			w = math.Max(w, cw)
		}
	}
	if l, ok := box.Style.Get(css.PWidth).(css.Length); ok && !l.IsPercent() {
		w = l.V - boxSizingWidthStyle(box.Style)
	}
	return adjustWidth(box, outer, w)
}

func isColumn(s *css.Style) bool {
	d := s.Kw(css.PFlexDirection)
	return d == "column" || d == "column-reverse"
}

func (c *Context) flexLayout(box *Box, bottomSpace float64, skip Skip, cb *Box, cbi cbInfo, pageIsEmpty bool, abs, fixed *absList) blockResult {
	if box.EstablishesBFC() {
		c.createBFC()
	}
	resolvePercentages(box, cbi.W)
	if math.IsNaN(box.W) || box.W == 0 && box.Style.IsAuto(css.PWidth) {
		box.W = specifiedWidth(box, cbi.W)
	}
	if box.IsInline || box.IsFloated() || box.IsAbsolutelyPositioned() {
		box.ML, box.MR = nanTo0(box.ML), nanTo0(box.MR)
		if math.IsNaN(box.W) {
			box.W = c.shrinkToFit(box, cbi.W-box.ML-box.MR-box.PL-box.PR-box.BL-box.BR)
		}
	} else {
		blockLevelWidth(box, cbi.W, true)
	}
	box.MT, box.MB = nanTo0(box.MT), nanTo0(box.MB)
	specH := specifiedHeight(box, cbi)
	column := isColumn(box.Style)
	dir := box.Style.Kw(css.PFlexDirection)
	reverse := dir == "row-reverse" || dir == "column-reverse"
	wrap := box.Style.Kw(css.PFlexWrap) != "nowrap"
	wrapReverse := box.Style.Kw(css.PFlexWrap) == "wrap-reverse"

	if box.Style.Kw(css.PPosition) != "static" {
		abs = &absList{}
	}
	var children []*Box
	var items []*flexItem
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
		items = append(items, &flexItem{box: ch})
	}
	sort.SliceStable(items, func(i, j int) bool {
		return items[i].box.Style.Num(css.POrder) < items[j].box.Style.Num(css.POrder)
	})
	mainSize := box.W
	mainDefinite := true
	if column {
		mainSize = specH
		mainDefinite = !math.IsNaN(specH)
		if !mainDefinite {
			mainSize = math.Inf(1)
		}
	}
	gapMain, gapCross := 0.0, 0.0
	if l, ok := box.Style.Get(css.PColumnGap).(css.Length); ok {
		gapMain = l.Resolve(box.W)
	}
	if l, ok := box.Style.Get(css.PRowGap).(css.Length); ok {
		gapCross = l.Resolve(box.W)
	}
	if column {
		gapMain, gapCross = gapCross, gapMain
	}
	itemCB := cbInfo{W: box.W, H: specH, HAuto: math.IsNaN(specH), Style: box.Style}

	// Base sizes.
	for _, it := range items {
		b := it.box.Copy()
		resolvePercentages(b, box.W)
		b.ML, b.MR, b.MT, b.MB = nanTo0(b.ML), nanTo0(b.MR), nanTo0(b.MT), nanTo0(b.MB)
		it.box = b
		s := b.Style
		it.grow = s.Num(css.PFlexGrow)
		it.shrink = s.Num(css.PFlexShrink)
		if column {
			it.outerExtra = b.MT + b.MB + b.PT + b.PB + b.BT + b.BB
		} else {
			it.outerExtra = b.ML + b.MR + b.PL + b.PR + b.BL + b.BR
		}
		basis := math.NaN()
		if l, ok := s.Get(css.PFlexBasis).(css.Length); ok {
			if !(l.IsPercent() && column && !mainDefinite) {
				ref := box.W
				if column {
					ref = specH
				}
				basis = l.Resolve(ref)
				if column {
					basis -= boxSizingHeight(b)
				} else {
					basis -= boxSizingWidth(b)
				}
			}
		}
		if math.IsNaN(basis) {
			if column {
				basis = specifiedHeight(b, itemCB)
			} else {
				basis = specifiedWidth(b, box.W)
			}
		}
		if math.IsNaN(basis) {
			if column {
				w := c.flexItemCrossWidth(b, box.W)
				lb := c.layoutFlexItem(b, w, math.NaN(), itemCB)
				basis = lb.H
			} else {
				basis = c.maxContent(b, false)
			}
		}
		it.base = math.Max(0, basis)
		// Min/max main sizes.
		if column {
			mn, mx := minMaxHeight(b, itemCB)
			it.minMain, it.maxMain = mn, mx
		} else {
			mn, mx := minMaxWidth(b, box.W)
			if b.Style.IsAuto(css.PMinWidth) && b.Style.Kw(css.POverflow) == "visible" {
				auto := c.minContent(b, false)
				if w := specifiedWidth(b, box.W); !math.IsNaN(w) {
					auto = math.Min(auto, w)
				}
				mn = math.Min(auto, mx)
			}
			it.minMain, it.maxMain = mn, mx
		}
		it.hypo = math.Max(it.minMain, math.Min(it.maxMain, it.base))
	}

	// Lines.
	var lines [][]*flexItem
	var cur []*flexItem
	used := 0.0
	for _, it := range items {
		outer := it.hypo + it.outerExtra
		gap := 0.0
		if len(cur) > 0 {
			gap = gapMain
		}
		if wrap && len(cur) > 0 && used+gap+outer > mainSize+1e-6 {
			lines = append(lines, cur)
			cur, used = nil, 0
			gap = 0
		}
		cur = append(cur, it)
		used += gap + outer
	}
	if len(cur) > 0 {
		lines = append(lines, cur)
	}

	// Resolve flexible lengths.
	for _, line := range lines {
		avail := mainSize
		if math.IsInf(avail, 1) {
			for _, it := range line {
				it.target = it.hypo
			}
			continue
		}
		resolveFlexible(line, avail-gapMain*float64(len(line)-1))
	}

	// Cross sizes.
	for _, line := range lines {
		for _, it := range line {
			if column {
				w := c.flexItemCrossWidth(it.box, box.W)
				it.laid = c.layoutFlexItem(it.box, w, it.target, itemCB)
				it.cross = it.laid.MarginWidth()
			} else {
				it.laid = c.layoutFlexItem(it.box, it.target, math.NaN(), itemCB)
				it.cross = it.laid.MarginHeight()
			}
		}
	}
	lineCross := make([]float64, len(lines))
	for i, line := range lines {
		for _, it := range line {
			lineCross[i] = math.Max(lineCross[i], it.cross)
		}
	}
	crossSize := 0.0
	if column {
		crossSize = box.W
	} else if !math.IsNaN(specH) {
		crossSize = specH
	} else {
		for i, lc := range lineCross {
			crossSize += lc
			if i > 0 {
				crossSize += gapCross
			}
		}
	}
	if len(lines) == 1 && (column || !math.IsNaN(specH)) {
		lineCross[0] = crossSize
	}
	// align-content.
	totalCross := 0.0
	for i, lc := range lineCross {
		totalCross += lc
		if i > 0 {
			totalCross += gapCross
		}
	}
	freeCross := crossSize - totalCross
	crossStart := 0.0
	crossBetween := 0.0
	ac := box.Style.Kw(css.PAlignContent)
	if len(lines) > 1 && freeCross > 0 {
		switch ac {
		case "flex-end", "end":
			crossStart = freeCross
		case "center":
			crossStart = freeCross / 2
		case "space-between":
			crossBetween = freeCross / float64(len(lines)-1)
		case "space-around":
			crossBetween = freeCross / float64(len(lines))
			crossStart = crossBetween / 2
		case "space-evenly":
			crossBetween = freeCross / float64(len(lines)+1)
			crossStart = crossBetween
		case "normal", "stretch":
			extra := freeCross / float64(len(lines))
			for i := range lineCross {
				lineCross[i] += extra
			}
		}
	}

	// Positioning.
	cx, cy := box.ContentX(), box.ContentY()
	crossPos := crossStart
	lineOrder := make([]int, len(lines))
	for i := range lineOrder {
		lineOrder[i] = i
	}
	if wrapReverse {
		for i, j := 0, len(lineOrder)-1; i < j; i, j = i+1, j-1 {
			lineOrder[i], lineOrder[j] = lineOrder[j], lineOrder[i]
		}
	}
	mainUsed := 0.0
	for _, li := range lineOrder {
		line := lines[li]
		lc := lineCross[li]
		sumMain := 0.0
		for i, it := range line {
			if column {
				sumMain += it.laid.MarginHeight()
			} else {
				sumMain += it.laid.MarginWidth()
			}
			if i > 0 {
				sumMain += gapMain
			}
		}
		ms := mainSize
		if math.IsInf(ms, 1) {
			ms = sumMain
		}
		free := ms - sumMain
		start, between := 0.0, 0.0
		jc := box.Style.Kw(css.PJustifyContent)
		if free > 0 {
			switch jc {
			case "flex-end", "end", "right":
				start = free
			case "center":
				start = free / 2
			case "space-between":
				if len(line) > 1 {
					between = free / float64(len(line)-1)
				}
			case "space-around":
				between = free / float64(len(line))
				start = between / 2
			case "space-evenly":
				between = free / float64(len(line)+1)
				start = between
			}
		}
		if reverse && (jc == "normal" || jc == "flex-start" || jc == "start") {
			start = free
		}
		order := line
		if reverse {
			order = make([]*flexItem, len(line))
			for i := range line {
				order[i] = line[len(line)-1-i]
			}
		}
		pos := start
		for _, it := range order {
			b := it.laid
			align := b.Style.Kw(css.PAlignSelf)
			if align == "auto" || align == "normal" {
				align = box.Style.Kw(css.PAlignItems)
			}
			var itemCross, outerCross float64
			if column {
				outerCross = b.MarginWidth()
			} else {
				outerCross = b.MarginHeight()
			}
			itemCross = outerCross
			off := 0.0
			switch align {
			case "flex-end", "end", "self-end":
				off = lc - itemCross
			case "center":
				off = (lc - itemCross) / 2
			case "normal", "stretch":
				if column {
					if b.Style.IsAuto(css.PWidth) && math.Abs(itemCross-lc) > 1e-6 {
						w := lc - (b.ML + b.MR + b.PL + b.PR + b.BL + b.BR)
						b = c.layoutFlexItem(it.box, math.Max(0, w), it.target, itemCB)
						it.laid = b
					}
				} else if b.Style.IsAuto(css.PHeight) {
					h := lc - (b.MT + b.MB + b.PT + b.PB + b.BT + b.BB)
					if h > b.H {
						b.H = h
					}
				}
			}
			if wrapReverse {
				off = lc - itemCross - off
			}
			if column {
				b.Translate(cx+crossPos+off-b.X, cy+pos-b.Y)
				pos += b.MarginHeight() + gapMain + between
			} else {
				b.Translate(cx+pos-b.X, cy+crossPos+off-b.Y)
				pos += b.MarginWidth() + gapMain + between
			}
			children = append(children, b)
		}
		mainUsed = math.Max(mainUsed, pos-gapMain-between)
		crossPos += lc + gapCross + crossBetween
	}
	nb := box.CopyWith(children)
	if !math.IsNaN(specH) {
		nb.H = specH
	} else if column {
		nb.H = math.Max(0, mainUsed)
	} else {
		nb.H = math.Max(0, crossSize)
	}
	mn, mx := minMaxHeight(nb, cbi)
	nb.H = math.Max(mn, math.Min(mx, nb.H))
	if box.Style.Kw(css.PPosition) != "static" {
		for _, a := range abs.boxes {
			c.absoluteLayout(a, nb, fixed)
		}
	}
	for _, ch := range nb.Children {
		relativePositioning(ch, nb.W, nb.H)
	}
	if box.EstablishesBFC() {
		c.finishBFC(nil)
	}
	np := nextPage{Break: "any", set: true}
	_, np.Page = pageValues(nb)
	return blockResult{box: nb, next: np}
}

func (c *Context) flexItemCrossWidth(b *Box, containerW float64) float64 {
	if w := specifiedWidth(b, containerW); !math.IsNaN(w) {
		return w
	}
	return math.Max(0, containerW-b.ML-b.MR-b.PL-b.PR-b.BL-b.BR)
}

func resolveFlexible(line []*flexItem, avail float64) {
	sumHypo := 0.0
	for _, it := range line {
		sumHypo += it.hypo + it.outerExtra
	}
	growing := sumHypo < avail
	for _, it := range line {
		it.frozen = false
		it.target = it.base
		if (growing && it.grow == 0) || (!growing && it.shrink == 0) || (growing && it.base > it.hypo) || (!growing && it.base < it.hypo) {
			it.target = it.hypo
			it.frozen = true
		}
	}
	for iter := 0; iter < 10; iter++ {
		free := avail
		var unfrozen []*flexItem
		for _, it := range line {
			if it.frozen {
				free -= it.target + it.outerExtra
			} else {
				free -= it.base + it.outerExtra
				unfrozen = append(unfrozen, it)
			}
		}
		if len(unfrozen) == 0 {
			break
		}
		if growing {
			sum := 0.0
			for _, it := range unfrozen {
				sum += it.grow
			}
			if sum < 1 && sum > 0 {
				free = free * sum
			}
			for _, it := range unfrozen {
				if sum > 0 {
					it.target = it.base + free*it.grow/sum
				}
			}
		} else {
			sum := 0.0
			for _, it := range unfrozen {
				sum += it.shrink * it.base
			}
			for _, it := range unfrozen {
				if sum > 0 {
					it.target = it.base + free*it.shrink*it.base/sum
				}
			}
		}
		violation := 0.0
		for _, it := range unfrozen {
			clamped := math.Max(it.minMain, math.Min(it.maxMain, it.target))
			violation += clamped - it.target
			it.target = clamped
		}
		for _, it := range unfrozen {
			if violation == 0 || (violation > 0 && it.target == it.minMain) || (violation < 0 && it.target == it.maxMain) {
				it.frozen = true
			}
		}
		if violation == 0 {
			break
		}
	}
	for _, it := range line {
		it.target = math.Max(0, it.target)
	}
}

// layoutFlexItem lays out an item with a given content width (and
// optionally height) at (0, 0).
func (c *Context) layoutFlexItem(orig *Box, w, h float64, cbi cbInfo) *Box {
	b := orig.Copy()
	b.X, b.Y = 0, 0
	b.W = w
	if !math.IsNaN(h) {
		b.H = h
	} else {
		b.H = specifiedHeight(b, cbi)
	}
	saved := c.PageBottom
	c.PageBottom = math.Inf(1)
	defer func() { c.PageBottom = saved }()
	switch b.Kind {
	case KReplaced:
		if math.IsNaN(b.H) {
			b.H = math.NaN()
			ww := b.W
			c.replacedWidthHeight(b, cbi)
			b.W = ww
			if r := b.Image.Ratio(); r > 0 && b.Style.IsAuto(css.PHeight) {
				b.H = ww / r
			}
		}
		return b
	case KFlex, KGrid:
		var res blockResult
		if b.Kind == KGrid {
			res = c.gridLayout(b, math.Inf(-1), nil, nil, cbi, true, &absList{}, &absList{})
		} else {
			res = c.flexLayout(b, math.Inf(-1), nil, nil, cbi, true, &absList{}, &absList{})
		}
		if res.box != nil {
			return res.box
		}
		return b
	case KTableWrapper:
		c.tableWrapperWidth(b, cbi)
	}
	c.createBFC()
	res := c.blockContainerLayout(b, math.Inf(-1), nil, true, &absList{}, &absList{}, nil, false, cbi)
	if res.box == nil {
		c.finishBFC(nil)
		return b
	}
	c.finishBFC(res.box)
	nb := res.box
	if !math.IsNaN(h) {
		nb.H = h
	}
	return nb
}

package layout

import (
	"math"

	"github.com/esoteric-programmer/weasyprint-go/internal/css"
)

// nextPage describes a forced break after some content.
type nextPage struct {
	Break string // any, left, right, recto, verso, page
	Page  string // page name, or ""
	set   bool
}

type blockResult struct {
	box               *Box
	resume            Skip
	next              nextPage
	adjoining         []float64
	collapsingThrough bool
}

// absList collects absolutely positioned placeholders.
type absList struct {
	boxes []*Box
}

func (l *absList) add(b *Box) { l.boxes = append(l.boxes, b) }

func (l *absList) remove(boxes []*Box) {
	if l == nil || len(l.boxes) == 0 {
		return
	}
	rm := map[*Box]bool{}
	for _, b := range boxes {
		b.Descendants(func(d *Box) {
			if d.IsAbsPlaceholder {
				rm[d] = true
			}
		})
	}
	if len(rm) == 0 {
		return
	}
	kept := l.boxes[:0]
	for _, b := range l.boxes {
		if !rm[b] {
			kept = append(kept, b)
		}
	}
	l.boxes = kept
}

func removePlaceholders(boxes []*Box, abs, fixed *absList) {
	abs.remove(boxes)
	fixed.remove(boxes)
}

// blockLevelLayout lays out a block-level box.
func (c *Context) blockLevelLayout(box *Box, bottomSpace float64, skip Skip, cb *Box, cbi cbInfo, pageIsEmpty bool,
	abs, fixed *absList, adjoining []float64, discard bool) blockResult {
	if box.Kind != KTable {
		resolvePercentages(box, cbi.W)
		if math.IsNaN(box.MT) {
			box.MT = 0
		}
		if math.IsNaN(box.MB) {
			box.MB = 0
		}
		if c.PageIndex > 0 && pageIsEmpty && (cbi.Root || len(adjoining) > 0) && !c.forcedBreak {
			box.MT = 0
		}
		collapsed := collapseMargins(append(append([]float64{}, adjoining...), box.MT))
		if clr, ok := c.getClearance(box, collapsed); ok {
			top := box.Y + collapsed + clr
			box.Clearance = clr
			box.Y = top - box.MT
			adjoining = nil
		} else {
			box.Clearance = 0
		}
	}
	switch box.Kind {
	case KTable:
		return c.tableLayout(box, bottomSpace, skip, cb, cbi, pageIsEmpty, abs, fixed)
	case KReplaced:
		nb := c.blockReplacedLayout(box, cbi)
		x, y, _ := c.avoidCollisions(nb, cbi, cb, false)
		nb.Translate(x-nb.X, y-nb.Y)
		return blockResult{box: nb, next: nextPage{Break: "any"}}
	case KFlex:
		return c.flexLayout(box, bottomSpace, skip, cb, cbi, pageIsEmpty, abs, fixed)
	case KGrid:
		return c.gridLayout(box, bottomSpace, skip, cb, cbi, pageIsEmpty, abs, fixed)
	}
	return c.blockBoxLayout(box, bottomSpace, skip, cb, cbi, pageIsEmpty, abs, fixed, adjoining, discard)
}

func (c *Context) blockBoxLayout(box *Box, bottomSpace float64, skip Skip, cb *Box, cbi cbInfo, pageIsEmpty bool,
	abs, fixed *absList, adjoining []float64, discard bool) blockResult {
	box.W = specifiedWidth(box, cbi.W)
	box.H = specifiedHeight(box, cbi)
	if box.Kind == KTableWrapper {
		c.tableWrapperWidth(box, cbi)
	}
	blockLevelWidth(box, cbi.W, true)
	var res blockResult
	if isMulticol(box) {
		res = c.columnsLayout(box, bottomSpace, skip, cbi, pageIsEmpty, abs, fixed, adjoining)
	} else {
		res = c.blockContainerLayout(box, bottomSpace, skip, pageIsEmpty, abs, fixed, adjoining, discard, cbi)
	}
	nb := res.box
	if nb == nil {
		return res
	}
	if nb.Kind == KTableWrapper || nb.EstablishesBFC() {
		x, y, _ := c.avoidCollisions(nb, cbi, cb, false)
		nb.Translate(x-nb.X, y-nb.Y)
	}
	return res
}

// blockLevelWidth computes width and horizontal margins (CSS 2.1 §10.3.3).
// box.W is NaN for auto; box.ML/MR are NaN for auto.
func blockLevelWidth(box *Box, cbWidth float64, withMinMax bool) {
	pb := box.PL + box.PR + box.BL + box.BR
	if math.IsNaN(box.W) {
		box.W = cbWidth - pb
		if !math.IsNaN(box.ML) {
			box.W -= box.ML
		}
		if !math.IsNaN(box.MR) {
			box.W -= box.MR
		}
		box.W = math.Max(0, box.W)
		if box.Kind == KTableWrapper {
			box.W = math.Max(0, box.W)
		}
	}
	if withMinMax && box.Style != nil && box.Kind != KTableWrapper {
		mn, mx := minMaxWidth(box, cbWidth)
		box.W = math.Max(mn, math.Min(mx, box.W))
	}
	mw := pb + box.W
	if !math.IsNaN(box.ML) {
		mw += box.ML
	}
	if !math.IsNaN(box.MR) {
		mw += box.MR
	}
	if mw > cbWidth {
		if math.IsNaN(box.ML) {
			box.ML = 0
		}
		if math.IsNaN(box.MR) {
			box.MR = 0
		}
	}
	rtl := box.Style != nil && box.Style.Kw(css.PDirection) == "rtl"
	sum := cbWidth - pb - box.W
	switch {
	case math.IsNaN(box.ML) && math.IsNaN(box.MR):
		box.ML, box.MR = sum/2, sum/2
	case math.IsNaN(box.ML):
		box.ML = sum - box.MR
	case math.IsNaN(box.MR):
		box.MR = sum - box.ML
	default:
		// Over-constrained: ignore margin-right (or left in rtl).
		if rtl {
			box.ML = sum - box.MR
		} else {
			box.MR = sum - box.ML
		}
	}
}

func relativePositioning(box *Box, cbW, cbH float64) {
	if box.Style != nil && box.Kind != KText && box.Style.Kw(css.PPosition) == "relative" {
		var dx, dy float64
		left, lok := lengthOrAuto(box.Style, css.PLeft, cbW)
		right, rok := lengthOrAuto(box.Style, css.PRight, cbW)
		top, tok := lengthOrAuto(box.Style, css.PTop, cbH)
		bottom, bok := lengthOrAuto(box.Style, css.PBottom, cbH)
		switch {
		case lok:
			dx = left
		case rok:
			dx = -right
		}
		switch {
		case tok:
			dy = top
		case bok:
			dy = -bottom
		}
		box.Translate(dx, dy)
	}
	if box.Kind == KInline || box.Kind == KLine {
		for _, ch := range box.Children {
			relativePositioning(ch, cbW, cbH)
		}
	}
}

func topMarginCollapses(b *Box) bool {
	return b.BT == 0 && b.PT == 0 && !b.EstablishesBFC() && !b.flexItem()
}

func bottomMarginCollapses(b *Box) bool {
	return b.BB == 0 && b.PB == 0 && b.Style.IsAuto(css.PHeight) && !b.EstablishesBFC() && !b.flexItem()
}

func (b *Box) flexItem() bool { return false }

func findLastInFlowChild(children []*Box) *Box {
	for i := len(children) - 1; i >= 0; i-- {
		ch := children[i]
		if ch.IsInNormalFlow() && !ch.IsAbsPlaceholder {
			return ch
		}
	}
	return nil
}

func findLastClassABreakPoint(children []*Box) *Box {
	for i := len(children) - 1; i >= 0; i-- {
		ch := children[i]
		if ch.IsAbsPlaceholder {
			continue
		}
		if ch.IsInNormalFlow() || ch.IsFloated() {
			return ch
		}
	}
	return nil
}

func breakValues(box *Box, side string) []string {
	var values []string
	for box != nil && box.Style != nil && (box.IsBlockLevel() || box.Kind == KRowGroup || box.Kind == KRow || box.Kind == KTable) && (box.IsInNormalFlow() || box.IsFloated()) {
		id := css.PBreakBefore
		if side == "after" {
			id = css.PBreakAfter
		}
		values = append(values, box.Style.Kw(id))
		if len(box.Children) == 0 {
			break
		}
		if side == "after" {
			box = box.Children[len(box.Children)-1]
		} else {
			box = box.Children[0]
		}
	}
	if side == "after" {
		for i, j := 0, len(values)-1; i < j; i, j = i+1, j-1 {
			values[i], values[j] = values[j], values[i]
		}
	}
	return values
}

func blockLevelPageBreak(before, after *Box) string {
	result := "auto"
	values := append(breakValues(before, "after"), breakValues(after, "before")...)
	for _, v := range values {
		if v == "always" {
			v = "page"
		}
		switch {
		case v == "left" || v == "right" || v == "recto" || v == "verso":
			result = v
		case v == "page" && (result == "auto" || result == "avoid" || result == "avoid-page" || result == "avoid-column"):
			result = v
		case v == "column" && (result == "auto" || result == "avoid" || result == "avoid-page" || result == "avoid-column"):
			result = v
		case (v == "avoid" || v == "avoid-page" || v == "avoid-column") && result == "auto":
			result = v
		}
	}
	return result
}

func forcePageBreak(v string) bool {
	switch v {
	case "page", "left", "right", "recto", "verso", "always":
		return true
	}
	return false
}

func avoidPageBreak(v string) bool { return v == "avoid" || v == "avoid-page" }

// pageValues returns the start and end page names of a box.
func pageValues(b *Box) (string, string) {
	if b.Style == nil {
		return "", ""
	}
	start := b.Style.PageName()
	end := start
	var children []*Box
	for _, ch := range b.Children {
		if ch.IsInNormalFlow() && ch.Kind != KText && ch.Kind != KLine {
			children = append(children, ch)
		}
	}
	if b.Kind == KLine || b.Kind == KInline || b.Kind == KText {
		return start, end
	}
	if len(children) > 0 {
		s, e := pageValues(children[0])
		if len(children) > 1 {
			_, e = pageValues(children[len(children)-1])
		}
		if s != "" {
			start = s
		}
		if e != "" {
			end = e
		}
	}
	return start, end
}

func blockLevelPageName(before, after *Box) (string, bool) {
	_, b := pageValues(before)
	a, _ := pageValues(after)
	if a != b {
		return a, true
	}
	return "", false
}

// findEarlierPageBreak finds the last possible break in children because
// of break-* avoid values.
func (c *Context) findEarlierPageBreak(children []*Box, abs, fixed *absList) ([]*Box, Skip, bool) {
	if len(children) > 0 && children[0].Kind == KLine {
		orphans := int(children[0].Style.Num(css.POrphans))
		widows := int(children[0].Style.Num(css.PWidows))
		index := len(children) - widows
		if index < orphans || index <= 0 {
			return nil, nil, false
		}
		nc := children[:index]
		removePlaceholders(children[index:], abs, fixed)
		return nc, Skip{0: nc[len(nc)-1].lineResume}, true
	}
	var prevInFlow *Box
	for i := len(children) - 1; i >= 0; i-- {
		ch := children[i]
		if ch.Kind == KRowGroup && (ch.IsHeader || ch.IsFooter) {
			continue
		}
		if ch.IsInNormalFlow() && !ch.IsAbsPlaceholder {
			if prevInFlow != nil && !avoidPageBreak(blockLevelPageBreak(ch, prevInFlow)) {
				idx := i + 1
				nc := children[:idx]
				removePlaceholders(children[idx:], abs, fixed)
				return nc, Skip{children[idx].index: nil}, true
			}
			prevInFlow = ch
		}
		if ch.IsInNormalFlow() && !ch.IsAbsPlaceholder && !avoidPageBreak(ch.Style.Kw(css.PBreakInside)) {
			if ch.Kind == KBlock || ch.Kind == KTable || ch.Kind == KRowGroup || ch.Kind == KTableWrapper {
				if ngc, rs, ok := c.findEarlierPageBreak(ch.Children, abs, fixed); ok {
					nch := ch.CopyWith(ngc)
					nc := append(append([]*Box{}, children[:i]...), nch)
					if ch.Kind == KRowGroup {
						for _, nx := range children[i:] {
							if nx.IsFooter {
								nc = append(nc, nx)
							}
						}
					}
					removePlaceholders(children[i+1:], abs, fixed)
					return nc, Skip{ch.index: rs}, true
				}
			}
		}
	}
	return nil, nil, false
}

// blockContainerLayout lays out the children of a block container and
// sets its height.
func (c *Context) blockContainerLayout(box *Box, bottomSpace float64, skip Skip, pageIsEmpty bool,
	abs, fixed *absList, adjoining []float64, discard bool, cbi cbInfo) blockResult {
	if box.EstablishesBFC() {
		c.createBFC()
	}
	isStart := skip == nil
	if !isStart {
		box.MT, box.PT, box.BT = 0, 0, 0
	}
	discard = discard || box.Style.Kw(css.PContinue) == "discard"
	drawBottomDecoration := discard || box.Style.Kw(css.PBoxDecorationBreak) == "clone"
	if drawBottomDecoration {
		bottomSpace += box.PB + box.BB + box.MB
	}
	adjoining = append(append([]float64{}, adjoining...), box.MT)
	thisBoxAdjoining := adjoining
	var posY float64
	if topMarginCollapses(box) {
		posY = box.Y
	} else {
		box.Y += collapseMargins(adjoining) - box.MT
		adjoining = nil
		posY = box.ContentY()
	}
	posX := box.ContentX()
	isCB := box.Style.Kw(css.PPosition) != "static" || box.Style.Transforms() != nil
	if isCB {
		abs = &absList{}
	}
	selfCB := cbInfo{W: box.W, H: box.H, HAuto: math.IsNaN(box.H), Style: box.Style}
	if box.Element != nil && box.Element.ParentElement() == nil && box.Pseudo == "" && !box.Anonymous {
		selfCB.Root = true
	}

	var newChildren []*Box
	np := nextPage{Break: "any"}
	var resume Skip
	startIdx := 0
	var childSkip Skip
	if !isStart {
		startIdx, childSkip = skipFirst(skip)
	}
	finished := true
	for index := startIdx; index < len(box.Children); index++ {
		child := box.Children[index]
		child.X = posX
		child.Y = posY
		child.index = index
		abort, stop := false, false

		if child.IsInNormalFlow() || child.IsFloated() {
			if last := findLastClassABreakPoint(newChildren); last != nil {
				pb := blockLevelPageBreak(last, child)
				name, changed := blockLevelPageName(last, child)
				if changed || forcePageBreak(pb) {
					np = nextPage{Break: pb, Page: name, set: changed}
					if !forcePageBreak(pb) {
						np.Break = "any"
					}
					resume = Skip{index: nil}
					stop = true
				}
			}
		}

		if !stop && child.RunningName != "" {
			c.addRunning(child)
		} else if !stop && !child.IsInNormalFlow() {
			var nc *Box
			stop, resume, nc = c.outOfFlowLayout(box, selfCB, index, child, &newChildren, pageIsEmpty, abs, fixed, adjoining, bottomSpace)
			_ = nc
		} else if !stop && child.Kind == KLine {
			abort, stop, resume, posY = c.lineboxLayout(box, selfCB, index, child, &newChildren, pageIsEmpty, abs, fixed, adjoining, bottomSpace, posY, childSkip, drawBottomDecoration)
			drawBottomDecoration = drawBottomDecoration || resume == nil
			adjoining = nil
		} else if !stop {
			var r inFlowResult
			r = c.inFlowLayout(box, selfCB, index, child, newChildren, pageIsEmpty, abs, fixed, adjoining, bottomSpace, posY, childSkip, discard, np)
			abort, stop, resume, posY, adjoining, np, newChildren = r.abort, r.stop, r.resume, r.posY, r.adjoining, r.next, r.children
			childSkip = nil
		}
		childSkip = nil

		if abort {
			removePlaceholders(box.Children[startIdx:], abs, fixed)
			if box.EstablishesBFC() {
				c.finishBFC(nil)
			}
			s, _ := pageValues(child)
			return blockResult{next: nextPage{Break: "any", Page: s}}
		}
		if stop {
			if !math.IsNaN(box.H) {
				boxBottom := box.Y + box.BorderHeight()
				if posY-collapseMargins(adjoining) > boxBottom {
					resume = nil
				}
			}
			adjoining = nil
			finished = false
			break
		}
	}
	if finished {
		resume = nil
	}
	fragmented := resume != nil
	if box.Style.Kw(css.PContinue) == "discard" {
		resume = nil
	}
	if fragmented && avoidPageBreak(box.Style.Kw(css.PBreakInside)) && !pageIsEmpty {
		removePlaceholders(append(append([]*Box{}, newChildren...), box.Children[startIdx:]...), abs, fixed)
		if box.EstablishesBFC() {
			c.finishBFC(nil)
		}
		return blockResult{next: nextPage{Break: "any"}}
	}
	if topMarginCollapses(box) {
		box.Y += collapseMargins(thisBoxAdjoining) - box.MT
	}
	last := findLastInFlowChild(newChildren)
	collapsingThrough := false
	if last == nil {
		collapsed := collapseMargins(adjoining)
		mnH, _ := minMaxHeight(box, cbi)
		_, hasClearance := c.getClearance(box, collapsed)
		if (math.IsNaN(box.H) || box.H == 0) && !hasClearance && mnH == 0 && box.BT == 0 && box.PT == 0 && box.BB == 0 && box.PB == 0 {
			collapsingThrough = true
		} else {
			posY += collapsed
			adjoining = nil
		}
	} else if !math.IsNaN(box.H) {
		adjoining = nil
	}
	if !bottomMarginCollapses(box) {
		posY += collapseMargins(adjoining)
		adjoining = nil
	}

	nb := box.CopyWith(newChildren)
	if !isStart {
		nb.NoStartDecor = true
	}
	if fragmented && !discard {
		nb.NoEndDecor = true
		if !drawBottomDecoration {
			nb.MB, nb.PB, nb.BB = 0, 0, 0
		}
	}
	if math.IsNaN(nb.H) {
		nb.H = math.Max(0, posY-nb.ContentY())
	}
	if isCB {
		for _, a := range abs.boxes {
			c.absoluteLayout(a, nb, fixed)
		}
	}
	for _, ch := range nb.Children {
		relativePositioning(ch, nb.W, nb.H)
	}
	if box.EstablishesBFC() {
		c.finishBFC(nb)
	}
	if discard || !fragmented {
		mn, mx := minMaxHeight(nb, cbi)
		nb.H = math.Max(math.Min(nb.H, mx), mn)
	} else if !math.IsInf(bottomSpace, -1) {
		h := c.PageBottom - bottomSpace - nb.Y - (nb.MarginHeight() - nb.H)
		if h > nb.H {
			nb.H = h
			if drawBottomDecoration {
				nb.H += box.PB + box.BB + box.MB
			}
		}
	}
	if !np.set {
		_, e := pageValues(nb)
		np.Page = e
		np.set = true
	}
	if nb.Marker != nil && nb.Marker.IsOutsideMarker == false {
		c.layoutOutsideMarker(nb, isStart)
	}
	return blockResult{box: nb, resume: resume, next: np, adjoining: adjoining, collapsingThrough: collapsingThrough}
}

func (c *Context) outOfFlowLayout(box *Box, cbi cbInfo, index int, child *Box, newChildren *[]*Box, pageIsEmpty bool,
	abs, fixed *absList, adjoining []float64, bottomSpace float64) (bool, Skip, *Box) {
	child.Y += collapseMargins(adjoining)
	if child.IsAbsolutelyPositioned() {
		ph := child.Copy()
		ph.IsAbsPlaceholder = true
		ph.StaticX, ph.StaticY = child.X, child.Y
		ph.index = index
		*newChildren = append(*newChildren, ph)
		if child.Style.Kw(css.PPosition) == "fixed" {
			fixed.add(ph)
		} else {
			abs.add(ph)
		}
		return false, nil, ph
	}
	if child.IsFloated() {
		nc := c.floatLayout(child, box, cbi, abs, fixed, bottomSpace)
		overflow := c.overflowsPage(bottomSpace, nc.Y+nc.MarginHeight())
		if (pageIsEmpty && len(*newChildren) == 0) || !overflow {
			nc.index = index
			*newChildren = append(*newChildren, nc)
			return false, nil, nc
		}
		removePlaceholders([]*Box{nc}, abs, fixed)
		// Remove the float from the exclusions.
		for i, e := range c.excluded {
			if e == nc {
				c.excluded = append(c.excluded[:i], c.excluded[i+1:]...)
				break
			}
		}
		return true, Skip{index: nil}, nil
	}
	return false, nil, nil
}

func (c *Context) lineboxLayout(box *Box, cbi cbInfo, index int, child *Box, newChildren *[]*Box, pageIsEmpty bool,
	abs, fixed *absList, adjoining []float64, bottomSpace, posY float64, skip Skip, drawBottom bool) (bool, bool, Skip, float64) {
	if len(adjoining) > 0 {
		posY += collapseMargins(adjoining)
	}
	it := c.newLineIterator(child, box, cbi, posY, bottomSpace, skip, abs, fixed)
	lineStart := len(*newChildren)
	orphans := int(box.Style.Num(css.POrphans))
	widows := int(box.Style.Num(css.PWidows))
	stop := false
	for {
		line, rs, ok := it.next()
		if !ok {
			break
		}
		line.lineResume = rs
		newY := line.Y + line.H
		offsetY := 0.0
		if drawBottom || rs == nil {
			offsetY = box.BB + box.PB
		}
		linesSoFar := len(*newChildren) - lineStart
		overflow := (linesSoFar > 0 || !pageIsEmpty) && c.overflowsPage(bottomSpace, newY+offsetY)
		if overflow {
			needed := widows - 1
			if rs != nil {
				for needed >= 0 {
					nl, _, ok := it.next()
					if !ok {
						break
					}
					removePlaceholders(nl.Children, abs, fixed)
					needed--
				}
			}
			if needed < 0 {
				needed = 0
			}
			overOrphans := linesSoFar - orphans
			if overOrphans < 0 && !pageIsEmpty {
				removePlaceholders(line.Children, abs, fixed)
				return true, false, nil, posY
			}
			if needed > overOrphans && !pageIsEmpty {
				removePlaceholders(line.Children, abs, fixed)
				return true, false, nil, posY
			}
			if needed > 0 && needed <= overOrphans {
				drop := (*newChildren)[len(*newChildren)-needed:]
				for _, d := range drop {
					removePlaceholders(d.Children, abs, fixed)
				}
				*newChildren = (*newChildren)[:len(*newChildren)-needed]
			}
			removePlaceholders(line.Children, abs, fixed)
			stop = true
			break
		} else if pageIsEmpty && c.overflowsPage(bottomSpace, newY) {
			newY -= box.MT
			line.Translate(0, -box.MT)
			box.MT = 0
		}
		*newChildren = append(*newChildren, line)
		posY = newY
		skip = rs
		if rs == nil {
			break
		}
	}
	if stop {
		if len(*newChildren) > lineStart {
			last := (*newChildren)[len(*newChildren)-1]
			return false, true, Skip{index: last.lineResume}, last.Y + last.H
		}
		return false, true, Skip{index: skip}, posY
	}
	return false, false, nil, posY
}

type inFlowResult struct {
	abort, stop bool
	resume      Skip
	posY        float64
	adjoining   []float64
	next        nextPage
	children    []*Box
}

func (c *Context) inFlowLayout(box *Box, cbi cbInfo, index int, child *Box, newChildren []*Box, pageIsEmpty bool,
	abs, fixed *absList, adjoining []float64, bottomSpace, posY float64, skip Skip, discard bool, np nextPage) inFlowResult {
	r := inFlowResult{posY: posY, next: np, children: newChildren}
	if box.Kind != KTableWrapper {
		resolvePercentages(child, box.W)
		if findLastInFlowChild(newChildren) == nil && topMarginCollapses(box) {
			old := collapseMargins(adjoining)
			cmt := child.MT
			if math.IsNaN(cmt) {
				cmt = 0
			} else if c.PageIndex > 0 && pageIsEmpty && !c.forcedBreak {
				cmt = 0
			}
			newC := collapseMargins(append(append([]float64{}, adjoining...), cmt))
			diff := newC - old
			for _, p := range newChildren {
				p.Translate(0, diff)
			}
			if _, ok := c.getClearance(child, newC); ok {
				for _, p := range newChildren {
					p.Translate(0, -diff)
				}
				collapsed := collapseMargins(adjoining)
				box.Y += collapsed - box.MT
				adjoining = nil
				r.posY = box.ContentY()
				child.Y = r.posY
			}
		}
	}
	if len(adjoining) > 0 {
		if box.Kind == KTableWrapper {
			child.Y += collapseMargins(adjoining)
			adjoining = nil
		} else if child.Kind != KBlock && child.Kind != KTableWrapper {
			mt := 0.0
			if v, ok := lengthOrAuto(child.Style, css.PMarginTop, box.W); ok {
				mt = v
			}
			adjoining = append(adjoining, mt)
			child.Y += collapseMargins(adjoining) - mt
			adjoining = nil
		}
	}
	emptyNoChildren := pageIsEmpty
	for _, ch := range newChildren {
		if !ch.IsAbsPlaceholder {
			emptyNoChildren = false
			break
		}
	}
	cx, cy := child.X, child.Y
	res := c.blockLevelLayout(child, bottomSpace, skip, box, cbi, emptyNoChildren, abs, fixed, adjoining, discard)
	nc := res.box
	if nc != nil {
		if !res.collapsingThrough {
			contentBottom := nc.ContentY() + nc.H
			contentOverflow := c.overflowsPage(bottomSpace, contentBottom)
			bs := bottomSpace + nc.PB + nc.BB
			if !bottomMarginCollapses(box) {
				bs += nc.MB
			}
			newY := nc.BorderBoxY() + nc.BorderHeight()
			borderOverflow := c.overflowsPage(bs, newY)
			canBreak := !emptyNoChildren
			if canBreak && contentOverflow {
				removePlaceholders([]*Box{nc}, abs, fixed)
				nc = nil
			} else if canBreak && borderOverflow {
				removePlaceholders([]*Box{nc}, abs, fixed)
				child.X, child.Y = cx, cy
				res = c.blockLevelLayout(child, bs, skip, box, cbi, emptyNoChildren, abs, fixed, adjoining, discard)
				nc = res.box
				if nc != nil {
					r.posY = nc.BorderBoxY() + nc.BorderHeight()
				}
			} else {
				r.posY = newY
			}
		}
		adjoining = res.adjoining
		if nc != nil {
			adjoining = append(append([]float64{}, adjoining...), nc.MB)
			if nc.Clearance > 0 {
				r.posY = nc.BorderBoxY() + nc.BorderHeight()
			}
		}
	}
	r.adjoining = adjoining
	r.next = res.next
	if nc == nil {
		pb := "auto"
		if last := findLastClassABreakPoint(newChildren); last != nil {
			pb = blockLevelPageBreak(last, child)
		}
		if avoidPageBreak(pb) {
			if nchildren, rs, ok := c.findEarlierPageBreak(newChildren, abs, fixed); ok {
				r.children = nchildren
				r.resume = rs
				r.stop = true
				return r
			}
			if !pageIsEmpty {
				r.abort = true
				return r
			}
		}
		allAbs := true
		for _, ch := range newChildren {
			if !ch.IsAbsPlaceholder {
				allAbs = false
			}
		}
		if allAbs && len(newChildren) > 0 {
			removePlaceholders(newChildren, abs, fixed)
			newChildren = nil
			r.children = nil
		}
		if len(newChildren) > 0 {
			r.resume = Skip{index: nil}
			r.stop = true
		} else {
			r.abort = true
		}
		return r
	}
	nc.index = index
	r.children = append(newChildren, nc)
	if res.resume != nil {
		r.resume = Skip{index: res.resume}
		r.stop = true
	}
	return r
}

// layoutOutsideMarker positions an outside list marker next to the first
// line of a list item.
func (c *Context) layoutOutsideMarker(item *Box, isStart bool) {
	m := item.Marker
	if m == nil || !isStart {
		item.Marker = nil
		return
	}
	mk := m.Copy()
	mk.IsOutsideMarker = true
	resolvePercentages(mk, item.W)
	mk.ML, mk.MR = nanTo0(mk.ML), nanTo0(mk.MR)
	mk.MT, mk.MB = nanTo0(mk.MT), nanTo0(mk.MB)
	_, maxW := c.preferredWidths(mk)
	mk.W = maxW
	mk.H = math.NaN()
	mk.X = 0
	mk.Y = item.ContentY()
	c.createBFC()
	res := c.blockContainerLayout(mk, math.Inf(-1), nil, true, &absList{}, &absList{}, nil, false, cbInfo{W: item.W, HAuto: true})
	c.finishBFC(nil)
	nb := res.box
	if nb == nil {
		item.Marker = nil
		return
	}
	nb.IsOutsideMarker = true
	// Align the marker's first baseline with the item's first baseline.
	mb, ok1 := firstBaseline(nb)
	ib, ok2 := firstBaseline(item)
	dy := 0.0
	if ok1 && ok2 {
		dy = ib - mb
	}
	rtl := item.Style.Kw(css.PDirection) == "rtl"
	x := item.BorderBoxX() - nb.MarginWidth()
	if rtl {
		x = item.BorderBoxX() + item.BorderWidth()
	}
	nb.Translate(x-nb.X, dy)
	item.Marker = nb
}

// firstBaseline returns the absolute y of the first line baseline in a box.
func firstBaseline(b *Box) (float64, bool) {
	if b.Kind == KLine {
		return b.Y + b.Baseline, true
	}
	if b.Kind == KReplaced {
		return 0, false
	}
	for _, ch := range b.Children {
		if !ch.IsInNormalFlow() || ch.IsAbsPlaceholder {
			continue
		}
		if y, ok := firstBaseline(ch); ok {
			return y, true
		}
	}
	return 0, false
}

// lastBaseline returns the absolute y of the last line baseline.
func lastBaseline(b *Box) (float64, bool) {
	if b.Kind == KLine {
		return b.Y + b.Baseline, true
	}
	for i := len(b.Children) - 1; i >= 0; i-- {
		ch := b.Children[i]
		if !ch.IsInNormalFlow() || ch.IsAbsPlaceholder {
			continue
		}
		if y, ok := lastBaseline(ch); ok {
			return y, true
		}
	}
	return 0, false
}

func (c *Context) addRunning(b *Box) {
	c.Running[b.RunningName] = append(c.Running[b.RunningName], &runningEntry{box: b, page: c.PageIndex})
	if c.pageRunning == nil {
		c.pageRunning = map[string][]*Box{}
	}
	c.pageRunning[b.RunningName] = append(c.pageRunning[b.RunningName], b)
}

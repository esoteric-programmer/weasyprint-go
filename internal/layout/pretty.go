package layout

import (
	"math"

	"github.com/esoteric-programmer/weasyprint-go/internal/css"
	"github.com/esoteric-programmer/weasyprint-go/internal/text"
)

// text-wrap-style: pretty and balance.
//
// pretty rates the possible breaks of a line like TeX (badness from the
// stretching or shrinking of its spaces, demerits for hyphens,
// consecutive hyphens, changes of tightness, hyphens at the end of a page
// and very short last lines) over a window of the following lines, and
// keeps the best one. Justified lines may shrink their spaces by up to a
// ninth of an em, ragged lines have a 2em flexible margin (as with
// \raggedright). Lines looser than the tolerance are only taken when
// nothing better fits, and then the greedy break is used: lines never
// overflow their box.
// balance makes the lines of short blocks (up to 10 lines) equally long.

// breakCand is a possible line break measured by breakLine.
type breakCand struct {
	pos    linePos
	hyphen bool
	width  float64 // line content width without trailing spaces
	spaces int     // stretchable spaces in the line
}

const (
	prettyWindow     = 3 // lines rated together
	prettyCandidates = 8 // candidates per line (the last ones)
	lineDemerits     = 10
	hyphenPenalty    = 50
	doubleHyphen     = 10000
	adjDemerits      = 5000
	pageEndHyphen    = 100000
	shortLastLine    = 3000
	infDemerits      = 1e12
	prettyTolerance  = 800
	infeasible       = 1e9
)

// chooseBreak returns the end of the line starting at start.
func (it *lineIterator) chooseBreak(start linePos, x0, indent, avail, y float64, placed map[int]*Box) (linePos, bool, []int, bool) {
	style := it.src.Style.Kw(css.PTextWrapStyle)
	maxX := x0 + avail
	if style == "balance" {
		if it.balanced == 0 && it.first {
			it.balanced = it.balanceWidth(start, indent, avail)
		}
		if it.balanced > 0 && it.balanced < avail {
			maxX = x0 + it.balanced
		}
	}
	end, forced, waiting, newFloat := it.breakLine(start, x0+indent, maxX, y, placed)
	if style != "pretty" || forced || newFloat || len(waiting) > 0 || end.item >= len(it.fl.items) || it.restIsEmpty(end) {
		it.track(end, forced, nil)
		return end, forced, waiting, newFloat
	}
	cands := it.measure(start, indent, avail+it.shrinkRoom(avail))
	if len(cands) < 2 {
		it.track(end, forced, cands)
		return end, forced, waiting, newFloat
	}
	cands = it.fitting(cands, avail-indent, end)
	if len(cands) == 0 {
		it.track(end, forced, nil)
		return end, forced, waiting, newFloat
	}
	if len(cands) > prettyCandidates {
		cands = cands[len(cands)-prettyCandidates:]
	}
	lastOnPage := false
	if it.c.PageBottom > 0 {
		strutH, _ := it.c.strut(it.src.Style)
		lastOnPage = y+2*strutH > it.c.PageBottom-it.bottomSpace+1e-6
	}
	memo := map[[2]int]float64{}
	best, bestScore := -1, math.Inf(1)
	lineAvail := avail - indent
	for i, c := range cands {
		d := it.demerits(c, lineAvail, it.hyphRun, it.prevFit, lastOnPage)
		next := it.skipSpacesAt(c.pos)
		score := d + it.rateFrom(next, avail, prettyWindow-1, c.hyphen, it.hyphRun, fitness(it.ratio(c, lineAvail)), memo)
		// Ties go to the later break (the greedy one).
		if score <= bestScore {
			best, bestScore = i, score
		}
	}
	// Without an acceptable line, the greedy break.
	if d := it.demerits(cands[best], lineAvail, it.hyphRun, it.prevFit, lastOnPage); d >= infeasible {
		for i, c := range cands {
			if c.pos == end {
				best = i
			}
		}
		if cands[best].pos != end {
			it.track(end, forced, nil)
			return end, forced, waiting, newFloat
		}
	}
	chosen := cands[best]
	it.track(chosen.pos, false, []breakCand{chosen}, lineAvail)
	return chosen.pos, false, waiting, false
}

// track updates the hyphenation run and fitness after a line.
func (it *lineIterator) track(end linePos, forced bool, chosen []breakCand, avail ...float64) {
	if len(chosen) == 1 && len(avail) == 1 {
		c := chosen[0]
		if c.hyphen {
			it.hyphRun++
		} else {
			it.hyphRun = 0
		}
		it.prevFit = fitness(it.ratio(c, avail[0]))
		return
	}
	// Greedy lines: hyphenated when the line ends inside a word.
	it.hyphRun = 0
	it.prevFit = 2
	if !forced && end.item < len(it.fl.items) && end.r > 0 {
		if item := &it.fl.items[end.item]; item.kind == itText {
			if item.breaks[end.r] == text.BreakHyphen {
				it.hyphRun = 1
			}
		}
	}
}

// measure returns the candidate breaks of a line starting at start (with
// floats ignored, without side effects), its greedy end and whether that
// end is the end of the paragraph or a forced break.
func (it *lineIterator) measureEnd(start linePos, indent, avail float64) ([]breakCand, linePos, bool) {
	var cands []breakCand
	saved := it.collect
	it.collect = &cands
	end, forced, _, _ := it.breakLine(start, indent, avail, 0, map[int]*Box{})
	it.collect = saved
	last := forced || end.item >= len(it.fl.items) || it.restIsEmpty(end)
	return cands, end, last
}

func (it *lineIterator) measure(start linePos, indent, avail float64) []breakCand {
	cands, _, _ := it.measureEnd(start, indent, avail)
	return cands
}

// justified reports whether the paragraph is justified (its spaces
// stretch and shrink).
func (it *lineIterator) justified() bool {
	return it.src.Style.Kw(css.PTextAlignAll) == "justify"
}

// shrinkRoom returns how much longer than the line candidates are
// measured: justified lines may shrink their spaces by a ninth of an em
// (TeX's interword shrink).
func (it *lineIterator) shrinkRoom(avail float64) float64 {
	if !it.justified() {
		return 0
	}
	return avail / 6
}

// ratio returns the adjustment ratio of a candidate line (negative when
// its spaces shrink).
func (it *lineIterator) ratio(c breakCand, avail float64) float64 {
	slack := avail - c.width
	em := it.src.Style.FontSize()
	if slack < 0 {
		shrink := float64(c.spaces) * em / 9
		if shrink <= 0 {
			return -100
		}
		return slack / shrink
	}
	var stretch float64
	if it.justified() {
		// Lines without spaces cannot stretch: they are rated as if
		// they had half a space, so that fuller lines rate better.
		stretch = math.Max(float64(c.spaces), 0.5) * em / 6
	} else {
		// Ragged lines: a flexible right margin, like \raggedright.
		stretch = 2 * em
	}
	return slack / stretch
}

func fitness(r float64) int {
	switch {
	case r > 1:
		return 0
	case r > 0.5:
		return 1
	case r >= -0.5:
		return 2
	}
	return 3
}

// demerits returns the demerits of a line ending at a candidate.
func (it *lineIterator) demerits(c breakCand, avail float64, hyphRun, prevFit int, lastOnPage bool) float64 {
	r := it.ratio(c, avail)
	// Lines looser than the tolerance are only taken when nothing else
	// fits (and then the fuller the better), like TeX's emergency pass.
	if r < -1 {
		// Spaces cannot shrink that much.
		return infDemerits
	}
	b := 100 * math.Abs(r*r*r)
	if b > prettyTolerance {
		return infeasible + infeasible*(avail-c.width)/avail
	}
	d := (lineDemerits + b) * (lineDemerits + b)
	if c.hyphen {
		d += hyphenPenalty * hyphenPenalty
		if hyphRun > 0 {
			d += doubleHyphen
		}
		if limit, ok := it.src.Style.Get(css.PHyphenateLimitLines).(css.Number); ok && hyphRun >= int(limit) {
			d += infDemerits
		}
		if lastOnPage {
			d += pageEndHyphen
		}
	}
	if f := fitness(r); f-prevFit > 1 || prevFit-f > 1 {
		d += adjDemerits
	}
	return d
}

// rateFrom returns the best demerits of the next lines from pos.
func (it *lineIterator) rateFrom(pos linePos, avail float64, depth int, prevHyph bool, hyphRun, prevFit int, memo map[[2]int]float64) float64 {
	if depth <= 0 || pos.item >= len(it.fl.items) || it.restIsEmpty(pos) {
		return 0
	}
	run := 0
	if prevHyph {
		run = hyphRun + 1
	}
	key := [2]int{encodePos(pos), depth*1000 + run*10 + prevFit}
	if v, ok := memo[key]; ok {
		return v
	}
	cands, end, last := it.measureEnd(pos, 0, avail+it.shrinkRoom(avail))
	var best float64
	if last {
		// The last line of the paragraph: only a very short one costs.
		best = 0
		if lw := it.restWidth(pos); lw < avail/6 {
			best = shortLastLine
		}
	} else {
		cands = it.fitting(cands, avail, end)
		if len(cands) > prettyCandidates {
			cands = cands[len(cands)-prettyCandidates:]
		}
		best = math.Inf(1)
		for _, c := range cands {
			d := it.demerits(c, avail, run, prevFit, false)
			if d >= best {
				continue
			}
			d += it.rateFrom(it.skipSpacesAt(c.pos), avail, depth-1, c.hyphen, run, fitness(it.ratio(c, avail)), memo)
			if d < best {
				best = d
			}
		}
		if math.IsInf(best, 1) {
			best = 0
		}
	}
	memo[key] = best
	return best
}

// restWidth returns the natural width of the content from pos to the end.
func (it *lineIterator) restWidth(pos linePos) float64 {
	w := 0.0
	items := it.fl.items
	for p := pos; p.item < len(items); p = (linePos{p.item + 1, 0}) {
		item := &items[p.item]
		switch item.kind {
		case itText:
			sh := item.shaped
			if p.r < len(sh.Runes) {
				w += sh.Pos[len(sh.Runes)] - sh.Pos[p.r]
			}
		case itAtomic:
			w += it.atomicBox(p.item).MarginWidth()
		}
	}
	return w
}

// balanceWidth returns the smallest line width that keeps the number of
// lines of a short block (at most 10 lines), for text-wrap: balance.
func (it *lineIterator) balanceWidth(start linePos, indent, avail float64) float64 {
	count := func(w float64) int {
		n := 0
		p := it.skipSpacesAt(start)
		ind := indent
		for p.item < len(it.fl.items) && !it.restIsEmpty(p) {
			saved := it.collect
			var dummy []breakCand
			it.collect = &dummy
			end, _, _, _ := it.breakLine(p, ind, w, 0, nil)
			it.collect = saved
			if !p.less(end) {
				return 1 << 20
			}
			n++
			if n > 10 {
				return n
			}
			p = it.skipSpacesAt(end)
			ind = 0
		}
		return n
	}
	n := count(avail)
	if n <= 1 || n > 10 {
		return 0
	}
	lo, hi := 0.0, avail
	for k := 0; k < 20 && hi-lo > 0.5; k++ {
		mid := (lo + hi) / 2
		if count(mid) <= n {
			hi = mid
		} else {
			lo = mid
		}
	}
	return hi
}

// fitting keeps the candidates whose line fits (with shrunk spaces for
// justified text), and the greedy end.
func (it *lineIterator) fitting(cands []breakCand, avail float64, end linePos) []breakCand {
	em := it.src.Style.FontSize()
	out := cands[:0:0]
	for _, c := range cands {
		room := 0.0
		if it.justified() {
			room = float64(c.spaces) * em / 9
		}
		if c.width <= avail+room+1e-6 && (!end.less(c.pos) || it.justified()) || c.pos == end {
			out = append(out, c)
		}
	}
	return out
}

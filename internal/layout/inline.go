package layout

import (
	"math"
	"strings"
	"unicode"

	"github.com/esoteric-programmer/weasyprint-go/internal/css"
	"github.com/esoteric-programmer/weasyprint-go/internal/hyphen"
	"github.com/esoteric-programmer/weasyprint-go/internal/otl"
	"github.com/esoteric-programmer/weasyprint-go/internal/text"
)

// ---------------------------------------------------------------- text spec

// SpecFor returns the text spec of a style.
func SpecFor(s *css.Style) text.Spec {
	spec := text.Spec{
		Families: s.FontFamily(),
		Weight:   int(s.Num(css.PFontWeight)),
		Italic:   s.Kw(css.PFontStyle) != "normal",
		Size:     s.FontSize(),
		RTL:      s.Kw(css.PDirection) == "rtl",
	}
	if o, ok := s.Get(css.PFontLanguageOverride).(css.Str); ok {
		// An OpenType language system tag.
		spec.Lang = string(o)
		if iso, ok := lstToISO[strings.ToLower(string(o))]; ok {
			spec.Lang = iso
		}
	} else if l, ok := s.Get(css.PLang).(css.Str); ok {
		spec.Lang = string(l)
	}
	if st := s.Kw(css.PFontStretch); st != "" {
		spec.Stretch = stretchNum(st)
	}
	if l, ok := s.Get(css.PLetterSpacing).(css.Length); ok {
		spec.LetterSpacing = l.Resolve(spec.Size)
	}
	if l, ok := s.Get(css.PWordSpacing).(css.Length); ok {
		spec.WordSpacing = l.Resolve(spec.Size)
	}
	spec.Caps = s.Kw(css.PFontVariantCaps)
	if vs, ok := s.Get(css.PFontVariationSettings).([]css.FontVariation); ok {
		spec.Variations = map[string]float64{}
		for _, v := range vs {
			spec.Variations[v.Tag] = v.Value
		}
	}
	spec.NoOpticalSizing = s.Kw(css.PFontOpticalSizing) == "none"
	alt := s.Get(css.PFontVariantAlternates)
	for _, f := range css.FontFeatures(s.Kw(css.PFontKerning), s.Get(css.PFontVariantLigatures), s.Get(css.PFontVariantPosition),
		s.Get(css.PFontVariantCaps), s.Get(css.PFontVariantNumeric), alt, s.Get(css.PFontVariantEastAsian),
		s.Get(css.PFontFeatureSettings)) {
		spec.Features = append(spec.Features, otl.Feature{Tag: f.Tag, Value: f.Value})
	}
	if fa, ok := alt.(css.FontAlternates); ok && len(fa.ByFamily) > 0 {
		spec.FamilyFeatures = map[string][]otl.Feature{}
		for fam, fs := range fa.ByFamily {
			for _, f := range fs {
				spec.FamilyFeatures[fam] = append(spec.FamilyFeatures[fam], otl.Feature{Tag: f.Tag, Value: f.Value})
			}
		}
	}
	return spec
}

func stretchNum(s string) int {
	switch s {
	case "ultra-condensed":
		return 1
	case "extra-condensed":
		return 2
	case "condensed":
		return 3
	case "semi-condensed":
		return 4
	case "semi-expanded":
		return 6
	case "expanded":
		return 7
	case "extra-expanded":
		return 8
	case "ultra-expanded":
		return 9
	}
	return 5
}

// strut returns the used line height and the baseline position from the
// top of the line height, for a style.
func (c *Context) strut(s *css.Style) (float64, float64) {
	if v, ok := c.strutCache[s]; ok {
		return v[0], v[1]
	}
	if s.FontSize() == 0 {
		return 0, 0
	}
	m := c.Shaper.PrimaryMetrics(SpecFor(s))
	textH := m.Ascent + m.Descent
	lh := s.LineHeight(textH)
	baseline := m.Ascent + (lh-textH)/2
	c.strutCache[s] = [2]float64{lh, baseline}
	return lh, baseline
}

func (c *Context) xHeight(s *css.Style) float64 {
	m := c.Shaper.PrimaryMetrics(SpecFor(s))
	return m.XHeight
}

// ---------------------------------------------------------------- flattening

type itemKind uint8

const (
	itText itemKind = iota
	itOpen
	itClose
	itAtomic
	itFloat
	itAbs
	itLeader
	itRunning
)

type flatItem struct {
	kind   itemKind
	box    *Box
	shaped *text.Shaped
	breaks []text.Break
	parent int // index of enclosing open item, -1 for the line
	match  int // for open: index of close; for close: index of open
	wrap   bool
	ws     string
	// autoHyph maps automatic hyphenation points to the start of their
	// word (rune indexes).
	autoHyph map[int]int
}

type flatLine struct {
	items []flatItem
}

func (c *Context) flatten(line *Box) *flatLine {
	if fl, ok := c.lineCache[line]; ok {
		return fl
	}
	fl := &flatLine{}
	var runes []rune
	type off struct{ item, start int }
	var offsets []off
	var walk func(b *Box, parent int)
	walk = func(b *Box, parent int) {
		for _, ch := range b.Children {
			switch {
			case ch.RunningName != "":
				fl.items = append(fl.items, flatItem{kind: itRunning, box: ch, parent: parent})
			case ch.IsFloated():
				fl.items = append(fl.items, flatItem{kind: itFloat, box: ch, parent: parent})
			case ch.IsAbsolutelyPositioned():
				fl.items = append(fl.items, flatItem{kind: itAbs, box: ch, parent: parent})
			case ch.Kind == KText:
				t := ch.Text
				if t == "" {
					continue
				}
				spec := SpecFor(ch.Style)
				sh := c.Shaper.Shape(t, spec)
				ws := ch.Style.WhiteSpace()
				idx := len(fl.items)
				fl.items = append(fl.items, flatItem{kind: itText, box: ch, shaped: sh, parent: parent, ws: ws,
					wrap: ws != "nowrap" && ws != "pre"})
				offsets = append(offsets, off{idx, len(runes)})
				runes = append(runes, sh.Runes...)
			case ch.Kind == KInline:
				idx := len(fl.items)
				fl.items = append(fl.items, flatItem{kind: itOpen, box: ch, parent: parent})
				walk(ch, idx)
				fl.items = append(fl.items, flatItem{kind: itClose, box: ch, parent: parent, match: idx})
				fl.items[idx].match = len(fl.items) - 1
			case ch.Kind == KLeader:
				fl.items = append(fl.items, flatItem{kind: itLeader, box: ch, parent: parent})
			default:
				idx := len(fl.items)
				fl.items = append(fl.items, flatItem{kind: itAtomic, box: ch, parent: parent, wrap: true})
				offsets = append(offsets, off{idx, len(runes)})
				runes = append(runes, 0xfffc)
			}
		}
	}
	walk(line, -1)
	opt := text.BreakOptions{}
	if line.Style != nil {
		switch line.Style.Kw(css.PWordBreak) {
		case "break-all":
			opt.BreakAll = true
		case "keep-all":
			opt.KeepAll = true
		}
		if line.Style.Kw(css.POverflowWrap) == "anywhere" {
			opt.Anywhere = true
		}
	}
	global := text.Breaks(runes, opt)
	prevWrap := true
	for _, o := range offsets {
		it := &fl.items[o.item]
		if it.kind == itText {
			n := len(it.shaped.Runes)
			it.breaks = make([]text.Break, n+1)
			copy(it.breaks, global[o.start:o.start+n+1])
			if !it.wrap {
				for i := 1; i < n; i++ {
					if it.breaks[i] != text.BreakMandatory {
						it.breaks[i] = text.BreakNone
					}
				}
				if it.breaks[0] != text.BreakMandatory {
					it.breaks[0] = text.BreakNone
				}
			}
			if !prevWrap && it.breaks[0] != text.BreakMandatory {
				it.breaks[0] = text.BreakNone
			}
			// Preserved newlines are mandatory breaks.
			if it.ws != "normal" && it.ws != "nowrap" {
				for i, r := range it.shaped.Runes {
					if r == '\n' {
						it.breaks[i+1] = text.BreakMandatory
					}
				}
			} else {
				for i := range it.breaks {
					if it.breaks[i] == text.BreakMandatory {
						it.breaks[i] = text.BreakAllowed
					}
				}
			}
			if it.wrap && it.box.Style.Kw(css.PHyphens) == "auto" {
				c.addAutoHyphens(it)
			}
			prevWrap = it.wrap
		} else {
			it.breaks = []text.Break{global[o.start], global[o.start+1]}
			if !prevWrap {
				it.breaks[0] = text.BreakNone
			}
			prevWrap = true
		}
	}
	c.lineCache[line] = fl
	return fl
}

// ---------------------------------------------------------------- positions

type linePos struct {
	item, r int
}

func encodePos(p linePos) int { return p.item<<20 | p.r }
func decodePos(v int) linePos { return linePos{v >> 20, v & (1<<20 - 1)} }

func (p linePos) less(q linePos) bool {
	return p.item < q.item || (p.item == q.item && p.r < q.r)
}

// ---------------------------------------------------------------- iterator

type lineIterator struct {
	c           *Context
	fl          *flatLine
	src         *Box
	cb          *Box
	cbi         cbInfo
	y           float64
	bottomSpace float64
	pos         linePos
	done        bool
	first       bool
	abs, fixed  *absList
	atomics     map[int]*Box

	// text-wrap-style: pretty and balance.
	collect  *[]breakCand // set while measuring candidate breaks
	hyphRun  int          // consecutive hyphenated lines so far
	prevFit  int          // fitness class of the previous line
	balanced float64      // line width for balance (0: not computed)
}

func (c *Context) newLineIterator(line *Box, cb *Box, cbi cbInfo, y, bottomSpace float64, skip Skip, abs, fixed *absList) *lineIterator {
	it := &lineIterator{c: c, fl: c.flatten(line), src: line, cb: cb, cbi: cbi, y: y, bottomSpace: bottomSpace,
		abs: abs, fixed: fixed, first: skip == nil, atomics: map[int]*Box{}}
	if skip != nil {
		k, _ := skipFirst(skip)
		it.pos = decodePos(k)
	}
	return it
}

func isCollapsibleSpace(it *flatItem, r rune) bool {
	return r == ' ' && (it.ws == "normal" || it.ws == "nowrap" || it.ws == "pre-line")
}

func (it *lineIterator) next() (*Box, Skip, bool) {
	if it.done || !it.first || it.src.FirstLine == nil {
		return it.nextLine()
	}
	// The first line is laid out from a copy of the line box restyled by
	// ::first-line, whose content is wrapped in an extra inline box: its
	// positions are shifted by one item.
	fl, src, atomics := it.fl, it.src, it.atomics
	clone := it.c.firstLineClone(src)
	it.fl, it.src, it.atomics = it.c.flatten(clone), clone, map[int]*Box{}
	line, skip, ok := it.nextLine()
	pos, done := it.pos, it.done
	it.fl, it.src, it.atomics = fl, src, atomics
	if !ok || done || skip == nil {
		it.done = true
		return line, nil, ok
	}
	p := linePos{pos.item - 1, pos.r}
	if p.item < 0 {
		p = linePos{}
	}
	if p.item >= len(fl.items) || it.restIsEmpty(p) {
		it.done = true
		return line, nil, true
	}
	it.pos = p
	return line, Skip{encodePos(p): nil}, true
}

func (it *lineIterator) nextLine() (*Box, Skip, bool) {
	if it.done {
		return nil, nil, false
	}
	items := it.fl.items
	start := it.pos
	// Skip leading collapsible spaces at the start of the line.
	start = it.skipSpacesAt(start)
	if start.item >= len(items) || it.restIsEmpty(start) {
		it.done = true
		return nil, nil, false
	}
	var line *Box
	var end linePos
	var forced bool
	var placedFloats map[int]*Box
	var waiting []int
	textIndent := 0.0
	if it.first {
		if l, ok := it.src.Style.Get(css.PTextIndent).(css.Length); ok {
			textIndent = l.Resolve(it.cbi.W)
		}
	}
	strutH, _ := it.c.strut(it.src.Style)
	placedFloats = map[int]*Box{}
	y := it.y
	retries := 0
	for {
		probe := &Box{Kind: KLine, Style: it.src.Style, X: it.cb.ContentX(), Y: y, H: strutH}
		x0, y0, avail := it.c.avoidCollisions(probe, it.cbi, it.cb, false)
		y = y0
		var newFloat bool
		end, forced, waiting, newFloat = it.chooseBreak(start, x0, textIndent, avail, y, placedFloats)
		if newFloat {
			continue
		}
		line = it.buildLine(start, end, x0, textIndent, avail, y, forced, placedFloats)
		line.LineRight = x0 + avail
		if st := it.src.Style; st.Kw(css.POverflow) != "visible" && st.Kw(css.PTextOverflow) == "ellipsis" {
			line.EllipsisRight = x0 + avail
		}
		// Lines taller than the strut may collide with floats lower down.
		if len(it.c.excluded) > 0 && line.H > strutH+1e-6 {
			probe.H = line.H
			x1, y1, avail1 := it.c.avoidCollisions(probe, it.cbi, it.cb, false)
			if (x1 != x0 || y1 != y0 || avail1 != avail) && retries < 4 {
				retries++
				strutH = line.H
				y = y1
				continue
			}
		}
		break
	}
	// Waiting floats go below the line.
	for _, idx := range waiting {
		fb := items[idx].box.Copy()
		fb.X = it.cb.ContentX()
		fb.Y = line.Y + line.H
		nf := it.c.floatLayout(fb, it.cb, it.cbi, it.abs, it.fixed, it.bottomSpace)
		line.Children = append(line.Children, nf)
	}
	it.first = false
	it.y = line.Y + line.H
	if end.item >= len(items) {
		it.done = true
		return line, nil, true
	}
	it.pos = end
	// If nothing but spaces remain, this is the last line.
	if it.restIsEmpty(end) {
		it.done = true
		return line, nil, true
	}
	return line, Skip{encodePos(end): nil}, true
}

// skipSpacesAt skips collapsible spaces at a line start.
func (it *lineIterator) skipSpacesAt(p linePos) linePos {
	items := it.fl.items
	for p.item < len(items) {
		item := &items[p.item]
		if item.kind != itText {
			return p
		}
		runes := item.shaped.Runes
		for p.r < len(runes) && isCollapsibleSpace(item, runes[p.r]) {
			p.r++
		}
		if p.r < len(runes) {
			return p
		}
		p = linePos{p.item + 1, 0}
	}
	return p
}

func (it *lineIterator) atomicBox(idx int) *Box {
	if b, ok := it.atomics[idx]; ok {
		return b
	}
	b := it.c.layoutAtomic(it.fl.items[idx].box, it.cb, it.cbi)
	it.atomics[idx] = b
	return b
}

func inlineStartEdge(b *Box, cbW float64) float64 {
	if b.NoStartDecor {
		return 0
	}
	s := b.Style
	ml := 0.0
	if v, ok := lengthOrAuto(s, css.PMarginLeft, cbW); ok {
		ml = v
	}
	return ml + s.BorderWidth(3) + s.Px(css.PPaddingLeft, cbW)
}

func inlineEndEdge(b *Box, cbW float64) float64 {
	if b.NoEndDecor {
		return 0
	}
	s := b.Style
	mr := 0.0
	if v, ok := lengthOrAuto(s, css.PMarginRight, cbW); ok {
		mr = v
	}
	return mr + s.BorderWidth(1) + s.Px(css.PPaddingRight, cbW)
}

// addAutoHyphens adds hyphenation opportunities to the words of a text
// item for "hyphens: auto".
func (c *Context) addAutoHyphens(it *flatItem) {
	st := it.box.Style
	lang, ok := st.Get(css.PLang).(css.Str)
	if !ok || lang == "" {
		return
	}
	dict := c.Hyphens.Lookup(string(lang), c.Hyphenation)
	if dict == nil {
		return
	}
	limits := css.LimitChars{-1, -1, -1}
	if l, ok := st.Get(css.PHyphenateLimitChars).(css.LimitChars); ok {
		limits = l
	}
	for i, def := range [3]int{dict.MinWord, dict.Left, dict.Right} {
		if limits[i] < 0 {
			limits[i] = def
		}
	}
	runes := it.shaped.Runes
	for i := 0; i < len(runes); {
		if !hyphen.IsWordRune(runes[i]) {
			i++
			continue
		}
		j := i
		for j < len(runes) && hyphen.IsWordRune(runes[j]) {
			j++
		}
		// Words containing soft hyphens use them instead; words split
		// across text boxes are not hyphenated.
		soft := (i > 0 && runes[i-1] == 0xad) || (j < len(runes) && runes[j] == 0xad)
		if !soft && j-i >= limits[0] {
			for _, p := range dict.Positions(string(runes[i:j]), limits[1], limits[2]) {
				if p <= 0 || p >= j-i || it.breaks[i+p] != text.BreakNone {
					continue
				}
				if it.autoHyph == nil {
					it.autoHyph = map[int]int{}
				}
				it.breaks[i+p] = text.BreakHyphen
				it.autoHyph[i+p] = i
			}
		}
		i = j
	}
}

func (it *lineIterator) hyphenWidth(item *flatItem) float64 {
	h := hyphenChar(item.box.Style)
	if h == "" {
		return 0
	}
	sh := it.c.Shaper.Shape(h, SpecFor(item.box.Style))
	return sh.Pos[len(sh.Runes)]
}

func hyphenChar(s *css.Style) string {
	if v, ok := s.Get(css.PHyphenateCharacter).(css.Str); ok {
		return string(v)
	}
	return "‐"
}

func tabWidth(item *flatItem, x, lineStart float64) float64 {
	s := item.box.Style
	space := item.shaped.Spec.Size * 0.25
	if len(item.shaped.Glyphs) > 0 {
		for _, g := range item.shaped.Glyphs {
			if item.shaped.Runes[g.Start] == '\t' && g.Face != nil {
				space = g.Face.Font.Advance(g.Face.Font.GlyphIndex(' ')) * g.Size / g.Face.Font.UnitsPerEm
				break
			}
		}
	}
	var stop float64
	switch v := s.Get(css.PTabSize).(type) {
	case css.Number:
		stop = float64(v) * (space + item.shaped.Spec.LetterSpacing + item.shaped.Spec.WordSpacing)
	case css.Length:
		stop = v.V
	}
	if stop <= 0 {
		return 0
	}
	rel := x - lineStart
	next := (math.Floor(rel/stop+1e-9) + 1) * stop
	return next - rel
}

// breakLine finds where the line starting at start ends.
func (it *lineIterator) breakLine(start linePos, x, maxX, y float64, placed map[int]*Box) (linePos, bool, []int, bool) {
	items := it.fl.items
	lineStartX := x
	type cand struct {
		pos    linePos
		hyphen bool
	}
	var last *cand
	hasContent := false
	var waiting []int
	cbW := it.cbi.W
	// Width without trailing spaces and space counts, for candidates.
	xContent, spaces, trailing := x, 0, 0
	record := func(pos linePos, hyphen bool, w float64) {
		if it.collect != nil {
			*it.collect = append(*it.collect, breakCand{pos: pos, hyphen: hyphen, width: w - lineStartX, spaces: spaces - trailing})
		}
	}
	overflowWrap := it.src.Style.Kw(css.POverflowWrap)
	breakWord := overflowWrap == "break-word" || overflowWrap == "anywhere" || it.src.Style.Kw(css.PWordBreak) == "break-word"
	// Inline boxes open at the start of the line that started earlier have
	// no start edge on this line.
	p := start
	firstPos := true
	for p.item < len(items) {
		item := &items[p.item]
		switch item.kind {
		case itOpen:
			x += inlineStartEdge(item.box, cbW)
		case itClose:
			x += inlineEndEdge(item.box, cbW)
			if trailing == 0 {
				xContent = x
			}
		case itText:
			runes := item.shaped.Runes
			for i := p.r; i < len(runes); i++ {
				b := item.breaks[i]
				if !firstPos && hasContent {
					if b == text.BreakMandatory {
						return it.endBefore(linePos{p.item, i}, start), true, waiting, false
					}
					if b == text.BreakAllowed {
						bp := linePos{p.item, i}
						if i == 0 {
							bp = it.backOverOpens(bp, start)
						}
						last = &cand{bp, false}
						record(bp, false, xContent)
					} else if b == text.BreakHyphen && i > 0 {
						// The hyphen must fit too; when nothing else
						// does, the shortest part is used.
						ok := true
						if ws, auto := item.autoHyph[i]; auto {
							space := maxX - (x - (item.shaped.Pos[i] - item.shaped.Pos[ws]))
							zone := item.box.Style.Px(css.PHyphenateLimitZone, maxX-lineStartX)
							ok = space > zone || space < 0
						}
						if ok && (x+it.hyphenWidth(item) <= maxX+1e-6 || last == nil) {
							last = &cand{linePos{p.item, i}, true}
							record(linePos{p.item, i}, true, x+it.hyphenWidth(item))
						}
					}
				}
				firstPos = false
				r := runes[i]
				var w float64
				if r == '\t' && !isCollapsibleSpace(item, r) {
					w = tabWidth(item, x, lineStartX)
				} else {
					w = item.shaped.Pos[i+1] - item.shaped.Pos[i]
				}
				isSpace := isCollapsibleSpace(item, r) || (r == ' ' && item.ws == "pre-wrap")
				if isCollapsibleSpace(item, r) && !hasContent {
					continue
				}
				if !isSpace && x+w > maxX+1e-6 && hasContent && maxX >= lineStartX {
					// (A negative available width, from a huge text-indent,
					// disables wrapping like in Pango.)
					if last != nil {
						return last.pos, false, waiting, false
					}
					if breakWord {
						// Avoid starting the next line with closing
						// punctuation or ending this one with opening
						// punctuation, when the word can break earlier.
						j := i
						for j > p.r+1 && badLineStart(runes, j) {
							j--
						}
						if badLineStart(runes, j) {
							j = i
						}
						return linePos{p.item, j}, false, waiting, false
					}
				}
				if r == '\n' && item.ws != "normal" && item.ws != "nowrap" {
					// Mandatory break after this newline.
					hasContent = true
					x += 0
					continue
				}
				x += w
				if !isSpace || item.ws == "pre" || item.ws == "pre-wrap" || item.ws == "break-spaces" {
					hasContent = true
				}
				if isSpace {
					spaces++
					trailing++
				} else {
					xContent = x
					trailing = 0
				}
				if last != nil && last.hyphen && x > maxX+1e-6 {
					// nothing
				}
			}
			if n := len(runes); n > 0 && item.breaks[n] == text.BreakMandatory && hasContent && runes[n-1] == '\n' {
				return linePos{p.item + 1, 0}, true, waiting, false
			}
		case itAtomic:
			b := item.breaks[0]
			if !firstPos && hasContent && (b == text.BreakAllowed || b == text.BreakHyphen) {
				last = &cand{it.backOverOpens(linePos{p.item, 0}, start), false}
				record(last.pos, false, xContent)
			}
			firstPos = false
			ab := it.atomicBox(p.item)
			w := ab.MarginWidth()
			if x+w > maxX+1e-6 && hasContent && last != nil {
				return last.pos, false, waiting, false
			}
			x += w
			xContent, trailing = x, 0
			hasContent = true
		case itFloat:
			if it.collect != nil {
				break
			}
			if _, ok := placed[p.item]; !ok {
				fb := item.box.Copy()
				fb.X = it.cb.ContentX()
				fb.Y = y
				nf := it.c.floatLayout(fb, it.cb, it.cbi, it.abs, it.fixed, it.bottomSpace)
				fits := nf.MarginWidth() <= maxX-x+1e-6 || !hasContent
				if fits && math.Abs(nf.Y-y) < 1e-6 {
					placed[p.item] = nf
					// Restart the line with the new exclusion.
					return linePos{}, false, nil, true
				}
				// Doesn't fit: place it after the line.
				it.c.excluded = it.c.excluded[:len(it.c.excluded)-1]
				it.c.removePlaceholders([]*Box{nf}, it.abs, it.fixed)
				waiting = append(waiting, p.item)
				placed[p.item] = nil
			}
		case itLeader:
			hasContent = true
		}
		p = linePos{p.item + 1, 0}
	}
	return p, false, waiting, false
}

// backOverOpens moves a break position before immediately preceding open
// items, so that inline boxes start on the next line with their edges.
func (it *lineIterator) backOverOpens(p, start linePos) linePos {
	items := it.fl.items
	for p.r == 0 && p.item > start.item && items[p.item-1].kind == itOpen {
		p = linePos{p.item - 1, 0}
	}
	return p
}

func (it *lineIterator) endBefore(p, start linePos) linePos {
	return it.backOverOpens(p, start)
}

// buildLine builds the line box for items in [start, end).
func (it *lineIterator) buildLine(start, end linePos, x0, textIndent, avail, y float64, forced bool, placed map[int]*Box) *Box {
	items := it.fl.items
	c := it.c
	line := it.src.CopyWith(nil)
	line.X = x0
	line.Y = y
	line.NoStartDecor, line.NoEndDecor = false, false

	// Reopen inline boxes enclosing the start position.
	var chain []int
	for p := it.parentAt(start); p >= 0; p = items[p].parent {
		chain = append([]int{p}, chain...)
	}
	type frame struct {
		box *Box
		idx int
	}
	stack := []frame{{line, -1}}
	for _, idx := range chain {
		ib := items[idx].box.CopyWith(nil)
		// Continued boxes have no start edge, unless they clone it.
		ib.NoStartDecor = !clonesDecoration(ib)
		ib.Anchor = ""
		ib.BookmarkLevel = 0
		ib.StringSets = nil
		stack[len(stack)-1].box.Children = append(stack[len(stack)-1].box.Children, ib)
		stack = append(stack, frame{ib, idx})
	}
	top := func() *Box { return stack[len(stack)-1].box }
	lineContent := false
	add := func(b *Box) { t := top(); t.Children = append(t.Children, b) }

	p := start
	for p.less(end) && p.item < len(items) {
		item := &items[p.item]
		switch item.kind {
		case itOpen:
			ib := item.box.CopyWith(nil)
			add(ib)
			stack = append(stack, frame{ib, p.item})
		case itClose:
			if len(stack) > 1 {
				stack = stack[:len(stack)-1]
			}
		case itText:
			e := len(item.shaped.Runes)
			if end.item == p.item {
				e = end.r
			}
			if !lineContent {
				for p.r < e && isCollapsibleSpace(item, item.shaped.Runes[p.r]) {
					p.r++
				}
			}
			if e > p.r {
				lineContent = true
				tb := item.box.CopyWith(nil)
				tb.Shaped = item.shaped
				tb.Start, tb.End = p.r, e
				tb.Spec = item.shaped.Spec
				add(tb)
			}
		case itAtomic:
			lineContent = true
			add(it.atomicBox(p.item))
		case itFloat:
			if nf := placed[p.item]; nf != nil {
				add(nf)
			}
		case itAbs:
			ph := item.box.Copy()
			ph.IsAbsPlaceholder = true
			if item.box.Style.Kw(css.PPosition) == "fixed" {
				it.fixed.add(ph)
			} else {
				it.abs.add(ph)
			}
			add(ph)
		case itRunning:
			c.addRunning(item.box)
		case itLeader:
			lb := item.box.CopyWith(nil)
			add(lb)
		}
		p = linePos{p.item + 1, 0}
	}
	// Inline boxes still open continue on the next line.
	for i := 1; i < len(stack); i++ {
		if stack[i].idx >= 0 {
			stack[i].box.NoEndDecor = !clonesDecoration(stack[i].box)
		}
	}
	// Soft hyphen at the end of the line.
	if !forced && end.item < len(items) && items[end.item].kind == itText && end.r > 0 && items[end.item].breaks[end.r] == text.BreakHyphen {
		if last := lastTextBox(line); last != nil {
			last.Hyphen = hyphenChar(last.Style)
		}
	}
	removeTrailingSpaces(line)
	// Horizontal layout.
	it.sizeInlines(line)
	width := positionInline(line, x0+textIndent)
	// Leaders fill the remaining space.
	expandLeaders(line, avail-textIndent-width)
	width = positionInline(line, x0+textIndent)
	// Leader patterns are aligned on the end of the line (WeasyPrint's
	// handle_leader).
	line.Descendants(func(d *Box) {
		if d.Kind == KLeader {
			d.LeaderAnchor = x0 + avail - (d.X + d.W)
		}
	})
	// Vertical alignment.
	c.lineVerticality(line)
	if !forced && isPhantomLine(line) {
		line.H = 0
	}
	// Text alignment.
	last := end.item >= len(items) || forced || it.restIsEmpty(end)
	style := it.src.Style
	align := style.TextAlign()
	if last {
		align = style.TextAlignLast()
	}
	if (align == "justify") && !last {
		extra := avail - textIndent - width
		// text-wrap-style: pretty may shrink spaces (see pretty.go).
		if extra > 0 || (extra < -1e-6 && style.Kw(css.PTextWrapStyle) == "pretty") {
			if n := countSpaces(line); n > 0 {
				addJustification(line, extra/float64(n))
				width = positionInline(line, x0+textIndent)
			}
		}
	}
	offset := 0.0
	switch align {
	case "right":
		offset = avail - textIndent - width
	case "center":
		offset = (avail - textIndent - width) / 2
	}
	if style.Kw(css.PDirection) == "rtl" && (align == "justify") && last {
		offset = avail - textIndent - width
	}
	if offset != 0 {
		for _, ch := range line.Children {
			translateInFlow(ch, offset, 0)
		}
	}
	line.W = width
	return line
}

func (it *lineIterator) restIsEmpty(p linePos) bool {
	items := it.fl.items
	for i := p.item; i < len(items); i++ {
		item := &items[i]
		switch item.kind {
		case itText:
			start := 0
			if i == p.item {
				start = p.r
			}
			for _, r := range item.shaped.Runes[start:] {
				if !isCollapsibleSpace(item, r) {
					return false
				}
			}
		case itClose:
		case itOpen:
			if inlineHasEdges(item.box) {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// parentAt returns the index of the open item enclosing position p.
func (it *lineIterator) parentAt(p linePos) int {
	items := it.fl.items
	if p.item >= len(items) {
		return -1
	}
	item := items[p.item]
	if item.kind == itClose {
		// Position at a close item: inside the open item it closes.
		return item.match
	}
	return item.parent
}

func lastTextBox(b *Box) *Box {
	for i := len(b.Children) - 1; i >= 0; i-- {
		ch := b.Children[i]
		if ch.Kind == KText {
			return ch
		}
		if ch.Kind == KInline {
			if t := lastTextBox(ch); t != nil {
				return t
			}
			continue
		}
		if ch.IsInNormalFlow() && !ch.IsAbsPlaceholder {
			return nil
		}
	}
	return nil
}

// removeTrailingSpaces trims collapsible spaces at the end of a line.
func removeTrailingSpaces(b *Box) bool {
	for i := len(b.Children) - 1; i >= 0; i-- {
		ch := b.Children[i]
		switch {
		case ch.Kind == KText:
			ws := ch.Style.WhiteSpace()
			if ws == "pre" || ws == "nowrap" && false {
				return true
			}
			collapsible := ws == "normal" || ws == "nowrap" || ws == "pre-line"
			for ch.End > ch.Start {
				r := ch.Shaped.Runes[ch.End-1]
				if (r == ' ' && (collapsible || ws == "pre-wrap")) || r == '\n' {
					ch.End--
					continue
				}
				break
			}
			if ch.End > ch.Start {
				return true
			}
			if ch.Hyphen != "" {
				return true
			}
			b.Children = append(b.Children[:i], b.Children[i+1:]...)
		case ch.Kind == KInline:
			if removeTrailingSpaces(ch) {
				return true
			}
			if len(ch.Children) > 0 || inlineHasEdges(ch) {
				return true
			}
		case ch.IsFloated() || ch.IsAbsPlaceholder:
			continue
		default:
			return true
		}
	}
	return false
}

func inlineHasEdges(b *Box) bool {
	s := b.Style
	return (!b.NoStartDecor && (s.BorderWidth(3) > 0 || s.Px(css.PPaddingLeft, 0) > 0)) || (!b.NoEndDecor && (s.BorderWidth(1) > 0 || s.Px(css.PPaddingRight, 0) > 0))
}

// sizeInlines sets the widths of text boxes and the edges of inline boxes.
func (it *lineIterator) sizeInlines(b *Box) {
	cbW := it.cbi.W
	for _, ch := range b.Children {
		switch ch.Kind {
		case KText:
			ch.W = ch.Shaped.Width(ch.Start, ch.End)
			// Tabs are sized once the position in the line is known.
			ch.HyphenW = 0
			if ch.Hyphen != "" {
				hs := it.c.Shaper.Shape(ch.Hyphen, ch.Spec)
				ch.HyphenW = hs.Pos[len(hs.Runes)]
				ch.W += ch.HyphenW
			}
			ch.ML, ch.MR, ch.PL, ch.PR, ch.BL, ch.BR = 0, 0, 0, 0, 0, 0
		case KInline:
			resolvePercentages(ch, cbW)
			ch.ML, ch.MR = nanTo0(ch.ML), nanTo0(ch.MR)
			it.sizeInlines(ch)
		case KLeader:
			ch.W = 0
		}
	}
}

// positionInline lays children out horizontally from x and returns the
// total margin width.
func positionInline(b *Box, x float64) float64 {
	return positionInlineFrom(b, x, x)
}

// hasTabs reports whether a text box contains preserved tabs.
func hasTabs(ch *Box) bool {
	if ch.Style.WhiteSpace() == "normal" || ch.Style.WhiteSpace() == "nowrap" {
		return false
	}
	for _, r := range ch.Shaped.Runes[ch.Start:ch.End] {
		if r == '\t' {
			return true
		}
	}
	return false
}

// textWidthAt returns the width of a text box with tabs starting at x,
// tab stops being relative to origin (the start of the line).
func textWidthAt(ch *Box, x, origin float64, hyphenW float64) float64 {
	item := &flatItem{box: ch, shaped: ch.Shaped, ws: ch.Style.WhiteSpace()}
	w := 0.0
	for i := ch.Start; i < ch.End; i++ {
		if ch.Shaped.Runes[i] == '\t' {
			w += tabWidth(item, x+w, origin)
		} else {
			w += ch.Shaped.Pos[i+1] - ch.Shaped.Pos[i]
		}
	}
	return w + hyphenW
}

func positionInlineFrom(b *Box, x, origin float64) float64 {
	start := x
	for _, ch := range b.Children {
		if ch.IsFloated() {
			continue
		}
		if ch.IsAbsPlaceholder {
			ch.X = x
			ch.StaticX = x
			continue
		}
		switch ch.Kind {
		case KInline:
			ch.X = x
			inner := positionInlineFrom(ch, x+ch.ML+ch.BL+ch.PL, origin)
			ch.W = inner
			x += ch.MarginWidth()
		case KText, KLeader:
			ch.X = x
			if ch.Kind == KText && ch.Shaped != nil && hasTabs(ch) {
				ch.TabOrigin = origin
				ch.W = textWidthAt(ch, x, origin, ch.HyphenW)
			}
			x += ch.W
		default:
			ch.Translate(x-ch.X, 0)
			x += ch.MarginWidth()
		}
	}
	// Right-to-left boxes show their children in reverse order
	// (WeasyPrint's split_inline_box); text is reordered when drawn.
	if b.Style != nil && b.Style.Kw(css.PDirection) == "rtl" {
		var inflow []*Box
		for _, ch := range b.Children {
			if !ch.IsFloated() && !ch.IsAbsPlaceholder && !ch.IsAbsolutelyPositioned() {
				inflow = append(inflow, ch)
			}
		}
		if len(inflow) > 1 {
			px := inflow[0].X
			for k := len(inflow) - 1; k >= 0; k-- {
				ch := inflow[k]
				ch.Translate(px-ch.X, 0)
				px += ch.MarginWidth()
			}
		}
	}
	return x - start
}

func expandLeaders(b *Box, extra float64) {
	if extra <= 0 {
		return
	}
	var leaders []*Box
	b.Descendants(func(d *Box) {
		if d.Kind == KLeader {
			leaders = append(leaders, d)
		}
	})
	if len(leaders) == 0 {
		return
	}
	each := extra / float64(len(leaders))
	for _, l := range leaders {
		l.W = each
	}
}

func countSpaces(b *Box) int {
	n := 0
	for _, ch := range b.Children {
		switch ch.Kind {
		case KText:
			for _, r := range ch.Shaped.Runes[ch.Start:ch.End] {
				if r == ' ' || r == 0xa0 {
					n++
				}
			}
		case KInline:
			n += countSpaces(ch)
		}
	}
	return n
}

func addJustification(b *Box, spacing float64) {
	for _, ch := range b.Children {
		switch ch.Kind {
		case KText:
			n := 0
			for _, r := range ch.Shaped.Runes[ch.Start:ch.End] {
				if r == ' ' || r == 0xa0 {
					n++
				}
			}
			ch.JustifySpacing = spacing
			ch.W += float64(n) * spacing
		case KInline:
			addJustification(ch, spacing)
		}
	}
}

// ---------------------------------------------------------------- vertical

func (c *Context) textMetrics(tb *Box) (float64, float64) {
	// Max ascent/descent of the faces used by the fragment.
	asc, desc := 0.0, 0.0
	found := false
	if tb.Shaped != nil {
		for _, g := range tb.Shaped.Glyphs {
			if g.End <= tb.Start || g.Start >= tb.End || g.Face == nil {
				continue
			}
			k := tb.Spec.Size / g.Face.Font.UnitsPerEm
			a, d := g.Face.Font.Ascent*k, g.Face.Font.Descent*k
			if !found || a > asc {
				asc = a
			}
			if !found || d > desc {
				desc = d
			}
			found = true
		}
	}
	if !found {
		m := c.Shaper.PrimaryMetrics(SpecFor(tb.Style))
		asc, desc = m.Ascent, m.Descent
	}
	return asc, desc
}

// setInlineHeights sets heights, vertical margins and baselines of the
// boxes of a line (relative to their own top).
func (c *Context) setInlineHeights(b *Box) {
	for _, ch := range b.Children {
		switch ch.Kind {
		case KText:
			asc, desc := c.textMetrics(ch)
			ch.H = asc + desc
			lh, _ := c.strut(ch.Style)
			half := (lh - ch.H) / 2
			ch.MT, ch.MB = half, half
			ch.Baseline = asc + half
			ch.Metrics = c.Shaper.PrimaryMetrics(ch.Spec)
		case KLeader:
			lh, bl := c.strut(ch.Style)
			ch.H = ch.Style.FontSize()
			half := (lh - ch.H) / 2
			ch.MT, ch.MB = half, half
			ch.Baseline = bl
		case KInline:
			c.adjustLineHeight(ch)
			c.setInlineHeights(ch)
		}
	}
}

func (c *Context) adjustLineHeight(b *Box) {
	lh, bl := c.strut(b.Style)
	b.Baseline = bl
	b.H = b.Style.FontSize()
	half := (lh - b.H) / 2
	b.MT = half - b.BT - b.PT
	b.MB = half - b.BB - b.PB
}

func (c *Context) lineVerticality(line *Box) {
	c.adjustLineHeight(line)
	line.PT, line.PB, line.BT, line.BB = 0, 0, 0, 0
	line.ML, line.MR, line.PL, line.PR, line.BL, line.BR = 0, 0, 0, 0, 0, 0
	c.setInlineHeights(line)
	var topBottom []*Box
	maxY, minY, has := c.alignedSubtreeVerticality(line, &topBottom, 0)
	if !has {
		maxY, minY = 0, 0
	}
	var subs []struct {
		b        *Box
		max, min float64
	}
	for _, sb := range topBottom {
		if sb.IsFloated() {
			continue
		}
		mx, mn, _ := c.alignedSubtreeVerticality(sb, &topBottom, 0)
		subs = append(subs, struct {
			b        *Box
			max, min float64
		}{sb, mx, mn})
	}
	highest := 0.0
	for _, s := range subs {
		if s.max-s.min > highest {
			highest = s.max - s.min
		}
	}
	if len(subs) > 0 {
		maxY = math.Max(maxY, minY+highest)
	}
	for _, s := range subs {
		var dy float64
		if s.b.Style.Kw(css.PVerticalAlign) == "top" {
			dy = minY - s.min
		} else {
			dy = maxY - s.max
		}
		translateSubtree(s.b, dy)
	}
	line.Baseline = -minY
	offsetY := line.Y - minY
	line.H = maxY - minY
	line.MT, line.MB = 0, 0
	for _, ch := range line.Children {
		translateInFlow(ch, 0, offsetY)
	}
}

// translateInFlow moves an inline-level box and its content, except the
// floats it contains, which are already placed.
func translateInFlow(b *Box, dx, dy float64) {
	if b.IsFloated() {
		return
	}
	if b.Kind != KInline {
		b.Translate(dx, dy)
		return
	}
	b.X += dx
	b.Y += dy
	for _, ch := range b.Children {
		translateInFlow(ch, dx, dy)
	}
}

func translateSubtree(b *Box, dy float64) {
	if b.Kind == KInline {
		b.Y += dy
		va := b.Style.Kw(css.PVerticalAlign)
		if va == "top" || va == "bottom" {
			for _, ch := range b.Children {
				translateSubtree(ch, dy)
			}
		}
		return
	}
	translateInFlow(b, 0, dy)
}

func (c *Context) alignedSubtreeVerticality(b *Box, tb *[]*Box, baselineY float64) (float64, float64, bool) {
	maxY, minY, has := c.inlineBoxVerticality(b, tb, baselineY)
	top := baselineY - b.Baseline
	bottom := top + b.MarginHeight()
	if !has || top < minY {
		minY = top
	}
	if !has || bottom > maxY {
		maxY = bottom
	}
	return maxY, minY, true
}

func (c *Context) inlineBoxVerticality(b *Box, tb *[]*Box, baselineY float64) (float64, float64, bool) {
	if b.Kind != KLine && b.Kind != KInline {
		return 0, 0, false
	}
	var maxY, minY float64
	has := false
	for _, ch := range b.Children {
		if ch.IsFloated() {
			*tb = append(*tb, ch)
			continue
		}
		if ch.IsAbsPlaceholder {
			ch.Y = baselineY - b.Baseline
			continue
		}
		va := ch.Style.Get(css.PVerticalAlign)
		if ch.Kind == KText || ch.Kind == KLeader || (ch.Anonymous && ch.Kind != KInline) {
			// Anonymous boxes have the initial vertical-align.
			va = css.Keyword("baseline")
		}
		var childBaseline float64
		kw, isKw := va.(css.Keyword)
		switch {
		case isKw && kw == "baseline":
			childBaseline = baselineY
		case isKw && kw == "middle":
			oneEx := c.xHeight(b.Style)
			top := baselineY - (oneEx+ch.MarginHeight())/2
			childBaseline = top + ch.Baseline
		case isKw && kw == "text-top":
			top := baselineY - b.Baseline + b.MT + b.BT + b.PT
			childBaseline = top + ch.Baseline
		case isKw && kw == "text-bottom":
			bottom := baselineY - b.Baseline + b.MT + b.BT + b.PT + b.H
			childBaseline = bottom - ch.MarginHeight() + ch.Baseline
		case isKw && (kw == "top" || kw == "bottom"):
			childBaseline = 0
		case isKw && kw == "sub":
			childBaseline = baselineY + b.Style.FontSize()*0.2
		case isKw && kw == "super":
			childBaseline = baselineY - b.Style.FontSize()*0.3
		default:
			if l, ok := va.(css.Length); ok {
				lh, _ := c.strut(b.Style)
				childBaseline = baselineY - l.Resolve(lh)
			} else {
				childBaseline = baselineY
			}
		}
		top := childBaseline - ch.Baseline
		if ch.IsAtomicInline() {
			ch.Translate(0, top-ch.Y)
		} else {
			ch.Y = top
		}
		if isKw && (kw == "top" || kw == "bottom") {
			*tb = append(*tb, ch)
			continue
		}
		bottom := top + ch.MarginHeight()
		if !has || top < minY {
			minY = top
		}
		if !has || bottom > maxY {
			maxY = bottom
		}
		has = true
		if ch.Kind == KInline {
			cmax, cmin, chas := c.inlineBoxVerticality(ch, tb, childBaseline)
			if chas {
				if cmin < minY {
					minY = cmin
				}
				if cmax > maxY {
					maxY = cmax
				}
			}
		}
	}
	return maxY, minY, has
}

// ---------------------------------------------------------------- atomic

// layoutAtomic lays out an atomic inline-level box at (0, 0).
func (c *Context) layoutAtomic(orig *Box, cb *Box, cbi cbInfo) *Box {
	defer c.lineBarrier()()
	box := orig.Copy()
	box.X, box.Y = 0, 0
	resolvePercentages(box, cbi.W)
	box.ML, box.MR, box.MT, box.MB = nanTo0(box.ML), nanTo0(box.MR), nanTo0(box.MT), nanTo0(box.MB)
	box.W = specifiedWidth(box, cbi.W)
	box.H = specifiedHeight(box, cbi)
	switch box.Kind {
	case KReplaced:
		c.replacedWidthHeight(box, cbi)
		box.Baseline = box.MarginHeight()
		return box
	case KTableWrapper:
		c.tableWrapperWidth(box, cbi)
		blockLevelWidth(box, cbi.W, false, cbi.Style)
		if math.IsNaN(box.ML) {
			box.ML = 0
		}
		res := c.blockContainerLayout(box, math.Inf(-1), nil, true, &absList{}, &absList{}, nil, false, cbi)
		nb := res.box
		if nb == nil {
			nb = box
		}
		nb.Baseline = nb.MarginHeight()
		if y, ok := firstBaseline(nb); ok {
			nb.Baseline = y - nb.Y
		}
		return nb
	case KFlex, KGrid:
		if math.IsNaN(box.W) {
			box.W = c.shrinkToFit(box, cbi.W-box.ML-box.MR-box.PL-box.PR-box.BL-box.BR)
		}
		var res blockResult
		if box.Kind == KGrid {
			res = c.gridLayout(box, math.Inf(-1), nil, cb, cbi, true, &absList{}, &absList{})
		} else {
			res = c.flexLayout(box, math.Inf(-1), nil, cb, cbi, true, &absList{}, &absList{})
		}
		nb := res.box
		if nb == nil {
			nb = box
		}
		nb.Baseline = nb.MarginHeight()
		if y, ok := firstBaseline(nb); ok {
			nb.Baseline = y - nb.Y
		}
		return nb
	}
	// Inline-block.
	if math.IsNaN(box.W) {
		box.W = c.shrinkToFit(box, cbi.W-box.ML-box.MR-box.PL-box.PR-box.BL-box.BR)
	}
	mn, mx := minMaxWidth(box, cbi.W)
	box.W = math.Max(mn, math.Min(mx, box.W))
	savedBottom := c.saveBottom()
	c.PageBottom = math.Inf(1)
	res := c.blockContainerLayout(box, math.Inf(-1), nil, true, &absList{}, &absList{}, nil, false, cbi)
	c.restoreBottom(savedBottom)
	nb := res.box
	if nb == nil {
		nb = box
	}
	nb.Baseline = nb.MarginHeight()
	if nb.Style.Kw(css.POverflow) == "visible" {
		if y, ok := lastBaseline(nb); ok {
			nb.Baseline = y - nb.Y
		}
	}
	return nb
}

// isPhantomLine reports whether a line has no content that gives it height.
func isPhantomLine(b *Box) bool {
	for _, ch := range b.Children {
		switch {
		case ch.IsFloated() || ch.IsAbsPlaceholder:
		case ch.Kind == KText:
			if ch.End > ch.Start || ch.Hyphen != "" {
				return false
			}
		case ch.Kind == KInline:
			if inlineHasEdges(ch) || !isPhantomLine(ch) {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// TabWidths returns the widths of the preserved tabs of a laid-out text
// box, in order.
func TabWidths(b *Box) []float64 {
	if b.Shaped == nil || !hasTabs(b) {
		return nil
	}
	item := &flatItem{box: b, shaped: b.Shaped, ws: b.Style.WhiteSpace()}
	var out []float64
	x := b.X
	for i := b.Start; i < b.End; i++ {
		if b.Shaped.Runes[i] == '\t' {
			w := tabWidth(item, x, b.TabOrigin)
			out = append(out, w)
			x += w
		} else {
			x += b.Shaped.Pos[i+1] - b.Shaped.Pos[i]
		}
	}
	return out
}

// clonesDecoration reports whether the fragments of b keep all their
// borders, padding and margins (box-decoration-break: clone).
func clonesDecoration(b *Box) bool {
	return b.Style != nil && b.Style.Kw(css.PBoxDecorationBreak) == "clone"
}

// badLineStart reports whether breaking before runes[i] would start a line
// with closing punctuation or end one with opening punctuation.
func badLineStart(runes []rune, i int) bool {
	if i <= 0 || i >= len(runes) {
		return false
	}
	r, prev := runes[i], runes[i-1]
	if unicode.In(r, unicode.Pe, unicode.Pf) || strings.ContainsRune(",.;:!?\u2026", r) {
		return true
	}
	return unicode.In(prev, unicode.Ps, unicode.Pi)
}

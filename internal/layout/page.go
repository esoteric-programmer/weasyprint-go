package layout

import (
	"math"
	"strings"

	"github.com/esoteric-programmer/weasyprint-go/internal/css"
)

// Page is a laid-out page.
type Page struct {
	Box        *Box // KPage box
	Width      float64
	Height     float64
	Type       css.PageType
	Counters   map[string][]int
	StringSets map[string][]string
	Running    map[string][]*Box
	Blank      bool
}

// A4 is the default page size, used when @page size is auto (CSS px).
var A4 = [2]float64{210 * 96 / 25.4, 297 * 96 / 25.4}

func (c *Context) pageGeometry(style *css.Style) *Box {
	w, h := A4[0], A4[1]
	if c.DefaultPageSize[0] > 0 && c.DefaultPageSize[1] > 0 {
		w, h = c.DefaultPageSize[0], c.DefaultPageSize[1]
	}
	if ps, ok := style.Get(css.PSize).(css.PageSize); ok {
		w, h = ps[0].V, ps[1].V
	}
	page := &Box{Kind: KPage, Style: style}
	page.X, page.Y = 0, 0
	m := func(id css.PropID, base float64) float64 {
		if v, ok := lengthOrAuto(style, id, base); ok {
			return v
		}
		return 0
	}
	page.MT, page.MB = m(css.PMarginTop, w), m(css.PMarginBottom, w)
	page.ML, page.MR = m(css.PMarginLeft, w), m(css.PMarginRight, w)
	page.PT, page.PR, page.PB, page.PL = style.Px(css.PPaddingTop, w), style.Px(css.PPaddingRight, w), style.Px(css.PPaddingBottom, w), style.Px(css.PPaddingLeft, w)
	page.BT, page.BR, page.BB, page.BL = style.BorderWidth(0), style.BorderWidth(1), style.BorderWidth(2), style.BorderWidth(3)
	page.W = math.Max(0, w-page.ML-page.MR-page.PL-page.PR-page.BL-page.BR)
	page.H = math.Max(0, h-page.MT-page.MB-page.PT-page.PB-page.BT-page.BB)
	return page
}

// FirstPageSize returns the size of the first page, for viewport units.
func (c *Context) FirstPageSize(root *Box) (float64, float64) {
	name := ""
	if root != nil {
		name, _ = pageValues(root)
	}
	st := c.R.PageStyle(css.PageType{Name: name, Side: "right", First: true})
	p := c.pageGeometry(st)
	return p.W + p.ML + p.MR + p.PL + p.PR + p.BL + p.BR, p.H + p.MT + p.MB + p.PT + p.PB + p.BT + p.BB
}

// LayoutDocument lays out the root box into pages.
func (c *Context) LayoutDocument(root *Box) []*Page {
	var pages []*Page
	if root == nil {
		return pages
	}
	rtl := root.Style.Kw(css.PDirection) == "rtl"
	var skip Skip
	nextBreak := "any"
	forced := false
	nextName, _ := pageValues(root)
	counters := map[string][]int{}
	var fixedSources []*Box
	c.LastStringSets = map[string]string{}
	c.fn = footnotes{enabled: c.Builder != nil && c.Builder.HasFootnotes, state: map[*Box]fnState{}, claim: map[*Box]int{}}
	contentDone := false
	for index := 0; index < 100000; index++ {
		side := "right"
		if (index%2 == 1) != rtl {
			side = "left"
		}
		blank := false
		switch nextBreak {
		case "left", "verso":
			blank = side != "left"
		case "right", "recto":
			blank = side != "right"
		}
		if (nextBreak == "recto" || nextBreak == "verso") && rtl {
			blank = !blank
		}
		if contentDone {
			// Only reported footnotes are left.
			blank = true
		}
		pt := css.PageType{Name: nextName, Side: side, Blank: blank, First: index == 0, Index: index}
		style := c.R.PageStyle(pt)
		page := c.pageGeometry(style)
		c.applyPageCounters(style, counters)
		p := &Page{Box: page, Type: pt, Blank: blank, StringSets: map[string][]string{}, Running: map[string][]*Box{}}
		p.Width = page.MarginWidth()
		p.Height = page.MarginHeight()
		p.Counters = snapshot(counters)
		c.PageIndex = index
		c.excluded = nil
		c.bfcStack = nil
		c.pageRunning = map[string][]*Box{}
		c.PageBottom = page.ContentY() + page.H
		c.PageTop = page.ContentY()
		c.startPageFootnotes(pt, style, page)
		if blank {
			if area := c.finishPageFootnotes(page); area != nil {
				page.Children = append(page.Children, area)
			}
			pages = append(pages, p)
			nextBreak = "any"
			forced = true
			if contentDone && len(c.fn.reported) == 0 {
				break
			}
			continue
		}
		c.forcedBreak = forced && index > 0
		abs, fixed := &absList{}, &absList{}
		r := root
		r.X, r.Y = page.ContentX(), page.ContentY()
		cbi := cbInfo{W: page.W, H: page.H, Root: true, Style: style}
		res := c.blockLevelLayout(r, 0, skip, page, cbi, true, abs, fixed, nil, false)
		if res.box != nil {
			page.Children = append(page.Children, res.box)
		}
		for _, a := range abs.boxes {
			c.absoluteLayout(a, page, fixed)
		}
		// Fixed boxes repeat on every page.
		for _, src := range fixedSources {
			ph := src.Copy()
			ph.IsAbsPlaceholder = true
			ph.Orig = src
			page.Children = append(page.Children, ph)
			c.absoluteLayout(ph, page, fixed)
		}
		for _, f := range fixed.boxes {
			if f.IsAbsPlaceholder {
				fixedSources = append(fixedSources, f.Original())
				c.absoluteLayout(f, page, fixed)
			}
		}
		if area := c.finishPageFootnotes(page); area != nil {
			page.Children = append(page.Children, area)
		}
		c.collectStringSets(page, p)
		p.Running = c.pageRunning
		pages = append(pages, p)
		if res.resume == nil {
			if len(c.fn.reported) == 0 {
				break
			}
			contentDone = true
			nextBreak = "any"
			continue
		}
		skip = res.resume
		nextBreak = res.next.Break
		forced = forcePageBreak(nextBreak) || res.next.Page != nextName
		if nextBreak == "" || nextBreak == "page" || nextBreak == "always" || nextBreak == "column" {
			nextBreak = "any"
		}
		nextName = res.next.Page
	}
	// Margin boxes need the total page count.
	c.layoutMarginBoxes(pages)
	return pages
}

func snapshot(m map[string][]int) map[string][]int {
	out := map[string][]int{}
	for k, v := range m {
		out[k] = append([]int(nil), v...)
	}
	return out
}

func (c *Context) applyPageCounters(style *css.Style, counters map[string][]int) {
	for _, op := range style.Counters(css.PCounterReset) {
		counters[op.Name] = []int{op.Value}
	}
	for _, op := range style.Counters(css.PCounterSet) {
		counters[op.Name] = []int{op.Value}
	}
	incPage := true
	for _, op := range style.Counters(css.PCounterIncrement) {
		if op.Name == "page" {
			incPage = false
		}
		v := counters[op.Name]
		if len(v) == 0 {
			v = []int{0}
		}
		v[len(v)-1] += op.Value
		counters[op.Name] = v
	}
	if incPage {
		v := counters["page"]
		if len(v) == 0 {
			v = []int{0}
		}
		v[len(v)-1]++
		counters["page"] = v
	}
}

func (c *Context) collectStringSets(page *Box, p *Page) {
	var walk func(b *Box)
	walk = func(b *Box) {
		if !b.NoStartDecor || b.Kind == KText {
			for _, ss := range b.StringSets {
				p.StringSets[ss.Name] = append(p.StringSets[ss.Name], ss.Value)
			}
		}
		for _, ch := range b.Children {
			walk(ch)
		}
	}
	for _, ch := range page.Children {
		walk(ch)
	}
}

// ---------------------------------------------------------------- margin boxes

type marginEval struct {
	page       *Page
	total      int
	prevString map[string]string // last value before this page
	prevRun    map[string]*Box
}

func (c *Context) layoutMarginBoxes(pages []*Page) {
	lastString := map[string]string{}
	lastRunning := map[string]*Box{}
	for _, p := range pages {
		ev := &marginEval{page: p, total: len(pages), prevString: copyStrings(lastString), prevRun: copyRunning(lastRunning)}
		c.makeMarginBoxes(p, ev)
		for name, vals := range p.StringSets {
			if len(vals) > 0 {
				lastString[name] = vals[len(vals)-1]
			}
		}
		for name, boxes := range p.Running {
			if len(boxes) > 0 {
				lastRunning[name] = boxes[len(boxes)-1]
			}
		}
	}
}

func copyStrings(m map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range m {
		out[k] = v
	}
	return out
}

func copyRunning(m map[string]*Box) map[string]*Box {
	out := map[string]*Box{}
	for k, v := range m {
		out[k] = v
	}
	return out
}

func (ev *marginEval) stringValue(name, which string) string {
	vals := ev.page.StringSets[name]
	prev, hasPrev := ev.prevString[name]
	switch which {
	case "start":
		if len(vals) > 0 {
			// Approximation: the value assigned first on the page.
			if hasPrev {
				return prev
			}
			return vals[0]
		}
	case "last":
		if len(vals) > 0 {
			return vals[len(vals)-1]
		}
	case "first-except":
		if len(vals) > 0 {
			return ""
		}
	default: // first
		if len(vals) > 0 {
			return vals[0]
		}
	}
	return prev
}

func (ev *marginEval) runningElement(name, which string) *Box {
	boxes := ev.page.Running[name]
	switch which {
	case "last":
		if len(boxes) > 0 {
			return boxes[len(boxes)-1]
		}
	case "first-except":
		if len(boxes) > 0 {
			return nil
		}
	case "start":
		if r, ok := ev.prevRun[name]; ok {
			return r
		}
		if len(boxes) > 0 {
			return boxes[0]
		}
	default:
		if len(boxes) > 0 {
			return boxes[0]
		}
	}
	return ev.prevRun[name]
}

// marginContent builds the boxes of a margin box from its content.
func (c *Context) marginContent(style *css.Style, ev *marginEval) []*Box {
	items := style.Content()
	if items == nil {
		return nil
	}
	var out []*Box
	var buf strings.Builder
	flush := func() {
		if buf.Len() > 0 {
			if c.Builder != nil {
				out = append(out, c.Builder.textBoxes(buf.String(), style)...)
			}
			buf.Reset()
		}
	}
	counter := func(name string) []int {
		if name == "pages" {
			return []int{ev.total}
		}
		if v, ok := ev.page.Counters[name]; ok && len(v) > 0 {
			return v
		}
		return []int{0}
	}
	for _, it := range items {
		switch it.Kind {
		case "string":
			buf.WriteString(it.Str)
		case "counter":
			v := counter(it.Name)
			buf.WriteString(c.R.CounterStyles.Represent(v[len(v)-1], it.Style))
		case "counters":
			v := counter(it.Name)
			parts := make([]string, len(v))
			for i, x := range v {
				parts[i] = c.R.CounterStyles.Represent(x, it.Style)
			}
			buf.WriteString(strings.Join(parts, it.Str))
		case "string-ref":
			buf.WriteString(ev.stringValue(it.Name, it.Which))
		case "url":
			flush()
			if c.Images != nil {
				if im := c.Images.Get(it.Str); im != nil {
					out = append(out, &Box{Kind: KReplaced, Style: c.R.AnonymousStyle(style), Image: im, IsInline: true, Anonymous: true})
				}
			}
		case "element":
			flush()
			if rb := ev.runningElement(it.Name, it.Which); rb != nil {
				cp := deepCopy(rb)
				cp.RunningName = ""
				out = append(out, cp)
			}
		case "leader":
			flush()
			out = append(out, &Box{Kind: KLeader, Style: style, Leader: it.Str, Anonymous: true})
		}
	}
	flush()
	return out
}

func deepCopy(b *Box) *Box {
	c := *b
	c.Children = make([]*Box, len(b.Children))
	for i, ch := range b.Children {
		c.Children[i] = deepCopy(ch)
	}
	if b.Marker != nil {
		c.Marker = deepCopy(b.Marker)
	}
	return &c
}

type marginSpec struct {
	name  string
	style *css.Style
	boxes []*Box
}

func (c *Context) makeMarginBoxes(p *Page, ev *marginEval) {
	page := p.Box
	pageW, pageH := p.Width, p.Height
	specs := map[string]*marginSpec{}
	for _, name := range css.MarginBoxNames {
		st := c.R.MarginBoxStyle(p.Type, page.Style, name)
		if st == nil {
			continue
		}
		boxes := c.marginContent(st, ev)
		if boxes == nil {
			continue
		}
		specs[name] = &marginSpec{name, st, boxes}
	}
	if len(specs) == 0 {
		return
	}
	mt, mb, ml, mr := page.MT, page.MB, page.ML, page.MR
	areaX := ml
	areaW := pageW - ml - mr
	areaY := mt
	areaH := pageH - mt - mb
	type rect struct{ x, y, w, h float64 }
	rects := map[string]rect{}
	rects["top-left-corner"] = rect{0, 0, ml, mt}
	rects["top-right-corner"] = rect{pageW - mr, 0, mr, mt}
	rects["bottom-left-corner"] = rect{0, pageH - mb, ml, mb}
	rects["bottom-right-corner"] = rect{pageW - mr, pageH - mb, mr, mb}
	// Variable dimension rules.
	distribute := func(names [3]string, avail float64, vertical bool) [3][2]float64 {
		var sizes [3]float64
		var gen [3]bool
		for i, n := range names {
			if s, ok := specs[n]; ok {
				gen[i] = true
				if vertical {
					sizes[i] = c.marginBoxMaxHeight(s, rects, avail)
				} else {
					sizes[i] = c.marginBoxMaxWidth(s)
				}
				if l, ok := s.style.Get(css.PWidth).(css.Length); ok && !vertical {
					sizes[i] = l.Resolve(avail)
				}
				if l, ok := s.style.Get(css.PHeight).(css.Length); ok && vertical {
					sizes[i] = l.Resolve(avail)
				}
			}
		}
		var out [3][2]float64 // offset, size
		if gen[1] {
			side := math.Max(sizes[0], sizes[2])
			center := sizes[1]
			if center+2*side > avail {
				total := center + 2*side
				if total > 0 {
					center = avail * center / total
				}
			}
			center = math.Min(center, avail)
			sideW := (avail - center) / 2
			out[0] = [2]float64{0, sideW}
			out[1] = [2]float64{sideW, center}
			out[2] = [2]float64{sideW + center, sideW}
			return out
		}
		switch {
		case gen[0] && gen[2]:
			total := sizes[0] + sizes[2]
			a, b := avail/2, avail/2
			if total > 0 {
				a = avail * sizes[0] / total
				b = avail - a
			}
			out[0] = [2]float64{0, a}
			out[2] = [2]float64{a, b}
		case gen[0]:
			out[0] = [2]float64{0, avail}
		case gen[2]:
			out[2] = [2]float64{0, avail}
		}
		return out
	}
	top := distribute([3]string{"top-left", "top-center", "top-right"}, areaW, false)
	bottom := distribute([3]string{"bottom-left", "bottom-center", "bottom-right"}, areaW, false)
	for i, n := range []string{"top-left", "top-center", "top-right"} {
		rects[n] = rect{areaX + top[i][0], 0, top[i][1], mt}
	}
	for i, n := range []string{"bottom-left", "bottom-center", "bottom-right"} {
		rects[n] = rect{areaX + bottom[i][0], pageH - mb, bottom[i][1], mb}
	}
	left := distribute([3]string{"left-top", "left-middle", "left-bottom"}, areaH, true)
	right := distribute([3]string{"right-top", "right-middle", "right-bottom"}, areaH, true)
	for i, n := range []string{"left-top", "left-middle", "left-bottom"} {
		rects[n] = rect{0, areaY + left[i][0], ml, left[i][1]}
	}
	for i, n := range []string{"right-top", "right-middle", "right-bottom"} {
		rects[n] = rect{pageW - mr, areaY + right[i][0], mr, right[i][1]}
	}
	for _, name := range css.MarginBoxNames {
		s, ok := specs[name]
		if !ok {
			continue
		}
		r := rects[name]
		if r.w <= 0 || r.h <= 0 {
			continue
		}
		mb := &Box{Kind: KMargin, Style: s.style, Children: s.boxes, Pseudo: name}
		fixupBlockContainer(c.Builder, mb)
		resolvePercentages(mb, r.w)
		mb.ML, mb.MR, mb.MT, mb.MB = nanTo0(mb.ML), nanTo0(mb.MR), nanTo0(mb.MT), nanTo0(mb.MB)
		mb.X, mb.Y = r.x, r.y
		mb.W = math.Max(0, r.w-mb.ML-mb.MR-mb.PL-mb.PR-mb.BL-mb.BR)
		boxH := math.Max(0, r.h-mb.MT-mb.MB-mb.PT-mb.PB-mb.BT-mb.BB)
		mb.H = math.NaN()
		saved := c.saveBottom()
		c.PageBottom = math.Inf(1)
		c.createBFC()
		res := c.blockContainerLayout(mb, math.Inf(-1), nil, true, &absList{}, &absList{}, nil, false, cbInfo{W: r.w, H: r.h, Style: s.style})
		c.finishBFC(nil)
		c.restoreBottom(saved)
		nb := res.box
		if nb == nil {
			continue
		}
		contentH := nb.H
		nb.H = boxH
		var dy float64
		switch s.style.Kw(css.PVerticalAlign) {
		case "middle":
			dy = (boxH - contentH) / 2
		case "bottom":
			dy = boxH - contentH
		}
		if dy != 0 {
			for _, ch := range nb.Children {
				ch.Translate(0, dy)
			}
		}
		page.Children = append(page.Children, nb)
	}
}

func (c *Context) marginBoxMaxWidth(s *marginSpec) float64 {
	b := &Box{Kind: KMargin, Style: s.style, Children: s.boxes}
	tmp := &Box{Kind: KBlock, Style: s.style, Children: append([]*Box(nil), s.boxes...)}
	fixupBlockContainer(c.Builder, tmp)
	b.Children = tmp.Children
	return c.maxContent(b, true)
}

func (c *Context) marginBoxMaxHeight(s *marginSpec, _ any, avail float64) float64 {
	return avail / 3
}

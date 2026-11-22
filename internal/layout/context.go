package layout

import (
	"math"

	"github.com/esoteric-programmer/weasyprint-go/internal/css"
	"github.com/esoteric-programmer/weasyprint-go/internal/hyphen"
	"github.com/esoteric-programmer/weasyprint-go/internal/images"
	"github.com/esoteric-programmer/weasyprint-go/internal/text"
)

// Skip is a skip stack: where to resume layout. Keys are child indices
// (or encoded inline positions); a nil Skip means "from the start".
type Skip map[int]Skip

func skipFirst(s Skip) (int, Skip) {
	for k, v := range s {
		return k, v
	}
	return 0, nil
}

// Context holds the layout state.
type Context struct {
	Shaper  *text.Shaper
	R       *css.Resolver
	Images  *images.Cache
	Builder *Builder
	Warn    func(string)
	// DefaultPageSize is the page size used when @page size is auto
	// (A4 if zero).
	DefaultPageSize [2]float64
	// Hyphenation selects the hyphenation patterns of German and English.
	Hyphenation hyphen.Source
	// Hyphens holds user-defined hyphenation rules (nil: none).
	Hyphens *hyphen.Set

	// Block formatting contexts: stack of excluded shapes (floats).
	excluded    []*Box
	bfcStack    [][]*Box
	PageBottom  float64
	PageTop     float64 // top of the page content area
	PageIndex   int     // 0-based index of the page being laid out
	forcedBreak bool

	// Running elements and string sets seen so far, by name.
	Running        map[string][]*runningEntry
	pageRunning    map[string][]*Box
	StringSets     map[string][]string // values set on the current page
	LastStringSets map[string]string   // last value set before the current page

	fn footnotes

	// Line counters of the boxes limited by max-lines, and the last line
	// laid out.
	lineLimits []*int
	lastLine   *Box

	lineCache  map[*Box]*flatLine
	flClones   map[*Box]*Box
	strutCache map[*css.Style][2]float64
	tableCache map[*Box]*tableInfo
}

type runningEntry struct {
	box  *Box
	page int
}

// NewContext returns a layout context.
func NewContext(shaper *text.Shaper, r *css.Resolver, imgs *images.Cache, b *Builder) *Context {
	return &Context{
		Shaper: shaper, R: r, Images: imgs, Builder: b,
		Running: map[string][]*runningEntry{}, StringSets: map[string][]string{},
		LastStringSets: map[string]string{}, lineCache: map[*Box]*flatLine{}, flClones: map[*Box]*Box{},
		strutCache: map[*css.Style][2]float64{}, tableCache: map[*Box]*tableInfo{},
	}
}

func (c *Context) warn(s string) {
	if c.Warn != nil {
		c.Warn(s)
	}
}

func (c *Context) createBFC() {
	c.bfcStack = append(c.bfcStack, c.excluded)
	c.excluded = nil
}

func (c *Context) finishBFC(box *Box) {
	// Floats inside a BFC root extend its auto height.
	if box != nil && box.Style != nil && box.Style.IsAuto(css.PHeight) {
		bottom := box.ContentY() + box.H
		for _, f := range c.excluded {
			fb := f.Y + f.MarginHeight()
			if fb > bottom {
				bottom = fb
			}
		}
		box.H = math.Max(box.H, bottom-box.ContentY())
	}
	if n := len(c.bfcStack); n > 0 {
		c.excluded = c.bfcStack[n-1]
		c.bfcStack = c.bfcStack[:n-1]
	} else {
		c.excluded = nil
	}
}

func (c *Context) overflowsPage(bottomSpace, y float64) bool {
	return y > c.PageBottom-bottomSpace+1e-6
}

// ---------------------------------------------------------------- helpers

// cbInfo is a containing block.
type cbInfo struct {
	W, H  float64
	HAuto bool
	Style *css.Style
	Root  bool
}

func lengthOrAuto(s *css.Style, id css.PropID, base float64) (float64, bool) {
	switch v := s.Get(id).(type) {
	case css.Length:
		return v.Resolve(base), true
	}
	return 0, false
}

// resolvePercentages sets margins, paddings and borders from the style.
// Auto margins are returned as NaN.
func resolvePercentages(b *Box, cbWidth float64) {
	s := b.Style
	get := func(id css.PropID) float64 {
		if v, ok := lengthOrAuto(s, id, cbWidth); ok {
			return v
		}
		return math.NaN()
	}
	b.MT, b.MR, b.MB, b.ML = get(css.PMarginTop), get(css.PMarginRight), get(css.PMarginBottom), get(css.PMarginLeft)
	b.PT, b.PR, b.PB, b.PL = s.Px(css.PPaddingTop, cbWidth), s.Px(css.PPaddingRight, cbWidth), s.Px(css.PPaddingBottom, cbWidth), s.Px(css.PPaddingLeft, cbWidth)
	b.BT, b.BR, b.BB, b.BL = s.BorderWidth(0), s.BorderWidth(1), s.BorderWidth(2), s.BorderWidth(3)
	if b.Kind == KTable && b.Collapse != nil {
		b.BT, b.BR, b.BB, b.BL = b.Collapse.tableBorders()
	}
	if b.Kind == KCell && b.Collapse != nil {
		b.BT, b.BR, b.BB, b.BL = b.Collapse.cellBorders(b)
	}
	if b.NoStartDecor && b.Kind != KInline {
		b.MT, b.PT, b.BT = 0, 0, 0
	}
	if b.NoEndDecor && b.Kind != KInline {
		b.MB, b.PB, b.BB = 0, 0, 0
	}
	if b.Kind == KInline {
		if b.NoStartDecor {
			b.ML, b.PL, b.BL = 0, 0, 0
		}
		if b.NoEndDecor {
			b.MR, b.PR, b.BR = 0, 0, 0
		}
	}
}

func nanTo0(f float64) float64 {
	if math.IsNaN(f) {
		return 0
	}
	return f
}

// boxSizingAdjust returns the amount to subtract from a specified width
// to get the content width.
func boxSizingWidth(b *Box) float64 {
	switch b.Style.Kw(css.PBoxSizing) {
	case "border-box":
		return b.PL + b.PR + b.BL + b.BR
	case "padding-box":
		return b.PL + b.PR
	}
	return 0
}

func boxSizingHeight(b *Box) float64 {
	switch b.Style.Kw(css.PBoxSizing) {
	case "border-box":
		return b.PT + b.PB + b.BT + b.BB
	case "padding-box":
		return b.PT + b.PB
	}
	return 0
}

// specifiedWidth returns the content width from the width property, or
// NaN for auto.
func specifiedWidth(b *Box, cbWidth float64) float64 {
	if v, ok := lengthOrAuto(b.Style, css.PWidth, cbWidth); ok {
		return math.Max(0, v-boxSizingWidth(b))
	}
	return math.NaN()
}

// specifiedHeight returns the content height, or NaN for auto (also for
// percentages against an auto containing block height).
func specifiedHeight(b *Box, cb cbInfo) float64 {
	l, ok := b.Style.Get(css.PHeight).(css.Length)
	if !ok {
		return math.NaN()
	}
	if l.IsPercent() && cb.HAuto {
		return math.NaN()
	}
	return math.Max(0, l.Resolve(cb.H)-boxSizingHeight(b))
}

func minMaxWidth(b *Box, cbWidth float64) (float64, float64) {
	min := 0.0
	if v, ok := lengthOrAuto(b.Style, css.PMinWidth, cbWidth); ok {
		min = math.Max(0, v-boxSizingWidth(b))
	}
	max := math.Inf(1)
	if v, ok := lengthOrAuto(b.Style, css.PMaxWidth, cbWidth); ok {
		max = math.Max(0, v-boxSizingWidth(b))
	}
	return min, max
}

func minMaxHeight(b *Box, cb cbInfo) (float64, float64) {
	min := 0.0
	if l, ok := b.Style.Get(css.PMinHeight).(css.Length); ok && !(l.IsPercent() && cb.HAuto) {
		min = math.Max(0, l.Resolve(cb.H)-boxSizingHeight(b))
	}
	max := math.Inf(1)
	if l, ok := b.Style.Get(css.PMaxHeight).(css.Length); ok && !(l.IsPercent() && cb.HAuto) {
		max = math.Max(0, l.Resolve(cb.H)-boxSizingHeight(b))
	}
	return min, max
}

func collapseMargins(ms []float64) float64 {
	pos, neg := 0.0, 0.0
	for _, m := range ms {
		if m > pos {
			pos = m
		}
		if m < neg {
			neg = m
		}
	}
	return pos + neg
}

// pushLineLimit starts counting lines for a box with max-lines (which only
// applies with continue: discard) and returns the function restoring the
// previous counters.
func (c *Context) pushLineLimit(box *Box) func() {
	saved := c.lineLimits
	n, ok := box.Style.Get(css.PMaxLines).(css.Number)
	if !ok || n < 1 || box.Style.Kw(css.PContinue) != "discard" {
		return func() { c.lineLimits = saved }
	}
	v := int(n)
	c.lineLimits = append(saved[:len(saved):len(saved)], &v)
	return func() { c.lineLimits = saved }
}

// lineBarrier stops the max-lines counters of ancestors from applying to
// independent layouts (floats, tables, flex items…).
func (c *Context) lineBarrier() func() {
	saved := c.lineLimits
	c.lineLimits = nil
	return func() { c.lineLimits = saved }
}

// linesExhausted reports whether a max-lines limit has been reached.
func (c *Context) linesExhausted() bool {
	for _, n := range c.lineLimits {
		if *n <= 0 {
			return true
		}
	}
	return false
}

// countLine counts a line laid out.
func (c *Context) countLine(line *Box) {
	for _, n := range c.lineLimits {
		*n--
	}
	if len(c.lineLimits) > 0 {
		c.lastLine = line
	}
}

// setBlockEllipsis marks the text ending a line to be followed by its
// block-ellipsis.
func (c *Context) setBlockEllipsis(line *Box) {
	if line == nil || line.Kind != KLine {
		return
	}
	var e string
	switch v := line.Style.Get(css.PBlockEllipsis).(type) {
	case css.Str:
		e = string(v)
	case css.Keyword:
		if v != "auto" {
			return
		}
		e = "…"
	default:
		return
	}
	b := line
	for len(b.Children) > 0 {
		last := b.Children[len(b.Children)-1]
		switch last.Kind {
		case KText:
			last.BlockEllipsis = e
			last.EllipsisRight = line.LineRight
			return
		case KInline:
			b = last
			continue
		}
		return
	}
}

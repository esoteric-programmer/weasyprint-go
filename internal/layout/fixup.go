package layout

import (
	"strings"

	"github.com/esoteric-programmer/weasyprint-go/internal/css"
)

// fixup applies anonymous box generation rules to a freshly built box and
// returns the box that replaces it (a table becomes a table wrapper).
func (b *Builder) fixup(box *Box) *Box {
	if box.Kind != KTable && box.Kind != KRowGroup && box.Kind != KRow {
		b.wrapOrphanTableParts(box)
	}
	switch box.Kind {
	case KTable:
		return b.fixupTable(box)
	case KRowGroup:
		b.fixupRowGroup(box)
	case KRow:
		b.fixupRow(box)
	case KFlex:
		b.fixupFlex(box)
	case KInline:
		// Handled by the parent block container.
	default:
		if box.IsBlockContainer() {
			fixupBlockContainer(b, box)
		}
	}
	return box
}

func isWhitespaceText(box *Box) bool {
	if box.Kind != KText || box.Deferred != nil {
		return false
	}
	if box.Style != nil && !isCollapsible(box.Style.WhiteSpace()) {
		return false
	}
	return strings.Trim(box.Text, " \n\t") == ""
}

func containsBlockLevel(box *Box) bool {
	for _, c := range box.Children {
		if c.IsBlockLevel() && c.IsInNormalFlow() {
			return true
		}
		if c.Kind == KInline && containsBlockLevel(c) {
			return true
		}
	}
	return false
}

// splitInline splits an inline box containing block-level boxes.
func splitInline(box *Box) []*Box {
	var out []*Box
	var cur []*Box
	var inlines []*Box
	flush := func() {
		c := box.CopyWith(cur)
		c.Orig = nil
		inlines = append(inlines, c)
		out = append(out, c)
		cur = nil
	}
	for _, c := range box.Children {
		if c.Kind == KInline && containsBlockLevel(c) {
			for _, p := range splitInline(c) {
				if p.IsBlockLevel() && p.IsInNormalFlow() {
					flush()
					out = append(out, p)
				} else {
					cur = append(cur, p)
				}
			}
		} else if c.IsBlockLevel() && c.IsInNormalFlow() {
			flush()
			out = append(out, c)
		} else {
			cur = append(cur, c)
		}
	}
	flush()
	for i, in := range inlines {
		if i > 0 {
			in.NoStartDecor = true
			in.Anchor = ""
			in.BookmarkLevel = 0
			in.StringSets = nil
		}
		if i < len(inlines)-1 {
			in.NoEndDecor = true
		}
	}
	return out
}

func fixupBlockContainer(b *Builder, box *Box) {
	// Block-in-inline splitting.
	var children []*Box
	changed := false
	for _, c := range box.Children {
		if c.Kind == KInline && containsBlockLevel(c) {
			children = append(children, splitInline(c)...)
			changed = true
		} else {
			children = append(children, c)
		}
	}
	if changed {
		box.Children = children
	}
	hasBlock := false
	for _, c := range box.Children {
		if c.IsBlockLevel() && c.IsInNormalFlow() {
			hasBlock = true
			break
		}
	}
	if !hasBlock {
		if len(box.Children) == 0 {
			return
		}
		hasContent := false
		for _, c := range box.Children {
			if !isWhitespaceText(c) && c.IsInNormalFlow() {
				hasContent = true
			}
		}
		if !hasContent {
			// Only whitespace and out-of-flow boxes.
			var kept []*Box
			for _, c := range box.Children {
				if !isWhitespaceText(c) {
					kept = append(kept, c)
				}
			}
			allOOF := true
			for _, c := range kept {
				if c.IsInlineLevel() && c.IsInNormalFlow() {
					allOOF = false
				}
			}
			if allOOF {
				box.Children = kept
				if len(kept) == 0 {
					return
				}
				// Keep floats in a line so they are laid out in order.
			}
		}
		line := &Box{Kind: KLine, Style: box.Style, Element: box.Element, Children: box.Children, Anonymous: true}
		collapseLineSpaces(line)
		box.Children = []*Box{line}
		return
	}
	var out []*Box
	var run []*Box
	flush := func() {
		if len(run) == 0 {
			return
		}
		content := false
		for _, c := range run {
			if c.IsInNormalFlow() && c.RunningName == "" && !isWhitespaceText(c) {
				content = true
				break
			}
		}
		if content {
			var style *css.Style
			if b != nil {
				style = b.R.AnonymousStyle(box.Style)
			} else {
				style = box.Style
			}
			anon := &Box{Kind: KBlock, Style: style, Element: box.Element, Anonymous: true, Children: run}
			line := &Box{Kind: KLine, Style: style, Element: box.Element, Children: run, Anonymous: true}
			collapseLineSpaces(line)
			anon.Children = []*Box{line}
			out = append(out, anon)
		} else {
			for _, c := range run {
				if !isWhitespaceText(c) {
					out = append(out, c)
				}
			}
		}
		run = nil
	}
	for _, c := range box.Children {
		if c.IsBlockLevel() && c.IsInNormalFlow() {
			flush()
			out = append(out, c)
		} else {
			run = append(run, c)
		}
	}
	flush()
	box.Children = out
}

// collapseLineSpaces removes collapsible spaces following other collapsible
// spaces across inline box boundaries.
func collapseLineSpaces(line *Box) {
	prevSpace := true // spaces at the start of a line are removed later anyway
	var walk func(parent *Box)
	walk = func(parent *Box) {
		kept := parent.Children[:0:0]
		for _, c := range parent.Children {
			switch {
			case c.Kind == KText && c.Deferred == nil:
				if c.Style != nil && isCollapsible(c.Style.WhiteSpace()) {
					if prevSpace && strings.HasPrefix(c.Text, " ") {
						c.Text = c.Text[1:]
					}
					if c.Text == "" {
						continue
					}
					prevSpace = strings.HasSuffix(c.Text, " ") || strings.HasSuffix(c.Text, "\n")
				} else if c.Text != "" {
					prevSpace = false
				}
			case c.Kind == KInline:
				walk(c)
			case c.IsInNormalFlow():
				prevSpace = false
			}
			kept = append(kept, c)
		}
		parent.Children = kept
	}
	walk(line)
}

// ---------------------------------------------------------------- tables

func isProperTableChild(c *Box) bool {
	switch c.Kind {
	case KRowGroup, KRow, KColGroup, KCol, KCaption, KCell:
		return true
	}
	return false
}

func (b *Builder) anon(parent *Box, display string, kind Kind, children []*Box) *Box {
	st := b.R.AnonymousStyleWith(parent.Style, map[css.PropID]css.Value{css.PDisplay: css.Keyword(display)})
	box := &Box{Kind: kind, Style: st, Element: parent.Element, Anonymous: true, Children: children}
	if kind == KCell {
		box.Colspan, box.Rowspan = 1, 1
		fixupBlockContainer(b, box)
	}
	return box
}

// wrapOrphanTableParts wraps misparented table-internal boxes in
// anonymous tables.
func (b *Builder) wrapOrphanTableParts(box *Box) {
	has := false
	for _, c := range box.Children {
		if c.IsTablePart() {
			has = true
			break
		}
	}
	if !has {
		return
	}
	inline := box.Kind == KInline || box.Kind == KLine
	var out []*Box
	var run []*Box
	flush := func() {
		if len(run) == 0 {
			return
		}
		display := "table"
		if inline {
			display = "inline-table"
		}
		t := b.anon(box, display, KTable, run)
		t.IsInline = inline
		out = append(out, b.fixupTable(t))
		run = nil
	}
	for _, c := range box.Children {
		if c.IsTablePart() {
			run = append(run, c)
		} else if isWhitespaceText(c) && len(run) > 0 {
			continue
		} else {
			flush()
			out = append(out, c)
		}
	}
	flush()
	box.Children = out
}

func (b *Builder) fixupRow(row *Box) {
	var out []*Box
	var run []*Box
	flush := func() {
		if len(run) == 0 {
			return
		}
		content := false
		for _, c := range run {
			if !isWhitespaceText(c) {
				content = true
			}
		}
		if content {
			out = append(out, b.anon(row, "table-cell", KCell, run))
		}
		run = nil
	}
	for _, c := range row.Children {
		if c.Kind == KCell {
			flush()
			out = append(out, c)
		} else {
			run = append(run, c)
		}
	}
	flush()
	row.Children = out
}

func (b *Builder) fixupRowGroup(g *Box) {
	var out []*Box
	var run []*Box
	flush := func() {
		if len(run) == 0 {
			return
		}
		content := false
		for _, c := range run {
			if !isWhitespaceText(c) {
				content = true
			}
		}
		if content {
			r := b.anon(g, "table-row", KRow, run)
			b.fixupRow(r)
			out = append(out, r)
		}
		run = nil
	}
	for _, c := range g.Children {
		if c.Kind == KRow {
			flush()
			out = append(out, c)
		} else {
			run = append(run, c)
		}
	}
	flush()
	g.Children = out
}

func (b *Builder) fixupTable(t *Box) *Box {
	var captions, groups, cols []*Box
	var run []*Box
	flush := func() {
		if len(run) == 0 {
			return
		}
		content := false
		for _, c := range run {
			if !isWhitespaceText(c) {
				content = true
			}
		}
		if content {
			g := b.anon(t, "table-row-group", KRowGroup, run)
			b.fixupRowGroup(g)
			groups = append(groups, g)
		}
		run = nil
	}
	var colRun []*Box
	flushCols := func() {
		if len(colRun) > 0 {
			cg := b.anon(t, "table-column-group", KColGroup, colRun)
			cols = append(cols, cg)
			colRun = nil
		}
	}
	for _, c := range t.Children {
		switch c.Kind {
		case KCaption:
			flush()
			captions = append(captions, c)
		case KRowGroup:
			flush()
			groups = append(groups, c)
		case KColGroup:
			flush()
			flushCols()
			cols = append(cols, c)
		case KCol:
			flush()
			colRun = append(colRun, c)
		default:
			run = append(run, c)
		}
	}
	flush()
	flushCols()
	// Only the first header and footer groups repeat; move them to the
	// start and the end of the table.
	var header, footer *Box
	var bodies []*Box
	for _, g := range groups {
		switch {
		case g.IsHeader && header == nil:
			header = g
		case g.IsFooter && footer == nil:
			footer = g
		default:
			g.IsHeader, g.IsFooter = false, false
			bodies = append(bodies, g)
		}
	}
	groups = nil
	if header != nil {
		groups = append(groups, header)
	}
	groups = append(groups, bodies...)
	if footer != nil {
		groups = append(groups, footer)
	}
	// Column groups without columns get anonymous columns.
	for _, cg := range cols {
		if len(cg.Children) == 0 {
			span := cg.Span
			if span < 1 {
				span = 1
			}
			for i := 0; i < span; i++ {
				cg.Children = append(cg.Children, &Box{Kind: KCol, Style: cg.Style, Anonymous: true, Span: 1})
			}
		}
	}
	t.Children = append(cols, groups...)

	// Wrapper.
	ws := t.Style.Copy()
	for _, id := range []css.PropID{css.PBorderTopWidth, css.PBorderRightWidth, css.PBorderBottomWidth, css.PBorderLeftWidth,
		css.PPaddingTop, css.PPaddingRight, css.PPaddingBottom, css.PPaddingLeft} {
		ws.Set(id, css.Px(0))
	}
	ws.Set(css.PBackgroundColor, css.Transparent)
	ws.Set(css.PBackgroundImage, []css.Value{css.Keyword("none")})
	ws.Set(css.PBoxShadow, css.Keyword("none"))
	ws.Set(css.POutlineWidth, css.Px(0))
	ws.Set(css.PWidth, css.Keyword("auto"))
	ws.Set(css.PHeight, css.Keyword("auto"))
	ws.Set(css.PMinWidth, css.Keyword("auto"))
	ws.Set(css.PMaxWidth, css.Keyword("none"))
	ws.Set(css.POverflow, css.Keyword("visible"))
	ws.Set(css.POpacity, css.Number(1))
	ws.Set(css.PTransform, css.Keyword("none"))
	ts := t.Style.Copy()
	for _, id := range []css.PropID{css.PMarginTop, css.PMarginRight, css.PMarginBottom, css.PMarginLeft} {
		ts.Set(id, css.Px(0))
	}
	ts.Set(css.PPosition, css.Keyword("static"))
	ts.Set(css.PFloat, css.Keyword("none"))
	ts.Set(css.PClear, css.Keyword("none"))
	for _, id := range []css.PropID{css.PTop, css.PRight, css.PBottom, css.PLeft} {
		ts.Set(id, css.Keyword("auto"))
	}
	ts.Set(css.PBreakBefore, css.Keyword("auto"))
	ts.Set(css.PBreakAfter, css.Keyword("auto"))
	ts.Set(css.PTransform, css.Keyword("none"))
	t.Style = ts
	var top, bottom []*Box
	for _, c := range captions {
		if c.Style.Kw(css.PCaptionSide) == "bottom" {
			bottom = append(bottom, c)
		} else {
			top = append(top, c)
		}
	}
	wrapper := &Box{Kind: KTableWrapper, Style: ws, Element: t.Element, IsInline: t.IsInline, Anonymous: true}
	wrapper.Children = append(append(append([]*Box{}, top...), t), bottom...)
	wrapper.RunningName = t.RunningName
	t.RunningName = ""
	wrapper.StringSets = t.StringSets
	wrapper.BookmarkLevel, wrapper.BookmarkLabel, wrapper.BookmarkOpen = t.BookmarkLevel, t.BookmarkLabel, t.BookmarkOpen
	wrapper.Anchor = t.Anchor
	t.StringSets, t.BookmarkLevel, t.Anchor = nil, 0, ""
	return wrapper
}

// ---------------------------------------------------------------- flex

func (b *Builder) fixupFlex(box *Box) {
	var out []*Box
	var run []*Box
	flush := func() {
		if len(run) == 0 {
			return
		}
		content := false
		for _, c := range run {
			if !isWhitespaceText(c) {
				content = true
			}
		}
		if content {
			anon := &Box{Kind: KBlock, Style: b.R.AnonymousStyle(box.Style), Element: box.Element, Anonymous: true, Children: run}
			fixupBlockContainer(b, anon)
			out = append(out, anon)
		}
		run = nil
	}
	for _, c := range box.Children {
		if c.Kind == KText || c.Kind == KLeader {
			run = append(run, c)
			continue
		}
		flush()
		if c.Kind == KInline {
			// Blockified inline element.
			c.Kind = KBlock
			fixupBlockContainer(b, c)
		}
		if c.IsInline && (c.Kind == KReplaced || c.Kind == KTableWrapper || c.Kind == KFlex) {
			c.IsInline = false
		}
		if c.Kind == KInlineBlock {
			c.Kind = KBlock
		}
		out = append(out, c)
	}
	flush()
	box.Children = out
}

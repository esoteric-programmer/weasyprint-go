package layout

import (
	"strconv"
	"strings"
	"unicode"

	"github.com/esoteric-programmer/weasyprint-go/internal/css"
	"github.com/esoteric-programmer/weasyprint-go/internal/fetch"
	"github.com/esoteric-programmer/weasyprint-go/internal/html"
	"github.com/esoteric-programmer/weasyprint-go/internal/images"
)

// Builder builds the box tree.
type Builder struct {
	R       *css.Resolver
	Images  *images.Cache
	BaseURL string
	Warn    func(string)
	// TargetValues gives counter values of anchors for target-counter(),
	// filled after a first layout pass.
	TargetValues map[string]map[string][]int
	// AnchorCounters records counter values at each anchor during build.
	AnchorCounters map[string]map[string][]int
	// UsesTargets is set if target-counter()/target-text() is used.
	UsesTargets bool
	AnchorText  map[string]map[string]string

	counters   map[string][]int
	scopes     []map[string]bool
	quoteDepth int
}

// NewBuilder returns a box tree builder.
func NewBuilder(r *css.Resolver, imgs *images.Cache, baseURL string) *Builder {
	return &Builder{R: r, Images: imgs, BaseURL: baseURL,
		AnchorCounters: map[string]map[string][]int{}, AnchorText: map[string]map[string]string{}}
}

// Build builds the box tree of a document and returns the root box.
func (b *Builder) Build(doc *html.Node) *Box {
	b.counters = map[string][]int{}
	b.scopes = []map[string]bool{{}}
	b.quoteDepth = 0
	root := doc.Root()
	if root == nil {
		return nil
	}
	boxes := b.element(root, nil)
	if len(boxes) == 0 {
		return &Box{Kind: KBlock, Style: b.R.RootStyle(), Element: root}
	}
	r := boxes[0]
	if r.Kind != KBlock && r.Kind != KFlex && r.Kind != KTableWrapper {
		r = &Box{Kind: KBlock, Style: r.Style, Element: root, Children: boxes}
	}
	fixupBlockContainer(b, r)
	return r
}

func (b *Builder) warn(s string) {
	if b.Warn != nil {
		b.Warn(s)
	}
}

// ---------------------------------------------------------------- counters

func (b *Builder) updateCounters(style *css.Style) {
	scope := b.scopes[len(b.scopes)-1]
	for _, op := range style.Counters(css.PCounterReset) {
		if scope[op.Name] {
			vals := b.counters[op.Name]
			vals[len(vals)-1] = op.Value
		} else {
			b.counters[op.Name] = append(b.counters[op.Name], op.Value)
			scope[op.Name] = true
		}
	}
	for _, op := range style.Counters(css.PCounterSet) {
		vals := b.counters[op.Name]
		if len(vals) == 0 {
			b.counters[op.Name] = []int{op.Value}
			scope[op.Name] = true
		} else {
			vals[len(vals)-1] = op.Value
		}
	}
	incs := style.Counters(css.PCounterIncrement)
	hasListItem := false
	for _, op := range incs {
		if op.Name == "list-item" {
			hasListItem = true
		}
	}
	if (style.Display() == "list-item" || style.Display() == "inline-list-item") && !hasListItem {
		incs = append(incs, css.CounterOp{Name: "list-item", Value: 1})
	}
	for _, op := range incs {
		vals := b.counters[op.Name]
		if len(vals) == 0 {
			b.counters[op.Name] = []int{0}
			scope[op.Name] = true
			vals = b.counters[op.Name]
		}
		vals[len(vals)-1] += op.Value
	}
}

func (b *Builder) pushScope() { b.scopes = append(b.scopes, map[string]bool{}) }

func (b *Builder) popScope() {
	scope := b.scopes[len(b.scopes)-1]
	b.scopes = b.scopes[:len(b.scopes)-1]
	for name := range scope {
		vals := b.counters[name]
		if len(vals) > 0 {
			b.counters[name] = vals[:len(vals)-1]
		}
		if len(b.counters[name]) == 0 {
			delete(b.counters, name)
		}
	}
}

func (b *Builder) counterValue(name string) int {
	if vals := b.counters[name]; len(vals) > 0 {
		return vals[len(vals)-1]
	}
	return 0
}

func (b *Builder) snapshotCounters() map[string][]int {
	out := map[string][]int{}
	for k, v := range b.counters {
		out[k] = append([]int(nil), v...)
	}
	return out
}

// ---------------------------------------------------------------- elements

func (b *Builder) element(e *html.Node, parentStyle *css.Style) []*Box {
	style := b.R.Style(e)
	if style == nil || style.Display() == "none" {
		return nil
	}
	// Counters.
	b.updateCounters(style)
	if a := style.Anchor(); a != "" {
		if _, ok := b.AnchorCounters[a]; !ok {
			b.AnchorCounters[a] = b.snapshotCounters()
		}
	}

	if style.Display() == "contents" {
		b.pushScope()
		var out []*Box
		out = append(out, b.pseudoBox(e, style, "before")...)
		out = append(out, b.children(e, style)...)
		out = append(out, b.pseudoBox(e, style, "after")...)
		b.popScope()
		return out
	}

	box := b.newBox(e, style, "")

	// Replaced elements.
	switch e.Tag {
	case "img":
		if r := b.replacedFor(e, style, e.Get("src")); r != nil {
			b.navigation(r, style, e)
			return []*Box{r}
		}
		alt := e.Get("alt")
		if alt == "" {
			return nil
		}
		box.Kind = KInline
		if style.Display() != "inline" {
			box.Kind = displayKind(style.Display())
		}
		box.Children = b.textBoxes(alt, style)
		b.navigation(box, style, e)
		return []*Box{box}
	case "embed":
		if r := b.replacedFor(e, style, e.Get("src")); r != nil {
			return []*Box{r}
		}
		return nil
	case "object":
		if r := b.replacedFor(e, style, e.Get("data")); r != nil {
			return []*Box{r}
		}
	case "svg":
		if im := b.inlineSVG(e); im != nil {
			r := b.newBox(e, style, "")
			r.Kind = KReplaced
			r.Image = im
			r.IsInline = !isBlockDisplay(style.Display())
			b.navigation(r, style, e)
			return []*Box{r}
		}
		return nil
	case "math", "video", "audio", "canvas", "iframe":
		if e.Tag != "iframe" {
			// Not rendered beyond their fallback content.
		}
	}

	b.pushScope()
	var children []*Box
	if style.Display() == "list-item" || style.Display() == "inline-list-item" {
		if m := b.markerBox(e, style); m != nil {
			if style.Kw(css.PListStylePosition) == "inside" || box.Kind != KBlock {
				children = append(children, m)
			} else {
				box.Marker = m
			}
		}
	}
	if items := style.Content(); items != nil && e.Tag != "br" {
		// The content property replaces the element's children.
		children = append(children, b.pseudoBox(e, style, "before")...)
		children = append(children, b.contentBoxes(style, items, e)...)
		children = append(children, b.pseudoBox(e, style, "after")...)
	} else if style.Kw(css.PContent) == "none" && e.Tag != "br" {
	} else {
		children = append(children, b.pseudoBox(e, style, "before")...)
		children = append(children, b.children(e, style)...)
		children = append(children, b.pseudoBox(e, style, "after")...)
	}
	b.popScope()
	box.Children = children

	// HTML specifics.
	switch e.Tag {
	case "td", "th":
		box.Colspan = atoiDefault(e.Get("colspan"), 1)
		if box.Colspan < 1 || box.Colspan > 1000 {
			box.Colspan = 1
		}
		box.Rowspan = atoiDefault(e.Get("rowspan"), 1)
		if box.Rowspan < 0 || box.Rowspan > 65534 {
			box.Rowspan = 1
		}
	case "col", "colgroup":
		box.Span = atoiDefault(e.Get("span"), 1)
		if box.Span < 1 || box.Span > 1000 {
			box.Span = 1
		}
	}
	if box.Kind == KCell || box.Kind == KCaption {
		if box.Colspan == 0 {
			box.Colspan = 1
		}
		if box.Rowspan == 0 && box.Kind == KCell && e.Get("rowspan") == "" {
			box.Rowspan = 1
		}
	}
	b.navigation(box, style, e)

	if style.Position() == "running" {
		box.RunningName = style.RunningName()
	}
	return []*Box{b.fixup(box)}
}

func atoiDefault(s string, d int) int {
	s = strings.TrimSpace(s)
	if s == "" {
		return d
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		// Leading digits.
		i := 0
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
		}
		if i == 0 {
			return d
		}
		n, _ = strconv.Atoi(s[:i])
	}
	return n
}

func isBlockDisplay(d string) bool {
	switch d {
	case "block", "list-item", "flow-root", "table", "flex", "grid":
		return true
	}
	return false
}

func displayKind(d string) Kind {
	switch d {
	case "inline", "inline-list-item":
		return KInline
	case "inline-block":
		return KInlineBlock
	case "table", "inline-table":
		return KTable
	case "table-row-group", "table-header-group", "table-footer-group":
		return KRowGroup
	case "table-row":
		return KRow
	case "table-cell":
		return KCell
	case "table-column-group":
		return KColGroup
	case "table-column":
		return KCol
	case "table-caption":
		return KCaption
	case "flex", "inline-flex":
		return KFlex
	}
	return KBlock
}

func (b *Builder) newBox(e *html.Node, style *css.Style, pseudo string) *Box {
	d := style.Display()
	box := &Box{Kind: displayKind(d), Style: style, Element: e, Pseudo: pseudo}
	switch d {
	case "inline-table", "inline-flex", "inline-grid":
		box.IsInline = true
	case "table-header-group":
		box.IsHeader = true
	case "table-footer-group":
		box.IsFooter = true
	}
	if d == "inline-grid" {
		box.Kind = KInlineBlock
		box.IsInline = false
	}
	return box
}

func (b *Builder) replacedFor(e *html.Node, style *css.Style, src string) *Box {
	src = strings.TrimSpace(src)
	if src == "" || b.Images == nil {
		return nil
	}
	url := fetch.Resolve(b.BaseURL, src)
	im := b.Images.Get(url)
	if im == nil {
		return nil
	}
	r := b.newBox(e, style, "")
	r.Kind = KReplaced
	r.Image = im
	r.IsInline = !isBlockDisplay(style.Display())
	r.Children = nil
	return r
}

func (b *Builder) navigation(box *Box, style *css.Style, e *html.Node) {
	box.Link = style.Link()
	box.Anchor = style.Anchor()
	if lvl := style.BookmarkLevel(); lvl > 0 {
		box.BookmarkLevel = lvl
		box.BookmarkOpen = style.Kw(css.PBookmarkState) != "closed"
		if items, ok := style.Get(css.PBookmarkLabel).([]css.ContentItem); ok {
			box.BookmarkLabel = strings.TrimSpace(collapseSpaces(b.contentText(items, box, style, e)))
		}
	}
	if sets, ok := style.Get(css.PStringSet).([]css.StringSet); ok {
		for _, ss := range sets {
			box.StringSets = append(box.StringSets, StringSetValue{ss.Name, b.contentText(ss.Content, box, style, e)})
		}
	}
}

func collapseSpaces(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// contentText evaluates content items to a string (for bookmark-label and
// string-set).
func (b *Builder) contentText(items []css.ContentItem, box *Box, style *css.Style, e *html.Node) string {
	var sb strings.Builder
	for _, it := range items {
		switch it.Kind {
		case "string":
			sb.WriteString(it.Str)
		case "counter":
			sb.WriteString(b.R.CounterStyles.Represent(b.counterValue(it.Name), it.Style))
		case "counters":
			vals := b.counters[it.Name]
			if len(vals) == 0 {
				vals = []int{0}
			}
			parts := make([]string, len(vals))
			for i, v := range vals {
				parts[i] = b.R.CounterStyles.Represent(v, it.Style)
			}
			sb.WriteString(strings.Join(parts, it.Str))
		case "content":
			switch it.Which {
			case "before", "after":
				for _, c := range box.Children {
					if c.Pseudo == it.Which {
						sb.WriteString(c.AllText())
					}
				}
			case "first-letter":
				t := strings.TrimSpace(boxText(box))
				if t != "" {
					sb.WriteString(string([]rune(t)[:1]))
				}
			case "marker":
				if box.Marker != nil {
					sb.WriteString(box.Marker.AllText())
				}
			default:
				sb.WriteString(boxText(box))
			}
		case "attr":
			sb.WriteString(e.Get(it.Name))
		case "open-quote", "close-quote":
		}
	}
	return sb.String()
}

// boxText returns the text of a box excluding ::before/::after/markers.
func boxText(box *Box) string {
	var sb strings.Builder
	var walk func(*Box)
	walk = func(x *Box) {
		if x.Kind == KText {
			sb.WriteString(x.AllText())
			return
		}
		for _, c := range x.Children {
			if c.Pseudo == "before" || c.Pseudo == "after" || c.Pseudo == "marker" {
				continue
			}
			walk(c)
		}
	}
	walk(box)
	return sb.String()
}

func (b *Builder) children(e *html.Node, style *css.Style) []*Box {
	var out []*Box
	for c := e.FirstChild; c != nil; c = c.NextSibling {
		switch c.Type {
		case html.TextNode:
			out = append(out, b.textBoxes(c.Data, style)...)
		case html.ElementNode:
			out = append(out, b.element(c, style)...)
		}
	}
	return out
}

func (b *Builder) pseudoBox(e *html.Node, parent *css.Style, pseudo string) []*Box {
	style := b.R.PseudoStyle(e, pseudo)
	if style == nil || style.Display() == "none" {
		return nil
	}
	items := style.Content()
	if items == nil {
		return nil
	}
	b.updateCounters(style)
	box := b.newBox(e, style, pseudo)
	b.pushScope()
	box.Children = b.contentBoxes(style, items, e)
	b.popScope()
	b.navigation(box, style, e)
	if style.Position() == "running" {
		box.RunningName = style.RunningName()
	}
	return []*Box{b.fixup(box)}
}

func (b *Builder) markerBox(e *html.Node, liStyle *css.Style) *Box {
	style := b.R.MarkerStyle(e)
	if style == nil || style.Display() == "none" {
		return nil
	}
	var children []*Box
	if items := style.Content(); items != nil {
		children = b.contentBoxes(style, items, e)
	} else if style.Kw(css.PContent) == "none" {
		return nil
	} else {
		if u, ok := liStyle.Get(css.PListStyleImage).(css.URL); ok && b.Images != nil {
			if im := b.Images.Get(string(u)); im != nil {
				r := &Box{Kind: KReplaced, Style: b.R.AnonymousStyle(style), Image: im, IsInline: true, Anonymous: true}
				children = append(children, r)
			}
		}
		if children == nil {
			var marker string
			switch t := liStyle.Get(css.PListStyleType).(type) {
			case css.Str:
				marker = string(t)
			case css.Keyword:
				if t == "none" {
					return nil
				}
				marker = b.R.CounterStyles.Marker(b.counterValue("list-item"), t)
			case *css.CounterStyle:
				marker = b.R.CounterStyles.Marker(b.counterValue("list-item"), t)
			}
			if marker == "" {
				return nil
			}
			children = b.textBoxes(marker, style)
		}
	}
	box := &Box{Kind: KInline, Style: style, Element: e, Pseudo: "marker", Children: children}
	if liStyle.Kw(css.PListStylePosition) == "outside" {
		// Outside markers are laid out as small blocks.
		box = &Box{Kind: KBlock, Style: style, Element: e, Pseudo: "marker", Children: children}
		fixupBlockContainer(b, box)
	}
	return box
}

func (b *Builder) quote(style *css.Style, open bool) string {
	var quotes []string
	switch q := style.Get(css.PQuotes).(type) {
	case []string:
		quotes = q
	case css.Keyword:
		if q == "none" {
			return ""
		}
		quotes = []string{"“", "”", "‘", "’"}
	}
	if len(quotes) == 0 {
		return ""
	}
	if open {
		i := b.quoteDepth
		b.quoteDepth++
		if 2*i >= len(quotes) {
			i = len(quotes)/2 - 1
		}
		return quotes[2*i]
	}
	if b.quoteDepth > 0 {
		b.quoteDepth--
	}
	i := b.quoteDepth
	if 2*i+1 >= len(quotes) {
		i = len(quotes)/2 - 1
	}
	return quotes[2*i+1]
}

func (b *Builder) contentBoxes(style *css.Style, items []css.ContentItem, e *html.Node) []*Box {
	var out []*Box
	var textBuf strings.Builder
	flush := func() {
		if textBuf.Len() > 0 {
			out = append(out, b.textBoxes(textBuf.String(), style)...)
			textBuf.Reset()
		}
	}
	for _, it := range items {
		switch it.Kind {
		case "string":
			textBuf.WriteString(it.Str)
		case "counter":
			textBuf.WriteString(b.R.CounterStyles.Represent(b.counterValue(it.Name), it.Style))
		case "counters":
			vals := b.counters[it.Name]
			if len(vals) == 0 {
				vals = []int{0}
			}
			parts := make([]string, len(vals))
			for i, v := range vals {
				parts[i] = b.R.CounterStyles.Represent(v, it.Style)
			}
			textBuf.WriteString(strings.Join(parts, it.Str))
		case "open-quote":
			textBuf.WriteString(b.quote(style, true))
		case "close-quote":
			textBuf.WriteString(b.quote(style, false))
		case "no-open-quote":
			b.quoteDepth++
		case "no-close-quote":
			if b.quoteDepth > 0 {
				b.quoteDepth--
			}
		case "url":
			flush()
			if b.Images != nil {
				if im := b.Images.Get(it.Str); im != nil {
					out = append(out, &Box{Kind: KReplaced, Style: b.R.AnonymousStyle(style), Image: im, IsInline: true, Anonymous: true})
				}
			}
		case "image":
			flush()
		case "leader":
			flush()
			out = append(out, &Box{Kind: KLeader, Style: style, Leader: it.Str, Anonymous: true})
		case "target-counter", "target-counters", "target-text":
			b.UsesTargets = true
			textBuf.WriteString(b.targetValue(it, e))
		case "string-ref", "element", "content":
			// Only meaningful in page margin boxes; handled there.
			flush()
			out = append(out, &Box{Kind: KText, Style: style, Deferred: []css.ContentItem{it}, Anonymous: true})
		}
	}
	flush()
	return out
}

func (b *Builder) targetAnchor(it css.ContentItem, e *html.Node) string {
	ref := it.Ref
	if len(ref) == 1 {
		t := ref[0]
		var v string
		switch {
		case t.Type == css.TString:
			v = t.Value
		case t.Type == css.TFunction && t.Value == "attr":
			args := css.RemoveWS(t.Children)
			if len(args) > 0 && e != nil {
				v = e.Get(args[0].Value)
			}
		case t.Type == css.TURL:
			v = t.Value
		}
		v = strings.TrimSpace(v)
		if i := strings.IndexByte(v, '#'); i >= 0 {
			return v[i+1:]
		}
	}
	return ""
}

func (b *Builder) targetValue(it css.ContentItem, e *html.Node) string {
	anchor := b.targetAnchor(it, e)
	if anchor == "" {
		return ""
	}
	switch it.Kind {
	case "target-text":
		if m, ok := b.AnchorText[anchor]; ok {
			return m[it.Which]
		}
		return ""
	}
	var vals []int
	if tv, ok := b.TargetValues[anchor]; ok {
		vals = tv[it.Name]
	} else if ac, ok := b.AnchorCounters[anchor]; ok {
		vals = ac[it.Name]
	}
	if len(vals) == 0 {
		vals = []int{0}
	}
	if it.Kind == "target-counter" {
		return b.R.CounterStyles.Represent(vals[len(vals)-1], it.Style)
	}
	parts := make([]string, len(vals))
	for i, v := range vals {
		parts[i] = b.R.CounterStyles.Represent(v, it.Style)
	}
	return strings.Join(parts, it.Str)
}

// ---------------------------------------------------------------- text

func (b *Builder) textBoxes(s string, style *css.Style) []*Box {
	s = processWhitespace(s, style.WhiteSpace())
	if s == "" {
		return nil
	}
	s = transformText(s, style.Kw(css.PTextTransform))
	return []*Box{{Kind: KText, Style: style, Text: s, Anonymous: true}}
}

func isCollapsible(ws string) bool {
	return ws == "normal" || ws == "nowrap" || ws == "pre-line"
}

func processWhitespace(s, ws string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	switch ws {
	case "pre", "pre-wrap", "break-spaces":
		return s
	}
	var out []rune
	rs := []rune(strings.ReplaceAll(s, "\t", " "))
	// Remove spaces around newlines.
	for i := 0; i < len(rs); i++ {
		r := rs[i]
		if r == '\n' {
			for len(out) > 0 && out[len(out)-1] == ' ' {
				out = out[:len(out)-1]
			}
			out = append(out, '\n')
			for i+1 < len(rs) && rs[i+1] == ' ' {
				i++
			}
			continue
		}
		out = append(out, r)
	}
	if ws != "pre-line" {
		for i, r := range out {
			if r == '\n' {
				out[i] = ' '
			}
		}
	}
	// Collapse consecutive spaces.
	res := out[:0]
	for i, r := range out {
		if r == ' ' && i > 0 && out[i-1] == ' ' && len(res) > 0 && res[len(res)-1] == ' ' {
			continue
		}
		res = append(res, r)
	}
	return string(res)
}

func transformText(s, t string) string {
	switch t {
	case "uppercase":
		return strings.ToUpper(s)
	case "lowercase":
		return strings.ToLower(s)
	case "capitalize":
		rs := []rune(s)
		start := true
		for i, r := range rs {
			if unicode.IsLetter(r) || unicode.IsDigit(r) {
				if start {
					rs[i] = unicode.ToTitle(r)
				}
				start = false
			} else if unicode.IsSpace(r) || unicode.IsPunct(r) && r != '\'' && r != '’' {
				start = true
			}
		}
		return string(rs)
	case "full-width":
		rs := []rune(s)
		for i, r := range rs {
			if r == ' ' {
				rs[i] = 0x3000
			} else if r > 0x20 && r < 0x7f {
				rs[i] = r + 0xfee0
			}
		}
		return string(rs)
	}
	return s
}

// inlineSVG serializes an inline <svg> element and parses it as an image.
func (b *Builder) inlineSVG(e *html.Node) *images.Image {
	var sb strings.Builder
	serializeXML(e, &sb)
	im, err := images.Decode([]byte(sb.String()), "image/svg+xml", b.BaseURL)
	if err != nil {
		b.warn("Failed to render inline SVG: " + err.Error())
		return nil
	}
	return im
}

func xmlEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", "\"", "&quot;")
	return r.Replace(s)
}

func serializeXML(n *html.Node, sb *strings.Builder) {
	switch n.Type {
	case html.TextNode:
		sb.WriteString(xmlEscape(n.Data))
	case html.ElementNode:
		sb.WriteString("<" + n.Tag)
		hasNS := false
		for _, a := range n.Attrs {
			if a.Name == "xmlns" {
				hasNS = true
			}
			sb.WriteString(" " + a.Name + "=\"" + xmlEscape(a.Value) + "\"")
		}
		if n.Tag == "svg" && !hasNS {
			sb.WriteString(` xmlns="http://www.w3.org/2000/svg"`)
		}
		sb.WriteString(">")
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			serializeXML(c, sb)
		}
		sb.WriteString("</" + n.Tag + ">")
	}
}

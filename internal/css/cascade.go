package css

import (
	"math"
	"sort"
	"strings"

	"github.com/esoteric-programmer/weasyprint-go/internal/fetch"
	"github.com/esoteric-programmer/weasyprint-go/internal/html"
)

type ruleEntry struct {
	rule *StyleRule
	sel  *Selector
}

type ruleIndex struct {
	byID    map[string][]ruleEntry
	byClass map[string][]ruleEntry
	byTag   map[string][]ruleEntry
	other   []ruleEntry
}

// Resolver computes styles for elements, pseudo-elements and pages.
type Resolver struct {
	Sheets        []*Stylesheet
	CounterStyles CounterStyles
	FontFaces     []*FontFaceRule
	ColorProfiles []*ColorProfileRule
	PageRules     []*PageRule
	BaseURL       string
	Viewport      [2]float64
	Doc           *html.Node
	// Hints returns presentational hint declarations for an element.
	Hints func(e *html.Node) []Decl

	index     ruleIndex
	styles    map[*html.Node]*Style
	pseudos   map[pseudoKey]*Style
	rootStyle *Style
	pageCache map[string]*Style
}

type pseudoKey struct {
	e      *html.Node
	pseudo string
}

// NewResolver builds a resolver from stylesheets in cascade order.
func NewResolver(doc *html.Node, sheets []*Stylesheet, baseURL string) *Resolver {
	r := &Resolver{
		Sheets:        sheets,
		CounterStyles: CounterStyles{},
		BaseURL:       baseURL,
		Doc:           doc,
		styles:        map[*html.Node]*Style{},
		pseudos:       map[pseudoKey]*Style{},
		pageCache:     map[string]*Style{},
		Viewport:      [2]float64{793.7007874, 1122.519685},
	}
	r.index = ruleIndex{byID: map[string][]ruleEntry{}, byClass: map[string][]ruleEntry{}, byTag: map[string][]ruleEntry{}}
	for _, sh := range sheets {
		for name, cs := range sh.CounterStyles {
			r.CounterStyles[name] = cs
		}
		r.FontFaces = append(r.FontFaces, sh.FontFaces...)
		r.ColorProfiles = append(r.ColorProfiles, sh.ColorProfiles...)
		r.PageRules = append(r.PageRules, sh.PageRules...)
		for _, rule := range sh.Rules {
			for _, sel := range rule.Selectors {
				e := ruleEntry{rule, sel}
				c := sel.rightmost()
				switch {
				case c.ID != "":
					r.index.byID[c.ID] = append(r.index.byID[c.ID], e)
				case len(c.Classes) > 0:
					r.index.byClass[c.Classes[0]] = append(r.index.byClass[c.Classes[0]], e)
				case c.Tag != "" && c.Tag != "*":
					r.index.byTag[strings.ToLower(c.Tag)] = append(r.index.byTag[strings.ToLower(c.Tag)], e)
				default:
					r.index.other = append(r.index.other, e)
				}
			}
		}
	}
	return r
}

func (r *Resolver) candidates(e *html.Node) []ruleEntry {
	var out []ruleEntry
	if id := e.Get("id"); id != "" {
		out = append(out, r.index.byID[id]...)
	}
	for _, cl := range strings.Fields(e.Get("class")) {
		out = append(out, r.index.byClass[cl]...)
	}
	out = append(out, r.index.byTag[e.Tag]...)
	out = append(out, r.index.other...)
	return out
}

type cascadeEntry struct {
	level     int
	layer     int
	spec      Specificity
	order     int
	decls     []Decl
	important bool
}

func levelOf(o Origin, important bool) int {
	if !important {
		return int(o)
	}
	return 5 - int(o)
}

type cascaded struct {
	vals    map[PropID]Value
	pending map[PropID]*Decl
	custom  map[string][]Token
}

func newCascaded() *cascaded {
	return &cascaded{vals: map[PropID]Value{}, pending: map[PropID]*Decl{}, custom: map[string][]Token{}}
}

func (c *cascaded) apply(entries []cascadeEntry) {
	sort.SliceStable(entries, func(i, j int) bool {
		a, b := entries[i], entries[j]
		if a.level != b.level {
			return a.level < b.level
		}
		if a.layer != b.layer {
			return a.layer < b.layer
		}
		if a.spec != b.spec {
			return a.spec.Less(b.spec)
		}
		return a.order < b.order
	})
	for _, e := range entries {
		for i := range e.decls {
			d := &e.decls[i]
			if d.Important != e.important {
				continue
			}
			switch {
			case d.Custom != "":
				c.custom[d.Custom] = d.Pending
			case d.Pending != nil:
				for _, id := range LonghandsOf(d.Name) {
					delete(c.vals, id)
					c.pending[id] = d
				}
			default:
				for _, pv := range d.Values {
					c.vals[pv.ID] = pv.Value
					delete(c.pending, pv.ID)
				}
			}
		}
	}
}

func addEntries(entries []cascadeEntry, origin Origin, layer int, spec Specificity, order int, decls []Decl) []cascadeEntry {
	hasImp, hasNorm := false, false
	for _, d := range decls {
		if d.Important {
			hasImp = true
		} else {
			hasNorm = true
		}
	}
	if hasNorm {
		entries = append(entries, cascadeEntry{levelOf(origin, false), layer, spec, order, decls, false})
	}
	if hasImp {
		entries = append(entries, cascadeEntry{levelOf(origin, true), layer, spec, order, decls, true})
	}
	return entries
}

// ResolveAll computes the styles of all elements of the document.
func (r *Resolver) ResolveAll() {
	root := r.Doc.Root()
	if root == nil {
		return
	}
	var walk func(e *html.Node, parent *Style)
	walk = func(e *html.Node, parent *Style) {
		byPseudo := map[string][]cascadeEntry{}
		for _, ce := range r.candidates(e) {
			if ce.sel.Match(e) {
				byPseudo[ce.sel.PseudoElement] = addEntries(byPseudo[ce.sel.PseudoElement], ce.rule.Origin, layerRank(ce.rule.Layer, ce.rule.layers), ce.sel.Specificity, ce.rule.Order, ce.rule.Decls)
			}
		}
		entries := byPseudo[""]
		if r.Hints != nil {
			if hints := r.Hints(e); len(hints) > 0 {
				entries = addEntries(entries, OriginAuthor, math.MaxInt32, Specificity{}, -1, hints)
			}
		}
		if styleAttr, ok := e.Attr("style"); ok {
			decls := preprocessDecls(ParseInlineDeclarations(styleAttr), &ParseCtx{BaseURL: r.BaseURL}, &Env{})
			entries = addEntries(entries, OriginAuthor, math.MaxInt32, Specificity{1 << 20, 0, 0}, math.MaxInt32, decls)
		}
		c := newCascaded()
		c.apply(entries)
		st := r.compute(c, parent, e, "")
		r.styles[e] = st
		if parent == nil {
			r.rootStyle = st
		}
		for pseudo, pe := range byPseudo {
			if pseudo == "" {
				continue
			}
			if (pseudo == "footnote-call" || pseudo == "footnote-marker") && st.Kw(PFloat) != "footnote" {
				continue
			}
			pc := newCascaded()
			pc.apply(pe)
			r.pseudos[pseudoKey{e, pseudo}] = r.compute(pc, st, e, pseudo)
		}
		for ch := e.FirstChild; ch != nil; ch = ch.NextSibling {
			if ch.Type == html.ElementNode {
				walk(ch, st)
			}
		}
	}
	walk(root, nil)
}

// Style returns the computed style of an element.
func (r *Resolver) Style(e *html.Node) *Style { return r.styles[e] }

// RootStyle returns the root element style.
func (r *Resolver) RootStyle() *Style { return r.rootStyle }

// PseudoStyle returns the computed style of a pseudo-element, or nil if no
// rule targets it.
func (r *Resolver) PseudoStyle(e *html.Node, pseudo string) *Style {
	return r.pseudos[pseudoKey{e, pseudo}]
}

// MarkerStyle returns the ::marker style, computing a default one if needed.
func (r *Resolver) MarkerStyle(e *html.Node) *Style {
	if s := r.pseudos[pseudoKey{e, "marker"}]; s != nil {
		return s
	}
	s := r.compute(newCascaded(), r.styles[e], e, "marker")
	r.pseudos[pseudoKey{e, "marker"}] = s
	return s
}

// AnonymousStyle returns the style of an anonymous box inheriting from parent.
func (r *Resolver) AnonymousStyle(parent *Style) *Style {
	return r.compute(newCascaded(), parent, nil, "")
}

// AnonymousStyleWith returns an anonymous style with some overrides.
func (r *Resolver) AnonymousStyleWith(parent *Style, overrides map[PropID]Value) *Style {
	c := newCascaded()
	for k, v := range overrides {
		c.vals[k] = v
	}
	return r.compute(c, parent, nil, "")
}

// PageType describes a page for @page selector matching.
type PageType struct {
	Name  string
	Side  string // left or right
	Blank bool
	First bool
	Index int
}

func (p PageType) key() string {
	k := p.Name + "|" + p.Side
	if p.Blank {
		k += "|blank"
	}
	if p.First {
		k += "|first"
	}
	return k
}

func (ps PageSelector) matches(p PageType) bool {
	if ps.Name != "" && ps.Name != p.Name {
		return false
	}
	if ps.First && !p.First {
		return false
	}
	if ps.Blank && !p.Blank {
		return false
	}
	if ps.Side != "" && ps.Side != p.Side {
		return false
	}
	if ps.Nth != nil && !nthMatch(ps.Nth[0], ps.Nth[1], p.Index+1) {
		return false
	}
	return true
}

func (r *Resolver) pageEntries(p PageType, margin string) []cascadeEntry {
	var entries []cascadeEntry
	for _, pr := range r.PageRules {
		decls := pr.Decls
		if margin != "" {
			decls = pr.MarginDecl[margin]
			if len(decls) == 0 {
				continue
			}
		}
		if len(pr.Selectors) == 0 {
			entries = addEntries(entries, pr.Origin, layerRank(pr.Layer, pr.layers), Specificity{}, pr.Order, decls)
			continue
		}
		for _, ps := range pr.Selectors {
			if ps.matches(p) {
				entries = addEntries(entries, pr.Origin, layerRank(pr.Layer, pr.layers), ps.Specificity, pr.Order, decls)
			}
		}
	}
	return entries
}

// PageStyle returns the computed style of a page.
func (r *Resolver) PageStyle(p PageType) *Style {
	key := p.key()
	if p.Nth() {
		key += "|" + itoa(p.Index)
	}
	if s, ok := r.pageCache[key]; ok {
		return s
	}
	c := newCascaded()
	c.apply(r.pageEntries(p, ""))
	s := r.compute(c, r.rootStyle, nil, "@page")
	r.pageCache[key] = s
	return s
}

// Nth reports whether any page rule uses :nth() (making styles index-dependent).
func (p PageType) Nth() bool { return true }

// MarginBoxStyle returns the computed style of a page margin box, or nil
// if no rule defines it.
func (r *Resolver) MarginBoxStyle(p PageType, page *Style, name string) *Style {
	entries := r.pageEntries(p, name)
	if len(entries) == 0 {
		return nil
	}
	c := newCascaded()
	c.apply(entries)
	return r.compute(c, page, nil, "@"+name)
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}

// ------------------------------------------------------------- var()

func substituteVars(ts []Token, custom map[string][]Token, depth int) ([]Token, bool) {
	if depth > 20 {
		return nil, false
	}
	var out []Token
	for _, t := range ts {
		if t.Type == TFunction && t.Value == "var" {
			args := StripWS(t.Children)
			if len(args) == 0 || args[0].Type != TIdent || !strings.HasPrefix(args[0].Raw, "--") {
				return nil, false
			}
			name := args[0].Raw
			if v, ok := custom[name]; ok && len(v) > 0 {
				sub, ok := substituteVars(v, custom, depth+1)
				if !ok {
					return nil, false
				}
				out = append(out, sub...)
				continue
			}
			// Fallback.
			rest := StripWS(args[1:])
			if len(rest) > 0 && rest[0].Type == TComma {
				sub, ok := substituteVars(rest[1:], custom, depth+1)
				if !ok {
					return nil, false
				}
				out = append(out, StripWS(sub)...)
				continue
			}
			return nil, false
		}
		if len(t.Children) > 0 && ContainsVar(t.Children) {
			ch, ok := substituteVars(t.Children, custom, depth)
			if !ok {
				return nil, false
			}
			t.Children = ch
		}
		out = append(out, t)
	}
	return out, true
}

// ------------------------------------------------------------- computing

type computeCtx struct {
	r              *Resolver
	parent         *Style
	style          *Style
	elem           *html.Node
	pseudo         string
	fontSize       float64
	parentFontSize float64
	rootFontSize   float64
}

var fontSizeKeywordValues = map[string]float64{
	"xx-small": 3.0 / 5 * 16, "x-small": 3.0 / 4 * 16, "small": 8.0 / 9 * 16,
	"medium": 16, "large": 6.0 / 5 * 16, "x-large": 3.0 / 2 * 16,
	"xx-large": 2 * 16, "xxx-large": 3 * 16,
}

func (ctx *computeCtx) toPx(l Length, fontSize float64) float64 {
	if f, ok := unitToPx[l.Unit]; ok {
		return l.V * f
	}
	switch l.Unit {
	case "em":
		return l.V * fontSize
	case "rem":
		return l.V * ctx.rootFontSize
	case "ex", "cap":
		return l.V * fontSize * 0.5
	case "ch", "ic":
		return l.V * fontSize * 0.5
	case "rex", "rch":
		return l.V * ctx.rootFontSize * 0.5
	case "lh":
		return l.V * fontSize * 1.2
	case "rlh":
		return l.V * ctx.rootFontSize * 1.2
	case "vw":
		return l.V * ctx.r.Viewport[0] / 100
	case "vh":
		return l.V * ctx.r.Viewport[1] / 100
	case "vmin":
		return l.V * math.Min(ctx.r.Viewport[0], ctx.r.Viewport[1]) / 100
	case "vmax":
		return l.V * math.Max(ctx.r.Viewport[0], ctx.r.Viewport[1]) / 100
	}
	return l.V
}

func (ctx *computeCtx) length(l Length, fontSize float64) Length {
	switch l.Unit {
	case "px", "%":
		return l
	case "calc":
		if l.calc == nil {
			return l
		}
		toPx := func(x Length) float64 { return ctx.toPx(x, fontSize) }
		lin, ok := l.calc.eval(toPx)
		if !ok {
			return Px(0)
		}
		if lin.deferred {
			c := Length{Unit: "calc", calc: l.calc.withPx(toPx)}
			c.V = c.Resolve(0) // for callers adding up fixed parts
			return c
		}
		if lin.isNum {
			return Px(lin.num)
		}
		px := lin.px + lin.num
		if lin.pct == 0 {
			return Px(px)
		}
		if px == 0 {
			return Percent(lin.pct)
		}
		return Length{V: px, Unit: "calc", Pct: lin.pct}
	}
	return Px(ctx.toPx(l, fontSize))
}

// computeLengths converts lengths in nested values to px.
func (ctx *computeCtx) computeLengths(v Value) Value {
	fs := ctx.fontSize
	switch v := v.(type) {
	case Length:
		return ctx.length(v, fs)
	case [2]Length:
		return [2]Length{ctx.length(v[0], fs), ctx.length(v[1], fs)}
	case PageSize:
		return PageSize{ctx.length(v[0], fs), ctx.length(v[1], fs)}
	case Position:
		return Position{v.XOrigin, ctx.length(v.X, fs), v.YOrigin, ctx.length(v.Y, fs)}
	case []Position:
		out := make([]Position, len(v))
		for i, p := range v {
			out[i] = ctx.computeLengths(p).(Position)
		}
		return out
	case []BgSize:
		out := make([]BgSize, len(v))
		for i, s := range v {
			out[i] = s
			if l, ok := s.W.(Length); ok {
				out[i].W = ctx.length(l, fs)
			}
			if l, ok := s.H.(Length); ok {
				out[i].H = ctx.length(l, fs)
			}
		}
		return out
	case []Transform:
		out := make([]Transform, len(v))
		for i, t := range v {
			args := make([]Value, len(t.Args))
			for j, a := range t.Args {
				if l, ok := a.(Length); ok {
					args[j] = ctx.length(l, fs)
				} else {
					args[j] = a
				}
			}
			out[i] = Transform{t.Name, args}
		}
		return out
	case []Shadow:
		out := make([]Shadow, len(v))
		for i, s := range v {
			s.X, s.Y = ctx.length(s.X, fs), ctx.length(s.Y, fs)
			s.Blur, s.Spread = ctx.length(s.Blur, fs), ctx.length(s.Spread, fs)
			s.Color = ctx.color(s.Color)
			out[i] = s
		}
		return out
	case []Value:
		out := make([]Value, len(v))
		for i, x := range v {
			out[i] = ctx.computeLengths(x)
		}
		return out
	case *Gradient:
		g := *v
		g.Stops = make([]ColorStop, len(v.Stops))
		for i, s := range v.Stops {
			s.Color = ctx.color(s.Color)
			if s.Pos != nil {
				l := ctx.length(*s.Pos, fs)
				s.Pos = &l
			}
			if s.Hint != nil {
				l := ctx.length(*s.Hint, fs)
				s.Hint = &l
			}
			g.Stops[i] = s
		}
		g.SizeLen = [2]Length{ctx.length(v.SizeLen[0], fs), ctx.length(v.SizeLen[1], fs)}
		g.Position = ctx.computeLengths(v.Position).(Position)
		return &g
	}
	return v
}

func (ctx *computeCtx) color(v Value) Value {
	if ld, ok := v.(LightDark); ok {
		v = ld.Choose(ctx.style.vals[PColorScheme])
	}
	if k, ok := v.(Keyword); ok && k == "currentcolor" {
		return ctx.style.vals[PColor]
	}
	return v
}

// ResolveColor returns the color of a color value (currentcolor,
// light-dark()...) for this style.
func (s *Style) ResolveColor(v Value) Color {
	if ld, ok := v.(LightDark); ok {
		v = ld.Choose(s.vals[PColorScheme])
	}
	if c, ok := v.(Color); ok {
		return c
	}
	return s.Color(PColor)
}

func (r *Resolver) compute(c *cascaded, parent *Style, elem *html.Node, pseudo string) *Style {
	s := &Style{vals: make([]Value, len(Props)), Pseudo: pseudo, Element: elem}
	s.IsRoot = parent == nil && pseudo == ""
	ctx := &computeCtx{r: r, parent: parent, style: s, elem: elem, pseudo: pseudo}
	ctx.parentFontSize = 16
	if parent != nil {
		ctx.parentFontSize = parent.FontSize()
	}
	if r.rootStyle != nil {
		ctx.rootFontSize = r.rootStyle.FontSize()
	} else {
		ctx.rootFontSize = 16
	}

	// Custom properties.
	s.Custom = map[string][]Token{}
	if parent != nil {
		for k, v := range parent.Custom {
			s.Custom[k] = v
		}
	}
	if len(c.custom) > 0 {
		for k, v := range c.custom {
			if len(v) == 1 && v[0].Type == TIdent {
				switch v[0].Value {
				case "initial":
					delete(s.Custom, k)
					continue
				case "inherit", "unset":
					continue
				}
			}
			s.Custom[k] = v
		}
		for k, v := range s.Custom {
			if ContainsVar(v) {
				if sub, ok := substituteVars(v, s.Custom, 0); ok {
					s.Custom[k] = sub
				} else {
					delete(s.Custom, k)
				}
			}
		}
	}

	specified := func(id PropID) Value {
		v, ok := c.vals[id]
		if !ok {
			if d, ok := c.pending[id]; ok {
				sub, ok := substituteVars(d.Pending, s.Custom, 0)
				if ok {
					for _, pv := range ExpandDeclaration(d.Name, sub, &ParseCtx{BaseURL: r.BaseURL}) {
						if pv.ID == id {
							return pv.Value
						}
					}
				}
				return CSSWide("unset")
			}
			return nil
		}
		return v
	}

	resolve := func(id PropID) (Value, bool) {
		v := specified(id)
		def := Props[id]
		if v == nil {
			if def.Inherited && parent != nil {
				return parent.vals[id], false
			}
			return def.Initial, true
		}
		if w, ok := v.(CSSWide); ok {
			switch w {
			case "initial":
				return def.Initial, true
			case "inherit":
				if parent != nil {
					return parent.vals[id], false
				}
				return def.Initial, true
			default: // unset, revert
				if def.Inherited && parent != nil {
					return parent.vals[id], false
				}
				return def.Initial, true
			}
		}
		return v, true
	}

	// font-size first.
	fsv, needCompute := resolve(PFontSize)
	if needCompute {
		s.vals[PFontSize] = Px(ctx.computeFontSize(fsv))
	} else {
		s.vals[PFontSize] = fsv
	}
	ctx.fontSize = s.FontSize()
	if parent == nil && pseudo == "" {
		ctx.rootFontSize = ctx.fontSize
	}
	// color-scheme and color next (for light-dark() and currentcolor).
	csv, _ := resolve(PColorScheme)
	s.vals[PColorScheme] = csv
	cv, needCompute := resolve(PColor)
	if ld, ok := cv.(LightDark); ok {
		cv = ld.Choose(csv)
		if k, ok := cv.(Keyword); ok && k == "currentcolor" {
			needCompute = true
		}
	}
	if needCompute {
		if k, ok := cv.(Keyword); ok && k == "currentcolor" {
			if parent != nil {
				cv = parent.vals[PColor]
			} else {
				cv = Color{R: 0, G: 0, B: 0, A: 1}
			}
		}
	}
	s.vals[PColor] = cv

	for i := range Props {
		id := PropID(i)
		if id == PFontSize || id == PColor || id == PColorScheme {
			continue
		}
		v, needCompute := resolve(id)
		if needCompute {
			v = ctx.computeValue(id, v, c)
		}
		s.vals[id] = v
	}
	ctx.finish(c)
	return s
}

func (ctx *computeCtx) computeFontSize(v Value) float64 {
	switch v := v.(type) {
	case Keyword:
		if f, ok := fontSizeKeywordValues[string(v)]; ok {
			return f
		}
		// Like WeasyPrint, snap to the next keyword size when possible.
		table := []float64{16 * 3 / 5.0, 16 * 3 / 4.0, 16 * 8 / 9.0, 16, 16 * 6 / 5.0, 16 * 3 / 2.0, 32}
		switch v {
		case "larger":
			for _, kv := range table {
				if kv > ctx.parentFontSize {
					return kv
				}
			}
			return ctx.parentFontSize * 1.2
		case "smaller":
			for i := len(table) - 1; i >= 0; i-- {
				if table[i] < ctx.parentFontSize {
					return table[i]
				}
			}
			return ctx.parentFontSize * 0.8
		}
	case Length:
		switch v.Unit {
		case "%":
			return v.V * ctx.parentFontSize / 100
		case "em", "ex", "ch", "lh":
			return ctx.toPx(v, ctx.parentFontSize)
		case "calc":
			l := ctx.length(v, ctx.parentFontSize)
			return l.Resolve(ctx.parentFontSize)
		}
		return ctx.toPx(v, ctx.parentFontSize)
	}
	return 16
}

var borderWidthKeywords = map[string]float64{"thin": 1, "medium": 3, "thick": 5}

func (ctx *computeCtx) computeValue(id PropID, v Value, c *cascaded) Value {
	switch id {
	case PFontWeight:
		if k, ok := v.(Keyword); ok {
			pw := 400.0
			if ctx.parent != nil {
				pw = ctx.parent.Num(PFontWeight)
			}
			if k == "bolder" {
				switch {
				case pw < 350:
					return Number(400)
				case pw < 550:
					return Number(700)
				default:
					return Number(900)
				}
			}
			switch {
			case pw < 550:
				return Number(100)
			case pw < 750:
				return Number(400)
			default:
				return Number(700)
			}
		}
		return v
	case PLineHeight:
		if l, ok := v.(Length); ok {
			if l.Unit == "%" {
				return Px(l.V * ctx.fontSize / 100)
			}
			return Px(ctx.length(l, ctx.fontSize).Resolve(ctx.fontSize))
		}
		return v
	case PBorderTopWidth, PBorderRightWidth, PBorderBottomWidth, PBorderLeftWidth, POutlineWidth, PColumnRuleWidth:
		var styleID PropID
		switch id {
		case PBorderTopWidth:
			styleID = PBorderTopStyle
		case PBorderRightWidth:
			styleID = PBorderRightStyle
		case PBorderBottomWidth:
			styleID = PBorderBottomStyle
		case PBorderLeftWidth:
			styleID = PBorderLeftStyle
		case POutlineWidth:
			styleID = POutlineStyle
		case PColumnRuleWidth:
			styleID = PColumnRuleStyle
		}
		st, _ := ctx.resolveKeyword(styleID, c)
		if st == "none" || st == "hidden" {
			return Px(0)
		}
		if k, ok := v.(Keyword); ok {
			return Px(borderWidthKeywords[string(k)])
		}
		return ctx.length(v.(Length), ctx.fontSize)
	case PBorderTopColor, PBorderRightColor, PBorderBottomColor, PBorderLeftColor,
		POutlineColor, PTextDecorationColor, PColumnRuleColor, PBackgroundColor:
		return ctx.color(v)
	case PWordSpacing:
		if k, ok := v.(Keyword); ok && k == "normal" {
			return Px(0)
		}
	case PVerticalAlign:
		if k, ok := v.(Keyword); ok {
			switch k {
			case "super":
				return Px(ctx.fontSize * 0.5)
			case "sub":
				return Px(-ctx.fontSize * 0.5)
			}
		}
		if l, ok := v.(Length); ok && l.Unit == "%" {
			lh := ctx.lineHeightPx(c)
			return Px(l.V * lh / 100)
		}
	case PContent:
		return ctx.computeContent(v)
	case PBookmarkLabel:
		if items, ok := v.([]ContentItem); ok {
			return ctx.resolveAttrs(items)
		}
		return v
	case PStringSet:
		if sets, ok := v.([]StringSet); ok {
			out := make([]StringSet, len(sets))
			for i, ss := range sets {
				out[i] = StringSet{ss.Name, ctx.resolveAttrs(ss.Content)}
			}
			return out
		}
		return v
	case PLink:
		switch lv := v.(type) {
		case ContentItem:
			if ctx.elem == nil {
				return Keyword("none")
			}
			val, ok := ctx.elem.Attr(lv.Name)
			if !ok || strings.TrimSpace(val) == "" && lv.Name != "href" {
				return Keyword("none")
			}
			return ctx.linkValue(val)
		case URL:
			return ctx.linkValue(string(lv))
		}
		return v
	case PLang:
		if ci, ok := v.(ContentItem); ok {
			if ctx.elem == nil {
				return Keyword("none")
			}
			val, ok := ctx.elem.Attr(ci.Name)
			if !ok || val == "" {
				return Keyword("none")
			}
			return Str(val)
		}
		return v
	case PAnchor:
		if ci, ok := v.(ContentItem); ok {
			if ctx.elem == nil {
				return Keyword("none")
			}
			val := strings.TrimSpace(ctx.elem.Get(ci.Name))
			if val == "" {
				return Keyword("none")
			}
			return Str(val)
		}
		return v
	case PTabSize:
		return ctx.computeLengths(v)
	case PPage:
		if k, ok := v.(Keyword); ok && k == "auto" && ctx.parent != nil && ctx.parent.Element != nil {
			return ctx.parent.vals[PPage]
		}
		return v
	}
	return ctx.computeLengths(v)
}

func (ctx *computeCtx) linkValue(val string) Value {
	val = strings.TrimSpace(val)
	if strings.HasPrefix(val, "#") {
		return LinkValue{Internal: true, Target: unescapeFragment(val[1:])}
	}
	abs := fetch.Resolve(ctx.r.BaseURL, val)
	if i := strings.IndexByte(abs, '#'); i >= 0 && ctx.r.BaseURL != "" {
		base := ctx.r.BaseURL
		if j := strings.IndexByte(base, '#'); j >= 0 {
			base = base[:j]
		}
		if abs[:i] == base {
			return LinkValue{Internal: true, Target: unescapeFragment(abs[i+1:])}
		}
	}
	return LinkValue{Target: abs}
}

func unescapeFragment(s string) string {
	if !strings.Contains(s, "%") {
		return s
	}
	var b []byte
	for i := 0; i < len(s); i++ {
		if s[i] == '%' && i+2 < len(s) {
			var v byte
			ok := true
			for _, c := range []byte(s[i+1 : i+3]) {
				v <<= 4
				switch {
				case c >= '0' && c <= '9':
					v |= c - '0'
				case c >= 'a' && c <= 'f':
					v |= c - 'a' + 10
				case c >= 'A' && c <= 'F':
					v |= c - 'A' + 10
				default:
					ok = false
				}
			}
			if ok {
				b = append(b, v)
				i += 2
				continue
			}
		}
		b = append(b, s[i])
	}
	return string(b)
}

func (ctx *computeCtx) resolveKeyword(id PropID, c *cascaded) (string, bool) {
	v, ok := c.vals[id]
	if !ok {
		if k, ok := Props[id].Initial.(Keyword); ok {
			return string(k), true
		}
		return "", false
	}
	switch w := v.(type) {
	case Keyword:
		return string(w), true
	case CSSWide:
		if w == "inherit" && ctx.parent != nil {
			return ctx.parent.Kw(id), true
		}
		if k, ok := Props[id].Initial.(Keyword); ok {
			return string(k), true
		}
	}
	return "", false
}

func (ctx *computeCtx) lineHeightPx(c *cascaded) float64 {
	switch v := ctx.style.vals[PLineHeight].(type) {
	case Number:
		return float64(v) * ctx.fontSize
	case Length:
		return v.Resolve(ctx.fontSize)
	}
	return ctx.fontSize * 1.2
}

func (ctx *computeCtx) resolveAttrs(items []ContentItem) []ContentItem {
	out := make([]ContentItem, 0, len(items))
	for _, it := range items {
		if it.Kind == "attr" {
			val := ""
			if ctx.elem != nil {
				val = ctx.elem.Get(it.Name)
			}
			out = append(out, ContentItem{Kind: "string", Str: val})
			continue
		}
		out = append(out, it)
	}
	return out
}

func (ctx *computeCtx) computeContent(v Value) Value {
	switch ctx.pseudo {
	case "before", "after":
		if k, ok := v.(Keyword); ok && k == "normal" {
			return Keyword("none")
		}
	}
	if items, ok := v.([]ContentItem); ok {
		out := ctx.resolveAttrs(items)
		for i := range out {
			if out[i].Image != nil {
				out[i].Image = ctx.computeLengths(out[i].Image)
			}
		}
		return out
	}
	return v
}

// finish applies the interdependent display/position/float rules.
func (ctx *computeCtx) finish(c *cascaded) {
	s := ctx.style
	pos := s.Kw(PPosition)
	_, running := s.vals[PPosition].(Running)
	if pos == "absolute" || pos == "fixed" || running {
		s.vals[PFloat] = Keyword("none")
	}
	if s.Kw(PFloat) == "inline-start" {
		s.vals[PFloat] = Keyword("left")
	} else if s.Kw(PFloat) == "inline-end" {
		s.vals[PFloat] = Keyword("right")
	}
	display := s.Kw(PDisplay)
	flexItem := false
	if ctx.parent != nil && (ctx.pseudo == "" || ctx.pseudo == "before" || ctx.pseudo == "after") && ctx.elem != nil {
		switch ctx.parent.Kw(PDisplay) {
		case "flex", "inline-flex", "grid", "inline-grid":
			flexItem = ctx.parent.Element != ctx.elem || ctx.pseudo != ""
		}
	}
	if display != "none" && display != "contents" && (pos == "absolute" || pos == "fixed" || s.IsFloat() || s.IsRoot || running || flexItem) {
		switch display {
		case "inline", "inline-block", "run-in", "table-row-group", "table-column",
			"table-column-group", "table-header-group", "table-footer-group",
			"table-row", "table-cell", "table-caption":
			s.vals[PDisplay] = Keyword("block")
		case "inline-table":
			s.vals[PDisplay] = Keyword("table")
		case "inline-flex":
			s.vals[PDisplay] = Keyword("flex")
		case "inline-grid":
			s.vals[PDisplay] = Keyword("grid")
		case "inline-list-item":
			s.vals[PDisplay] = Keyword("list-item")
		}
	}
	if s.Kw(PFloat) == "footnote" && display != "none" {
		// Footnotes are displayed according to footnote-display.
		if s.Kw(PFootnoteDisplay) == "block" {
			s.vals[PDisplay] = Keyword("block")
		} else {
			s.vals[PDisplay] = Keyword("inline")
		}
	}
	if s.IsRoot && s.Kw(PDisplay) == "contents" {
		s.vals[PDisplay] = Keyword("block")
	}
	display = s.Kw(PDisplay)
	// Table internal boxes have no margins; collapsed tables no padding.
	if strings.HasPrefix(display, "table-") && display != "table-caption" {
		for _, id := range []PropID{PMarginTop, PMarginRight, PMarginBottom, PMarginLeft} {
			s.vals[id] = Px(0)
		}
	}
	if (display == "table" || display == "inline-table") && s.Kw(PBorderCollapse) == "collapse" {
		for _, id := range []PropID{PPaddingTop, PPaddingRight, PPaddingBottom, PPaddingLeft} {
			s.vals[id] = Px(0)
		}
	}
	if s.Kw(POverflow) == "clip" {
		s.vals[POverflow] = Keyword("hidden")
	}
	if s.Kw(POverflow) == "scroll" || s.Kw(POverflow) == "auto" {
		s.vals[POverflow] = Keyword("hidden")
	}
	if ctx.pseudo == "marker" {
		// ::marker accepts only a few properties; others inherit.
	}
}

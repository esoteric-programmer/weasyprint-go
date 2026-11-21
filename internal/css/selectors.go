package css

import (
	"strconv"
	"strings"

	"github.com/esoteric-programmer/weasyprint-go/internal/html"
)

// Specificity is (ids, classes/attributes/pseudo-classes, types).
type Specificity [3]int

// Less compares specificities.
func (a Specificity) Less(b Specificity) bool {
	for i := 0; i < 3; i++ {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}

func (a Specificity) add(b Specificity) Specificity {
	return Specificity{a[0] + b[0], a[1] + b[1], a[2] + b[2]}
}

// Selector is a complex selector.
type Selector struct {
	// Compounds from left to right; Combinators[i] joins Compounds[i] and
	// Compounds[i+1] (' ', '>', '+', '~').
	Compounds     []*Compound
	Combinators   []byte
	PseudoElement string
	Specificity   Specificity
}

// Compound is a compound selector.
type Compound struct {
	Tag     string // "" or "*" for any
	ID      string
	Classes []string
	Attrs   []attrSel
	Pseudos []pseudoSel
}

type attrSel struct {
	name, op, value string
	ci              bool
}

type pseudoSel struct {
	name string
	a, b int         // for nth-*
	list []*Selector // for :not(), :is(), :where(), :has(), nth-child(of S)
	arg  string      // for :lang()
	rel  []byte      // leading combinators for :has() relative selectors
}

type selParser struct {
	ts  []Token
	pos int
}

func (p *selParser) peek() *Token {
	if p.pos < len(p.ts) {
		return &p.ts[p.pos]
	}
	return nil
}

func (p *selParser) skipWS() bool {
	any := false
	for p.pos < len(p.ts) && p.ts[p.pos].Type == TWhitespace {
		p.pos++
		any = true
	}
	return any
}

// ParseSelectorList parses a comma-separated selector list. It returns nil
// if any selector is invalid.
func ParseSelectorList(ts []Token) []*Selector {
	var out []*Selector
	for _, part := range SplitOnComma(ts) {
		s := parseComplex(StripWS(part), false)
		if s == nil {
			return nil
		}
		out = append(out, s)
	}
	return out
}

// parseForgiving parses a forgiving selector list (for :is()/:where()).
func parseForgiving(ts []Token, relative bool) []*Selector {
	var out []*Selector
	for _, part := range SplitOnComma(ts) {
		if s := parseComplex(StripWS(part), relative); s != nil {
			out = append(out, s)
		}
	}
	return out
}

func parseComplex(ts []Token, relative bool) *Selector {
	if len(ts) == 0 {
		return nil
	}
	p := &selParser{ts: ts}
	sel := &Selector{}
	var leading byte
	if relative {
		if t := p.peek(); t != nil && t.Type == TDelim && (t.Value == ">" || t.Value == "+" || t.Value == "~") {
			leading = t.Value[0]
			p.pos++
			p.skipWS()
		} else {
			leading = ' '
		}
	}
	for {
		c, pe, ok := p.parseCompound()
		if !ok {
			return nil
		}
		sel.Compounds = append(sel.Compounds, c)
		if pe != "" {
			sel.PseudoElement = pe
			p.skipWS()
			if p.pos < len(p.ts) {
				return nil
			}
			break
		}
		ws := p.skipWS()
		t := p.peek()
		if t == nil {
			break
		}
		comb := byte(' ')
		if t.Type == TDelim && (t.Value == ">" || t.Value == "+" || t.Value == "~") {
			comb = t.Value[0]
			p.pos++
			p.skipWS()
		} else if !ws {
			return nil
		}
		sel.Combinators = append(sel.Combinators, comb)
	}
	if relative {
		sel.Combinators = append([]byte{leading}, sel.Combinators...)
	}
	for _, c := range sel.Compounds {
		sel.Specificity = sel.Specificity.add(c.specificity())
	}
	if sel.PseudoElement != "" {
		sel.Specificity[2]++
	}
	return sel
}

func maxSpecificity(list []*Selector) Specificity {
	var m Specificity
	for _, s := range list {
		if m.Less(s.Specificity) {
			m = s.Specificity
		}
	}
	return m
}

func (c *Compound) specificity() Specificity {
	var s Specificity
	if c.ID != "" {
		s[0]++
	}
	s[1] += len(c.Classes) + len(c.Attrs)
	if c.Tag != "" && c.Tag != "*" {
		s[2]++
	}
	for _, ps := range c.Pseudos {
		switch ps.name {
		case "where":
		case "is", "not", "has", "matches", "any":
			s = s.add(maxSpecificity(ps.list))
		case "nth-child", "nth-last-child":
			s[1]++
			s = s.add(maxSpecificity(ps.list))
		default:
			s[1]++
		}
	}
	return s
}

var legacyPseudoElements = map[string]bool{
	"before": true, "after": true, "first-line": true, "first-letter": true,
}

func (p *selParser) parseCompound() (*Compound, string, bool) {
	c := &Compound{}
	start := p.pos
	if t := p.peek(); t != nil {
		if t.Type == TIdent {
			c.Tag = t.Value
			p.pos++
		} else if t.IsDelim("*") {
			c.Tag = "*"
			p.pos++
		}
		// Namespace prefixes: ignore "ns|tag" and "*|tag".
		if t2 := p.peek(); t2 != nil && t2.IsDelim("|") {
			p.pos++
			if t3 := p.peek(); t3 != nil && t3.Type == TIdent {
				c.Tag = t3.Value
				p.pos++
			} else if t3 != nil && t3.IsDelim("*") {
				c.Tag = "*"
				p.pos++
			}
		}
	}
	pseudoElement := ""
	for p.pos < len(p.ts) {
		t := p.ts[p.pos]
		switch {
		case t.Type == THash:
			if !t.IsID {
				return nil, "", false
			}
			c.ID = t.Value
			p.pos++
		case t.IsDelim("."):
			p.pos++
			n := p.peek()
			if n == nil || n.Type != TIdent {
				return nil, "", false
			}
			c.Classes = append(c.Classes, n.Raw)
			p.pos++
		case t.Type == TSquareBlock:
			a, ok := parseAttrSel(t.Children)
			if !ok {
				return nil, "", false
			}
			c.Attrs = append(c.Attrs, a)
			p.pos++
		case t.Type == TColon:
			p.pos++
			n := p.peek()
			if n == nil {
				return nil, "", false
			}
			if n.Type == TColon {
				p.pos++
				n = p.peek()
				if n == nil {
					return nil, "", false
				}
				if n.Type == TIdent {
					pseudoElement = n.Value
					p.pos++
				} else if n.Type == TFunction {
					// ::footnote-call() etc are not supported.
					return nil, "", false
				} else {
					return nil, "", false
				}
				return c, pseudoElement, true
			}
			if n.Type == TIdent {
				if legacyPseudoElements[n.Value] {
					p.pos++
					return c, n.Value, true
				}
				if !supportedPseudoClass[n.Value] {
					return nil, "", false
				}
				c.Pseudos = append(c.Pseudos, pseudoSel{name: n.Value})
				p.pos++
			} else if n.Type == TFunction {
				ps, ok := parseFunctionalPseudo(*n)
				if !ok {
					return nil, "", false
				}
				c.Pseudos = append(c.Pseudos, ps)
				p.pos++
			} else {
				return nil, "", false
			}
		default:
			if p.pos == start {
				return nil, "", false
			}
			return c, "", true
		}
	}
	if p.pos == start {
		return nil, "", false
	}
	return c, pseudoElement, true
}

var supportedPseudoClass = map[string]bool{
	"root": true, "empty": true, "first-child": true, "last-child": true,
	"only-child": true, "first-of-type": true, "last-of-type": true,
	"only-of-type": true, "link": true, "any-link": true, "visited": true,
	"hover": true, "active": true, "focus": true, "focus-within": true,
	"focus-visible": true, "target": true, "checked": true, "disabled": true,
	"enabled": true, "required": true, "optional": true, "read-only": true,
	"read-write": true, "placeholder-shown": true, "default": true,
	"indeterminate": true, "scope": true, "defined": true,
}

func parseAttrSel(ts []Token) (attrSel, bool) {
	ts = RemoveWS(ts)
	if len(ts) == 0 {
		return attrSel{}, false
	}
	i := 0
	// Optional namespace prefix.
	if len(ts) > 1 && (ts[0].IsDelim("*") || ts[0].Type == TIdent) && ts[1].IsDelim("|") && len(ts) > 2 && ts[2].Type == TIdent {
		i = 2
	} else if ts[0].IsDelim("|") {
		i = 1
	}
	if ts[i].Type != TIdent {
		return attrSel{}, false
	}
	a := attrSel{name: ts[i].Value}
	i++
	if i == len(ts) {
		return a, true
	}
	op := ""
	if ts[i].IsDelim("=") {
		op = "="
		i++
	} else if i+1 < len(ts) && ts[i].Type == TDelim && ts[i+1].IsDelim("=") && strings.Contains("~|^$*", ts[i].Value) {
		op = ts[i].Value + "="
		i += 2
	} else {
		return attrSel{}, false
	}
	if i >= len(ts) {
		return attrSel{}, false
	}
	switch ts[i].Type {
	case TIdent:
		a.value = ts[i].Raw
	case TString:
		a.value = ts[i].Value
	case TNumber, TDimension:
		a.value = ts[i].String()
	default:
		return attrSel{}, false
	}
	a.op = op
	i++
	if i < len(ts) {
		if ts[i].IsIdent("i") {
			a.ci = true
		} else if !ts[i].IsIdent("s") {
			return attrSel{}, false
		}
		i++
	}
	if i != len(ts) {
		return attrSel{}, false
	}
	return a, true
}

func parseFunctionalPseudo(t Token) (pseudoSel, bool) {
	ps := pseudoSel{name: t.Value}
	switch t.Value {
	case "not", "is", "where", "matches", "any":
		if t.Value == "not" {
			ps.list = ParseSelectorList(StripWS(t.Children))
			if ps.list == nil {
				return ps, false
			}
		} else {
			ps.list = parseForgiving(t.Children, false)
		}
		return ps, true
	case "has":
		ps.list = parseForgiving(t.Children, true)
		return ps, len(ps.list) > 0
	case "lang":
		args := RemoveWS(t.Children)
		if len(args) == 0 {
			return ps, false
		}
		if args[0].Type == TIdent {
			ps.arg = strings.ToLower(args[0].Raw)
		} else if args[0].Type == TString {
			ps.arg = strings.ToLower(args[0].Value)
		} else {
			return ps, false
		}
		return ps, true
	case "nth-child", "nth-last-child", "nth-of-type", "nth-last-of-type":
		args := t.Children
		// "of S" clause.
		for i, a := range args {
			if a.IsIdent("of") && (t.Value == "nth-child" || t.Value == "nth-last-child") {
				ps.list = ParseSelectorList(StripWS(args[i+1:]))
				if ps.list == nil {
					return ps, false
				}
				args = args[:i]
				break
			}
		}
		a, b, ok := parseNth(StripWS(args))
		if !ok {
			return ps, false
		}
		ps.a, ps.b = a, b
		return ps, true
	}
	return ps, false
}

// parseNth parses the An+B microsyntax.
func parseNth(ts []Token) (int, int, bool) {
	s := strings.ToLower(strings.ReplaceAll(Serialize(ts), " ", ""))
	switch s {
	case "odd":
		return 2, 1, true
	case "even":
		return 2, 0, true
	}
	n := strings.IndexByte(s, 'n')
	if n < 0 {
		b, err := strconv.Atoi(strings.TrimPrefix(s, "+"))
		return 0, b, err == nil
	}
	as := s[:n]
	var a int
	switch as {
	case "", "+":
		a = 1
	case "-":
		a = -1
	default:
		v, err := strconv.Atoi(strings.TrimPrefix(as, "+"))
		if err != nil {
			return 0, 0, false
		}
		a = v
	}
	bs := s[n+1:]
	b := 0
	if bs != "" {
		v, err := strconv.Atoi(strings.TrimPrefix(bs, "+"))
		if err != nil {
			return 0, 0, false
		}
		b = v
	}
	return a, b, true
}

// ------------------------------------------------------------------ matching

// Match returns whether the selector matches the element.
func (s *Selector) Match(e *html.Node) bool {
	return s.matchAt(e, len(s.Compounds)-1)
}

func (s *Selector) matchAt(e *html.Node, i int) bool {
	if !s.Compounds[i].match(e) {
		return false
	}
	if i == 0 {
		return true
	}
	switch s.Combinators[i-1] {
	case ' ':
		for p := e.ParentElement(); p != nil; p = p.ParentElement() {
			if s.matchAt(p, i-1) {
				return true
			}
		}
	case '>':
		if p := e.ParentElement(); p != nil {
			return s.matchAt(p, i-1)
		}
	case '+':
		if p := e.PrevElement(); p != nil {
			return s.matchAt(p, i-1)
		}
	case '~':
		for p := e.PrevElement(); p != nil; p = p.PrevElement() {
			if s.matchAt(p, i-1) {
				return true
			}
		}
	}
	return false
}

// matchRelative matches a relative selector (from :has()) anchored at e.
func (s *Selector) matchRelative(anchor *html.Node) bool {
	var candidates []*html.Node
	switch s.Combinators[0] {
	case ' ':
		anchor.Walk(func(n *html.Node) bool {
			if n != anchor && n.Type == html.ElementNode {
				candidates = append(candidates, n)
			}
			return true
		})
	case '>':
		candidates = anchor.Children()
	case '+':
		if n := anchor.NextElement(); n != nil {
			candidates = append(candidates, n)
		}
	case '~':
		for n := anchor.NextElement(); n != nil; n = n.NextElement() {
			candidates = append(candidates, n)
		}
	}
	inner := &Selector{Compounds: s.Compounds, Combinators: s.Combinators[1:]}
	for _, c := range candidates {
		if inner.Match(c) && inner.anchoredAt(c, anchor, s.Combinators[0]) {
			return true
		}
	}
	return false
}

// anchoredAt checks that the leftmost compound of a matching relative
// selector relates to the anchor. This simplified version accepts any match
// whose first compound is a descendant/sibling as required.
func (s *Selector) anchoredAt(e, anchor *html.Node, comb byte) bool {
	if len(s.Compounds) == 1 {
		return true
	}
	// Ensure the chain of ancestors reaches inside the anchor for
	// descendant combinators.
	for p := e; p != nil; p = p.Parent {
		if p == anchor {
			return true
		}
	}
	return comb == '+' || comb == '~'
}

func (c *Compound) match(e *html.Node) bool {
	if e == nil || e.Type != html.ElementNode {
		return false
	}
	if c.Tag != "" && c.Tag != "*" && !strings.EqualFold(c.Tag, e.Tag) {
		return false
	}
	if c.ID != "" && e.Get("id") != c.ID {
		return false
	}
	if len(c.Classes) > 0 {
		classes := strings.Fields(e.Get("class"))
		for _, cl := range c.Classes {
			found := false
			for _, x := range classes {
				if x == cl {
					found = true
					break
				}
			}
			if !found {
				return false
			}
		}
	}
	for _, a := range c.Attrs {
		if !a.match(e) {
			return false
		}
	}
	for _, ps := range c.Pseudos {
		if !ps.match(e) {
			return false
		}
	}
	return true
}

func (a attrSel) match(e *html.Node) bool {
	v, ok := e.Attr(a.name)
	if !ok {
		return false
	}
	want := a.value
	if a.ci {
		v = strings.ToLower(v)
		want = strings.ToLower(want)
	}
	switch a.op {
	case "":
		return true
	case "=":
		return v == want
	case "~=":
		for _, f := range strings.Fields(v) {
			if f == want {
				return true
			}
		}
		return false
	case "|=":
		return v == want || strings.HasPrefix(v, want+"-")
	case "^=":
		return want != "" && strings.HasPrefix(v, want)
	case "$=":
		return want != "" && strings.HasSuffix(v, want)
	case "*=":
		return want != "" && strings.Contains(v, want)
	}
	return false
}

func nthMatch(a, b, idx int) bool {
	// idx is 1-based.
	if a == 0 {
		return idx == b
	}
	d := idx - b
	return d/a >= 0 && d%a == 0
}

func (ps pseudoSel) match(e *html.Node) bool {
	switch ps.name {
	case "root":
		return e.ParentElement() == nil
	case "scope":
		return e.ParentElement() == nil
	case "empty":
		for c := e.FirstChild; c != nil; c = c.NextSibling {
			if c.Type == html.ElementNode || (c.Type == html.TextNode && c.Data != "") {
				return false
			}
		}
		return true
	case "first-child":
		return e.PrevElement() == nil
	case "last-child":
		return e.NextElement() == nil
	case "only-child":
		return e.PrevElement() == nil && e.NextElement() == nil
	case "first-of-type":
		for p := e.PrevElement(); p != nil; p = p.PrevElement() {
			if p.Tag == e.Tag {
				return false
			}
		}
		return true
	case "last-of-type":
		for p := e.NextElement(); p != nil; p = p.NextElement() {
			if p.Tag == e.Tag {
				return false
			}
		}
		return true
	case "only-of-type":
		return pseudoSel{name: "first-of-type"}.match(e) && pseudoSel{name: "last-of-type"}.match(e)
	case "link", "any-link":
		_, ok := e.Attr("href")
		return ok && (e.Tag == "a" || e.Tag == "area" || e.Tag == "link")
	case "checked":
		_, ok := e.Attr("checked")
		if !ok {
			_, ok = e.Attr("selected")
		}
		return ok
	case "disabled":
		_, ok := e.Attr("disabled")
		return ok
	case "enabled":
		_, ok := e.Attr("disabled")
		return !ok && (e.Tag == "input" || e.Tag == "button" || e.Tag == "select" || e.Tag == "textarea" || e.Tag == "option")
	case "required":
		_, ok := e.Attr("required")
		return ok
	case "optional":
		_, ok := e.Attr("required")
		return !ok && (e.Tag == "input" || e.Tag == "select" || e.Tag == "textarea")
	case "defined":
		return true
	case "not":
		for _, s := range ps.list {
			if s.PseudoElement == "" && s.Match(e) {
				return false
			}
		}
		return true
	case "is", "where", "matches", "any":
		for _, s := range ps.list {
			if s.PseudoElement == "" && s.Match(e) {
				return true
			}
		}
		return false
	case "has":
		for _, s := range ps.list {
			if s.matchRelative(e) {
				return true
			}
		}
		return false
	case "lang":
		lang := strings.ToLower(e.Lang())
		return lang == ps.arg || strings.HasPrefix(lang, ps.arg+"-")
	case "nth-child", "nth-last-child":
		idx := 1
		ok := func(x *html.Node) bool {
			if ps.list == nil {
				return true
			}
			for _, s := range ps.list {
				if s.Match(x) {
					return true
				}
			}
			return false
		}
		if !ok(e) {
			return false
		}
		if ps.name == "nth-child" {
			for p := e.PrevElement(); p != nil; p = p.PrevElement() {
				if ok(p) {
					idx++
				}
			}
		} else {
			for p := e.NextElement(); p != nil; p = p.NextElement() {
				if ok(p) {
					idx++
				}
			}
		}
		return nthMatch(ps.a, ps.b, idx)
	case "nth-of-type", "nth-last-of-type":
		idx := 1
		if ps.name == "nth-of-type" {
			for p := e.PrevElement(); p != nil; p = p.PrevElement() {
				if p.Tag == e.Tag {
					idx++
				}
			}
		} else {
			for p := e.NextElement(); p != nil; p = p.NextElement() {
				if p.Tag == e.Tag {
					idx++
				}
			}
		}
		return nthMatch(ps.a, ps.b, idx)
	}
	// hover, focus, visited, target... never match in print.
	return false
}

// keyHints returns the rightmost compound's id, classes and tag for rule
// indexing.
func (s *Selector) rightmost() *Compound { return s.Compounds[len(s.Compounds)-1] }

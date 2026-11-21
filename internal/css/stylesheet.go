package css

import (
	"strings"

	"github.com/esoteric-programmer/weasyprint-go/internal/fetch"
	"github.com/esoteric-programmer/weasyprint-go/internal/html"
)

// Origin is the origin of a stylesheet.
type Origin int

const (
	OriginUA Origin = iota
	OriginUser
	OriginAuthor
)

// Decl is a preprocessed declaration.
type Decl struct {
	Values    []PropValue // validated longhands
	Custom    string      // custom property name (with "--")
	Pending   []Token     // raw value containing var()
	Name      string      // original property name for pending declarations
	Important bool
}

// StyleRule is a qualified style rule.
type StyleRule struct {
	Selectors []*Selector
	Decls     []Decl
	Order     int
	Origin    Origin
}

// PageSelector is a selector of an @page rule.
type PageSelector struct {
	Name        string
	First       bool
	Blank       bool
	Side        string // left, right
	Nth         *[2]int
	Specificity Specificity
}

// PageRule is an @page rule.
type PageRule struct {
	Selectors  []PageSelector // empty means all pages
	Decls      []Decl
	MarginDecl map[string][]Decl // margin box name → declarations
	Order      int
	Origin     Origin
}

// FontFaceRule is an @font-face rule.
type FontFaceRule struct {
	Family       string
	Sources      []FontSource
	Style        string
	Weight       [2]float64
	Stretch      string
	UnicodeRange [][2]rune
	BaseURL      string
	Features     []FontFeature
}

// FontSource is an item of the src descriptor.
type FontSource struct {
	URL    string
	Local  string
	Format string
}

// Stylesheet is a parsed stylesheet.
type Stylesheet struct {
	Origin        Origin
	Rules         []*StyleRule
	PageRules     []*PageRule
	FontFaces     []*FontFaceRule
	CounterStyles map[string]*CounterStyle
	BaseURL       string
}

// Env is the environment needed to parse stylesheets.
type Env struct {
	Fetcher   fetch.Fetcher
	MediaType string
	Warn      func(string)
	order     *int
	Viewport  [2]float64
}

func (e *Env) nextOrder() int {
	if e.order == nil {
		e.order = new(int)
	}
	*e.order++
	return *e.order
}

func (e *Env) warn(msg string) {
	if e.Warn != nil {
		e.Warn(msg)
	}
}

// ParseStylesheet parses a stylesheet source.
func ParseStylesheet(src, baseURL string, origin Origin, env *Env) *Stylesheet {
	sh := &Stylesheet{Origin: origin, CounterStyles: map[string]*CounterStyle{}, BaseURL: baseURL}
	rules := ParseStylesheetRules(Tokenize(src))
	sh.addRules(rules, baseURL, env, 0, true)
	return sh
}

func (sh *Stylesheet) addRules(rules []Rule, baseURL string, env *Env, depth int, allowImport bool) {
	ctx := &ParseCtx{BaseURL: baseURL}
	for _, r := range rules {
		switch r.AtKeyword {
		case "":
			sels := ParseSelectorList(StripWS(r.Prelude))
			if sels == nil {
				env.warn("Invalid or unsupported selector: " + strings.TrimSpace(Serialize(r.Prelude)))
				continue
			}
			decls, nested := ParseDeclarations(r.Content)
			sr := &StyleRule{Selectors: sels, Decls: preprocessDecls(decls, ctx, env), Order: env.nextOrder(), Origin: sh.Origin}
			sh.Rules = append(sh.Rules, sr)
			_ = nested
		case "import":
			if !allowImport || depth > 10 {
				continue
			}
			prelude := RemoveWS(r.Prelude)
			if len(prelude) == 0 {
				continue
			}
			var u string
			switch prelude[0].Type {
			case TString:
				u = fetch.Resolve(baseURL, prelude[0].Value)
			default:
				var ok bool
				u, ok = parseURLToken(prelude[0], ctx)
				if !ok {
					continue
				}
			}
			mediaToks := StripWS(r.Prelude)
			// Remove the URL token from the media query.
			for i, t := range mediaToks {
				if t.Type == TString || t.Type == TURL || (t.Type == TFunction && t.Value == "url") {
					mediaToks = mediaToks[i+1:]
					break
				}
			}
			mediaToks = stripImportModifiers(mediaToks)
			if !EvaluateMediaQuery(mediaToks, env) {
				continue
			}
			if env.Fetcher == nil {
				continue
			}
			res, err := env.Fetcher(u)
			if err != nil {
				env.warn("Failed to load stylesheet at " + u + ": " + err.Error())
				continue
			}
			src := html.DecodeBytes(res.Data, res.Encoding)
			sub := ParseStylesheetRules(Tokenize(src))
			base := res.RedirectURL
			if base == "" {
				base = u
			}
			sh.addRules(sub, base, env, depth+1, true)
		case "media":
			if EvaluateMediaQuery(StripWS(r.Prelude), env) {
				sh.addRules(ParseRuleList(r.Content), baseURL, env, depth, false)
			}
		case "supports":
			if evaluateSupports(RemoveWSKeepStructure(r.Prelude), ctx) {
				sh.addRules(ParseRuleList(r.Content), baseURL, env, depth, false)
			}
		case "layer", "document", "-moz-document", "scope", "container":
			if r.HasBlock {
				sh.addRules(ParseRuleList(r.Content), baseURL, env, depth, false)
			}
		case "page":
			sh.addPageRule(r, ctx, env)
		case "font-face":
			if ff := parseFontFace(r.Content, baseURL); ff != nil {
				sh.FontFaces = append(sh.FontFaces, ff)
			}
		case "counter-style":
			prelude := RemoveWS(r.Prelude)
			if len(prelude) == 1 && prelude[0].Type == TIdent {
				name := prelude[0].Value
				if name != "none" && name != "decimal" && name != "disc" || sh.Origin == OriginUA {
					sh.CounterStyles[name] = ParseCounterStyle(name, r.Content)
				}
			}
		case "charset", "namespace":
		default:
			env.warn("Unsupported at-rule: @" + r.AtKeyword)
		}
	}
}

func stripImportModifiers(ts []Token) []Token {
	var out []Token
	for _, t := range ts {
		if (t.Type == TFunction && (t.Value == "layer" || t.Value == "supports")) || t.IsIdent("layer") {
			continue
		}
		out = append(out, t)
	}
	return StripWS(out)
}

// RemoveWSKeepStructure returns tokens stripped of outer whitespace.
func RemoveWSKeepStructure(ts []Token) []Token { return StripWS(ts) }

func evaluateSupports(ts []Token, ctx *ParseCtx) bool {
	ts = StripWS(ts)
	if len(ts) == 0 {
		return false
	}
	if ts[0].IsIdent("not") {
		return !evaluateSupports(ts[1:], ctx)
	}
	// Split on top-level "and"/"or".
	var parts [][]Token
	var ops []string
	cur := []Token{}
	for _, t := range ts {
		if t.IsIdent("and") || t.IsIdent("or") {
			parts = append(parts, cur)
			ops = append(ops, t.Value)
			cur = []Token{}
			continue
		}
		cur = append(cur, t)
	}
	parts = append(parts, cur)
	eval := func(p []Token) bool {
		p = RemoveWS(p)
		if len(p) != 1 {
			return false
		}
		t := p[0]
		switch t.Type {
		case TParenBlock:
			inner := StripWS(t.Children)
			// Declaration?
			for i, x := range inner {
				if x.Type == TColon && i > 0 {
					name := strings.ToLower(StripWS(inner[:i])[0].Raw)
					if strings.HasPrefix(name, "--") {
						return true
					}
					return ExpandDeclaration(name, inner[i+1:], ctx) != nil
				}
			}
			return evaluateSupports(inner, ctx)
		case TFunction:
			if t.Value == "selector" {
				return ParseSelectorList(StripWS(t.Children)) != nil
			}
		}
		return false
	}
	result := eval(parts[0])
	for i, op := range ops {
		v := eval(parts[i+1])
		if op == "and" {
			result = result && v
		} else {
			result = result || v
		}
	}
	return result
}

func preprocessDecls(decls []Declaration, ctx *ParseCtx, env *Env) []Decl {
	var out []Decl
	for _, d := range decls {
		if strings.HasPrefix(d.Name, "--") {
			out = append(out, Decl{Custom: d.Name, Pending: StripWS(d.Value), Important: d.Important})
			continue
		}
		if ContainsVar(d.Value) {
			if !KnownProperty(d.Name) {
				env.warn("Ignored unknown property " + d.Name)
				continue
			}
			out = append(out, Decl{Name: d.Name, Pending: d.Value, Important: d.Important})
			continue
		}
		vals := ExpandDeclaration(d.Name, d.Value, ctx)
		if vals == nil {
			if KnownProperty(d.Name) {
				env.warn("Ignored `" + d.Name + ": " + strings.TrimSpace(Serialize(d.Value)) + "`, invalid value.")
			} else if _, ok := aliases[d.Name]; !ok {
				env.warn("Ignored `" + d.Name + ": " + strings.TrimSpace(Serialize(d.Value)) + "`, unknown property.")
			}
			continue
		}
		out = append(out, Decl{Values: vals, Important: d.Important})
	}
	return out
}

var marginBoxNames = map[string]bool{
	"top-left-corner": true, "top-left": true, "top-center": true, "top-right": true,
	"top-right-corner": true, "bottom-left-corner": true, "bottom-left": true,
	"bottom-center": true, "bottom-right": true, "bottom-right-corner": true,
	"left-top": true, "left-middle": true, "left-bottom": true, "right-top": true,
	"right-middle": true, "right-bottom": true,
}

// MarginBoxNames lists margin box names in painting order.
var MarginBoxNames = []string{
	"top-left-corner", "top-left", "top-center", "top-right", "top-right-corner",
	"right-top", "right-middle", "right-bottom",
	"bottom-right-corner", "bottom-right", "bottom-center", "bottom-left", "bottom-left-corner",
	"left-bottom", "left-middle", "left-top",
}

func (sh *Stylesheet) addPageRule(r Rule, ctx *ParseCtx, env *Env) {
	sels, ok := parsePageSelectors(StripWS(r.Prelude))
	if !ok {
		env.warn("Unsupported @page selector: " + Serialize(r.Prelude))
		return
	}
	decls, nested := ParseDeclarations(r.Content)
	pr := &PageRule{Selectors: sels, Decls: preprocessDecls(decls, ctx, env), MarginDecl: map[string][]Decl{}, Order: env.nextOrder(), Origin: sh.Origin}
	for _, n := range nested {
		if marginBoxNames[n.AtKeyword] {
			d, _ := ParseDeclarations(n.Content)
			pr.MarginDecl[n.AtKeyword] = append(pr.MarginDecl[n.AtKeyword], preprocessDecls(d, ctx, env)...)
		}
	}
	sh.PageRules = append(sh.PageRules, pr)
}

func parsePageSelectors(ts []Token) ([]PageSelector, bool) {
	if len(ts) == 0 {
		return nil, true
	}
	var out []PageSelector
	for _, part := range SplitOnComma(ts) {
		part = StripWS(part)
		var ps PageSelector
		i := 0
		if i < len(part) && part[i].Type == TIdent {
			ps.Name = part[i].Raw
			ps.Specificity[0] = 1
			i++
		}
		for i < len(part) {
			if part[i].Type != TColon || i+1 >= len(part) {
				return nil, false
			}
			t := part[i+1]
			if t.Type == TIdent {
				switch t.Value {
				case "first":
					ps.First = true
					ps.Specificity[1]++
				case "blank":
					ps.Blank = true
					ps.Specificity[1]++
				case "left", "right":
					ps.Side = t.Value
					ps.Specificity[2]++
				default:
					return nil, false
				}
			} else if t.Type == TFunction && t.Value == "nth" {
				a, b, ok := parseNth(StripWS(t.Children))
				if !ok {
					return nil, false
				}
				ps.Nth = &[2]int{a, b}
				ps.Specificity[1]++
			} else {
				return nil, false
			}
			i += 2
		}
		out = append(out, ps)
	}
	return out, true
}

func parseFontFace(content []Token, baseURL string) *FontFaceRule {
	decls, _ := ParseDeclarations(content)
	ff := &FontFaceRule{Style: "normal", Weight: [2]float64{400, 400}, Stretch: "normal", BaseURL: baseURL}
	ctx := &ParseCtx{BaseURL: baseURL}
	for _, d := range decls {
		v := RemoveWS(d.Value)
		switch d.Name {
		case "font-family":
			if f := parseFontFamily(v, ctx); f != nil {
				ff.Family = f.([]string)[0]
			}
		case "src":
			for _, part := range SplitOnComma(d.Value) {
				part = RemoveWS(part)
				if len(part) == 0 {
					continue
				}
				var src FontSource
				if u, ok := parseURLToken(part[0], ctx); ok {
					src.URL = u
				} else if part[0].Type == TFunction && part[0].Value == "local" {
					args := RemoveWS(part[0].Children)
					var names []string
					for _, a := range args {
						if a.Type == TString {
							names = append(names, a.Value)
						} else if a.Type == TIdent {
							names = append(names, a.Raw)
						}
					}
					src.Local = strings.Join(names, " ")
				} else {
					continue
				}
				for _, t := range part[1:] {
					if t.Type == TFunction && t.Value == "format" {
						args := RemoveWS(t.Children)
						if len(args) > 0 {
							src.Format = strings.ToLower(args[0].Value)
						}
					}
				}
				ff.Sources = append(ff.Sources, src)
			}
		case "font-style":
			if len(v) >= 1 && v[0].Type == TIdent {
				ff.Style = v[0].Value
			}
		case "font-weight":
			var ws []float64
			for _, t := range v {
				if t.Type == TNumber {
					ws = append(ws, t.Num)
				} else if t.IsIdent("bold") {
					ws = append(ws, 700)
				} else if t.IsIdent("normal") {
					ws = append(ws, 400)
				}
			}
			if len(ws) == 1 {
				ff.Weight = [2]float64{ws[0], ws[0]}
			} else if len(ws) == 2 {
				ff.Weight = [2]float64{ws[0], ws[1]}
			}
		case "font-stretch":
			if len(v) >= 1 && v[0].Type == TIdent {
				ff.Stretch = v[0].Value
			}
		case "unicode-range":
			ff.UnicodeRange = parseUnicodeRange(Serialize(d.Value))
		case "font-feature-settings":
			if f, ok := parseFontFeatures(v, ctx).([]FontFeature); ok {
				ff.Features = f
			}
		}
	}
	if ff.Family == "" || len(ff.Sources) == 0 {
		return nil
	}
	return ff
}

func parseUnicodeRange(s string) [][2]rune {
	var out [][2]rune
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		part = strings.TrimPrefix(strings.TrimPrefix(part, "U+"), "u+")
		if part == "" {
			continue
		}
		parseHex := func(h string) (rune, bool) {
			var r rune
			if h == "" {
				return 0, false
			}
			for _, c := range h {
				r <<= 4
				switch {
				case c >= '0' && c <= '9':
					r |= c - '0'
				case c >= 'a' && c <= 'f':
					r |= c - 'a' + 10
				case c >= 'A' && c <= 'F':
					r |= c - 'A' + 10
				default:
					return 0, false
				}
			}
			return r, true
		}
		if i := strings.IndexByte(part, '-'); i >= 0 {
			a, ok1 := parseHex(part[:i])
			b, ok2 := parseHex(part[i+1:])
			if ok1 && ok2 {
				out = append(out, [2]rune{a, b})
			}
		} else if strings.Contains(part, "?") {
			a, ok1 := parseHex(strings.ReplaceAll(part, "?", "0"))
			b, ok2 := parseHex(strings.ReplaceAll(part, "?", "F"))
			if ok1 && ok2 {
				out = append(out, [2]rune{a, b})
			}
		} else if a, ok := parseHex(part); ok {
			out = append(out, [2]rune{a, a})
		}
	}
	return out
}

// EvaluateMediaQuery evaluates a media query list for the environment.
func EvaluateMediaQuery(ts []Token, env *Env) bool {
	ts = StripWS(ts)
	if len(ts) == 0 {
		return true
	}
	media := env.MediaType
	if media == "" {
		media = "print"
	}
	for _, q := range SplitOnComma(ts) {
		if evalSingleMedia(RemoveWS(q), media, env) {
			return true
		}
	}
	return false
}

func evalSingleMedia(q []Token, media string, env *Env) bool {
	if len(q) == 0 {
		return false
	}
	negate := false
	if q[0].IsIdent("not") {
		negate = true
		q = q[1:]
	} else if q[0].IsIdent("only") {
		q = q[1:]
	}
	result := true
	expectType := true
	for i := 0; i < len(q); i++ {
		t := q[i]
		if t.IsIdent("and") {
			expectType = false
			continue
		}
		if t.Type == TIdent && expectType {
			result = result && (t.Value == "all" || t.Value == media)
			expectType = false
			continue
		}
		if t.Type == TParenBlock {
			result = result && evalMediaFeature(t.Children, env)
			expectType = false
			continue
		}
		return false
	}
	if negate {
		return !result
	}
	return result
}

func evalMediaFeature(ts []Token, env *Env) bool {
	ts = RemoveWS(ts)
	if len(ts) == 0 {
		return false
	}
	if ts[0].IsIdent("not") {
		return !evalMediaFeature(ts[1:], env)
	}
	if ts[0].Type != TIdent {
		return false
	}
	name := ts[0].Value
	w, h := env.Viewport[0], env.Viewport[1]
	if w == 0 {
		w, h = 793.7, 1122.5
	}
	if len(ts) == 1 {
		switch name {
		case "color", "grid", "hover", "pointer", "any-hover", "any-pointer", "monochrome", "prefers-reduced-motion":
			return name == "color"
		}
		return true
	}
	if len(ts) < 3 || ts[1].Type != TColon {
		return false
	}
	val := ts[2]
	toPx := func(t Token) (float64, bool) {
		l, ok := parseLengthToken(t, true, false)
		if !ok {
			return 0, false
		}
		if f, ok := unitToPx[l.Unit]; ok {
			return l.V * f, true
		}
		if l.Unit == "em" || l.Unit == "rem" {
			return l.V * 16, true
		}
		return 0, false
	}
	switch name {
	case "width", "min-width", "max-width", "height", "min-height", "max-height":
		v, ok := toPx(val)
		if !ok {
			return false
		}
		ref := w
		if strings.HasSuffix(name, "height") {
			ref = h
		}
		switch {
		case strings.HasPrefix(name, "min-"):
			return ref >= v
		case strings.HasPrefix(name, "max-"):
			return ref <= v
		}
		return ref == v
	case "orientation":
		if w > h {
			return val.IsIdent("landscape")
		}
		return val.IsIdent("portrait")
	case "prefers-color-scheme":
		return val.IsIdent("light")
	case "color", "min-color":
		return true
	}
	return false
}

// DeclsFromString parses and validates a declaration block.
func DeclsFromString(src, baseURL string) []Decl {
	return preprocessDecls(ParseInlineDeclarations(src), &ParseCtx{BaseURL: baseURL}, &Env{})
}

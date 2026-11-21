package css

import "strings"

// Rule is a qualified rule or an at-rule.
type Rule struct {
	AtKeyword string // empty for qualified rules
	Prelude   []Token
	Content   []Token // block content
	HasBlock  bool
}

// Declaration is a property declaration.
type Declaration struct {
	Name      string // lowercase (custom properties keep their case)
	Value     []Token
	Important bool
}

// ParseStylesheetRules parses top-level rules.
func ParseStylesheetRules(ts []Token) []Rule {
	return parseRules(ts, true)
}

// ParseRuleList parses a list of rules inside a block.
func ParseRuleList(ts []Token) []Rule {
	return parseRules(ts, false)
}

func parseRules(ts []Token, top bool) []Rule {
	var rules []Rule
	i := 0
	for i < len(ts) {
		t := ts[i]
		switch t.Type {
		case TWhitespace:
			i++
			continue
		case TCDO, TCDC:
			if top {
				i++
				continue
			}
		case TAtKeyword:
			r := Rule{AtKeyword: t.Value}
			i++
			for i < len(ts) {
				if ts[i].Type == TSemicolon {
					i++
					break
				}
				if ts[i].Type == TCurlyBlock {
					r.Content = ts[i].Children
					r.HasBlock = true
					i++
					break
				}
				r.Prelude = append(r.Prelude, ts[i])
				i++
			}
			rules = append(rules, r)
			continue
		}
		// Qualified rule.
		r := Rule{}
		ok := false
		for i < len(ts) {
			if ts[i].Type == TCurlyBlock {
				r.Content = ts[i].Children
				r.HasBlock = true
				i++
				ok = true
				break
			}
			r.Prelude = append(r.Prelude, ts[i])
			i++
		}
		if ok {
			rules = append(rules, r)
		}
	}
	return rules
}

// ParseDeclarations parses a declaration list, also returning nested
// at-rules (used for @page margin boxes) and nested qualified rules.
func ParseDeclarations(ts []Token) ([]Declaration, []Rule) {
	var decls []Declaration
	var rules []Rule
	i := 0
	for i < len(ts) {
		t := ts[i]
		switch t.Type {
		case TWhitespace, TSemicolon:
			i++
			continue
		case TAtKeyword:
			r := Rule{AtKeyword: t.Value}
			i++
			for i < len(ts) {
				if ts[i].Type == TSemicolon {
					i++
					break
				}
				if ts[i].Type == TCurlyBlock {
					r.Content = ts[i].Children
					r.HasBlock = true
					i++
					break
				}
				r.Prelude = append(r.Prelude, ts[i])
				i++
			}
			rules = append(rules, r)
			continue
		}
		// Collect until semicolon.
		start := i
		for i < len(ts) && ts[i].Type != TSemicolon {
			i++
		}
		part := ts[start:i]
		if d, ok := parseDeclaration(part); ok {
			decls = append(decls, d)
		}
	}
	return decls, rules
}

func parseDeclaration(ts []Token) (Declaration, bool) {
	ts = StripWS(ts)
	if len(ts) < 2 || ts[0].Type != TIdent {
		return Declaration{}, false
	}
	name := ts[0].Raw
	if !strings.HasPrefix(name, "--") {
		name = strings.ToLower(name)
	}
	rest := ts[1:]
	for len(rest) > 0 && rest[0].Type == TWhitespace {
		rest = rest[1:]
	}
	if len(rest) == 0 || rest[0].Type != TColon {
		return Declaration{}, false
	}
	value := rest[1:]
	important := false
	v := StripWS(value)
	if n := len(v); n >= 2 && v[n-1].IsIdent("important") {
		j := n - 2
		for j >= 0 && v[j].Type == TWhitespace {
			j--
		}
		if j >= 0 && v[j].IsDelim("!") {
			important = true
			v = StripWS(v[:j])
		}
	}
	if !strings.HasPrefix(name, "--") {
		value = v
	} else {
		value = v
	}
	return Declaration{Name: name, Value: value, Important: important}, true
}

// ParseInlineDeclarations parses a style attribute.
func ParseInlineDeclarations(src string) []Declaration {
	d, _ := ParseDeclarations(Tokenize(src))
	return d
}

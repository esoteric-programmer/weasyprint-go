package css

import (
	"math"
	"strconv"
	"strings"
)

// CounterStyle is a @counter-style definition.
type CounterStyle struct {
	Name      string
	System    string // cyclic, numeric, alphabetic, symbolic, additive, fixed, extends
	Extends   string
	First     int // for fixed
	Symbols   []string
	Additive  []additiveSymbol
	NegPrefix string
	NegSuffix string
	Prefix    string
	Suffix    string
	Ranges    [][2]float64 // nil means auto
	PadWidth  int
	PadSymbol string
	Fallback  string

	set map[string]bool // which descriptors were set
}

type additiveSymbol struct {
	weight int
	symbol string
}

func symbolValue(t Token) (string, bool) {
	switch t.Type {
	case TString:
		return t.Value, true
	case TIdent:
		return t.Raw, true
	case TDelim:
		return t.Value, true
	case TNumber:
		return t.String(), true
	}
	return "", false
}

// ParseCounterStyle parses a @counter-style rule body.
func ParseCounterStyle(name string, content []Token) *CounterStyle {
	cs := &CounterStyle{Name: name, set: map[string]bool{}}
	decls, _ := ParseDeclarations(content)
	for _, d := range decls {
		v := RemoveWS(d.Value)
		switch d.Name {
		case "system":
			if len(v) >= 1 && v[0].Type == TIdent {
				cs.System = v[0].Value
				if cs.System == "extends" && len(v) == 2 && v[1].Type == TIdent {
					cs.Extends = v[1].Value
				} else if cs.System == "fixed" {
					cs.First = 1
					if len(v) == 2 && v[1].Type == TNumber {
						cs.First = int(v[1].Num)
					}
				}
				cs.set["system"] = true
			}
		case "symbols":
			cs.Symbols = nil
			for _, t := range v {
				if s, ok := symbolValue(t); ok {
					cs.Symbols = append(cs.Symbols, s)
				}
			}
			cs.set["symbols"] = true
		case "additive-symbols":
			cs.Additive = nil
			for _, part := range SplitOnComma(v) {
				part = RemoveWS(part)
				if len(part) == 2 {
					w, s := part[0], part[1]
					if w.Type != TNumber {
						w, s = s, w
					}
					sym, ok := symbolValue(s)
					if w.Type == TNumber && ok {
						cs.Additive = append(cs.Additive, additiveSymbol{int(w.Num), sym})
					}
				}
			}
			cs.set["additive-symbols"] = true
		case "negative":
			if len(v) >= 1 {
				cs.NegPrefix, _ = symbolValue(v[0])
				if len(v) == 2 {
					cs.NegSuffix, _ = symbolValue(v[1])
				}
				cs.set["negative"] = true
			}
		case "prefix":
			if len(v) == 1 {
				cs.Prefix, _ = symbolValue(v[0])
				cs.set["prefix"] = true
			}
		case "suffix":
			if len(v) == 1 {
				cs.Suffix, _ = symbolValue(v[0])
				cs.set["suffix"] = true
			}
		case "range":
			if len(v) == 1 && v[0].IsIdent("auto") {
				cs.Ranges = nil
			} else {
				for _, part := range SplitOnComma(v) {
					part = RemoveWS(part)
					if len(part) != 2 {
						continue
					}
					var r [2]float64
					for i, t := range part {
						if t.IsIdent("infinite") {
							if i == 0 {
								r[i] = math.Inf(-1)
							} else {
								r[i] = math.Inf(1)
							}
						} else if t.Type == TNumber {
							r[i] = t.Num
						}
					}
					cs.Ranges = append(cs.Ranges, r)
				}
			}
			cs.set["range"] = true
		case "pad":
			for _, t := range v {
				if t.Type == TNumber {
					cs.PadWidth = int(t.Num)
				} else if s, ok := symbolValue(t); ok {
					cs.PadSymbol = s
				}
			}
			cs.set["pad"] = true
		case "fallback":
			if len(v) == 1 && v[0].Type == TIdent {
				cs.Fallback = v[0].Value
				cs.set["fallback"] = true
			}
		}
	}
	if cs.System == "" {
		cs.System = "symbolic"
	}
	return cs
}

func parseSymbolsFunction(t Token) *CounterStyle {
	args := RemoveWS(t.Children)
	cs := &CounterStyle{System: "symbolic", Suffix: " ", set: map[string]bool{"suffix": true}}
	if len(args) > 0 && args[0].Type == TIdent {
		cs.System = args[0].Value
		args = args[1:]
	}
	for _, a := range args {
		if a.Type != TString {
			return nil
		}
		cs.Symbols = append(cs.Symbols, a.Value)
	}
	if len(cs.Symbols) == 0 {
		return nil
	}
	if (cs.System == "alphabetic" || cs.System == "numeric") && len(cs.Symbols) < 2 {
		return nil
	}
	return cs
}

// CounterStyles holds the known counter styles.
type CounterStyles map[string]*CounterStyle

func (cs *CounterStyle) resolved(styles CounterStyles, depth int) *CounterStyle {
	if cs.System != "extends" || depth > 10 {
		return cs
	}
	base, ok := styles[cs.Extends]
	if !ok {
		base = styles["decimal"]
	}
	if base == nil {
		return cs
	}
	b := base.resolved(styles, depth+1)
	out := *b
	out.Name = cs.Name
	out.set = map[string]bool{}
	for k := range b.set {
		out.set[k] = true
	}
	for k := range cs.set {
		out.set[k] = true
	}
	for k := range cs.set {
		switch k {
		case "negative":
			out.NegPrefix, out.NegSuffix = cs.NegPrefix, cs.NegSuffix
		case "prefix":
			out.Prefix = cs.Prefix
		case "suffix":
			out.Suffix = cs.Suffix
		case "range":
			out.Ranges = cs.Ranges
		case "pad":
			out.PadWidth, out.PadSymbol = cs.PadWidth, cs.PadSymbol
		case "fallback":
			out.Fallback = cs.Fallback
		}
	}
	return &out
}

func (cs *CounterStyle) inRange(v int) bool {
	if cs.Ranges == nil {
		switch cs.System {
		case "alphabetic", "symbolic":
			return v >= 1
		case "additive":
			return v >= 0
		}
		return true
	}
	for _, r := range cs.Ranges {
		if float64(v) >= r[0] && float64(v) <= r[1] {
			return true
		}
	}
	return false
}

func (cs *CounterStyle) usesNegative() bool {
	switch cs.System {
	case "symbolic", "alphabetic", "numeric", "additive":
		return true
	}
	return false
}

func (cs *CounterStyle) generate(v int) (string, bool) {
	syms := cs.Symbols
	switch cs.System {
	case "cyclic":
		if len(syms) == 0 {
			return "", false
		}
		i := (v - 1) % len(syms)
		if i < 0 {
			i += len(syms)
		}
		return syms[i], true
	case "fixed":
		i := v - cs.First
		if i < 0 || i >= len(syms) {
			return "", false
		}
		return syms[i], true
	case "symbolic":
		if len(syms) == 0 || v < 1 {
			return "", false
		}
		n := len(syms)
		return strings.Repeat(syms[(v-1)%n], (v-1)/n+1), true
	case "alphabetic":
		n := len(syms)
		if n < 2 || v < 1 {
			return "", false
		}
		var out []string
		for v > 0 {
			v--
			out = append([]string{syms[v%n]}, out...)
			v /= n
		}
		return strings.Join(out, ""), true
	case "numeric":
		n := len(syms)
		if n < 2 {
			return "", false
		}
		if v == 0 {
			return syms[0], true
		}
		var out []string
		for v > 0 {
			out = append([]string{syms[v%n]}, out...)
			v /= n
		}
		return strings.Join(out, ""), true
	case "additive":
		if len(cs.Additive) == 0 {
			return "", false
		}
		if v == 0 {
			for _, a := range cs.Additive {
				if a.weight == 0 {
					return a.symbol, true
				}
			}
			return "", false
		}
		var b strings.Builder
		for _, a := range cs.Additive {
			if a.weight == 0 {
				continue
			}
			for v >= a.weight {
				b.WriteString(a.symbol)
				v -= a.weight
			}
		}
		if v != 0 {
			return "", false
		}
		return b.String(), true
	}
	return "", false
}

// Represent renders a counter value with a counter style (without prefix
// and suffix).
func (styles CounterStyles) Represent(v int, style Value) string {
	s, _, _ := styles.represent(v, style, 0)
	return s
}

// Marker renders a counter value as a list marker, with prefix and suffix.
func (styles CounterStyles) Marker(v int, style Value) string {
	s, prefix, suffix := styles.represent(v, style, 0)
	return prefix + s + suffix
}

func (styles CounterStyles) lookup(style Value) *CounterStyle {
	switch st := style.(type) {
	case *CounterStyle:
		return st
	case Keyword:
		if cs, ok := styles[string(st)]; ok {
			return cs.resolved(styles, 0)
		}
	}
	return nil
}

func (styles CounterStyles) represent(v int, style Value, depth int) (string, string, string) {
	if k, ok := style.(Keyword); ok && k == "none" {
		return "", "", ""
	}
	cs := styles.lookup(style)
	if cs == nil || depth > 5 {
		return strconv.Itoa(v), "", ". "
	}
	suffix := ". "
	if cs.set["suffix"] {
		suffix = cs.Suffix
	}
	prefix := cs.Prefix
	fallback := func() (string, string, string) {
		fb := cs.Fallback
		if fb == "" {
			fb = "decimal"
		}
		if fb == cs.Name {
			return strconv.Itoa(v), prefix, suffix
		}
		s, _, _ := styles.represent(v, Keyword(fb), depth+1)
		return s, prefix, suffix
	}
	if !cs.inRange(v) {
		return fallback()
	}
	neg := v < 0 && cs.usesNegative()
	av := v
	if neg {
		av = -v
	}
	s, ok := cs.generate(av)
	if !ok {
		return fallback()
	}
	if cs.PadWidth > 0 {
		n := len([]rune(s))
		if neg {
			n += len([]rune(cs.NegPrefix)) + len([]rune(cs.NegSuffix))
		}
		if n < cs.PadWidth {
			s = strings.Repeat(cs.PadSymbol, cs.PadWidth-n) + s
		}
	}
	if neg {
		np := cs.NegPrefix
		if !cs.set["negative"] {
			np = "-"
		}
		s = np + s + cs.NegSuffix
	}
	return s, prefix, suffix
}

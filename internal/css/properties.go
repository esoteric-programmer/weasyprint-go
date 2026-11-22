package css

import (
	"math"
	"strings"
)

// PropID identifies a longhand property.
type PropID int

type parseFunc func(ts []Token, ctx *ParseCtx) Value

type propDef struct {
	Name      string
	Inherited bool
	Initial   Value
	Parse     parseFunc
}

// Props is the table of supported longhand properties.
var Props []propDef

var propIndex = map[string]PropID{}

func def(name string, inherited bool, initial Value, parse parseFunc) PropID {
	Props = append(Props, propDef{name, inherited, initial, parse})
	id := PropID(len(Props) - 1)
	propIndex[name] = id
	return id
}

// LookupProp returns the ID of a longhand property.
func LookupProp(name string) (PropID, bool) {
	id, ok := propIndex[name]
	return id, ok
}

// Name returns the property name.
func (p PropID) Name() string { return Props[p].Name }

// ------------------------------------------------------------- validators

func kw(words ...string) parseFunc {
	set := map[string]bool{}
	for _, w := range words {
		set[w] = true
	}
	return func(ts []Token, _ *ParseCtx) Value {
		if t, ok := single(ts); ok && t.Type == TIdent && set[t.Value] {
			return Keyword(t.Value)
		}
		return nil
	}
}

func lengthKw(negative, percent bool, words ...string) parseFunc {
	k := kw(words...)
	return func(ts []Token, ctx *ParseCtx) Value {
		if v := k(ts, ctx); v != nil {
			return v
		}
		if t, ok := single(ts); ok {
			if l, ok := parseLengthToken(t, negative, percent); ok {
				return l
			}
		}
		return nil
	}
}

func parseColorProp(ts []Token, _ *ParseCtx) Value {
	if t, ok := single(ts); ok {
		if c, ok := parseColorToken(t); ok {
			return c
		}
	}
	return nil
}

func parseInteger(negative bool, words ...string) parseFunc {
	k := kw(words...)
	return func(ts []Token, ctx *ParseCtx) Value {
		if v := k(ts, ctx); v != nil {
			return v
		}
		if t, ok := single(ts); ok && t.Type == TNumber && t.IsInt && (negative || t.Num >= 0) {
			return Number(t.Num)
		}
		return nil
	}
}

func parseNumber(negative bool, words ...string) parseFunc {
	k := kw(words...)
	return func(ts []Token, ctx *ParseCtx) Value {
		if v := k(ts, ctx); v != nil {
			return v
		}
		if t, ok := single(ts); ok && t.Type == TNumber && (negative || t.Num >= 0) {
			return Number(t.Num)
		}
		return nil
	}
}

func parseOpacity(ts []Token, _ *ParseCtx) Value {
	if t, ok := single(ts); ok {
		if t.Type == TNumber {
			return Number(clamp01(t.Num))
		}
		if t.Type == TPercentage {
			return Number(clamp01(t.Num / 100))
		}
	}
	return nil
}

var borderStyles = []string{"none", "hidden", "dotted", "dashed", "solid", "double", "groove", "ridge", "inset", "outset"}

func parseBorderWidth(ts []Token, ctx *ParseCtx) Value {
	return lengthKw(false, false, "thin", "medium", "thick")(ts, ctx)
}

func parseRadius(ts []Token, _ *ParseCtx) Value {
	if len(ts) == 1 || len(ts) == 2 {
		var ls [2]Length
		for i, t := range ts {
			l, ok := parseLengthToken(t, false, true)
			if !ok {
				return nil
			}
			ls[i] = l
		}
		if len(ts) == 1 {
			ls[1] = ls[0]
		}
		return ls
	}
	return nil
}

var displayValues = map[string]string{
	"none": "none", "inline": "inline", "block": "block", "list-item": "list-item",
	"inline-block": "inline-block", "table": "table", "inline-table": "inline-table",
	"table-row-group": "table-row-group", "table-header-group": "table-header-group",
	"table-footer-group": "table-footer-group", "table-row": "table-row",
	"table-column-group": "table-column-group", "table-column": "table-column",
	"table-cell": "table-cell", "table-caption": "table-caption",
	"flex": "flex", "inline-flex": "inline-flex", "grid": "grid",
	"inline-grid": "inline-grid", "flow-root": "flow-root", "contents": "contents",
	"run-in": "block", "inline-list-item": "inline-list-item",
}

func parseDisplay(ts []Token, _ *ParseCtx) Value {
	if len(ts) == 1 && ts[0].Type == TIdent {
		if d, ok := displayValues[ts[0].Value]; ok {
			return Keyword(d)
		}
		return nil
	}
	// Multi-keyword syntax.
	outer, inner, list := "", "", false
	for _, t := range ts {
		if t.Type != TIdent {
			return nil
		}
		switch t.Value {
		case "block", "inline", "run-in":
			if outer != "" {
				return nil
			}
			outer = t.Value
		case "flow", "flow-root", "table", "flex", "grid":
			if inner != "" {
				return nil
			}
			inner = t.Value
		case "list-item":
			list = true
		default:
			return nil
		}
	}
	if outer == "" {
		outer = "block"
	}
	if outer == "run-in" {
		outer = "block"
	}
	if inner == "" {
		inner = "flow"
	}
	if list {
		if inner != "flow" && inner != "flow-root" {
			return nil
		}
		if outer == "inline" {
			return Keyword("inline-list-item")
		}
		return Keyword("list-item")
	}
	switch inner {
	case "flow":
		return Keyword(outer)
	case "flow-root":
		if outer == "inline" {
			return Keyword("inline-block")
		}
		return Keyword("flow-root")
	default:
		if outer == "inline" {
			return Keyword("inline-" + inner)
		}
		return Keyword(inner)
	}
}

func parsePositionProp(ts []Token, _ *ParseCtx) Value {
	if t, ok := single(ts); ok && t.Type == TIdent {
		switch t.Value {
		case "static", "relative", "absolute", "fixed":
			// sticky is invalid, as in WeasyPrint: pages don't scroll.
			return Keyword(t.Value)
		}
	}
	if t, ok := single(ts); ok && t.Type == TFunction && t.Value == "running" {
		args := RemoveWS(t.Children)
		if len(args) == 1 && args[0].Type == TIdent {
			return Running(args[0].Raw)
		}
	}
	return nil
}

// Running is the value of position: running(name).
type Running string

func parseFontFamily(ts []Token, _ *ParseCtx) Value {
	var out []string
	for _, part := range SplitOnComma(ts) {
		part = RemoveWS(part)
		if len(part) == 0 {
			return nil
		}
		if len(part) == 1 && part[0].Type == TString {
			out = append(out, part[0].Value)
			continue
		}
		var names []string
		for _, t := range part {
			if t.Type != TIdent {
				return nil
			}
			names = append(names, t.Raw)
		}
		out = append(out, strings.Join(names, " "))
	}
	return out
}

func parseFontSize(ts []Token, ctx *ParseCtx) Value {
	return lengthKw(false, true, "xx-small", "x-small", "small", "medium", "large", "x-large", "xx-large", "xxx-large", "larger", "smaller")(ts, ctx)
}

func parseFontWeight(ts []Token, _ *ParseCtx) Value {
	if t, ok := single(ts); ok {
		if t.Type == TIdent {
			switch t.Value {
			case "normal":
				return Number(400)
			case "bold":
				return Number(700)
			case "bolder", "lighter":
				return Keyword(t.Value)
			}
		}
		if t.Type == TNumber && t.Num >= 1 && t.Num <= 1000 {
			return Number(t.Num)
		}
	}
	return nil
}

func parseLineHeight(ts []Token, _ *ParseCtx) Value {
	if t, ok := single(ts); ok {
		if t.IsIdent("normal") {
			return Keyword("normal")
		}
		if t.Type == TNumber && t.Num >= 0 {
			return Number(t.Num)
		}
		if l, ok := parseLengthToken(t, false, true); ok {
			return l
		}
	}
	return nil
}

func parseVerticalAlign(ts []Token, ctx *ParseCtx) Value {
	return lengthKw(true, true, "baseline", "middle", "sub", "super", "text-top", "text-bottom", "top", "bottom")(ts, ctx)
}

func parseSpacing(ts []Token, ctx *ParseCtx) Value {
	return lengthKw(true, true, "normal")(ts, ctx)
}

func parseTextDecorationLine(ts []Token, _ *ParseCtx) Value {
	if t, ok := single(ts); ok && t.IsIdent("none") {
		return Keyword("none")
	}
	var out []string
	seen := map[string]bool{}
	for _, t := range ts {
		if t.Type != TIdent {
			return nil
		}
		switch t.Value {
		case "underline", "overline", "line-through", "blink":
			if seen[t.Value] {
				return nil
			}
			seen[t.Value] = true
			out = append(out, t.Value)
		default:
			return nil
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func parseListStyleType(ts []Token, _ *ParseCtx) Value {
	if t, ok := single(ts); ok {
		switch t.Type {
		case TIdent:
			return Keyword(t.Value)
		case TString:
			return Str(t.Value)
		case TFunction:
			if t.Value == "symbols" {
				if cs := parseSymbolsFunction(t); cs != nil {
					return cs
				}
			}
		}
	}
	return nil
}

func parseImageProp(ts []Token, ctx *ParseCtx) Value {
	if t, ok := single(ts); ok {
		if v, ok := parseImage(t, ctx); ok {
			return v
		}
	}
	return nil
}

func parseContentList(ts []Token, ctx *ParseCtx, allowImages bool) ([]ContentItem, bool) {
	var items []ContentItem
	for _, t := range ts {
		switch t.Type {
		case TString:
			items = append(items, ContentItem{Kind: "string", Str: t.Value})
		case TIdent:
			switch t.Value {
			case "open-quote", "close-quote", "no-open-quote", "no-close-quote":
				items = append(items, ContentItem{Kind: t.Value})
			default:
				return nil, false
			}
		case TURL:
			if !allowImages {
				return nil, false
			}
			u, _ := parseURLToken(t, ctx)
			items = append(items, ContentItem{Kind: "url", Str: u})
		case TFunction:
			args := RemoveWS(t.Children)
			parts := SplitOnComma(RemoveWS(t.Children))
			for i := range parts {
				parts[i] = RemoveWS(parts[i])
			}
			switch t.Value {
			case "url":
				if !allowImages {
					return nil, false
				}
				u, ok := parseURLToken(t, ctx)
				if !ok {
					return nil, false
				}
				items = append(items, ContentItem{Kind: "url", Str: u})
			case "linear-gradient", "radial-gradient", "repeating-linear-gradient", "repeating-radial-gradient":
				img, ok := parseImage(t, ctx)
				if !ok {
					return nil, false
				}
				items = append(items, ContentItem{Kind: "image", Image: img})
			case "attr":
				if len(args) < 1 || args[0].Type != TIdent {
					return nil, false
				}
				items = append(items, ContentItem{Kind: "attr", Name: args[0].Value})
			case "counter":
				if len(parts) < 1 || len(parts) > 2 || len(parts[0]) != 1 || parts[0][0].Type != TIdent {
					return nil, false
				}
				var style Value = Keyword("decimal")
				if len(parts) == 2 {
					style = counterStyleArg(parts[1])
					if style == nil {
						return nil, false
					}
				}
				items = append(items, ContentItem{Kind: "counter", Name: parts[0][0].Raw, Style: style})
			case "counters":
				if len(parts) < 2 || len(parts) > 3 || len(parts[0]) != 1 || parts[0][0].Type != TIdent || len(parts[1]) != 1 || parts[1][0].Type != TString {
					return nil, false
				}
				var style Value = Keyword("decimal")
				if len(parts) == 3 {
					style = counterStyleArg(parts[2])
					if style == nil {
						return nil, false
					}
				}
				items = append(items, ContentItem{Kind: "counters", Name: parts[0][0].Raw, Str: parts[1][0].Value, Style: style})
			case "string":
				if len(parts) < 1 || len(parts[0]) != 1 || parts[0][0].Type != TIdent {
					return nil, false
				}
				which := "first"
				if len(parts) == 2 && len(parts[1]) == 1 && parts[1][0].Type == TIdent {
					which = parts[1][0].Value
				}
				items = append(items, ContentItem{Kind: "string-ref", Name: parts[0][0].Raw, Which: which})
			case "content":
				which := "text"
				if len(args) == 1 && args[0].Type == TIdent {
					which = args[0].Value
				}
				items = append(items, ContentItem{Kind: "content", Which: which})
			case "target-counter", "target-counters", "target-text":
				if len(parts) < 1 {
					return nil, false
				}
				it := ContentItem{Kind: t.Value, Ref: parts[0], Style: Keyword("decimal")}
				switch t.Value {
				case "target-counter":
					if len(parts) < 2 || len(parts[1]) != 1 || parts[1][0].Type != TIdent {
						return nil, false
					}
					it.Name = parts[1][0].Raw
					if len(parts) == 3 {
						it.Style = counterStyleArg(parts[2])
					}
				case "target-counters":
					if len(parts) < 3 || len(parts[1]) != 1 || len(parts[2]) != 1 || parts[2][0].Type != TString {
						return nil, false
					}
					it.Name = parts[1][0].Raw
					it.Str = parts[2][0].Value
					if len(parts) == 4 {
						it.Style = counterStyleArg(parts[3])
					}
				case "target-text":
					it.Which = "content"
					if len(parts) == 2 && len(parts[1]) == 1 {
						it.Which = parts[1][0].Value
					}
				}
				if it.Style == nil {
					return nil, false
				}
				items = append(items, it)
			case "leader":
				if len(args) != 1 {
					return nil, false
				}
				s := ""
				if args[0].Type == TString {
					s = args[0].Value
				} else if args[0].Type == TIdent {
					switch args[0].Value {
					case "dotted":
						s = "."
					case "solid":
						s = "_"
					case "space":
						s = " "
					default:
						return nil, false
					}
				}
				items = append(items, ContentItem{Kind: "leader", Str: s})
			case "element":
				if len(parts) < 1 || len(parts[0]) != 1 || parts[0][0].Type != TIdent {
					return nil, false
				}
				which := "first"
				if len(parts) == 2 && len(parts[1]) == 1 {
					which = parts[1][0].Value
				}
				items = append(items, ContentItem{Kind: "element", Name: parts[0][0].Raw, Which: which})
			default:
				return nil, false
			}
		default:
			return nil, false
		}
	}
	return items, len(items) > 0
}

func counterStyleArg(ts []Token) Value {
	if len(ts) != 1 {
		return nil
	}
	if ts[0].Type == TIdent && ts[0].Value != "none" {
		return Keyword(ts[0].Value)
	}
	if ts[0].IsIdent("none") {
		return Keyword("none")
	}
	if ts[0].Type == TFunction && ts[0].Value == "symbols" {
		if cs := parseSymbolsFunction(ts[0]); cs != nil {
			return cs
		}
	}
	return nil
}

func parseContent(ts []Token, ctx *ParseCtx) Value {
	if t, ok := single(ts); ok && t.Type == TIdent && (t.Value == "normal" || t.Value == "none") {
		return Keyword(t.Value)
	}
	// Alternative text after "/" is ignored.
	for i, t := range ts {
		if t.IsDelim("/") {
			ts = ts[:i]
			break
		}
	}
	if items, ok := parseContentList(ts, ctx, true); ok {
		return items
	}
	return nil
}

func parseCounters(defaultValue int) parseFunc {
	return func(ts []Token, _ *ParseCtx) Value {
		if t, ok := single(ts); ok && t.IsIdent("none") {
			return Keyword("none")
		}
		var ops []CounterOp
		for i := 0; i < len(ts); i++ {
			if ts[i].Type != TIdent || ts[i].Value == "none" || ts[i].Value == "initial" || ts[i].Value == "inherit" {
				return nil
			}
			op := CounterOp{Name: ts[i].Raw, Value: defaultValue}
			if i+1 < len(ts) && ts[i+1].Type == TNumber && ts[i+1].IsInt {
				op.Value = int(ts[i+1].Num)
				i++
			}
			ops = append(ops, op)
		}
		if len(ops) == 0 {
			return nil
		}
		return ops
	}
}

func parseQuotes(ts []Token, _ *ParseCtx) Value {
	if t, ok := single(ts); ok && t.Type == TIdent && (t.Value == "none" || t.Value == "auto") {
		return Keyword(t.Value)
	}
	if len(ts) == 0 || len(ts)%2 != 0 {
		return nil
	}
	var out []string
	for _, t := range ts {
		if t.Type != TString {
			return nil
		}
		out = append(out, t.Value)
	}
	return out
}

func parseBorderSpacing(ts []Token, _ *ParseCtx) Value {
	if len(ts) == 1 || len(ts) == 2 {
		var ls [2]Length
		for i, t := range ts {
			l, ok := parseLengthToken(t, false, false)
			if !ok {
				return nil
			}
			ls[i] = l
		}
		if len(ts) == 1 {
			ls[1] = ls[0]
		}
		return ls
	}
	return nil
}

// PageSize is the computed value of the size descriptor.
type PageSize [2]Length

var pageSizes = map[string][2]float64{
	// in mm, portrait (the sizes of WeasyPrint)
	"a10": {26, 37}, "a9": {37, 52}, "a8": {52, 74}, "a7": {74, 105},
	"a6": {105, 148}, "a5": {148, 210}, "a4": {210, 297}, "a3": {297, 420},
	"a2": {420, 594}, "a1": {594, 841}, "a0": {841, 1189}, "b10": {31, 44},
	"b9": {44, 62}, "b8": {62, 88}, "b7": {88, 125}, "b6": {125, 176},
	"b5": {176, 250}, "b4": {250, 353}, "b3": {353, 500}, "b2": {500, 707},
	"b1": {707, 1000}, "b0": {1000, 1414}, "c10": {28, 40}, "c9": {40, 57},
	"c8": {57, 81}, "c7": {81, 114}, "c6": {114, 162}, "c5": {162, 229},
	"c4": {229, 324}, "c3": {324, 458}, "c2": {458, 648}, "c1": {648, 917},
	"c0": {917, 1297}, "jis-b10": {32, 45}, "jis-b9": {45, 64},
	"jis-b8": {64, 91}, "jis-b7": {91, 128}, "jis-b6": {128, 182},
	"jis-b5": {182, 257}, "jis-b4": {257, 364}, "jis-b3": {364, 515},
	"jis-b2": {515, 728}, "jis-b1": {728, 1030}, "jis-b0": {1030, 1456},
	"letter": {215.9, 279.4}, "legal": {215.9, 355.6}, "ledger": {279.4, 431.8},
}

func parseSize(ts []Token, _ *ParseCtx) Value {
	var lens []Length
	var named string
	orient := ""
	for _, t := range ts {
		if l, ok := parseLengthToken(t, false, false); ok {
			lens = append(lens, l)
			continue
		}
		if t.Type != TIdent {
			return nil
		}
		switch t.Value {
		case "auto":
			if len(ts) != 1 {
				return nil
			}
			return Keyword("auto")
		case "portrait", "landscape":
			if orient != "" {
				return nil
			}
			orient = t.Value
		default:
			if _, ok := pageSizes[t.Value]; ok && named == "" {
				named = t.Value
			} else {
				return nil
			}
		}
	}
	var w, h Length
	switch {
	case len(lens) == 1 && named == "" && orient == "":
		w, h = lens[0], lens[0]
	case len(lens) == 2 && named == "" && orient == "":
		w, h = lens[0], lens[1]
	case len(lens) == 0:
		if named == "" {
			named = "a4"
		}
		s := pageSizes[named]
		w, h = Length{V: s[0], Unit: "mm"}, Length{V: s[1], Unit: "mm"}
		if orient == "landscape" {
			w, h = h, w
		}
	default:
		return nil
	}
	return PageSize{w, h}
}

func parseStringSet(ts []Token, ctx *ParseCtx) Value {
	if t, ok := single(ts); ok && t.IsIdent("none") {
		return Keyword("none")
	}
	var out []StringSet
	for _, part := range SplitOnComma(ts) {
		part = RemoveWS(part)
		if len(part) < 2 || part[0].Type != TIdent {
			return nil
		}
		items, ok := parseContentList(part[1:], ctx, false)
		if !ok {
			return nil
		}
		out = append(out, StringSet{Name: part[0].Raw, Content: items})
	}
	return out
}

func parseBookmarkLabel(ts []Token, ctx *ParseCtx) Value {
	if t, ok := single(ts); ok && t.IsIdent("none") {
		return Keyword("none")
	}
	if items, ok := parseContentList(ts, ctx, false); ok {
		return items
	}
	return nil
}

func parseLink(ts []Token, ctx *ParseCtx) Value {
	if t, ok := single(ts); ok {
		if t.IsIdent("none") {
			return Keyword("none")
		}
		if u, ok := parseURLToken(t, ctx); ok {
			return URL(u)
		}
		if t.Type == TFunction && t.Value == "attr" {
			args := RemoveWS(t.Children)
			if len(args) >= 1 && args[0].Type == TIdent {
				return ContentItem{Kind: "attr", Name: args[0].Value}
			}
		}
	}
	return nil
}

func parseAnchor(ts []Token, ctx *ParseCtx) Value {
	if t, ok := single(ts); ok {
		if t.IsIdent("none") {
			return Keyword("none")
		}
		if t.Type == TIdent {
			return Str(t.Raw)
		}
		if t.Type == TFunction && t.Value == "attr" {
			args := RemoveWS(t.Children)
			if len(args) >= 1 && args[0].Type == TIdent {
				return ContentItem{Kind: "attr", Name: args[0].Value}
			}
		}
	}
	return nil
}

func parseBreak(words ...string) parseFunc { return kw(words...) }

func parsePage(ts []Token, _ *ParseCtx) Value {
	if t, ok := single(ts); ok && t.Type == TIdent {
		if t.Value == "auto" {
			return Keyword("auto")
		}
		return Str(t.Raw)
	}
	return nil
}

func parseTransform(ts []Token, _ *ParseCtx) Value {
	if t, ok := single(ts); ok && t.IsIdent("none") {
		return Keyword("none")
	}
	var out []Transform
	for _, t := range ts {
		if t.Type != TFunction {
			return nil
		}
		var args []Value
		for _, part := range SplitOnComma(t.Children) {
			for _, a := range RemoveWS(part) {
				if a.Type == TNumber {
					args = append(args, Number(a.Num))
				} else if ang, ok := angleToRad(a); ok && a.Type == TDimension && !lengthUnits[a.Value] {
					args = append(args, Number(ang))
				} else if l, ok := parseLengthToken(a, true, true); ok {
					args = append(args, l)
				} else {
					return nil
				}
			}
		}
		name := t.Value
		switch name {
		case "rotate", "skewx", "skewy":
			if len(args) != 1 {
				return nil
			}
			if _, ok := args[0].(Number); !ok {
				if l, ok := args[0].(Length); !ok || l.V != 0 {
					return nil
				}
				args[0] = Number(0)
			}
		case "skew":
			if len(args) < 1 || len(args) > 2 {
				return nil
			}
		case "translate", "translatex", "translatey", "scale", "scalex", "scaley", "matrix":
		default:
			return nil
		}
		out = append(out, Transform{Name: name, Args: args})
	}
	return out
}

func parsePositionList(ts []Token, _ *ParseCtx) Value {
	var out []Position
	for _, part := range SplitOnComma(ts) {
		p, ok := parsePosition(part)
		if !ok {
			return nil
		}
		out = append(out, p)
	}
	return out
}

func parseSinglePosition(ts []Token, _ *ParseCtx) Value {
	p, ok := parsePosition(ts)
	if !ok {
		return nil
	}
	return p
}

func parseBgImage(ts []Token, ctx *ParseCtx) Value {
	var out []Value
	for _, part := range SplitOnComma(ts) {
		part = RemoveWS(part)
		if len(part) != 1 {
			return nil
		}
		v, ok := parseImage(part[0], ctx)
		if !ok {
			return nil
		}
		out = append(out, v)
	}
	return out
}

func parseRepeatSingle(ts []Token) (Repeat, bool) {
	if len(ts) == 1 && ts[0].Type == TIdent {
		switch ts[0].Value {
		case "repeat-x":
			return Repeat{"repeat", "no-repeat"}, true
		case "repeat-y":
			return Repeat{"no-repeat", "repeat"}, true
		case "repeat", "no-repeat", "space", "round":
			return Repeat{ts[0].Value, ts[0].Value}, true
		}
		return Repeat{}, false
	}
	if len(ts) == 2 {
		var r Repeat
		for i, t := range ts {
			if t.Type != TIdent {
				return r, false
			}
			switch t.Value {
			case "repeat", "no-repeat", "space", "round":
				r[i] = t.Value
			default:
				return r, false
			}
		}
		return r, true
	}
	return Repeat{}, false
}

func parseBgRepeat(ts []Token, _ *ParseCtx) Value {
	var out []Repeat
	for _, part := range SplitOnComma(ts) {
		r, ok := parseRepeatSingle(RemoveWS(part))
		if !ok {
			return nil
		}
		out = append(out, r)
	}
	return out
}

func parseSizeSingle(ts []Token) (BgSize, bool) {
	if len(ts) == 1 && ts[0].Type == TIdent && (ts[0].Value == "cover" || ts[0].Value == "contain") {
		return BgSize{Keyword: ts[0].Value}, true
	}
	if len(ts) == 1 || len(ts) == 2 {
		var vals [2]Value
		for i, t := range ts {
			if t.IsIdent("auto") {
				vals[i] = Keyword("auto")
			} else if l, ok := parseLengthToken(t, false, true); ok {
				vals[i] = l
			} else {
				return BgSize{}, false
			}
		}
		if len(ts) == 1 {
			vals[1] = Keyword("auto")
		}
		return BgSize{W: vals[0], H: vals[1]}, true
	}
	return BgSize{}, false
}

func parseBgSize(ts []Token, _ *ParseCtx) Value {
	var out []BgSize
	for _, part := range SplitOnComma(ts) {
		s, ok := parseSizeSingle(RemoveWS(part))
		if !ok {
			return nil
		}
		out = append(out, s)
	}
	return out
}

func parseKwList(words ...string) parseFunc {
	set := map[string]bool{}
	for _, w := range words {
		set[w] = true
	}
	return func(ts []Token, _ *ParseCtx) Value {
		var out []string
		for _, part := range SplitOnComma(ts) {
			part = RemoveWS(part)
			if len(part) != 1 || part[0].Type != TIdent || !set[part[0].Value] {
				return nil
			}
			out = append(out, part[0].Value)
		}
		return out
	}
}

func parseShadows(ts []Token, _ *ParseCtx) Value {
	if t, ok := single(ts); ok && t.IsIdent("none") {
		return Keyword("none")
	}
	var out []Shadow
	for _, part := range SplitOnComma(ts) {
		part = RemoveWS(part)
		var s Shadow
		s.Color = Keyword("currentcolor")
		var lens []Length
		for _, t := range part {
			if t.IsIdent("inset") {
				s.Inset = true
			} else if l, ok := parseLengthToken(t, true, false); ok {
				lens = append(lens, l)
			} else if c, ok := parseColorToken(t); ok {
				s.Color = c
			} else {
				return nil
			}
		}
		if len(lens) < 2 || len(lens) > 4 {
			return nil
		}
		s.X, s.Y = lens[0], lens[1]
		if len(lens) > 2 {
			s.Blur = lens[2]
		} else {
			s.Blur = Px(0)
		}
		if len(lens) > 3 {
			s.Spread = lens[3]
		} else {
			s.Spread = Px(0)
		}
		out = append(out, s)
	}
	return out
}

func parseTextAlign(ts []Token, ctx *ParseCtx) Value {
	return kw("left", "right", "center", "justify", "start", "end", "match-parent", "justify-all")(ts, ctx)
}

func parseFlexBasis(ts []Token, ctx *ParseCtx) Value {
	return lengthKw(false, true, "auto", "content")(ts, ctx)
}

func parseGap(ts []Token, ctx *ParseCtx) Value {
	return lengthKw(false, true, "normal")(ts, ctx)
}

func parseAlign(words ...string) parseFunc {
	set := map[string]bool{}
	for _, w := range words {
		set[w] = true
	}
	return func(ts []Token, _ *ParseCtx) Value {
		var parts []string
		for _, t := range ts {
			if t.Type != TIdent {
				return nil
			}
			parts = append(parts, t.Value)
		}
		if len(parts) == 0 {
			return nil
		}
		// Drop overflow/baseline position modifiers.
		last := parts[len(parts)-1]
		if len(parts) > 1 && (parts[0] == "safe" || parts[0] == "unsafe" || parts[0] == "first" || parts[0] == "last") {
			if parts[0] == "last" {
				last = "baseline"
			}
		} else if len(parts) > 1 {
			return nil
		}
		if !set[last] {
			return nil
		}
		return Keyword(last)
	}
}

func parseFontFeatures(ts []Token, _ *ParseCtx) Value {
	if t, ok := single(ts); ok && t.IsIdent("normal") {
		return Keyword("normal")
	}
	var out []FontFeature
	for _, part := range SplitOnComma(ts) {
		part = RemoveWS(part)
		if len(part) < 1 || part[0].Type != TString || len(part[0].Value) != 4 {
			return nil
		}
		f := FontFeature{Tag: part[0].Value, Value: 1}
		if len(part) == 2 {
			if part[1].Type == TNumber && part[1].IsInt {
				f.Value = int(part[1].Num)
			} else if part[1].IsIdent("on") {
				f.Value = 1
			} else if part[1].IsIdent("off") {
				f.Value = 0
			} else {
				return nil
			}
		}
		out = append(out, f)
	}
	return out
}

func parseFontVariant(ts []Token, _ *ParseCtx) Value {
	// Store as a list of keywords; only small-caps has an effect.
	var out []string
	for _, t := range ts {
		if t.Type != TIdent {
			return nil
		}
		out = append(out, t.Value)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func parseTabSize(ts []Token, _ *ParseCtx) Value {
	if t, ok := single(ts); ok {
		if t.Type == TNumber && t.Num >= 0 {
			return Number(t.Num)
		}
		if l, ok := parseLengthToken(t, false, false); ok {
			return l
		}
	}
	return nil
}

func parseHyphenateCharacter(ts []Token, _ *ParseCtx) Value {
	if t, ok := single(ts); ok {
		if t.IsIdent("auto") {
			return Str("‐")
		}
		if t.Type == TString {
			return Str(t.Value)
		}
	}
	return nil
}

// LimitChars is the computed value of hyphenate-limit-chars: minimum
// word length, and minimum characters before and after the hyphen.
type LimitChars [3]int

func parseHyphenateLimitChars(ts []Token, _ *ParseCtx) Value {
	ts = RemoveWS(ts)
	if len(ts) == 0 || len(ts) > 3 {
		return nil
	}
	defaults := []int{5, 2, 2}
	var vals []int
	for i, t := range ts {
		switch {
		case t.IsIdent("auto"):
			vals = append(vals, defaults[i])
		case t.Type == TNumber && t.IsInt:
			vals = append(vals, int(t.Num))
		default:
			return nil
		}
	}
	switch len(vals) {
	case 1:
		return LimitChars{vals[0], 2, 2}
	case 2:
		return LimitChars{vals[0], vals[1], vals[1]}
	}
	return LimitChars{vals[0], vals[1], vals[2]}
}

func parseColumns(ts []Token, ctx *ParseCtx) Value {
	return lengthKw(false, false, "auto")(ts, ctx)
}

// ------------------------------------------------------------- properties

var (
	PDisplay    = def("display", false, Keyword("inline"), parseDisplay)
	PPosition   = def("position", false, Keyword("static"), parsePositionProp)
	PTop        = def("top", false, Keyword("auto"), lengthKw(true, true, "auto"))
	PRight      = def("right", false, Keyword("auto"), lengthKw(true, true, "auto"))
	PBottom     = def("bottom", false, Keyword("auto"), lengthKw(true, true, "auto"))
	PLeft       = def("left", false, Keyword("auto"), lengthKw(true, true, "auto"))
	PFloat      = def("float", false, Keyword("none"), kw("none", "left", "right", "footnote", "inline-start", "inline-end"))
	PClear      = def("clear", false, Keyword("none"), kw("none", "left", "right", "both", "inline-start", "inline-end"))
	PZIndex     = def("z-index", false, Keyword("auto"), parseInteger(true, "auto"))
	PVisibility = def("visibility", true, Keyword("visible"), kw("visible", "hidden", "collapse"))
	POverflow   = def("overflow", false, Keyword("visible"), kw("visible", "hidden", "scroll", "auto", "clip"))
	PBoxSizing  = def("box-sizing", false, Keyword("content-box"), kw("content-box", "border-box", "padding-box"))

	PWidth     = def("width", false, Keyword("auto"), lengthKw(false, true, "auto", "min-content", "max-content", "fit-content"))
	PHeight    = def("height", false, Keyword("auto"), lengthKw(false, true, "auto", "min-content", "max-content", "fit-content"))
	PMinWidth  = def("min-width", false, Keyword("auto"), lengthKw(false, true, "auto", "min-content", "max-content", "fit-content"))
	PMinHeight = def("min-height", false, Keyword("auto"), lengthKw(false, true, "auto", "min-content", "max-content", "fit-content"))
	PMaxWidth  = def("max-width", false, Keyword("none"), lengthKw(false, true, "none", "min-content", "max-content", "fit-content"))
	PMaxHeight = def("max-height", false, Keyword("none"), lengthKw(false, true, "none", "min-content", "max-content", "fit-content"))

	PMarginTop    = def("margin-top", false, Px(0), lengthKw(true, true, "auto"))
	PMarginRight  = def("margin-right", false, Px(0), lengthKw(true, true, "auto"))
	PMarginBottom = def("margin-bottom", false, Px(0), lengthKw(true, true, "auto"))
	PMarginLeft   = def("margin-left", false, Px(0), lengthKw(true, true, "auto"))

	PPaddingTop    = def("padding-top", false, Px(0), lengthKw(false, true))
	PPaddingRight  = def("padding-right", false, Px(0), lengthKw(false, true))
	PPaddingBottom = def("padding-bottom", false, Px(0), lengthKw(false, true))
	PPaddingLeft   = def("padding-left", false, Px(0), lengthKw(false, true))

	PBorderTopWidth    = def("border-top-width", false, Px(3), parseBorderWidth)
	PBorderRightWidth  = def("border-right-width", false, Px(3), parseBorderWidth)
	PBorderBottomWidth = def("border-bottom-width", false, Px(3), parseBorderWidth)
	PBorderLeftWidth   = def("border-left-width", false, Px(3), parseBorderWidth)
	PBorderTopStyle    = def("border-top-style", false, Keyword("none"), kw(borderStyles...))
	PBorderRightStyle  = def("border-right-style", false, Keyword("none"), kw(borderStyles...))
	PBorderBottomStyle = def("border-bottom-style", false, Keyword("none"), kw(borderStyles...))
	PBorderLeftStyle   = def("border-left-style", false, Keyword("none"), kw(borderStyles...))
	PBorderTopColor    = def("border-top-color", false, Keyword("currentcolor"), parseColorProp)
	PBorderRightColor  = def("border-right-color", false, Keyword("currentcolor"), parseColorProp)
	PBorderBottomColor = def("border-bottom-color", false, Keyword("currentcolor"), parseColorProp)
	PBorderLeftColor   = def("border-left-color", false, Keyword("currentcolor"), parseColorProp)

	PBorderTopLeftRadius     = def("border-top-left-radius", false, [2]Length{Px(0), Px(0)}, parseRadius)
	PBorderTopRightRadius    = def("border-top-right-radius", false, [2]Length{Px(0), Px(0)}, parseRadius)
	PBorderBottomRightRadius = def("border-bottom-right-radius", false, [2]Length{Px(0), Px(0)}, parseRadius)
	PBorderBottomLeftRadius  = def("border-bottom-left-radius", false, [2]Length{Px(0), Px(0)}, parseRadius)

	POutlineWidth  = def("outline-width", false, Px(3), parseBorderWidth)
	POutlineStyle  = def("outline-style", false, Keyword("none"), kw(append(borderStyles, "auto")...))
	POutlineColor  = def("outline-color", false, Keyword("currentcolor"), parseColorProp)
	POutlineOffset = def("outline-offset", false, Px(0), lengthKw(true, false))

	PColor   = def("color", true, Color{R: 0, G: 0, B: 0, A: 1}, parseColorProp)
	POpacity = def("opacity", false, Number(1), parseOpacity)

	PBackgroundColor      = def("background-color", false, Transparent, parseColorProp)
	PBackgroundImage      = def("background-image", false, []Value{Keyword("none")}, parseBgImage)
	PBackgroundRepeat     = def("background-repeat", false, []Repeat{{"repeat", "repeat"}}, parseBgRepeat)
	PBackgroundAttachment = def("background-attachment", false, []string{"scroll"}, parseKwList("scroll", "fixed", "local"))
	PBackgroundPosition   = def("background-position", false, []Position{{"left", Percent(0), "top", Percent(0)}}, parsePositionList)
	PBackgroundSize       = def("background-size", false, []BgSize{{W: Keyword("auto"), H: Keyword("auto")}}, parseBgSize)
	PBackgroundClip       = def("background-clip", false, []string{"border-box"}, parseKwList("border-box", "padding-box", "content-box", "text"))
	PBackgroundOrigin     = def("background-origin", false, []string{"padding-box"}, parseKwList("border-box", "padding-box", "content-box"))
	PBoxShadow            = def("box-shadow", false, Keyword("none"), parseShadows)

	PFontFamily          = def("font-family", true, []string{"serif"}, parseFontFamily)
	PFontSize            = def("font-size", true, Px(16), parseFontSize)
	PFontStyle           = def("font-style", true, Keyword("normal"), kw("normal", "italic", "oblique"))
	PFontWeight          = def("font-weight", true, Number(400), parseFontWeight)
	PFontStretch         = def("font-stretch", true, Keyword("normal"), kw("normal", "ultra-condensed", "extra-condensed", "condensed", "semi-condensed", "semi-expanded", "expanded", "extra-expanded", "ultra-expanded"))
	PFontVariantCaps     = def("font-variant-caps", true, Keyword("normal"), kw("normal", "small-caps", "all-small-caps", "petite-caps", "all-petite-caps", "unicase", "titling-caps"))
	PFontVariantNumeric  = def("font-variant-numeric", true, Keyword("normal"), parseFontVariant)
	PFontFeatureSettings = def("font-feature-settings", true, Keyword("normal"), parseFontFeatures)
	PFontKerning         = def("font-kerning", true, Keyword("auto"), kw("auto", "normal", "none"))
	PLineHeight          = def("line-height", true, Keyword("normal"), parseLineHeight)

	PTextAlignAll            = def("text-align-all", true, Keyword("start"), parseTextAlign)
	PTextAlignLast           = def("text-align-last", true, Keyword("auto"), kw("auto", "left", "right", "center", "justify", "start", "end", "match-parent"))
	PTextIndent              = def("text-indent", true, Px(0), lengthKw(true, true))
	PTextTransform           = def("text-transform", true, Keyword("none"), kw("none", "capitalize", "uppercase", "lowercase", "full-width"))
	PTextDecorationLine      = def("text-decoration-line", false, Keyword("none"), parseTextDecorationLine)
	PTextDecorationColor     = def("text-decoration-color", false, Keyword("currentcolor"), parseColorProp)
	PTextDecorationStyle     = def("text-decoration-style", false, Keyword("solid"), kw("solid", "double", "dotted", "dashed", "wavy"))
	PTextDecorationThickness = def("text-decoration-thickness", false, Keyword("auto"), lengthKw(false, true, "auto", "from-font"))
	PTextUnderlineOffset     = def("text-underline-offset", true, Keyword("auto"), lengthKw(true, true, "auto"))
	PTextShadow              = def("text-shadow", true, Keyword("none"), parseShadows)
	PTextOverflow            = def("text-overflow", false, Keyword("clip"), kw("clip", "ellipsis"))
	PLetterSpacing           = def("letter-spacing", true, Keyword("normal"), parseSpacing)
	PWordSpacing             = def("word-spacing", true, Keyword("normal"), parseSpacing)
	PWhiteSpace              = def("white-space", true, Keyword("normal"), kw("normal", "pre", "nowrap", "pre-wrap", "pre-line", "break-spaces"))
	PVerticalAlign           = def("vertical-align", false, Keyword("baseline"), parseVerticalAlign)
	PDirection               = def("direction", true, Keyword("ltr"), kw("ltr", "rtl"))
	PUnicodeBidi             = def("unicode-bidi", false, Keyword("normal"), kw("normal", "embed", "isolate", "bidi-override", "isolate-override", "plaintext"))
	PHyphens                 = def("hyphens", true, Keyword("manual"), kw("none", "manual", "auto"))
	PHyphenateCharacter      = def("hyphenate-character", true, Str("‐"), parseHyphenateCharacter)
	POverflowWrap            = def("overflow-wrap", true, Keyword("normal"), kw("normal", "break-word", "anywhere"))
	PWordBreak               = def("word-break", true, Keyword("normal"), kw("normal", "break-all", "keep-all", "break-word"))
	PTabSize                 = def("tab-size", true, Number(8), parseTabSize)
	PHyphenateLimitZone      = def("hyphenate-limit-zone", true, Px(0), lengthKw(false, true))
	PHyphenateLimitChars     = def("hyphenate-limit-chars", true, LimitChars{5, 2, 2}, parseHyphenateLimitChars)
	PLang                    = def("lang", true, Keyword("none"), func(ts []Token, _ *ParseCtx) Value {
		if t, ok := single(ts); ok && t.Type == TString {
			return Str(t.Value)
		}
		if t, ok := single(ts); ok && t.Type == TFunction && t.Value == "attr" {
			args := RemoveWS(t.Children)
			if len(args) >= 1 && args[0].Type == TIdent {
				return ContentItem{Kind: "attr", Name: args[0].Value}
			}
		}
		return nil
	})

	PListStyleType     = def("list-style-type", true, Keyword("disc"), parseListStyleType)
	PListStylePosition = def("list-style-position", true, Keyword("outside"), kw("inside", "outside"))
	PListStyleImage    = def("list-style-image", true, Keyword("none"), parseImageProp)

	PContent          = def("content", false, Keyword("normal"), parseContent)
	PQuotes           = def("quotes", true, Keyword("auto"), parseQuotes)
	PCounterReset     = def("counter-reset", false, Keyword("none"), parseCounters(0))
	PCounterIncrement = def("counter-increment", false, Keyword("none"), parseCounters(1))
	PCounterSet       = def("counter-set", false, Keyword("none"), parseCounters(0))

	PBreakBefore        = def("break-before", false, Keyword("auto"), parseBreak("auto", "avoid", "avoid-page", "page", "left", "right", "recto", "verso", "avoid-column", "column", "always"))
	PBreakAfter         = def("break-after", false, Keyword("auto"), parseBreak("auto", "avoid", "avoid-page", "page", "left", "right", "recto", "verso", "avoid-column", "column", "always"))
	PBreakInside        = def("break-inside", false, Keyword("auto"), parseBreak("auto", "avoid", "avoid-page", "avoid-column"))
	POrphans            = def("orphans", true, Number(2), parseInteger(false))
	PWidows             = def("widows", true, Number(2), parseInteger(false))
	PPage               = def("page", false, Keyword("auto"), parsePage)
	PBoxDecorationBreak = def("box-decoration-break", false, Keyword("slice"), kw("slice", "clone"))
	PMaxLines           = def("max-lines", false, Keyword("none"), parseInteger(false, "none"))
	PContinue           = def("continue", false, Keyword("auto"), kw("auto", "discard"))

	PBorderCollapse = def("border-collapse", true, Keyword("separate"), kw("separate", "collapse"))
	PBorderSpacing  = def("border-spacing", true, [2]Length{Px(0), Px(0)}, parseBorderSpacing)
	PTableLayout    = def("table-layout", false, Keyword("auto"), kw("auto", "fixed"))
	PCaptionSide    = def("caption-side", true, Keyword("top"), kw("top", "bottom"))
	PEmptyCells     = def("empty-cells", true, Keyword("show"), kw("show", "hide"))

	PFlexDirection  = def("flex-direction", false, Keyword("row"), kw("row", "row-reverse", "column", "column-reverse"))
	PFlexWrap       = def("flex-wrap", false, Keyword("nowrap"), kw("nowrap", "wrap", "wrap-reverse"))
	PFlexGrow       = def("flex-grow", false, Number(0), parseNumber(false))
	PFlexShrink     = def("flex-shrink", false, Number(1), parseNumber(false))
	PFlexBasis      = def("flex-basis", false, Keyword("auto"), parseFlexBasis)
	PJustifyContent = def("justify-content", false, Keyword("normal"), parseAlign("normal", "flex-start", "flex-end", "center", "space-between", "space-around", "space-evenly", "start", "end", "left", "right", "stretch"))
	PJustifyItems   = def("justify-items", false, Keyword("normal"), parseAlign("normal", "stretch", "start", "end", "center", "flex-start", "flex-end", "left", "right", "baseline", "legacy", "self-start", "self-end"))
	PJustifySelf    = def("justify-self", false, Keyword("auto"), parseAlign("auto", "normal", "stretch", "start", "end", "center", "flex-start", "flex-end", "left", "right", "baseline", "self-start", "self-end"))
	PAlignItems     = def("align-items", false, Keyword("normal"), parseAlign("normal", "stretch", "flex-start", "flex-end", "center", "baseline", "start", "end", "self-start", "self-end"))
	PAlignSelf      = def("align-self", false, Keyword("auto"), parseAlign("auto", "normal", "stretch", "flex-start", "flex-end", "center", "baseline", "start", "end", "self-start", "self-end"))
	PAlignContent   = def("align-content", false, Keyword("normal"), parseAlign("normal", "flex-start", "flex-end", "center", "space-between", "space-around", "space-evenly", "stretch", "start", "end", "baseline"))
	POrder          = def("order", false, Number(0), parseInteger(true))
	PRowGap         = def("row-gap", false, Keyword("normal"), parseGap)
	PColumnGap      = def("column-gap", false, Keyword("normal"), parseGap)

	PColumnCount     = def("column-count", false, Keyword("auto"), parseInteger(false, "auto"))
	PColumnWidth     = def("column-width", false, Keyword("auto"), parseColumns)
	PColumnFill      = def("column-fill", false, Keyword("balance"), kw("auto", "balance"))
	PColumnSpan      = def("column-span", false, Keyword("none"), kw("none", "all"))
	PColumnRuleWidth = def("column-rule-width", false, Px(3), parseBorderWidth)
	PColumnRuleStyle = def("column-rule-style", false, Keyword("none"), kw(borderStyles...))
	PColumnRuleColor = def("column-rule-color", false, Keyword("currentcolor"), parseColorProp)

	PSize = def("size", false, Keyword("auto"), parseSize)

	PBookmarkLevel = def("bookmark-level", false, Keyword("none"), parseInteger(false, "none"))
	PBookmarkLabel = def("bookmark-label", false, []ContentItem{{Kind: "content", Which: "text"}}, parseBookmarkLabel)
	PBookmarkState = def("bookmark-state", false, Keyword("open"), kw("open", "closed"))
	PStringSet     = def("string-set", false, Keyword("none"), parseStringSet)
	PLink          = def("link", false, Keyword("none"), parseLink)
	PAnchor        = def("anchor", false, Keyword("none"), parseAnchor)

	PTransform        = def("transform", false, Keyword("none"), parseTransform)
	PTransformOrigin  = def("transform-origin", false, Position{"left", Percent(50), "top", Percent(50)}, parseSinglePosition)
	PObjectFit        = def("object-fit", false, Keyword("fill"), kw("fill", "contain", "cover", "none", "scale-down"))
	PObjectPosition   = def("object-position", false, Position{"left", Percent(50), "top", Percent(50)}, parseSinglePosition)
	PImageRendering   = def("image-rendering", true, Keyword("auto"), kw("auto", "crisp-edges", "pixelated", "smooth", "high-quality"))
	PColorScheme      = def("color-scheme", true, Keyword("normal"), parseColorScheme)
	PImageOrientation = def("image-orientation", false, Keyword("from-image"), parseImageOrientation)
	PImageResolution  = def("image-resolution", true, Number(1), func(ts []Token, _ *ParseCtx) Value {
		if t, ok := single(ts); ok && t.Type == TDimension {
			switch t.Value {
			case "dppx", "x":
				return Number(t.Num)
			case "dpi":
				return Number(t.Num / 96)
			case "dpcm":
				return Number(t.Num * 2.54 / 96)
			}
		}
		return nil
	})
	PAppearance      = def("appearance", false, Keyword("none"), kw("none", "auto"))
	PFootnoteDisplay = def("footnote-display", false, Keyword("block"), kw("block", "inline", "compact"))
	PFootnotePolicy  = def("footnote-policy", false, Keyword("auto"), kw("auto", "line", "block"))
	PBleedLeft       = def("bleed-left", false, Keyword("auto"), lengthKw(true, false, "auto"))
	PBleedRight      = def("bleed-right", false, Keyword("auto"), lengthKw(true, false, "auto"))
	PBleedTop        = def("bleed-top", false, Keyword("auto"), lengthKw(true, false, "auto"))
	PBleedBottom     = def("bleed-bottom", false, Keyword("auto"), lengthKw(true, false, "auto"))
	PMarks           = def("marks", false, Keyword("none"), func(ts []Token, _ *ParseCtx) Value {
		var out []string
		for _, t := range ts {
			if t.Type != TIdent {
				return nil
			}
			out = append(out, t.Value)
		}
		return out
	})
)

// NumProps is the number of longhand properties.
func NumProps() int { return len(Props) }

var _ = math.Pi

// ParsePageSize parses a value of the @page size descriptor ("A4",
// "letter landscape", "210mm 297mm"...) into CSS px. Relative lengths and
// auto are rejected.
func ParsePageSize(value string) ([2]float64, bool) {
	var ts []Token
	for _, t := range Tokenize(strings.ToLower(value)) {
		if t.Type != TWhitespace {
			ts = append(ts, t)
		}
	}
	ps, ok := parseSize(ts, nil).(PageSize)
	if !ok || len(ts) == 0 {
		return [2]float64{}, false
	}
	var out [2]float64
	for i, l := range ps {
		k, ok := unitToPx[l.Unit]
		if !ok || l.V <= 0 {
			return [2]float64{}, false
		}
		out[i] = l.V * k
	}
	return out, true
}

// ImageOrientation is an angle (multiple of 90 degrees, clockwise) and a
// horizontal flip of image-orientation.
type ImageOrientation struct {
	Angle int
	Flip  bool
}

func parseImageOrientation(ts []Token, _ *ParseCtx) Value {
	ts = RemoveWS(ts)
	if t, ok := single(ts); ok && t.Type == TIdent && (t.Value == "none" || t.Value == "from-image") {
		return Keyword(t.Value)
	}
	var o ImageOrientation
	seenAngle, seenFlip := false, false
	for _, t := range ts {
		if t.Type == TIdent && t.Value == "flip" && !seenFlip {
			seenFlip, o.Flip = true, true
			continue
		}
		if rad, ok := angleToRad(t); ok && !seenAngle {
			seenAngle = true
			q := int(math.Round(rad / (math.Pi / 2)))
			o.Angle = ((q % 4) + 4) % 4 * 90
			continue
		}
		return nil
	}
	if !seenAngle && !seenFlip {
		return nil
	}
	return o
}

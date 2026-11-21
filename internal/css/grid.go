package css

import (
	"strconv"
	"strings"
)

// TrackSize is a grid track sizing function.
type TrackSize struct {
	Min, Max   TrackBreadth
	FitContent *Length
}

// TrackBreadth is a track breadth: a length, a flex value or a keyword.
type TrackBreadth struct {
	Kind string // length, fr, auto, min-content, max-content
	L    Length
	Fr   float64
}

// TrackRepeat is a repeat() of tracks.
type TrackRepeat struct {
	Count int    // 0 for auto-fill/auto-fit
	Auto  string // auto-fill or auto-fit
	Sizes []TrackSize
}

// TrackList is a grid-template-rows/columns value: a list of TrackSize
// and TrackRepeat items.
type TrackList []any

// GridLine is a grid-row-start (etc.) value.
type GridLine struct {
	Auto bool
	Span int // > 0 for span
	Line int // line number (may be negative)
	Name string
}

// GridAreas is a grid-template-areas value: area name → [rowStart,
// colStart, rowEnd, colEnd] (0-based, end exclusive).
type GridAreas struct {
	Rows, Cols int
	Areas      map[string][4]int
}

func parseBreadth(t Token, allowFr bool) (TrackBreadth, bool) {
	if t.Type == TIdent {
		switch t.Value {
		case "auto", "min-content", "max-content":
			return TrackBreadth{Kind: t.Value}, true
		}
		return TrackBreadth{}, false
	}
	if t.Type == TDimension && t.Value == "fr" && allowFr && t.Num >= 0 {
		return TrackBreadth{Kind: "fr", Fr: t.Num}, true
	}
	if l, ok := parseLengthToken(t, false, true); ok {
		return TrackBreadth{Kind: "length", L: l}, true
	}
	return TrackBreadth{}, false
}

func parseTrackSize(t Token) (TrackSize, bool) {
	if t.Type == TFunction {
		args := SplitOnComma(t.Children)
		switch t.Value {
		case "minmax":
			if len(args) != 2 {
				return TrackSize{}, false
			}
			a, b := RemoveWS(args[0]), RemoveWS(args[1])
			if len(a) != 1 || len(b) != 1 {
				return TrackSize{}, false
			}
			mn, ok1 := parseBreadth(a[0], false)
			mx, ok2 := parseBreadth(b[0], true)
			if !ok1 || !ok2 {
				return TrackSize{}, false
			}
			return TrackSize{Min: mn, Max: mx}, true
		case "fit-content":
			a := RemoveWS(t.Children)
			if len(a) != 1 {
				return TrackSize{}, false
			}
			l, ok := parseLengthToken(a[0], false, true)
			if !ok {
				return TrackSize{}, false
			}
			return TrackSize{Min: TrackBreadth{Kind: "auto"}, Max: TrackBreadth{Kind: "max-content"}, FitContent: &l}, true
		}
		return TrackSize{}, false
	}
	b, ok := parseBreadth(t, true)
	if !ok {
		return TrackSize{}, false
	}
	if b.Kind == "fr" {
		return TrackSize{Min: TrackBreadth{Kind: "auto"}, Max: b}, true
	}
	return TrackSize{Min: b, Max: b}, true
}

func parseTrackList(ts []Token, _ *ParseCtx) Value {
	if t, ok := single(ts); ok && t.IsIdent("none") {
		return Keyword("none")
	}
	var out TrackList
	for _, t := range ts {
		if t.Type == TSquareBlock {
			continue // line names are ignored
		}
		if t.Type == TFunction && t.Value == "repeat" {
			args := SplitOnComma(t.Children)
			if len(args) != 2 {
				return nil
			}
			first := RemoveWS(args[0])
			if len(first) != 1 {
				return nil
			}
			rep := TrackRepeat{}
			switch {
			case first[0].Type == TNumber && first[0].IsInt && first[0].Num > 0:
				rep.Count = int(first[0].Num)
			case first[0].IsIdent("auto-fill") || first[0].IsIdent("auto-fit"):
				rep.Auto = first[0].Value
			default:
				return nil
			}
			for _, a := range RemoveWS(args[1]) {
				if a.Type == TSquareBlock {
					continue
				}
				s, ok := parseTrackSize(a)
				if !ok {
					return nil
				}
				rep.Sizes = append(rep.Sizes, s)
			}
			if len(rep.Sizes) == 0 {
				return nil
			}
			out = append(out, rep)
			continue
		}
		s, ok := parseTrackSize(t)
		if !ok {
			return nil
		}
		out = append(out, s)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func parseAutoTracks(ts []Token, _ *ParseCtx) Value {
	var out TrackList
	for _, t := range ts {
		s, ok := parseTrackSize(t)
		if !ok {
			return nil
		}
		out = append(out, s)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func parseGridLine(ts []Token, _ *ParseCtx) Value {
	if t, ok := single(ts); ok && t.IsIdent("auto") {
		return GridLine{Auto: true}
	}
	g := GridLine{}
	for _, t := range ts {
		switch {
		case t.IsIdent("span"):
			g.Span = -1
		case t.Type == TNumber && t.IsInt && t.Num != 0:
			if g.Span == -1 {
				if t.Num < 0 {
					return nil
				}
				g.Span = int(t.Num)
			} else {
				g.Line = int(t.Num)
			}
		case t.Type == TIdent:
			g.Name = t.Raw
		default:
			return nil
		}
	}
	if g.Span == -1 {
		g.Span = 1
	}
	if g.Span == 0 && g.Line == 0 && g.Name == "" {
		return nil
	}
	return g
}

func parseGridAreas(ts []Token, _ *ParseCtx) Value {
	if t, ok := single(ts); ok && t.IsIdent("none") {
		return Keyword("none")
	}
	var rows [][]string
	for _, t := range ts {
		if t.Type != TString {
			return nil
		}
		rows = append(rows, strings.Fields(t.Value))
	}
	if len(rows) == 0 {
		return nil
	}
	cols := len(rows[0])
	areas := map[string][4]int{}
	for r, row := range rows {
		if len(row) != cols {
			return nil
		}
		for c, name := range row {
			if strings.Trim(name, ".") == "" {
				continue
			}
			a, ok := areas[name]
			if !ok {
				areas[name] = [4]int{r, c, r + 1, c + 1}
				continue
			}
			if r+1 > a[2] {
				a[2] = r + 1
			}
			if c+1 > a[3] {
				a[3] = c + 1
			}
			areas[name] = a
		}
	}
	return GridAreas{Rows: len(rows), Cols: cols, Areas: areas}
}

func parseAutoFlow(ts []Token, _ *ParseCtx) Value {
	var parts []string
	for _, t := range ts {
		if t.Type != TIdent {
			return nil
		}
		switch t.Value {
		case "row", "column", "dense":
			parts = append(parts, t.Value)
		default:
			return nil
		}
	}
	if len(parts) == 0 {
		return nil
	}
	return Keyword(strings.Join(parts, " "))
}

var (
	PGridTemplateColumns = def("grid-template-columns", false, Keyword("none"), parseTrackList)
	PGridTemplateRows    = def("grid-template-rows", false, Keyword("none"), parseTrackList)
	PGridTemplateAreas   = def("grid-template-areas", false, Keyword("none"), parseGridAreas)
	PGridAutoColumns     = def("grid-auto-columns", false, TrackList{TrackSize{Min: TrackBreadth{Kind: "auto"}, Max: TrackBreadth{Kind: "auto"}}}, parseAutoTracks)
	PGridAutoRows        = def("grid-auto-rows", false, TrackList{TrackSize{Min: TrackBreadth{Kind: "auto"}, Max: TrackBreadth{Kind: "auto"}}}, parseAutoTracks)
	PGridAutoFlow        = def("grid-auto-flow", false, Keyword("row"), parseAutoFlow)
	PGridColumnStart     = def("grid-column-start", false, GridLine{Auto: true}, parseGridLine)
	PGridColumnEnd       = def("grid-column-end", false, GridLine{Auto: true}, parseGridLine)
	PGridRowStart        = def("grid-row-start", false, GridLine{Auto: true}, parseGridLine)
	PGridRowEnd          = def("grid-row-end", false, GridLine{Auto: true}, parseGridLine)
)

func splitSlash(ts []Token) [][]Token {
	var out [][]Token
	var cur []Token
	for _, t := range ts {
		if t.IsDelim("/") {
			out = append(out, cur)
			cur = nil
			continue
		}
		cur = append(cur, t)
	}
	return append(out, cur)
}

func init() {
	lineShorthand := func(start, end PropID) expandFunc {
		return func(ts []Token, ctx *ParseCtx) ([]PropValue, bool) {
			parts := splitSlash(ts)
			if len(parts) > 2 {
				return nil, false
			}
			s := parseGridLine(parts[0], ctx)
			if s == nil {
				return nil, false
			}
			var e Value = GridLine{Auto: true}
			if len(parts) == 2 {
				e = parseGridLine(parts[1], ctx)
				if e == nil {
					return nil, false
				}
			} else if g := s.(GridLine); g.Name != "" && g.Span == 0 && g.Line == 0 {
				e = g
			}
			return []PropValue{{start, s}, {end, e}}, true
		}
	}
	shorthand("grid-column", []PropID{PGridColumnStart, PGridColumnEnd}, lineShorthand(PGridColumnStart, PGridColumnEnd))
	shorthand("grid-row", []PropID{PGridRowStart, PGridRowEnd}, lineShorthand(PGridRowStart, PGridRowEnd))
	shorthand("grid-area", []PropID{PGridRowStart, PGridColumnStart, PGridRowEnd, PGridColumnEnd}, func(ts []Token, ctx *ParseCtx) ([]PropValue, bool) {
		parts := splitSlash(ts)
		if len(parts) > 4 {
			return nil, false
		}
		vals := make([]Value, 4)
		for i, p := range parts {
			v := parseGridLine(p, ctx)
			if v == nil {
				return nil, false
			}
			vals[i] = v
		}
		for i := len(parts); i < 4; i++ {
			// Missing values copy a custom ident, else auto.
			var src Value
			switch i {
			case 1, 2:
				src = vals[0]
			case 3:
				src = vals[1]
			}
			if g, ok := src.(GridLine); ok && g.Name != "" && g.Span == 0 && g.Line == 0 {
				vals[i] = g
			} else {
				vals[i] = GridLine{Auto: true}
			}
		}
		return []PropValue{{PGridRowStart, vals[0]}, {PGridColumnStart, vals[1]}, {PGridRowEnd, vals[2]}, {PGridColumnEnd, vals[3]}}, true
	})
	shorthand("grid-template", []PropID{PGridTemplateRows, PGridTemplateColumns, PGridTemplateAreas}, func(ts []Token, ctx *ParseCtx) ([]PropValue, bool) {
		if t, ok := single(ts); ok && t.IsIdent("none") {
			return []PropValue{{PGridTemplateRows, Keyword("none")}, {PGridTemplateColumns, Keyword("none")}, {PGridTemplateAreas, Keyword("none")}}, true
		}
		parts := splitSlash(ts)
		if len(parts) != 2 {
			return nil, false
		}
		// Areas form: strings (with optional row sizes) / columns.
		var strs, sizes []Token
		hasString := false
		for _, t := range parts[0] {
			if t.Type == TString {
				strs = append(strs, t)
				hasString = true
			} else {
				sizes = append(sizes, t)
			}
		}
		cols := parseTrackList(parts[1], ctx)
		if cols == nil {
			return nil, false
		}
		if hasString {
			areas := parseGridAreas(strs, ctx)
			if areas == nil {
				return nil, false
			}
			var rows Value = Keyword("none")
			if len(sizes) > 0 {
				rows = parseTrackList(sizes, ctx)
				if rows == nil {
					return nil, false
				}
			}
			return []PropValue{{PGridTemplateRows, rows}, {PGridTemplateColumns, cols}, {PGridTemplateAreas, areas}}, true
		}
		rows := parseTrackList(parts[0], ctx)
		if rows == nil {
			return nil, false
		}
		return []PropValue{{PGridTemplateRows, rows}, {PGridTemplateColumns, cols}, {PGridTemplateAreas, Keyword("none")}}, true
	})
	shorthand("grid", []PropID{PGridTemplateRows, PGridTemplateColumns, PGridTemplateAreas, PGridAutoFlow}, func(ts []Token, ctx *ParseCtx) ([]PropValue, bool) {
		if f, ok := expanders["grid-template"]; ok {
			if v, ok := f(ts, ctx); ok {
				return append(v, PropValue{PGridAutoFlow, Keyword("row")}), true
			}
		}
		return nil, false
	})
	_ = strconv.Itoa
}

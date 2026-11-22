package css

import "strings"

// CSSWide is a CSS-wide keyword: initial, inherit, unset or revert.
type CSSWide string

// PropValue is a validated longhand declaration.
type PropValue struct {
	ID    PropID
	Value Value
}

type expandFunc func(ts []Token, ctx *ParseCtx) ([]PropValue, bool)

var shorthands = map[string][]PropID{}
var expanders = map[string]expandFunc{}

func shorthand(name string, longhands []PropID, f expandFunc) {
	shorthands[name] = longhands
	expanders[name] = f
}

var aliases = map[string]string{
	"word-wrap":                  "overflow-wrap",
	"-weasy-hyphens":             "hyphens",
	"-webkit-hyphens":            "hyphens",
	"-moz-hyphens":               "hyphens",
	"-ms-hyphens":                "hyphens",
	"grid-gap":                   "gap",
	"grid-row-gap":               "row-gap",
	"grid-column-gap":            "column-gap",
	"-webkit-box-sizing":         "box-sizing",
	"-moz-box-sizing":            "box-sizing",
	"-webkit-transform":          "transform",
	"-webkit-transform-origin":   "transform-origin",
	"-weasy-bookmark-level":      "bookmark-level",
	"-weasy-bookmark-label":      "bookmark-label",
	"-weasy-link":                "link",
	"-weasy-anchor":              "anchor",
	"-weasy-string-set":          "string-set",
	"-weasy-text-align-last":     "text-align-last",
	"-webkit-print-color-adjust": "",
	"print-color-adjust":         "",
	"color-adjust":               "",
}

func sides(prefix, suffix string) []PropID {
	var out []PropID
	for _, s := range []string{"top", "right", "bottom", "left"} {
		id, _ := LookupProp(prefix + s + suffix)
		out = append(out, id)
	}
	return out
}

func expandFour(ids []PropID, parse parseFunc) expandFunc {
	return func(ts []Token, ctx *ParseCtx) ([]PropValue, bool) {
		if len(ts) < 1 || len(ts) > 4 {
			return nil, false
		}
		vals := make([]Value, len(ts))
		for i, t := range ts {
			v := parse([]Token{t}, ctx)
			if v == nil {
				return nil, false
			}
			vals[i] = v
		}
		var four [4]Value
		switch len(vals) {
		case 1:
			four = [4]Value{vals[0], vals[0], vals[0], vals[0]}
		case 2:
			four = [4]Value{vals[0], vals[1], vals[0], vals[1]}
		case 3:
			four = [4]Value{vals[0], vals[1], vals[2], vals[1]}
		case 4:
			four = [4]Value{vals[0], vals[1], vals[2], vals[3]}
		}
		out := make([]PropValue, 4)
		for i := range out {
			out[i] = PropValue{ids[i], four[i]}
		}
		return out, true
	}
}

// parseBorderParts parses "width style color" in any order.
func parseBorderParts(ts []Token, ctx *ParseCtx) (w, s, c Value, ok bool) {
	if len(ts) == 0 || len(ts) > 3 {
		return nil, nil, nil, false
	}
	styleP := kw(borderStyles...)
	for _, t := range ts {
		one := []Token{t}
		if v := parseBorderWidth(one, ctx); v != nil && w == nil {
			w = v
		} else if v := styleP(one, ctx); v != nil && s == nil {
			s = v
		} else if v := parseColorProp(one, ctx); v != nil && c == nil {
			c = v
		} else {
			return nil, nil, nil, false
		}
	}
	if w == nil {
		w = CSSWide("initial")
	}
	if s == nil {
		s = CSSWide("initial")
	}
	if c == nil {
		c = CSSWide("initial")
	}
	return w, s, c, true
}

func expandBorderSide(side string) expandFunc {
	return func(ts []Token, ctx *ParseCtx) ([]PropValue, bool) {
		w, s, c, ok := parseBorderParts(ts, ctx)
		if !ok {
			return nil, false
		}
		wid, _ := LookupProp("border-" + side + "-width")
		sid, _ := LookupProp("border-" + side + "-style")
		cid, _ := LookupProp("border-" + side + "-color")
		return []PropValue{{wid, w}, {sid, s}, {cid, c}}, true
	}
}

func init() {
	shorthand("margin", sides("margin-", ""), withLogical("margin", expandFour(sides("margin-", ""), lengthKw(true, true, "auto"))))
	shorthand("padding", sides("padding-", ""), withLogical("padding", expandFour(sides("padding-", ""), lengthKw(false, true))))
	shorthand("border-width", sides("border-", "-width"), withLogical("border-width", expandFour(sides("border-", "-width"), parseBorderWidth)))
	shorthand("border-style", sides("border-", "-style"), withLogical("border-style", expandFour(sides("border-", "-style"), kw(borderStyles...))))
	shorthand("border-color", sides("border-", "-color"), withLogical("border-color", expandFour(sides("border-", "-color"), parseColorProp)))
	shorthand("inset", []PropID{PTop, PRight, PBottom, PLeft}, withLogical("inset", expandFour([]PropID{PTop, PRight, PBottom, PLeft}, lengthKw(true, true, "auto"))))
	for _, side := range []string{"top", "right", "bottom", "left"} {
		ids := []PropID{}
		for _, p := range []string{"width", "style", "color"} {
			id, _ := LookupProp("border-" + side + "-" + p)
			ids = append(ids, id)
		}
		shorthand("border-"+side, ids, expandBorderSide(side))
	}
	var allBorder []PropID
	for _, p := range []string{"width", "style", "color"} {
		allBorder = append(allBorder, sides("border-", "-"+p)...)
	}
	shorthand("border", allBorder, func(ts []Token, ctx *ParseCtx) ([]PropValue, bool) {
		w, s, c, ok := parseBorderParts(ts, ctx)
		if !ok {
			return nil, false
		}
		var out []PropValue
		for _, id := range sides("border-", "-width") {
			out = append(out, PropValue{id, w})
		}
		for _, id := range sides("border-", "-style") {
			out = append(out, PropValue{id, s})
		}
		for _, id := range sides("border-", "-color") {
			out = append(out, PropValue{id, c})
		}
		return out, true
	})
	shorthand("outline", []PropID{POutlineWidth, POutlineStyle, POutlineColor}, func(ts []Token, ctx *ParseCtx) ([]PropValue, bool) {
		if len(ts) == 0 || len(ts) > 3 {
			return nil, false
		}
		var w, s, c Value = CSSWide("initial"), CSSWide("initial"), CSSWide("initial")
		styleP := kw(append(borderStyles, "auto")...)
		seen := map[int]bool{}
		for _, t := range ts {
			one := []Token{t}
			if v := parseBorderWidth(one, ctx); v != nil && !seen[0] {
				w, seen[0] = v, true
			} else if v := styleP(one, ctx); v != nil && !seen[1] {
				s, seen[1] = v, true
			} else if v := parseColorProp(one, ctx); v != nil && !seen[2] {
				c, seen[2] = v, true
			} else {
				return nil, false
			}
		}
		return []PropValue{{POutlineWidth, w}, {POutlineStyle, s}, {POutlineColor, c}}, true
	})
	radii := []PropID{PBorderTopLeftRadius, PBorderTopRightRadius, PBorderBottomRightRadius, PBorderBottomLeftRadius}
	shorthand("border-radius", radii, func(ts []Token, ctx *ParseCtx) ([]PropValue, bool) {
		h, v := ts, []Token(nil)
		for i, t := range ts {
			if t.IsDelim("/") {
				h, v = ts[:i], ts[i+1:]
				break
			}
		}
		expand := func(part []Token) ([4]Length, bool) {
			var out [4]Length
			if len(part) < 1 || len(part) > 4 {
				return out, false
			}
			var ls []Length
			for _, t := range part {
				l, ok := parseLengthToken(t, false, true)
				if !ok {
					return out, false
				}
				ls = append(ls, l)
			}
			switch len(ls) {
			case 1:
				out = [4]Length{ls[0], ls[0], ls[0], ls[0]}
			case 2:
				out = [4]Length{ls[0], ls[1], ls[0], ls[1]}
			case 3:
				out = [4]Length{ls[0], ls[1], ls[2], ls[1]}
			case 4:
				out = [4]Length{ls[0], ls[1], ls[2], ls[3]}
			}
			return out, true
		}
		hs, ok := expand(h)
		if !ok {
			return nil, false
		}
		vs := hs
		if v != nil {
			vs, ok = expand(v)
			if !ok {
				return nil, false
			}
		}
		out := make([]PropValue, 4)
		for i := range out {
			out[i] = PropValue{radii[i], [2]Length{hs[i], vs[i]}}
		}
		return out, true
	})
	shorthand("list-style", []PropID{PListStylePosition, PListStyleImage, PListStyleType}, func(ts []Token, ctx *ParseCtx) ([]PropValue, bool) {
		var pos, img, typ Value
		nones := 0
		for _, t := range ts {
			one := []Token{t}
			if t.IsIdent("none") {
				nones++
				continue
			}
			if v := kw("inside", "outside")(one, ctx); v != nil && pos == nil {
				pos = v
			} else if v := parseImageProp(one, ctx); v != nil && img == nil {
				img = v
			} else if v := parseListStyleType(one, ctx); v != nil && typ == nil {
				typ = v
			} else {
				return nil, false
			}
		}
		for ; nones > 0; nones-- {
			if typ == nil {
				typ = Keyword("none")
			} else if img == nil {
				img = Keyword("none")
			} else {
				return nil, false
			}
		}
		if pos == nil {
			pos = CSSWide("initial")
		}
		if img == nil {
			img = CSSWide("initial")
		}
		if typ == nil {
			typ = CSSWide("initial")
		}
		return []PropValue{{PListStylePosition, pos}, {PListStyleImage, img}, {PListStyleType, typ}}, true
	})
	shorthand("text-decoration", []PropID{PTextDecorationLine, PTextDecorationColor, PTextDecorationStyle, PTextDecorationThickness}, func(ts []Token, ctx *ParseCtx) ([]PropValue, bool) {
		var lines []Token
		var color, style, thick Value
		for _, t := range ts {
			one := []Token{t}
			if t.Type == TIdent && (t.Value == "none" || t.Value == "underline" || t.Value == "overline" || t.Value == "line-through" || t.Value == "blink") {
				lines = append(lines, t)
			} else if v := kw("solid", "double", "dotted", "dashed", "wavy")(one, ctx); v != nil && style == nil {
				style = v
			} else if v := parseColorProp(one, ctx); v != nil && color == nil {
				color = v
			} else if v := lengthKw(false, true, "auto", "from-font")(one, ctx); v != nil && thick == nil {
				thick = v
			} else {
				return nil, false
			}
		}
		var line Value = CSSWide("initial")
		if lines != nil {
			line = parseTextDecorationLine(lines, ctx)
			if line == nil {
				return nil, false
			}
		}
		if color == nil {
			color = CSSWide("initial")
		}
		if style == nil {
			style = CSSWide("initial")
		}
		if thick == nil {
			thick = CSSWide("initial")
		}
		return []PropValue{{PTextDecorationLine, line}, {PTextDecorationColor, color}, {PTextDecorationStyle, style}, {PTextDecorationThickness, thick}}, true
	})
	shorthand("text-align", []PropID{PTextAlignAll, PTextAlignLast}, func(ts []Token, ctx *ParseCtx) ([]PropValue, bool) {
		v := parseTextAlign(ts, ctx)
		if v == nil {
			return nil, false
		}
		if v == Keyword("justify-all") {
			return []PropValue{{PTextAlignAll, Keyword("justify")}, {PTextAlignLast, Keyword("justify")}}, true
		}
		return []PropValue{{PTextAlignAll, v}, {PTextAlignLast, Keyword("auto")}}, true
	})
	shorthand("overflow", []PropID{POverflow}, func(ts []Token, ctx *ParseCtx) ([]PropValue, bool) {
		if len(ts) == 2 {
			ts = ts[:1]
		}
		v := Props[POverflow].Parse(ts, ctx)
		if v == nil {
			return nil, false
		}
		return []PropValue{{POverflow, v}}, true
	})
	legacyBreak := func(id PropID, inside bool) expandFunc {
		return func(ts []Token, _ *ParseCtx) ([]PropValue, bool) {
			t, ok := single(ts)
			if !ok || t.Type != TIdent {
				return nil, false
			}
			switch t.Value {
			case "always":
				if inside {
					return nil, false
				}
				return []PropValue{{id, Keyword("page")}}, true
			case "left", "right":
				if inside {
					return nil, false
				}
				return []PropValue{{id, Keyword(t.Value)}}, true
			case "auto", "avoid":
				return []PropValue{{id, Keyword(t.Value)}}, true
			}
			return nil, false
		}
	}
	shorthand("page-break-before", []PropID{PBreakBefore}, legacyBreak(PBreakBefore, false))
	shorthand("page-break-after", []PropID{PBreakAfter}, legacyBreak(PBreakAfter, false))
	shorthand("page-break-inside", []PropID{PBreakInside}, legacyBreak(PBreakInside, true))
	shorthand("font-variant", []PropID{PFontVariantLigatures, PFontVariantCaps, PFontVariantAlternates, PFontVariantNumeric, PFontVariantEastAsian, PFontVariantPosition}, expandFontVariant)
	shorthand("font", []PropID{PFontStyle, PFontVariantCaps, PFontWeight, PFontStretch, PFontSize, PLineHeight, PFontFamily}, expandFont)
	shorthand("flex", []PropID{PFlexGrow, PFlexShrink, PFlexBasis}, func(ts []Token, ctx *ParseCtx) ([]PropValue, bool) {
		if t, ok := single(ts); ok && t.Type == TIdent {
			switch t.Value {
			case "none":
				return []PropValue{{PFlexGrow, Number(0)}, {PFlexShrink, Number(0)}, {PFlexBasis, Keyword("auto")}}, true
			case "auto":
				return []PropValue{{PFlexGrow, Number(1)}, {PFlexShrink, Number(1)}, {PFlexBasis, Keyword("auto")}}, true
			}
		}
		var nums []float64
		var basis Value
		for _, t := range ts {
			if t.Type == TNumber && t.Num >= 0 && len(nums) < 2 && (basis == nil || len(nums) == 0) {
				if t.Num == 0 && len(nums) == 2 {
					basis = Px(0)
					continue
				}
				nums = append(nums, t.Num)
			} else if v := parseFlexBasis([]Token{t}, ctx); v != nil && basis == nil {
				basis = v
			} else {
				return nil, false
			}
		}
		grow, shrink := 1.0, 1.0
		if len(nums) >= 1 {
			grow = nums[0]
		}
		if len(nums) == 2 {
			shrink = nums[1]
		}
		if basis == nil {
			basis = Px(0)
			if len(nums) == 0 {
				basis = Keyword("auto")
			}
		}
		return []PropValue{{PFlexGrow, Number(grow)}, {PFlexShrink, Number(shrink)}, {PFlexBasis, basis}}, true
	})
	shorthand("flex-flow", []PropID{PFlexDirection, PFlexWrap}, func(ts []Token, ctx *ParseCtx) ([]PropValue, bool) {
		var dir, wrap Value
		for _, t := range ts {
			one := []Token{t}
			if v := Props[PFlexDirection].Parse(one, ctx); v != nil && dir == nil {
				dir = v
			} else if v := Props[PFlexWrap].Parse(one, ctx); v != nil && wrap == nil {
				wrap = v
			} else {
				return nil, false
			}
		}
		if dir == nil {
			dir = CSSWide("initial")
		}
		if wrap == nil {
			wrap = CSSWide("initial")
		}
		return []PropValue{{PFlexDirection, dir}, {PFlexWrap, wrap}}, true
	})
	shorthand("gap", []PropID{PRowGap, PColumnGap}, func(ts []Token, ctx *ParseCtx) ([]PropValue, bool) {
		if len(ts) < 1 || len(ts) > 2 {
			return nil, false
		}
		r := parseGap(ts[:1], ctx)
		c := r
		if len(ts) == 2 {
			c = parseGap(ts[1:], ctx)
		}
		if r == nil || c == nil {
			return nil, false
		}
		return []PropValue{{PRowGap, r}, {PColumnGap, c}}, true
	})
	shorthand("place-items", []PropID{PAlignItems, PJustifyItems}, func(ts []Token, ctx *ParseCtx) ([]PropValue, bool) {
		if len(ts) < 1 || len(ts) > 2 {
			return nil, false
		}
		a := Props[PAlignItems].Parse(ts[:1], ctx)
		j := Props[PJustifyItems].Parse(ts[len(ts)-1:], ctx)
		if a == nil || j == nil {
			return nil, false
		}
		return []PropValue{{PAlignItems, a}, {PJustifyItems, j}}, true
	})
	shorthand("border-image", []PropID{PBorderImageSource, PBorderImageSlice, PBorderImageWidth, PBorderImageOutset, PBorderImageRepeat}, func(ts []Token, ctx *ParseCtx) ([]PropValue, bool) {
		return expandBorderImage(ts, ctx, []PropID{PBorderImageSource, PBorderImageSlice, PBorderImageWidth, PBorderImageOutset, PBorderImageRepeat, 0})
	})
	shorthand("mask-border", []PropID{PMaskBorderSource, PMaskBorderSlice, PMaskBorderWidth, PMaskBorderOutset, PMaskBorderRepeat, PMaskBorderMode}, func(ts []Token, ctx *ParseCtx) ([]PropValue, bool) {
		return expandBorderImage(ts, ctx, []PropID{PMaskBorderSource, PMaskBorderSlice, PMaskBorderWidth, PMaskBorderOutset, PMaskBorderRepeat, PMaskBorderMode})
	})
	// line-clamp: none | <integer [1,∞]> <'block-ellipsis'>?
	shorthand("line-clamp", []PropID{PMaxLines, PContinue, PBlockEllipsis}, func(ts []Token, ctx *ParseCtx) ([]PropValue, bool) {
		if len(ts) == 1 && ts[0].Type == TIdent && strings.EqualFold(ts[0].Value, "none") {
			return []PropValue{{PMaxLines, Keyword("none")}, {PContinue, Keyword("auto")}, {PBlockEllipsis, Keyword("none")}}, true
		}
		if len(ts) < 1 || len(ts) > 2 || ts[0].Type != TNumber || !ts[0].IsInt || ts[0].Num < 1 {
			return nil, false
		}
		var e Value = Keyword("auto")
		if len(ts) == 2 {
			if e = parseBlockEllipsis(ts[1:], ctx); e == nil {
				return nil, false
			}
		}
		return []PropValue{{PMaxLines, Number(ts[0].Num)}, {PContinue, Keyword("discard")}, {PBlockEllipsis, e}}, true
	})
	shorthand("place-self", []PropID{PAlignSelf, PJustifySelf}, func(ts []Token, ctx *ParseCtx) ([]PropValue, bool) {
		if len(ts) < 1 || len(ts) > 2 {
			return nil, false
		}
		a := Props[PAlignSelf].Parse(ts[:1], ctx)
		j := Props[PJustifySelf].Parse(ts[len(ts)-1:], ctx)
		if a == nil || j == nil {
			return nil, false
		}
		return []PropValue{{PAlignSelf, a}, {PJustifySelf, j}}, true
	})
	shorthand("place-content", []PropID{PAlignContent, PJustifyContent}, func(ts []Token, ctx *ParseCtx) ([]PropValue, bool) {
		if len(ts) < 1 || len(ts) > 2 {
			return nil, false
		}
		a := Props[PAlignContent].Parse(ts[:1], ctx)
		j := Props[PJustifyContent].Parse(ts[len(ts)-1:], ctx)
		if a == nil || j == nil {
			return nil, false
		}
		return []PropValue{{PAlignContent, a}, {PJustifyContent, j}}, true
	})
	shorthand("columns", []PropID{PColumnWidth, PColumnCount}, func(ts []Token, ctx *ParseCtx) ([]PropValue, bool) {
		var w, c Value
		autos := 0
		for _, t := range ts {
			one := []Token{t}
			if t.IsIdent("auto") {
				autos++
			} else if v := parseColumns(one, ctx); v != nil && w == nil {
				w = v
			} else if v := Props[PColumnCount].Parse(one, ctx); v != nil && c == nil {
				c = v
			} else {
				return nil, false
			}
		}
		if len(ts) == 0 || len(ts) > 2 {
			return nil, false
		}
		if w == nil {
			w = Keyword("auto")
		}
		if c == nil {
			c = Keyword("auto")
		}
		return []PropValue{{PColumnWidth, w}, {PColumnCount, c}}, true
	})
	shorthand("column-rule", []PropID{PColumnRuleWidth, PColumnRuleStyle, PColumnRuleColor}, func(ts []Token, ctx *ParseCtx) ([]PropValue, bool) {
		w, s, c, ok := parseBorderParts(ts, ctx)
		if !ok {
			return nil, false
		}
		return []PropValue{{PColumnRuleWidth, w}, {PColumnRuleStyle, s}, {PColumnRuleColor, c}}, true
	})
	bgLonghands := []PropID{PBackgroundColor, PBackgroundImage, PBackgroundRepeat, PBackgroundAttachment, PBackgroundPosition, PBackgroundSize, PBackgroundClip, PBackgroundOrigin}
	shorthand("background", bgLonghands, expandBackground)
	shorthand("bleed", []PropID{PBleedTop, PBleedRight, PBleedBottom, PBleedLeft}, expandFour([]PropID{PBleedTop, PBleedRight, PBleedBottom, PBleedLeft}, lengthKw(true, false, "auto")))
}

var fontSizeKeywords = map[string]bool{
	"xx-small": true, "x-small": true, "small": true, "medium": true, "large": true,
	"x-large": true, "xx-large": true, "xxx-large": true, "larger": true, "smaller": true,
}

func expandFont(ts []Token, ctx *ParseCtx) ([]PropValue, bool) {
	if t, ok := single(ts); ok && t.Type == TIdent {
		switch t.Value {
		case "caption", "icon", "menu", "message-box", "small-caption", "status-bar":
			return []PropValue{
				{PFontStyle, CSSWide("initial")}, {PFontVariantCaps, CSSWide("initial")},
				{PFontWeight, CSSWide("initial")}, {PFontStretch, CSSWide("initial")},
				{PFontSize, CSSWide("initial")}, {PLineHeight, CSSWide("initial")},
				{PFontFamily, []string{"sans-serif"}},
			}, true
		}
	}
	var style, variant, weight, stretch Value
	i := 0
	for ; i < len(ts) && i < 4; i++ {
		t := ts[i]
		one := []Token{t}
		if t.IsIdent("normal") {
			continue
		}
		if v := Props[PFontStyle].Parse(one, ctx); v != nil && style == nil {
			style = v
		} else if t.Type == TIdent && t.Value == "small-caps" && variant == nil {
			variant = Keyword("small-caps")
		} else if v := parseFontWeight(one, ctx); v != nil && weight == nil && !(t.Type == TNumber && i+1 < len(ts) && ts[i+1].IsDelim("/")) {
			weight = v
		} else if v := Props[PFontStretch].Parse(one, ctx); v != nil && stretch == nil {
			stretch = v
		} else {
			break
		}
	}
	if i >= len(ts) {
		return nil, false
	}
	size := parseFontSize(ts[i:i+1], ctx)
	if size == nil {
		return nil, false
	}
	i++
	var lh Value = CSSWide("initial")
	if i < len(ts) && ts[i].IsDelim("/") {
		if i+1 >= len(ts) {
			return nil, false
		}
		lh = parseLineHeight(ts[i+1:i+2], ctx)
		if lh == nil {
			return nil, false
		}
		i += 2
	}
	family := parseFontFamily(insertCommaWS(ts[i:]), ctx)
	if family == nil {
		return nil, false
	}
	or := func(v Value) Value {
		if v == nil {
			return CSSWide("initial")
		}
		return v
	}
	return []PropValue{
		{PFontStyle, or(style)}, {PFontVariantCaps, or(variant)}, {PFontWeight, or(weight)},
		{PFontStretch, or(stretch)}, {PFontSize, size}, {PLineHeight, lh}, {PFontFamily, family},
	}, true
}

func insertCommaWS(ts []Token) []Token { return ts }

func expandBackground(ts []Token, ctx *ParseCtx) ([]PropValue, bool) {
	layers := SplitOnComma(ts)
	var color Value = CSSWide("initial")
	var images []Value
	var repeats []Repeat
	var attachments, clips, origins []string
	var positions []Position
	var sizes []BgSize
	for li, layer := range layers {
		layer = RemoveWS(layer)
		if len(layer) == 0 {
			return nil, false
		}
		var img Value
		var rep *Repeat
		var att, clip, origin string
		var pos *Position
		var size *BgSize
		var boxes []string
		for i := 0; i < len(layer); {
			t := layer[i]
			if li == len(layers)-1 {
				if c, ok := parseColorToken(t); ok && color == CSSWide("initial") {
					if _, isImg := parseImage(t, ctx); !isImg || t.Type != TFunction {
						color = c
						i++
						continue
					}
				}
			}
			if img == nil {
				if v, ok := parseImage(t, ctx); ok {
					img = v
					i++
					continue
				}
			}
			if rep == nil {
				if i+1 < len(layer) {
					if r, ok := parseRepeatSingle(layer[i : i+2]); ok {
						rep = &r
						i += 2
						continue
					}
				}
				if r, ok := parseRepeatSingle(layer[i : i+1]); ok {
					rep = &r
					i++
					continue
				}
			}
			if att == "" && t.Type == TIdent && (t.Value == "scroll" || t.Value == "fixed" || t.Value == "local") {
				att = t.Value
				i++
				continue
			}
			if t.Type == TIdent && (t.Value == "border-box" || t.Value == "padding-box" || t.Value == "content-box") && len(boxes) < 2 {
				boxes = append(boxes, t.Value)
				i++
				continue
			}
			if pos == nil {
				// Position: greedily take up to 4 tokens before '/'.
				matched := false
				for n := 4; n >= 1; n-- {
					if i+n > len(layer) {
						continue
					}
					seg := layer[i : i+n]
					hasSlash := false
					for _, x := range seg {
						if x.IsDelim("/") {
							hasSlash = true
						}
					}
					if hasSlash {
						continue
					}
					if p, ok := parsePosition(seg); ok {
						pos = &p
						i += n
						matched = true
						break
					}
				}
				if matched {
					if i < len(layer) && layer[i].IsDelim("/") {
						i++
						found := false
						for n := 2; n >= 1; n-- {
							if i+n > len(layer) {
								continue
							}
							if s, ok := parseSizeSingle(layer[i : i+n]); ok {
								size = &s
								i += n
								found = true
								break
							}
						}
						if !found {
							return nil, false
						}
					}
					continue
				}
			}
			return nil, false
		}
		if img == nil {
			img = Keyword("none")
		}
		images = append(images, img)
		if rep == nil {
			rep = &Repeat{"repeat", "repeat"}
		}
		repeats = append(repeats, *rep)
		if att == "" {
			att = "scroll"
		}
		attachments = append(attachments, att)
		if pos == nil {
			pos = &Position{"left", Percent(0), "top", Percent(0)}
		}
		positions = append(positions, *pos)
		if size == nil {
			size = &BgSize{W: Keyword("auto"), H: Keyword("auto")}
		}
		sizes = append(sizes, *size)
		switch len(boxes) {
		case 0:
			origin, clip = "padding-box", "border-box"
		case 1:
			origin, clip = boxes[0], boxes[0]
		case 2:
			origin, clip = boxes[0], boxes[1]
		}
		clips = append(clips, clip)
		origins = append(origins, origin)
	}
	return []PropValue{
		{PBackgroundColor, color}, {PBackgroundImage, images}, {PBackgroundRepeat, repeats},
		{PBackgroundAttachment, attachments}, {PBackgroundPosition, positions},
		{PBackgroundSize, sizes}, {PBackgroundClip, clips}, {PBackgroundOrigin, origins},
	}, true
}

// ExpandDeclaration validates a declaration and expands shorthands. It
// returns nil if the declaration is invalid or unsupported.
func ExpandDeclaration(name string, value []Token, ctx *ParseCtx) []PropValue {
	if a, ok := aliases[name]; ok {
		if a == "" {
			return nil
		}
		name = a
	}
	if strings.HasPrefix(name, "-") && !strings.HasPrefix(name, "--") {
		// Unknown vendor prefix.
		if _, ok := propIndex[name]; !ok {
			return nil
		}
	}
	ts := RemoveWS(StripWS(value))
	if len(ts) == 1 && ts[0].Type == TIdent {
		switch ts[0].Value {
		case "initial", "inherit", "unset", "revert", "revert-layer":
			w := CSSWide(ts[0].Value)
			if w == "revert-layer" {
				w = "revert"
			}
			if id, ok := propIndex[name]; ok {
				return []PropValue{{id, w}}
			}
			if ls, ok := shorthands[name]; ok {
				out := make([]PropValue, len(ls))
				for i, id := range ls {
					out[i] = PropValue{id, w}
				}
				return out
			}
			return nil
		}
	}
	if id, ok := propIndex[name]; ok {
		v := Props[id].Parse(ts, ctx)
		if v == nil {
			return nil
		}
		return []PropValue{{id, v}}
	}
	if f, ok := expanders[name]; ok {
		out, ok := f(ts, ctx)
		if !ok {
			return nil
		}
		return out
	}
	return nil
}

// ContainsVar reports whether the tokens contain a var() function.
func ContainsVar(ts []Token) bool {
	for _, t := range ts {
		if t.Type == TFunction && t.Value == "var" {
			return true
		}
		if len(t.Children) > 0 && ContainsVar(t.Children) {
			return true
		}
	}
	return false
}

// KnownProperty reports whether name is a supported property or shorthand.
func KnownProperty(name string) bool {
	if a, ok := aliases[name]; ok {
		name = a
	}
	_, ok1 := propIndex[name]
	_, ok2 := shorthands[name]
	return ok1 || ok2
}

// LonghandsOf returns the longhands set by a property name.
func LonghandsOf(name string) []PropID {
	if a, ok := aliases[name]; ok {
		name = a
	}
	if id, ok := propIndex[name]; ok {
		return []PropID{id}
	}
	return shorthands[name]
}

// withLogical accepts the "logical" keyword before four values, like
// WeasyPrint.
func withLogical(name string, f expandFunc) expandFunc {
	return func(ts []Token, ctx *ParseCtx) ([]PropValue, bool) {
		if len(ts) > 0 && ts[0].Type == TIdent && ts[0].Value == "logical" {
			return expandLogicalFour(name, ts[1:], ctx)
		}
		return f(ts, ctx)
	}
}

// expandBorderImage expands border-image and mask-border:
// <source> || <slice> [ / <width> | / <width>? / <outset> ]? || <repeat> || <mode>.
// ids are the source, slice, width, outset, repeat and mode (0 for none)
// longhands.
func expandBorderImage(ts []Token, ctx *ParseCtx, ids []PropID) ([]PropValue, bool) {
	vals := map[PropID]Value{}
	set := func(id PropID, v Value) bool {
		if v == nil {
			return false
		}
		if _, dup := vals[id]; dup {
			return false
		}
		vals[id] = v
		return true
	}
	isSlash := func(t Token) bool { return t.Type == TDelim && t.Value == "/" }
	// take returns the longest prefix of ts accepted by parse.
	take := func(ts []Token, parse func([]Token, *ParseCtx) Value) (Value, int) {
		var best Value
		n := 0
		for i := 1; i <= len(ts) && i <= 5; i++ {
			if isSlash(ts[i-1]) {
				break
			}
			if v := parse(ts[:i], ctx); v != nil {
				best, n = v, i
			}
		}
		return best, n
	}
	for len(ts) > 0 {
		if v := parseBorderImageSource(ts[:1], ctx); v != nil {
			if !set(ids[0], v) {
				return nil, false
			}
			ts = ts[1:]
			continue
		}
		if ids[5] != 0 {
			if v := Props[ids[5]].Parse(ts[:1], ctx); v != nil {
				if !set(ids[5], v) {
					return nil, false
				}
				ts = ts[1:]
				continue
			}
		}
		if v, n := take(ts, parseBorderImageRepeat); n > 0 {
			if !set(ids[4], v) {
				return nil, false
			}
			ts = ts[n:]
			continue
		}
		v, n := take(ts, parseBorderImageSlice)
		if n == 0 || !set(ids[1], v) {
			return nil, false
		}
		ts = ts[n:]
		if len(ts) == 0 || !isSlash(ts[0]) {
			continue
		}
		ts = ts[1:]
		if len(ts) == 0 {
			return nil, false
		}
		if v, n := take(ts, parseBorderImageWidth); n > 0 {
			set(ids[2], v)
			ts = ts[n:]
			if len(ts) == 0 || !isSlash(ts[0]) {
				continue
			}
			ts = ts[1:]
		} else if isSlash(ts[0]) {
			ts = ts[1:]
		} else {
			return nil, false
		}
		v, n = take(ts, parseBorderImageOutset)
		if n == 0 {
			return nil, false
		}
		set(ids[3], v)
		ts = ts[n:]
	}
	var out []PropValue
	for _, id := range ids {
		if id == 0 {
			continue
		}
		v, ok := vals[id]
		if !ok {
			v = Props[id].Initial
		}
		out = append(out, PropValue{id, v})
	}
	return out, true
}

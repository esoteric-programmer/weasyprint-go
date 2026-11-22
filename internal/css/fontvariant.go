package css

import "strings"

// Font variant properties (CSS Fonts 4) and @font-feature-values.

// FontAlternates is the computed value of font-variant-alternates.
type FontAlternates struct {
	Historical bool
	// Functions are stylistic(), styleset(), character-variant(),
	// swash(), ornaments() and annotation() with their (case-sensitive)
	// feature value names.
	Functions []AlternateFunc
	// ByFamily are the features of the functions for each family with
	// @font-feature-values (lowercase), set when computed.
	ByFamily map[string][]FontFeature
}

// AlternateFunc is a font-variant-alternates function.
type AlternateFunc struct {
	Name  string
	Names []string
}

// FontVariation is a font-variation-settings item.
type FontVariation struct {
	Tag   string
	Value float64
}

// FeatureValues holds the @font-feature-values of a family: feature type
// ("styleset", "swash"…) → value name → values.
type FeatureValues map[string]map[string][]int

// parseExclusiveKeywords parses a list of keywords where each group's
// keywords are mutually exclusive.
func parseExclusiveKeywords(ts []Token, groups [][]string, singles ...string) Value {
	ts = RemoveWS(ts)
	if t, ok := single(ts); ok {
		for _, s := range singles {
			if t.IsIdent(s) {
				return Keyword(s)
			}
		}
	}
	if len(ts) == 0 {
		return nil
	}
	used := map[int]bool{}
	var out []string
	for _, t := range ts {
		if t.Type != TIdent {
			return nil
		}
		found := false
		for gi, g := range groups {
			for _, k := range g {
				if t.Value == k {
					if used[gi] {
						return nil
					}
					used[gi] = true
					found = true
				}
			}
		}
		if !found {
			return nil
		}
		out = append(out, t.Value)
	}
	return out
}

var (
	ligatureGroups = [][]string{{"common-ligatures", "no-common-ligatures"}, {"historical-ligatures", "no-historical-ligatures"},
		{"discretionary-ligatures", "no-discretionary-ligatures"}, {"contextual", "no-contextual"}}
	numericGroups = [][]string{{"lining-nums", "oldstyle-nums"}, {"proportional-nums", "tabular-nums"},
		{"diagonal-fractions", "stacked-fractions"}, {"ordinal"}, {"slashed-zero"}}
	eastAsianGroups = [][]string{{"jis78", "jis83", "jis90", "jis04", "simplified", "traditional"},
		{"full-width", "proportional-width"}, {"ruby"}}
)

func parseFontVariantLigatures(ts []Token, _ *ParseCtx) Value {
	return parseExclusiveKeywords(ts, ligatureGroups, "normal", "none")
}

func parseFontVariantNumeric(ts []Token, _ *ParseCtx) Value {
	return parseExclusiveKeywords(ts, numericGroups, "normal")
}

func parseFontVariantEastAsian(ts []Token, _ *ParseCtx) Value {
	return parseExclusiveKeywords(ts, eastAsianGroups, "normal")
}

func parseFontVariantAlternates(ts []Token, _ *ParseCtx) Value {
	ts = RemoveWS(ts)
	if t, ok := single(ts); ok && t.IsIdent("normal") {
		return Keyword("normal")
	}
	if len(ts) == 0 {
		return nil
	}
	var fa FontAlternates
	seen := map[string]bool{}
	for _, t := range ts {
		switch {
		case t.IsIdent("historical-forms"):
			if fa.Historical {
				return nil
			}
			fa.Historical = true
		case t.Type == TFunction:
			name := t.Value
			multi := false
			switch name {
			case "styleset", "character-variant":
				multi = true
			case "stylistic", "swash", "ornaments", "annotation":
			default:
				return nil
			}
			if seen[name] {
				return nil
			}
			seen[name] = true
			var names []string
			for _, part := range SplitOnComma(t.Children) {
				part = RemoveWS(part)
				if len(part) != 1 || part[0].Type != TIdent {
					return nil
				}
				names = append(names, part[0].Raw)
			}
			if len(names) == 0 || (!multi && len(names) > 1) {
				return nil
			}
			fa.Functions = append(fa.Functions, AlternateFunc{name, names})
		default:
			return nil
		}
	}
	return fa
}

func parseFontVariationSettings(ts []Token, _ *ParseCtx) Value {
	if t, ok := single(RemoveWS(ts)); ok && t.IsIdent("normal") {
		return Keyword("normal")
	}
	var out []FontVariation
	for _, part := range SplitOnComma(ts) {
		part = RemoveWS(part)
		if len(part) != 2 || part[0].Type != TString || len(part[0].Value) != 4 || part[1].Type != TNumber {
			return nil
		}
		out = append(out, FontVariation{part[0].Value, part[1].Num})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func parseFontOpticalSizing(ts []Token, ctx *ParseCtx) Value {
	return kw("auto", "none")(ts, ctx)
}

// expandFontVariant expands the font-variant shorthand.
func expandFontVariant(ts []Token, ctx *ParseCtx) ([]PropValue, bool) {
	ts = RemoveWS(ts)
	props := []PropID{PFontVariantLigatures, PFontVariantCaps, PFontVariantAlternates, PFontVariantNumeric, PFontVariantEastAsian, PFontVariantPosition}
	out := []PropValue{}
	if t, ok := single(ts); ok && (t.IsIdent("normal") || t.IsIdent("none")) {
		for _, p := range props {
			out = append(out, PropValue{p, Keyword("normal")})
		}
		if t.IsIdent("none") {
			out[0].Value = Keyword("none")
		}
		return out, true
	}
	if len(ts) == 0 {
		return nil, false
	}
	groups := map[PropID][]Token{}
	for _, t := range ts {
		matched := false
		for _, p := range []PropID{PFontVariantAlternates, PFontVariantCaps, PFontVariantEastAsian, PFontVariantLigatures, PFontVariantNumeric, PFontVariantPosition} {
			if t.IsIdent("normal") || t.IsIdent("none") {
				return nil, false
			}
			if Props[p].Parse([]Token{t}, ctx) != nil {
				groups[p] = append(groups[p], t)
				matched = true
				break
			}
		}
		if !matched {
			return nil, false
		}
	}
	for _, p := range props {
		var v Value = Keyword("normal")
		if g, ok := groups[p]; ok {
			if v = Props[p].Parse(g, ctx); v == nil {
				return nil, false
			}
		}
		out = append(out, PropValue{p, v})
	}
	return out, true
}

// parseFontFeatureValues parses the content of a @font-feature-values
// rule.
func parseFontFeatureValues(prelude, content []Token) ([]string, FeatureValues) {
	var families []string
	for _, part := range SplitOnComma(prelude) {
		part = RemoveWS(part)
		if len(part) == 1 && part[0].Type == TString {
			families = append(families, strings.ToLower(part[0].Value))
			continue
		}
		var words []string
		for _, t := range part {
			if t.Type != TIdent {
				return nil, nil
			}
			words = append(words, t.Value)
		}
		if len(words) == 0 {
			return nil, nil
		}
		families = append(families, strings.ToLower(strings.Join(words, " ")))
	}
	fv := FeatureValues{}
	for _, r := range ParseRuleList(content) {
		if r.AtKeyword == "" {
			continue
		}
		typ := strings.ToLower(r.AtKeyword)
		switch typ {
		case "stylistic", "styleset", "character-variant", "swash", "ornaments", "annotation":
		default:
			continue
		}
		decls, _ := ParseDeclarations(r.Content)
		for _, d := range decls {
			var nums []int
			ok := true
			for _, t := range RemoveWS(d.Value) {
				if t.Type != TNumber || !t.IsInt || t.Num < 0 {
					ok = false
					break
				}
				nums = append(nums, int(t.Num))
			}
			if !ok || len(nums) == 0 {
				continue
			}
			if fv[typ] == nil {
				fv[typ] = map[string][]int{}
			}
			fv[typ][d.Name] = nums
		}
	}
	return families, fv
}

// AlternateFeatures returns the OpenType features of font-variant-
// alternates functions for a family's feature values.
func AlternateFeatures(fa FontAlternates, fv FeatureValues) []FontFeature {
	var out []FontFeature
	for _, fn := range fa.Functions {
		vals := fv[fn.Name]
		switch fn.Name {
		case "stylistic", "swash", "ornaments", "annotation":
			v, ok := vals[fn.Names[0]]
			if !ok || len(v) != 1 {
				continue
			}
			tag := map[string]string{"stylistic": "salt", "swash": "swsh", "ornaments": "ornm", "annotation": "nalt"}[fn.Name]
			out = append(out, FontFeature{tag, v[0]})
			if fn.Name == "swash" {
				out = append(out, FontFeature{"cswh", v[0]})
			}
		case "styleset":
			for _, n := range fn.Names {
				for _, k := range vals[n] {
					if k >= 1 && k <= 20 {
						out = append(out, FontFeature{"ss" + twoDigits(k), 1})
					}
				}
			}
		case "character-variant":
			for _, n := range fn.Names {
				v, ok := vals[n]
				if !ok || len(v) < 1 || len(v) > 2 || v[0] < 1 || v[0] > 99 {
					continue
				}
				val := 1
				if len(v) == 2 {
					val = v[1]
				}
				out = append(out, FontFeature{"cv" + twoDigits(v[0]), val})
			}
		}
	}
	return out
}

func twoDigits(n int) string {
	return string([]byte{byte('0' + n/10), byte('0' + n%10)})
}

// FontFeatures returns the OpenType features of font variant values, in
// WeasyPrint's order (CSS Fonts 3 feature precedence, step 4 and 6):
// kerning, ligatures, position, caps, numeric, alternates, east asian,
// then font-feature-settings. alt are the features of
// font-variant-alternates functions (resolved per family).
func FontFeatures(kerning string, lig, pos, caps, num, alternates, eastAsian, settings Value) []FontFeature {
	var out []FontFeature
	set := func(tag string, v int) {
		for i := range out {
			if out[i].Tag == tag {
				out = append(out[:i], out[i+1:]...)
				break
			}
		}
		out = append(out, FontFeature{tag, v})
	}
	switch kerning {
	case "normal":
		set("kern", 1)
	case "none":
		set("kern", 0)
	}
	ligKeys := map[string][]string{"common-ligatures": {"liga", "clig"}, "historical-ligatures": {"hlig"},
		"discretionary-ligatures": {"dlig"}, "contextual": {"calt"}}
	switch v := lig.(type) {
	case Keyword:
		if v == "none" {
			for _, k := range []string{"common-ligatures", "historical-ligatures", "discretionary-ligatures", "contextual"} {
				for _, t := range ligKeys[k] {
					set(t, 0)
				}
			}
		}
	case []string:
		for _, l := range v {
			val := 1
			if strings.HasPrefix(l, "no-") {
				val = 0
				l = l[3:]
			}
			for _, t := range ligKeys[l] {
				set(t, val)
			}
		}
	}
	switch pos {
	case Keyword("sub"):
		set("subs", 1)
	case Keyword("super"):
		set("sups", 1)
	}
	capsKeys := map[Keyword][]string{"small-caps": {"smcp"}, "all-small-caps": {"c2sc", "smcp"}, "petite-caps": {"pcap"},
		"all-petite-caps": {"c2pc", "pcap"}, "unicase": {"unic"}, "titling-caps": {"titl"}}
	if c, ok := caps.(Keyword); ok {
		for _, t := range capsKeys[c] {
			set(t, 1)
		}
	}
	numKeys := map[string]string{"lining-nums": "lnum", "oldstyle-nums": "onum", "proportional-nums": "pnum",
		"tabular-nums": "tnum", "diagonal-fractions": "frac", "stacked-fractions": "afrc", "ordinal": "ordn", "slashed-zero": "zero"}
	if v, ok := num.([]string); ok {
		for _, n := range v {
			set(numKeys[n], 1)
		}
	}
	if fa, ok := alternates.(FontAlternates); ok && fa.Historical {
		set("hist", 1)
	}
	eaKeys := map[string]string{"jis78": "jp78", "jis83": "jp83", "jis90": "jp90", "jis04": "jp04", "simplified": "smpl",
		"traditional": "trad", "full-width": "fwid", "proportional-width": "pwid", "ruby": "ruby"}
	if v, ok := eastAsian.([]string); ok {
		for _, e := range v {
			set(eaKeys[e], 1)
		}
	}
	if v, ok := settings.([]FontFeature); ok {
		for _, f := range v {
			set(f.Tag, f.Value)
		}
	}
	return out
}

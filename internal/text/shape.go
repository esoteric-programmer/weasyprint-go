package text

import (
	"math"
	"strings"
	"unicode"

	"github.com/esoteric-programmer/weasyprint-go/internal/bidi"
	"github.com/esoteric-programmer/weasyprint-go/internal/fonts"
	"github.com/esoteric-programmer/weasyprint-go/internal/otl"
	"github.com/esoteric-programmer/weasyprint-go/internal/unorm"
)

// isMark reports whether r is a combining mark (Mn, Mc or Me).
func isMark(r rune) bool {
	return r >= 0x300 && (unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Mc, r) || unicode.Is(unicode.Me, r))
}

// clusterFits reports whether a font can draw a grapheme cluster (a
// base and combining marks) by itself, composing or decomposing
// characters as the shaper does.
func clusterFits(cl []rune, f *fonts.Font) bool {
	seq := []rune{cl[0]}
	if !f.HasRune(cl[0]) {
		seq = unorm.FullDecompose(nil, cl[0])
		for _, r := range seq {
			if !f.HasRune(r) {
				return false
			}
		}
	}
	seq = append(seq, cl[1:]...)
	// Canonical order of the marks.
	for i := 2; i < len(seq); i++ {
		for k := i; k > 1 && unorm.CombiningClass(seq[k-1]) > unorm.CombiningClass(seq[k]) && unorm.CombiningClass(seq[k]) != 0; k-- {
			seq[k], seq[k-1] = seq[k-1], seq[k]
		}
	}
	starter := seq[0]
	last := -1
	for _, m := range seq[1:] {
		c := int(unorm.CombiningClass(m))
		if last < c || last == -1 {
			if comp, ok := unorm.Compose(starter, m); ok && f.HasRune(comp) {
				starter = comp
				continue
			}
		}
		if !f.HasRune(m) {
			return false
		}
		last = c
	}
	return f.HasRune(starter)
}

// clusterFaces draws grapheme clusters with combining marks with a single
// font when possible: the base's font, or the first font that can draw
// the whole cluster. Letters missing from the preferred fonts are also
// drawn with them when their decomposition fits.
func (s *Shaper) clusterFaces(spec Spec, runes []rune, runeFace []*fonts.Face, sameRun func(int) bool, runScript []int) {
	n := len(runes)
	for i := 0; i < n; {
		if isMark(runes[i]) || runeFace[i] == nil {
			i++
			continue
		}
		j := i + 1
		for j < n && isMark(runes[j]) && sameRun(j) {
			j++
		}
		cl := runes[i:j]
		uniform := true
		for k := i + 1; k < j; k++ {
			uniform = uniform && runeFace[k] == runeFace[i]
		}
		decomposable := false
		if j-i == 1 {
			if _, b, ok := unorm.Decompose(runes[i]); ok && b != 0 {
				decomposable = true
			}
		}
		if (j-i > 1 && (!uniform || !clusterFits(cl, runeFace[i].Font))) || decomposable {
			var best *fonts.Face
			if j-i > 1 && runeFace[i].Loaded() && clusterFits(cl, runeFace[i].Font) {
				best = runeFace[i]
			} else {
				for _, f := range s.facesForScript(spec, runScript[i]) {
					if f == runeFace[i] && decomposable {
						break
					}
					if (f.Info == nil || f.Info.Covers(runes[i])) && f.Loaded() && clusterFits(cl, f.Font) {
						best = f
						break
					}
				}
			}
			if best != nil {
				for k := i; k < j; k++ {
					runeFace[k] = best
				}
			}
		}
		i = j
	}
}

// scriptTags are the OpenType script tags of the scripts table entries.
var scriptTags = [][]string{
	{"latn"}, {"arab"}, {"hebr"}, {"hani"}, {"kana"}, {"kana"}, {"hang"}, {"cyrl"},
	{"grek"}, {"thai"}, {"dev2", "deva"}, {"armn"}, {"geor"}, {"bng2", "beng"},
	{"tml2", "taml"}, {"ethi"},
}

func scriptTagsOf(sc int) []string {
	if sc >= 0 && sc < len(scriptTags) {
		return scriptTags[sc]
	}
	return []string{"DFLT"}
}

// capsFeatures returns the features used for a font-variant-caps value
// when the font has them (and whether it has them), as Pango does.
func capsFeatures(p func(string) bool, caps string) ([]otl.Feature, bool) {
	on := func(tags ...string) []otl.Feature {
		var out []otl.Feature
		for _, t := range tags {
			out = append(out, otl.Feature{Tag: t, Value: 1})
		}
		return out
	}
	switch caps {
	case "small-caps":
		if p("smcp") {
			return on("smcp"), true
		}
	case "all-small-caps":
		if p("smcp") && p("c2sc") {
			return on("smcp", "c2sc"), true
		}
	case "petite-caps":
		if p("pcap") {
			return on("pcap"), true
		}
		if p("smcp") {
			return on("smcp"), true
		}
	case "all-petite-caps":
		if p("pcap") && p("c2pc") {
			return on("pcap", "c2pc"), true
		}
		if p("smcp") && p("c2sc") {
			return on("smcp", "c2sc"), true
		}
	case "unicase":
		if p("unic") {
			return on("unic"), true
		}
	case "titling-caps":
		if p("titl") {
			return on("titl"), true
		}
		return nil, true
	default:
		return nil, true
	}
	return nil, false
}

type shapeRun struct {
	start, end int
	face       *fonts.Face
	level      uint8
	script     int
	small      bool // emulated small caps
}

// shapeRuns shapes runs of one face, bidi level and script with the
// OpenType engine, then applies spacing and fills the positions.
func (s *Shaper) shapeRuns(sh *Shaped, runeFace []*fonts.Face, levels []uint8, runScript []int) {
	runes := sh.Runes
	spec := sh.Spec
	n := len(runes)
	level := func(i int) uint8 {
		if levels == nil {
			return 0
		}
		return levels[i]
	}
	caps := spec.Caps
	if caps == "normal" {
		caps = ""
	}
	// Arabic joining forms over the whole text.
	var forms []uint8
	if hasArabic(runes) {
		forms = arabicForms(runes)
	}
	// Font features.
	features := func(face *fonts.Face, sc int, rtl bool, extra []otl.Feature) []otl.Feature {
		var fs []otl.Feature
		if spec.LetterSpacing != 0 {
			fs = append(fs, otl.Feature{Tag: "liga", Value: 0}, otl.Feature{Tag: "clig", Value: 0}, otl.Feature{Tag: "dlig", Value: 0})
		}
		if face.Info != nil {
			for _, f := range face.Info.Features {
				fs = append(fs, otl.Feature{Tag: f.Tag, Value: f.Value})
			}
		}
		fs = append(fs, extra...)
		fs = append(fs, spec.Features...)
		if len(spec.FamilyFeatures) > 0 && face.Info != nil {
			for _, fam := range face.Info.Families {
				if ff, ok := spec.FamilyFeatures[fam]; ok {
					fs = append(fs, ff...)
					break
				}
			}
		}
		return fs
	}
	planFor := func(face *fonts.Face, sc int, rtl bool, extra []otl.Feature) *otl.Plan {
		o := otl.Options{
			Scripts:  scriptTagsOf(sc),
			Language: spec.Lang,
			RTL:      rtl,
			Arabic:   sc == 1,
			Features: features(face, sc, rtl, extra),
		}
		if c := face.Font.VarCoords(); c != nil {
			o.VarCoords = c
			o.VarDelta = face.Font.VarDelta
		}
		return otl.NewPlan(face.Font, o)
	}
	// Small caps: the font's features or emulation, per face.
	type capsInfo struct {
		feats    []otl.Feature
		emulated bool
	}
	capsOf := map[*fonts.Face]capsInfo{}
	getCaps := func(face *fonts.Face, sc int, rtl bool) capsInfo {
		if c, ok := capsOf[face]; ok {
			return c
		}
		base := planFor(face, sc, rtl, nil)
		feats, ok := capsFeatures(base.HasFeature, caps)
		c := capsInfo{feats, !ok}
		capsOf[face] = c
		return c
	}
	// Emulated caps, like Pango: the text is split in segments of
	// lowercase letters and of other characters (spaces and controls go
	// with the previous character, lowercase at the start of a face). The
	// lowercase segments (the others for unicase, all for all-small-caps)
	// are shaped at 0.8 times the size, with the lowercase letters
	// uppercased by the full case mappings ("ß" → "SS").
	emulated := func(i int) bool {
		f := runeFace[i]
		return caps != "" && f != nil && getCaps(f, runScript[i], level(i)%2 == 1).emulated
	}
	var mapped map[int][]rune // expanded case mappings, by rune index
	german := isGerman(spec.Lang)
	small := make([]bool, n)
	lower := make([]bool, n) // segment of lowercase letters
	shapeText := make([]rune, n)
	copy(shapeText, runes)
	for i, r := range runes {
		if !emulated(i) {
			continue
		}
		if capsSpace(r) {
			lower[i] = i == 0 || runeFace[i-1] != runeFace[i] || lower[i-1]
		} else {
			lower[i] = unicode.IsLower(r)
		}
		switch caps {
		case "small-caps", "petite-caps":
			small[i] = lower[i]
		case "all-small-caps", "all-petite-caps":
			small[i] = true
		case "unicase":
			small[i] = !lower[i]
		}
		if !small[i] || !unicode.IsLower(r) || caps == "unicase" {
			continue
		}
		up := []rune(unorm.UpperRune(r))
		if r == 'ß' && german && runeFace[i].Font.HasRune('ẞ') {
			// German uses the capital sharp s when the font has it.
			up = []rune{'ẞ'}
		}
		ok := true
		for _, u := range up {
			ok = ok && runeFace[i].Font.HasRune(u)
		}
		switch {
		case !ok:
		case len(up) == 1:
			shapeText[i] = up[0]
		case runScript[i] != 1:
			if mapped == nil {
				mapped = map[int][]rune{}
			}
			mapped[i] = up
		}
	}
	// Runs.
	var runs []shapeRun
	for i := 0; i < n; {
		j := i + 1
		for j < n && runeFace[j] == runeFace[i] && level(j) == level(i) && runScript[j] == runScript[i] && small[j] == small[i] && lower[j] == lower[i] {
			j++
		}
		runs = append(runs, shapeRun{i, j, runeFace[i], level(i), runScript[i], small[i]})
		i = j
	}
	sh.Glyphs = sh.Glyphs[:0]
	for _, r := range runs {
		size := spec.Size
		if r.small {
			size *= 0.8
		}
		if r.face == nil {
			for k := r.start; k < r.end; k++ {
				sh.Glyphs = append(sh.Glyphs, Glyph{Size: size, Start: k, End: k + 1})
			}
			continue
		}
		rtl := r.level%2 == 1
		var extra []otl.Feature
		if caps != "" && !r.small {
			extra = getCaps(r.face, r.script, rtl).feats
		}
		plan := planFor(r.face, r.script, rtl, extra)
		in := otl.Input{Runes: shapeText[r.start:r.end], Offset: r.start, Size: size}
		// Expanded case mappings: shaped from offset 0, the clusters
		// mapped back to the original characters.
		var origin []int
		if mapped != nil {
			for k := r.start; k < r.end; k++ {
				if _, ok := mapped[k]; ok {
					origin = make([]int, 0, r.end-r.start+4)
					break
				}
			}
		}
		if origin != nil {
			var rs []rune
			for k := r.start; k < r.end; k++ {
				m, ok := mapped[k]
				if !ok {
					m = shapeText[k : k+1]
				}
				for range m {
					origin = append(origin, k)
				}
				rs = append(rs, m...)
			}
			in = otl.Input{Runes: rs, Size: size}
		}
		var lamAlefs map[int]uint16
		if forms != nil && r.script == 1 {
			in.Masks = make([]uint32, r.end-r.start)
			for k := range in.Masks {
				switch forms[r.start+k] {
				case formIsol:
					in.Masks[k] = otl.MaskIsol
				case formFina:
					in.Masks[k] = otl.MaskFina
				case formMedi:
					in.Masks[k] = otl.MaskMedi
				case formInit:
					in.Masks[k] = otl.MaskInit
				}
			}
			if plan.ArabicFallback {
				in.Glyphs, lamAlefs = arabicFallback(r.face.Font, runes[r.start:r.end], forms[r.start:r.end], r.start, !plan.HasFeature("rlig"))
			}
		}
		out := plan.Shape(in)
		for gi := range out {
			if origin != nil {
				out[gi].Cluster = origin[out[gi].Cluster]
			}
		}
		k := 1024.0
		for gi := 0; gi < len(out); gi++ {
			og := &out[gi]
			id := og.ID
			if lig, ok := lamAlefs[og.Cluster]; ok && gi+1 < len(out) && out[gi+1].Cluster == og.Cluster+1 {
				// Presentation-form lam-alef ligature.
				id = lig
				adv := float64(int32(math.Round(r.face.Font.Advance(lig) * size * 1024 / r.face.Font.UnitsPerEm)))
				sh.Glyphs = append(sh.Glyphs, Glyph{ID: id, Face: r.face, Size: size, Advance: adv / k, Start: og.Cluster})
				gi++
				continue
			}
			sh.Glyphs = append(sh.Glyphs, Glyph{ID: id, Face: r.face, Size: size,
				Advance: float64(og.XAdvance) / k, XOff: float64(og.XOffset) / k, YOff: float64(og.YOffset) / k,
				Start: og.Cluster, Char: og.Rune(), Mapped: r.small && (shapeText[og.Cluster] != runes[og.Cluster] || mapped[og.Cluster] != nil)})
		}
	}
	gl := sh.Glyphs
	s.positionForeignMarks(runes, gl)
	// Cluster ends.
	for i := len(gl) - 1; i >= 0; i-- {
		end := n
		for j := i + 1; j < len(gl); j++ {
			if gl[j].Start > gl[i].Start {
				end = gl[j].Start
				break
			}
		}
		gl[i].End = end
	}
	// Spacing, as Pango attributes (integer Pango units, truncated), on
	// the last glyph of each cluster.
	ls := math.Trunc(spec.LetterSpacing*1024) / 1024
	for i := 0; i < len(gl); i++ {
		if i+1 < len(gl) && gl[i+1].Start == gl[i].Start {
			continue
		}
		r := runes[gl[i].Start]
		switch {
		case r == ' ' || r == 0xa0 || r == '\t':
			if spec.WordSpacing != 0 {
				gl[i].Advance += math.Trunc(spec.WordSpacing*1024+ls*1024) / 1024
			} else {
				gl[i].Advance += ls
			}
		case r != '\n' && !isDefaultIgnorable(r):
			gl[i].Advance += ls
		}
	}
	// Positions.
	x := 0.0
	for i := 0; i < len(gl); {
		j := i
		w := 0.0
		for j < len(gl) && gl[j].Start == gl[i].Start {
			w += gl[j].Advance
			j++
		}
		start, end := gl[i].Start, gl[i].End
		for k := start; k < end; k++ {
			sh.Pos[k] = x + w*float64(k-start)/float64(end-start)
		}
		x += w
		i = j
	}
	sh.Pos[n] = x
}

// arabicFallback returns the presentation form glyphs of Arabic letters
// for fonts without joining forms in GSUB, and the lam-alef ligatures
// (by cluster of the lam).
func arabicFallback(f *fonts.Font, runes []rune, forms []uint8, offset int, ligatures bool) ([]uint16, map[int]uint16) {
	glyphs := make([]uint16, len(runes))
	var ligs map[int]uint16
	for i, r := range runes {
		if forms[i] == formNone {
			continue
		}
		if pf, ok := bidi.PresentationForms[r]; ok {
			if cp := pf[forms[i]-1]; cp != 0 {
				if g := f.GlyphIndex(cp); g != 0 {
					glyphs[i] = g
				}
			}
		}
		if ligatures && r == 0x644 && i+1 < len(runes) {
			if lg := lamAlef(f, forms[i], runes[i+1]); lg != 0 {
				if ligs == nil {
					ligs = map[int]uint16{}
				}
				ligs[offset+i] = lg
			}
		}
	}
	return glyphs, ligs
}

// positionForeignMarks positions combining marks drawn with another font
// than their base (when no font has both) around the base glyph, like
// HarfBuzz's fallback mark positioning, and joins them to its cluster.
func (s *Shaper) positionForeignMarks(runes []rune, gl []Glyph) {
	for k := 1; k < len(gl); k++ {
		m := &gl[k]
		if m.Face == nil || !isMark(runes[m.Start]) || gl[k-1].Face == m.Face || gl[k-1].Start == m.Start {
			continue
		}
		// The base: the first glyph of the previous cluster.
		b := k - 1
		for b > 0 && gl[b-1].Start == gl[k-1].Start {
			b--
		}
		base := &gl[b]
		if base.Face == nil || isMark(runes[base.Start]) {
			continue
		}
		// Marks of the same cluster in this font.
		e := k + 1
		for e < len(gl) && gl[e].Start == m.Start {
			e++
		}
		bf := base.Face.Font
		bk := base.Size / bf.UnitsPerEm
		x0, y0, _, y1, ok := bf.GlyphExtents(base.ID)
		if !ok {
			continue
		}
		_ = x0
		baseW := bf.Advance(base.ID) * bk
		top, bottom := float64(y1)*bk, float64(y0)*bk
		gap := base.Size / 16
		// Pen position of the mark relative to the base's pen position.
		pen := 0.0
		for q := b; q < k; q++ {
			pen += gl[q].Advance
		}
		for q := k; q < e; q++ {
			g := &gl[q]
			mf := g.Face.Font
			mk := g.Size / mf.UnitsPerEm
			mx0, my0, mx1, my1, ok := mf.GlyphExtents(g.ID)
			if !ok {
				continue
			}
			mw := float64(mx1-mx0) * mk
			g.XOff = -pen + (baseW-mw)/2 - float64(mx0)*mk
			ccc := unorm.CombiningClass(runes[g.Start])
			switch {
			case ccc >= 218 && ccc <= 222, ccc == 233, ccc == 202, ccc == 200:
				g.YOff = bottom - gap - float64(my1)*mk
				bottom -= gap + float64(my1-my0)*mk
			case ccc >= 228 && ccc <= 232, ccc == 234, ccc == 214, ccc == 216:
				g.YOff = top + gap - float64(my0)*mk
				top += gap + float64(my1-my0)*mk
			}
			pen += g.Advance
			g.Advance = 0
			g.Start = base.Start
		}
		k = e - 1
	}
}

// capsEmulated reports whether font-variant-caps is emulated for a face
// (the font lacks the OpenType features).
// isGerman reports whether a language tag is German ("de", "de-DE"...).
func isGerman(lang string) bool {
	l := strings.ToLower(lang)
	return l == "de" || strings.HasPrefix(l, "de-") || strings.HasPrefix(l, "de_")
}

// capsSpace reports whether r goes with the previous character in caps
// emulation (Pango's consider_as_space).
func capsSpace(r rune) bool {
	return unicode.In(r, unicode.Cc, unicode.Cf, unicode.Cs, unicode.Zl, unicode.Zp, unicode.Zs)
}

func capsEmulated(face *fonts.Face, spec Spec, sc int) bool {
	switch spec.Caps {
	case "", "normal", "titling-caps":
		return false
	}
	p := otl.NewPlan(face.Font, otl.Options{Scripts: scriptTagsOf(sc), Language: spec.Lang})
	_, ok := capsFeatures(p.HasFeature, spec.Caps)
	return !ok
}

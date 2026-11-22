package otl

import (
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
	"unicode"

	"github.com/esoteric-programmer/weasyprint-go/internal/fonts"
	"github.com/esoteric-programmer/weasyprint-go/internal/unorm"
)

// Feature is an OpenType feature setting.
type Feature struct {
	Tag   string
	Value int
}

// Arabic joining form masks.
const (
	MaskIsol = 1 << iota
	MaskFina
	MaskMedi
	MaskInit
	maskNumr
	maskFrac
	maskDnom
)

type lookupRef struct {
	index int
	mask  uint32
	value int
}

// Plan is the shaping plan of a font for a script, language, direction
// and feature settings.
type Plan struct {
	font            *fonts.Font
	gsub, gpos      *layoutTable
	gdef            *gdef
	hasGlyphClasses bool
	gsubStages      [][]lookupRef
	gposStages      [][]lookupRef
	kernTable       bool // apply the legacy kern table
	rtl             bool
	arabic          bool
	// ArabicFallback reports that the font has no GSUB joining forms:
	// the caller substitutes Unicode presentation forms.
	ArabicFallback bool
	// features found in the font's GSUB/GPOS for the script.
	found map[string]bool
	// VarDelta returns the delta of a variation store item for the
	// font's variation instance (nil for default instances).
	varDelta func(outer, inner int) float64
}

// Options select a plan.
type Options struct {
	Scripts  []string // OpenType script tags, in order of preference
	Language string   // BCP 47 language tag
	RTL      bool
	Arabic   bool // use the Arabic shaper (joining forms)
	Features []Feature
	// VarDelta, if not nil, gives variation store deltas (for GDEF/GPOS
	// variation device tables) of a variable font instance; VarKey
	// identifies the instance in the plan cache.
	VarDelta func(outer, inner int) float64
	VarKey   string
	// FeatureVariations is the index of the GSUB/GPOS feature variation
	// record matching the instance (-1 for none).
	FeatureVariations [2]int
}

type planKey struct {
	font *fonts.Font
	key  string
}

var plans sync.Map

// NewPlan returns the (cached) plan for a font and options.
func NewPlan(f *fonts.Font, o Options) *Plan {
	var kb strings.Builder
	kb.WriteString(strings.Join(o.Scripts, ","))
	kb.WriteByte('|')
	kb.WriteString(o.Language)
	if o.RTL {
		kb.WriteString("|r")
	}
	if o.Arabic {
		kb.WriteString("|a")
	}
	for _, ft := range o.Features {
		kb.WriteString("|" + ft.Tag + "=" + strconv.Itoa(ft.Value))
	}
	kb.WriteString("|" + o.VarKey)
	kb.WriteString("|" + strconv.Itoa(o.FeatureVariations[0]) + "," + strconv.Itoa(o.FeatureVariations[1]))
	k := planKey{f, kb.String()}
	if p, ok := plans.Load(k); ok {
		return p.(*Plan)
	}
	p := buildPlan(f, o)
	plans.Store(k, p)
	return p
}

type planFeature struct {
	tag   string
	value int
	mask  uint32
	stage int
}

func buildPlan(f *fonts.Font, o Options) (p *Plan) {
	p = &Plan{font: f, rtl: o.RTL, arabic: o.Arabic, found: map[string]bool{}, varDelta: o.VarDelta}
	defer func() {
		if recover() != nil {
			p = &Plan{font: f, rtl: o.RTL, arabic: o.Arabic, found: map[string]bool{}, kernTable: true}
		}
	}()
	p.gsub = newLayoutTable(f.Table("GSUB"), false)
	p.gpos = newLayoutTable(f.Table("GPOS"), true)
	p.gdef = newGDEF(f.Table("GDEF"))
	p.hasGlyphClasses = p.gdef != nil && p.gdef.classDef != 0
	langs := languageTags(o.Language)

	// Features, by stage (HarfBuzz's default and Arabic shapers).
	var feats []planFeature
	stage := 0
	add := func(tag string, mask uint32) {
		feats = append(feats, planFeature{tag, 1, mask, stage})
	}
	add("rvrn", 0)
	stage++
	if o.RTL {
		add("rtla", 0)
		add("rtlm", 0)
	} else {
		add("ltra", 0)
		add("ltrm", 0)
	}
	// Automatic fractions around U+2044 FRACTION SLASH.
	add("frac", maskFrac)
	add("numr", maskNumr)
	add("dnom", maskDnom)
	if o.Arabic {
		add("stch", 0)
		stage++
		add("ccmp", 0)
		add("locl", 0)
		stage++
		for _, ff := range []struct {
			tag  string
			mask uint32
		}{{"isol", MaskIsol}, {"fina", MaskFina}, {"fin2", MaskFina}, {"fin3", MaskFina},
			{"medi", MaskMedi}, {"med2", MaskMedi}, {"init", MaskInit}} {
			add(ff.tag, ff.mask)
			stage++
		}
		add("rlig", 0)
		stage++
		add("rclt", 0)
		add("calt", 0)
		stage++
		add("mset", 0)
	}
	for _, t := range []string{"abvm", "blwm", "ccmp", "locl", "mark", "mkmk", "rlig",
		"calt", "clig", "curs", "dist", "kern", "liga", "rclt"} {
		add(t, 0)
	}
	// User features override the defaults.
	for _, uf := range o.Features {
		replaced := false
		for i := range feats {
			if feats[i].tag == uf.Tag && feats[i].mask == 0 {
				feats[i].value = uf.Value
				replaced = true
			}
		}
		if !replaced {
			feats = append(feats, planFeature{uf.Tag, uf.Value, 0, stage})
		}
	}
	kernOn := true
	for _, ft := range feats {
		if ft.tag == "kern" {
			kernOn = ft.value != 0
		}
	}

	build := func(t *layoutTable, fv int) [][]lookupRef {
		if t == nil {
			return nil
		}
		ls := t.langSys(o.Scripts, langs)
		if ls == 0 {
			return nil
		}
		subst := t.featureVariations(fv)
		stages := make([][]lookupRef, stage+1)
		addLookups := func(st int, lookups []int, mask uint32, value int) {
			for _, li := range lookups {
				stages[st] = append(stages[st], lookupRef{li, mask, value})
			}
		}
		// Required feature.
		if req := t.featureLookups(ls, "", subst); len(req) > 0 {
			addLookups(0, req, 0, 1)
		}
		for _, ft := range feats {
			if t.hasFeature(ls, ft.tag) {
				p.found[ft.tag] = true
			}
			if ft.value == 0 {
				continue
			}
			addLookups(ft.stage, t.featureLookups(ls, ft.tag, subst), ft.mask, ft.value)
		}
		for i, st := range stages {
			stages[i] = mergeLookups(st)
		}
		return stages
	}
	p.gsubStages = build(p.gsub, o.FeatureVariations[0])
	gposKern := false
	if p.gpos != nil {
		if ls := p.gpos.langSys(o.Scripts, langs); ls != 0 {
			gposKern = p.gpos.hasFeature(ls, "kern")
		}
	}
	p.gposStages = build(p.gpos, o.FeatureVariations[1])
	p.kernTable = kernOn && !gposKern && len(f.Table("kern")) > 0
	if o.Arabic {
		p.ArabicFallback = !p.found["init"] && !p.found["medi"] && !p.found["fina"] && !p.found["isol"]
	}
	return p
}

func mergeLookups(st []lookupRef) []lookupRef {
	sort.SliceStable(st, func(i, j int) bool { return st[i].index < st[j].index })
	out := st[:0]
	for _, r := range st {
		if n := len(out); n > 0 && out[n-1].index == r.index {
			if out[n-1].mask == 0 || r.mask == 0 {
				out[n-1].mask = 0
			} else {
				out[n-1].mask |= r.mask
			}
			continue
		}
		out = append(out, r)
	}
	return out
}

// HasFeature reports whether the font has a GSUB or GPOS feature for the
// plan's script and language.
func (p *Plan) HasFeature(tag string) bool { return p.found[tag] }

// languageTags returns the OpenType language system tags of a BCP 47
// language tag.
func languageTags(lang string) []string {
	if lang == "" {
		return nil
	}
	l := strings.ToLower(lang)
	if i := strings.IndexAny(l, "-_"); i >= 0 {
		l = l[:i]
	}
	s, ok := langTags[l]
	if !ok {
		if len(l) == 3 {
			return []string{strings.ToUpper(l) + " "}
		}
		return nil
	}
	var out []string
	for i := 0; i+4 <= len(s); i += 4 {
		out = append(out, s[i:i+4])
	}
	return out
}

// shaper is the state of one shaping call.
type shaper struct {
	*Plan
	buf       []Glyph
	k         float64 // Pango units per font unit
	size      float64
	nextLigID uint8
	depth     int
}

func (s *shaper) scale(v int) int32 {
	return int32(math.Round(float64(v) * s.k))
}

func isDefaultIgnorable(r rune) bool {
	switch {
	case r == 0x00ad, r == 0x034f, r == 0x061c, r == 0x115f, r == 0x1160,
		r == 0x17b4, r == 0x17b5, r >= 0x180b && r <= 0x180f,
		r >= 0x200b && r <= 0x200f, r >= 0x202a && r <= 0x202e,
		r >= 0x2060 && r <= 0x206f, r == 0x3164, r >= 0xfe00 && r <= 0xfe0f,
		r == 0xfeff, r == 0xffa0, r >= 0xfff0 && r <= 0xfff8:
		return true
	}
	return false
}

// Input is a run of text to shape.
type Input struct {
	Runes []rune
	// Offset is added to the clusters (the index of Runes[0] in the
	// whole text).
	Offset int
	// Size is the font size in px.
	Size float64
	// Masks are per-rune feature masks (Arabic joining forms), or nil.
	Masks []uint32
	// Glyphs, if not nil, overrides the nominal glyph of runes (non-zero
	// entries), e.g. for presentation forms.
	Glyphs []uint16
}

// Shape shapes a run. The glyphs are returned in logical order.
func (p *Plan) Shape(in Input) (out []Glyph) {
	f := p.font
	s := &shaper{Plan: p, size: in.Size, k: in.Size * 1024 / f.UnitsPerEm}
	s.buf = make([]Glyph, 0, len(in.Runes))
	for i, r := range in.Runes {
		g := Glyph{cp: r, Cluster: in.Offset + i}
		if in.Masks != nil {
			g.mask = in.Masks[i]
		}
		s.setUnicodeProps(&g)
		if in.Glyphs != nil && in.Glyphs[i] != 0 {
			g.ID = in.Glyphs[i]
		} else {
			g.ID = s.nominal(r)
		}
		// Grapheme clusters (HarfBuzz's monotone graphemes level).
		if i > 0 && (g.unicodeMark || g.zwj || g.zwnj || (r >= 0x1f3fb && r <= 0x1f3ff) ||
			(r >= 0xff9e && r <= 0xff9f) || (r >= 0xe0020 && r <= 0xe007f)) {
			g.Cluster = s.buf[len(s.buf)-1].Cluster
		}
		s.buf = append(s.buf, g)
	}
	defer func() {
		if e := recover(); e != nil {
			if e != errBounds {
				panic(e)
			}
			// Broken tables: nominal glyphs with default advances.
			out = s.fallbackOutput(in)
		}
	}()
	if in.Glyphs == nil {
		s.normalize()
	}
	s.fractions()
	for i := range s.buf {
		s.setProps(&s.buf[i], 0)
		if !s.hasGlyphClasses {
			g := &s.buf[i]
			if g.nonSpacing && !g.ignorable {
				g.props = propMark
			} else {
				g.props = propBase
			}
		}
	}
	// Substitutions.
	if p.gsub != nil {
		for _, st := range p.gsubStages {
			for _, lr := range st {
				s.applyLookupAll(p.gsub, lr.index, lr.mask, lr.value)
			}
		}
	}
	// Positions.
	for i := range s.buf {
		g := &s.buf[i]
		if g.ignorable {
			continue
		}
		g.XAdvance = s.scale(int(f.Advance(g.ID)))
	}
	hasGPOS := p.gpos != nil && len(p.gpos.lookups) > 0
	if hasGPOS {
		for _, st := range p.gposStages {
			for _, lr := range st {
				s.applyLookupAll(p.gpos, lr.index, lr.mask, lr.value)
			}
		}
	}
	if p.kernTable {
		s.applyKernTable()
	}
	// Zero the advances of marks (HarfBuzz's "by GDEF, late").
	adjust := !hasGPOS && !p.kernTable && !s.rtl
	for i := range s.buf {
		g := &s.buf[i]
		if g.props&propMark != 0 {
			if adjust {
				g.XOffset -= g.XAdvance
				g.YOffset -= g.YAdvance
			}
			g.XAdvance, g.YAdvance = 0, 0
		}
	}
	s.propagateAttachments()
	for i := range s.buf {
		if g := &s.buf[i]; g.ignorable {
			g.XAdvance, g.YAdvance, g.XOffset, g.YOffset = 0, 0, 0, 0
		}
	}
	if !hasGPOS && !p.kernTable {
		s.fallbackMarkPositions()
	}
	return s.buf
}

func (s *shaper) fallbackOutput(in Input) []Glyph {
	out := make([]Glyph, len(in.Runes))
	for i, r := range in.Runes {
		g := Glyph{cp: r, Cluster: in.Offset + i}
		s.setUnicodeProps(&g)
		g.ID = s.nominal(r)
		if !g.ignorable {
			g.XAdvance = s.scale(int(s.font.Advance(g.ID)))
		}
		out[i] = g
	}
	return out
}

func (s *shaper) setUnicodeProps(g *Glyph) {
	r := g.cp
	g.ignorable = isDefaultIgnorable(r)
	g.zwnj = r == 0x200c
	g.zwj = r == 0x200d
	if r >= 0x300 {
		g.nonSpacing = unicode.Is(unicode.Mn, r)
		g.unicodeMark = g.nonSpacing || unicode.Is(unicode.Mc, r) || unicode.Is(unicode.Me, r)
		g.ccc = unorm.CombiningClass(r)
	} else {
		g.nonSpacing, g.unicodeMark, g.ccc = false, false, 0
	}
}

// nominal returns the glyph of a character, with the replacements
// Pango and HarfBuzz make for spaces and controls.
func (s *shaper) nominal(r rune) uint16 {
	f := s.font
	switch {
	case r == '\n' || r == '\r' || isDefaultIgnorable(r):
		return 0
	case r == '\t':
		return f.GlyphIndex(' ')
	}
	g := f.GlyphIndex(r)
	if g == 0 && unicode.Is(unicode.Zs, r) {
		g = f.GlyphIndex(' ')
	}
	if g == 0 && r == 0x2011 {
		g = f.GlyphIndex(0x2010)
	}
	return g
}

// normalize decomposes characters missing from the font, reorders marks
// and composes characters the font has (HarfBuzz's "composed diacritics"
// normalization).
func (s *shaper) normalize() {
	need := false
	for i := range s.buf {
		g := &s.buf[i]
		if g.unicodeMark || (g.ID == 0 && !g.ignorable && g.cp > 0x7f) {
			need = true
			break
		}
	}
	if !need {
		return
	}
	f := s.font
	// Decompose.
	out := make([]Glyph, 0, len(s.buf))
	for _, g := range s.buf {
		if g.ID != 0 || g.ignorable || g.cp < 0x80 {
			out = append(out, g)
			continue
		}
		if parts, ok := s.decompose(g.cp); ok {
			for _, r := range parts {
				ng := g
				ng.cp = r
				s.setUnicodeProps(&ng)
				ng.ID = f.GlyphIndex(r)
				out = append(out, ng)
			}
			continue
		}
		out = append(out, g)
	}
	// Reorder marks by combining class.
	for i := 0; i < len(out); i++ {
		if out[i].ccc == 0 {
			continue
		}
		j := i
		for j < len(out) && out[j].ccc != 0 {
			j++
		}
		if j-i > 1 && j-i <= 32 {
			seg := out[i:j]
			sort.SliceStable(seg, func(a, b int) bool { return seg[a].ccc < seg[b].ccc })
		}
		i = j
	}
	// Recompose.
	res := out[:0:0]
	starter := -1
	for _, g := range out {
		if g.unicodeMark && starter >= 0 {
			prev := res[len(res)-1]
			if starter == len(res)-1 || prev.ccc < g.ccc {
				if c, ok := unorm.Compose(res[starter].cp, g.cp); ok {
					if id := f.GlyphIndex(c); id != 0 {
						st := &res[starter]
						st.cp = c
						st.ID = id
						cl := st.Cluster
						s.setUnicodeProps(st)
						st.Cluster = min(cl, g.Cluster)
						continue
					}
				}
			}
		}
		res = append(res, g)
		if g.ccc == 0 {
			starter = len(res) - 1
		}
	}
	s.buf = res
}

// decompose returns the canonical decomposition of r into characters the
// font has (HarfBuzz's shortest decomposition).
func (s *shaper) decompose(r rune) ([]rune, bool) {
	a, b, ok := unorm.Decompose(r)
	if !ok {
		return nil, false
	}
	f := s.font
	if b != 0 && f.GlyphIndex(b) == 0 {
		return nil, false
	}
	if f.GlyphIndex(a) != 0 {
		if b != 0 {
			return []rune{a, b}, true
		}
		return []rune{a}, true
	}
	if parts, ok := s.decompose(a); ok {
		if b != 0 {
			parts = append(parts, b)
		}
		return parts, true
	}
	return nil, false
}

// applyKernTable applies the legacy kern table: the kerning of a pair is
// split between the two glyphs, as HarfBuzz does.
func (s *shaper) applyKernTable() {
	m := &matcher{s: s, flag: 8, ignoreZWNJ: true}
	for i := 0; i < len(s.buf); i++ {
		j := m.next(i)
		if j < 0 {
			break
		}
		if s.buf[i].ignorable || s.buf[i].props&propMark != 0 {
			continue
		}
		k := s.font.KernTable(s.buf[i].ID, s.buf[j].ID)
		if k == 0 {
			continue
		}
		kern := s.scale(int(k))
		k1 := kern >> 1
		k2 := kern - k1
		s.buf[i].XAdvance += k1
		s.buf[j].XAdvance += k2
		s.buf[j].XOffset += k2
	}
}

// Combining classes used by fallback mark positioning.
const (
	cccAttachedBelowLeft  = 200
	cccAttachedBelow      = 202
	cccAttachedAbove      = 214
	cccAttachedAboveRight = 216
	cccBelowLeft          = 218
	cccBelow              = 220
	cccBelowRight         = 222
	cccAboveLeft          = 228
	cccAbove              = 230
	cccAboveRight         = 232
	cccDoubleBelow        = 233
	cccDoubleAbove        = 234
)

type extents struct {
	xBearing, yBearing, width, height int32
}

func (s *shaper) extents(id uint16) (extents, bool) {
	x0, y0, x1, y1, ok := s.font.GlyphExtents(id)
	if !ok {
		return extents{}, false
	}
	return extents{s.scale(x0), s.scale(y1), s.scale(x1 - x0), s.scale(y0 - y1)}, true
}

// fallbackMarkPositions positions marks around their bases with glyph
// extents, for fonts without GPOS (HarfBuzz's fallback positioning).
func (s *shaper) fallbackMarkPositions() {
	start := 0
	for i := 1; i < len(s.buf); i++ {
		if !s.buf[i].unicodeMark {
			s.positionCluster(start, i)
			start = i
		}
	}
	s.positionCluster(start, len(s.buf))
}

func (s *shaper) positionCluster(start, end int) {
	if end-start < 2 {
		return
	}
	for i := start; i < end; i++ {
		if !s.buf[i].unicodeMark {
			j := i + 1
			for j < end && s.buf[j].unicodeMark {
				j++
			}
			s.positionAroundBase(i, j)
			i = j - 1
		}
	}
}

func (s *shaper) positionAroundBase(base, end int) {
	b := &s.buf[base]
	be, ok := s.extents(b.ID)
	if !ok {
		for i := base + 1; i < end; i++ {
			if s.buf[i].ccc != 0 {
				s.buf[i].XAdvance, s.buf[i].YAdvance = 0, 0
			}
		}
		return
	}
	be.yBearing += b.YOffset
	be.xBearing = 0
	be.width = s.scale(int(s.font.Advance(b.ID)))
	ligID := b.ligID
	numComps := int(b.ligComps)
	var xOff, yOff int32
	if !s.rtl {
		xOff -= b.XAdvance
		yOff -= b.YAdvance
	}
	component := be
	lastComp := -1
	lastClass := -1
	cluster := be
	for i := base + 1; i < end; i++ {
		g := &s.buf[i]
		if g.ccc == 0 {
			if !s.rtl {
				xOff -= g.XAdvance
				yOff -= g.YAdvance
			} else {
				xOff += g.XAdvance
				yOff += g.YAdvance
			}
			continue
		}
		if numComps > 1 {
			comp := int(g.ligComp) - 1
			if ligID == 0 || ligID != g.ligID || comp >= numComps || comp < 0 {
				comp = numComps - 1
			}
			if lastComp != comp {
				lastComp = comp
				lastClass = -1
				component = be
				if !s.rtl {
					component.xBearing += int32(comp) * component.width / int32(numComps)
				} else {
					component.xBearing += int32(numComps-1-comp) * component.width / int32(numComps)
				}
				component.width /= int32(numComps)
			}
		}
		if int(g.ccc) != lastClass {
			lastClass = int(g.ccc)
			cluster = component
		}
		s.positionMark(&cluster, g)
		g.XAdvance, g.YAdvance = 0, 0
		g.XOffset += xOff
		g.YOffset += yOff
	}
}

func (s *shaper) positionMark(be *extents, g *Glyph) {
	me, ok := s.extents(g.ID)
	if !ok {
		return
	}
	yGap := int32(math.Round(s.size * 1024 / 16))
	g.XOffset, g.YOffset = 0, 0
	switch g.ccc {
	case cccDoubleBelow, cccDoubleAbove:
		if !s.rtl {
			g.XOffset += be.xBearing + be.width - me.width/2 - me.xBearing
		} else {
			g.XOffset += be.xBearing - me.width/2 - me.xBearing
		}
	case cccAttachedBelowLeft, cccBelowLeft, cccAboveLeft:
		g.XOffset += be.xBearing - me.xBearing
	case cccAttachedAboveRight, cccBelowRight, cccAboveRight:
		g.XOffset += be.xBearing + be.width - me.width - me.xBearing
	default:
		g.XOffset += be.xBearing + (be.width-me.width)/2 - me.xBearing
	}
	switch g.ccc {
	case cccDoubleBelow, cccBelowLeft, cccBelow, cccBelowRight, cccAttachedBelowLeft, cccAttachedBelow:
		if g.ccc != cccAttachedBelowLeft && g.ccc != cccAttachedBelow {
			be.height -= yGap
		}
		g.YOffset = be.yBearing + be.height - me.yBearing
		if (yGap > 0) == (g.YOffset > 0) {
			be.height -= g.YOffset
			g.YOffset = 0
		}
		be.height += me.height
	case cccDoubleAbove, cccAboveLeft, cccAbove, cccAboveRight, cccAttachedAbove, cccAttachedAboveRight:
		if g.ccc != cccAttachedAbove && g.ccc != cccAttachedAboveRight {
			be.yBearing += yGap
			be.height -= yGap
		}
		g.YOffset = be.yBearing - (me.yBearing + me.height)
		if (yGap > 0) != (g.YOffset > 0) {
			correction := -g.YOffset / 2
			be.yBearing += correction
			be.height -= correction
			g.YOffset += correction
		}
		be.yBearing -= me.height
		be.height += me.height
	}
}

// fractions marks the digits around fraction slashes for the numr, frac
// and dnom features (HarfBuzz's automatic fractions).
func (s *shaper) fractions() {
	if !s.found["frac"] && !(s.found["numr"] && s.found["dnom"]) {
		return
	}
	isDigit := func(i int) bool { return unicode.IsDigit(s.buf[i].cp) }
	for i := range s.buf {
		if s.buf[i].cp != 0x2044 {
			continue
		}
		start, end := i, i+1
		for start > 0 && isDigit(start-1) {
			start--
		}
		for end < len(s.buf) && isDigit(end) {
			end++
		}
		if start == i || end == i+1 {
			continue
		}
		for j := start; j < i; j++ {
			s.buf[j].mask |= maskNumr
		}
		s.buf[i].mask |= maskFrac
		for j := i + 1; j < end; j++ {
			s.buf[j].mask |= maskDnom
		}
	}
}

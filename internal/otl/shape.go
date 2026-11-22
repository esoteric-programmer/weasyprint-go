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
	hebrew          bool
	hasGPOSMark     bool
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
	p = &Plan{font: f, rtl: o.RTL, arabic: o.Arabic, found: map[string]bool{}, varDelta: o.VarDelta,
		hebrew: len(o.Scripts) > 0 && o.Scripts[0] == "hebr"}
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
			p.hasGPOSMark = p.gpos.hasFeature(ls, "mark")
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
	continued := false
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
		cont := g.unicodeMark || g.zwj || (r >= 0x1f3fb && r <= 0x1f3ff) ||
			(r >= 0xff9e && r <= 0xff9f) || (r >= 0xe0020 && r <= 0xe007f)
		if i > 0 && isRegionalIndicator(r) && isRegionalIndicator(in.Runes[i-1]) && !continued {
			cont = true
		}
		if i > 0 && in.Runes[i-1] == 0x200d && isPictographic(r) {
			cont = true
		}
		continued = cont && i > 0
		if continued {
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
		for i := range s.buf {
			if g := &s.buf[i]; g.nonSpacing {
				g.ccc = recategorize(g.cp, g.ccc)
			}
		}
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
		g.ccc = modifiedCCC(r, unorm.CombiningClass(r))
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
// normalization): characters followed by marks are fully decomposed
// before recomposition, others only when the font lacks them.
func (s *shaper) normalize() {
	f := s.font
	in := s.buf
	n := len(in)
	out := make([]Glyph, 0, n)
	emit := func(g Glyph, shortest bool) {
		if g.ignorable || g.cp < 0x80 {
			out = append(out, g)
			return
		}
		if shortest && g.ID != 0 {
			out = append(out, g)
			return
		}
		if parts := s.decompose(g.cp, shortest); parts != nil {
			for _, r := range parts {
				ng := g
				ng.cp = r
				s.setUnicodeProps(&ng)
				ng.ID = f.GlyphIndex(r)
				out = append(out, ng)
			}
			return
		}
		out = append(out, g)
	}
	allSimple := true
	for i := 0; i < n; {
		end := i + 1
		for end < n && !in[end].unicodeMark {
			end++
		}
		if end < n {
			end-- // the base of the following marks
		}
		for ; i < end; i++ {
			emit(in[i], true)
		}
		if i >= n {
			break
		}
		allSimple = false
		end = i + 1
		for end < n && in[end].unicodeMark {
			end++
		}
		for ; i < end; i++ {
			emit(in[i], false)
		}
	}
	if allSimple {
		s.buf = out
		return
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
	starter := 0
	for k, g := range out {
		if k > 0 && g.unicodeMark && (starter == len(res)-1 || res[len(res)-1].ccc < g.ccc) {
			if c, ok := s.compose(res[starter].cp, g.cp); ok {
				if id := f.GlyphIndex(c); id != 0 {
					st := &res[starter]
					cl := st.Cluster
					st.cp = c
					st.ID = id
					s.setUnicodeProps(st)
					st.Cluster = min(cl, g.Cluster)
					continue
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
// font has: the shortest one, or the longest one (HarfBuzz's decompose).
func (s *shaper) decompose(r rune, shortest bool) []rune {
	a, b, ok := unorm.Decompose(r)
	if !ok {
		return nil
	}
	f := s.font
	if b != 0 && f.GlyphIndex(b) == 0 {
		return nil
	}
	hasA := f.GlyphIndex(a) != 0
	pair := func(head []rune) []rune {
		if b != 0 {
			return append(head, b)
		}
		return head
	}
	if shortest && hasA {
		return pair([]rune{a})
	}
	if parts := s.decompose(a, shortest); parts != nil {
		return pair(parts)
	}
	if hasA {
		return pair([]rune{a})
	}
	return nil
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
		pre, post := uint32(maskNumr|maskFrac), uint32(maskFrac|maskDnom)
		if s.rtl {
			pre, post = post, pre
		}
		for j := start; j < i; j++ {
			s.buf[j].mask |= pre
		}
		s.buf[i].mask |= maskFrac
		for j := i + 1; j < end; j++ {
			s.buf[j].mask |= post
		}
	}
}

// modifiedCCC returns HarfBuzz's modified combining class of a character
// (Hebrew, Arabic, Syriac, Telugu, Thai, Lao and Tibetan classes are
// renumbered for a better mark order).
func modifiedCCC(r rune, c uint8) uint8 {
	switch r {
	case 0x1a60, 0x0fc6:
		return 254
	case 0x0f39:
		return 127
	}
	switch {
	case c >= 10 && c <= 26:
		return [...]uint8{22, 15, 16, 17, 23, 18, 19, 20, 21, 14, 24, 12, 25, 13, 10, 11, 26}[c-10]
	case c >= 27 && c <= 35:
		return [...]uint8{28, 29, 30, 31, 32, 33, 27, 34, 35}[c-27]
	}
	switch c {
	case 84:
		return 4
	case 91:
		return 5
	case 103:
		return 3
	case 130:
		return 132
	case 132:
		return 131
	}
	return c
}

// recategorize maps modified combining classes to positional classes for
// fallback mark positioning (HarfBuzz's recategorize_combining_class).
func recategorize(u rune, c uint8) uint8 {
	if c >= 200 {
		return c
	}
	if u&^0xff == 0x0e00 {
		if c == 0 {
			switch u {
			case 0x0e31, 0x0e34, 0x0e35, 0x0e36, 0x0e37, 0x0e47, 0x0e4c, 0x0e4d, 0x0e4e:
				c = cccAboveRight
			case 0x0eb1, 0x0eb4, 0x0eb5, 0x0eb6, 0x0eb7, 0x0ebb, 0x0ecc, 0x0ecd:
				c = cccAbove
			case 0x0ebc:
				c = cccBelow
			}
		} else if u == 0x0e3a {
			c = cccBelowRight
		}
	}
	switch c {
	case 22, 15, 16, 17, 23, 18, 19, 20, 21, 24, 25:
		return cccBelow
	case 13:
		return cccAttachedAbove
	case 10:
		return cccAboveRight
	case 11, 14:
		return cccAboveLeft
	case 26:
		return cccAbove
	case 28, 29, 31, 32, 27, 34, 35, 36:
		return cccAbove
	case 30, 33:
		return cccBelow
	case 3:
		return cccBelowRight
	case 107:
		return cccAboveRight
	case 118, 129, 131:
		return cccBelow
	case 122, 132:
		return cccAbove
	}
	return c
}

var dageshForms = [...]rune{0xfb30, 0xfb31, 0xfb32, 0xfb33, 0xfb34, 0xfb35, 0xfb36, 0, 0xfb38, 0xfb39,
	0xfb3a, 0xfb3b, 0xfb3c, 0, 0xfb3e, 0, 0xfb40, 0xfb41, 0, 0xfb43, 0xfb44, 0, 0xfb46, 0xfb47, 0xfb48,
	0xfb49, 0xfb4a}

// compose composes two characters, with the Hebrew presentation forms
// HarfBuzz's Hebrew shaper uses for fonts without mark positioning.
func (s *shaper) compose(a, b rune) (rune, bool) {
	if c, ok := unorm.Compose(a, b); ok || !s.hebrew || s.hasGPOSMark {
		return c, ok
	}
	switch b {
	case 0x05b4:
		if a == 0x05d9 {
			return 0xfb1d, true
		}
	case 0x05b7:
		if a == 0x05f2 {
			return 0xfb1f, true
		} else if a == 0x05d0 {
			return 0xfb2e, true
		}
	case 0x05b8:
		if a == 0x05d0 {
			return 0xfb2f, true
		}
	case 0x05b9:
		if a == 0x05d5 {
			return 0xfb4b, true
		}
	case 0x05bc:
		if a >= 0x05d0 && a <= 0x05ea {
			c := dageshForms[a-0x05d0]
			return c, c != 0
		} else if a == 0xfb2a {
			return 0xfb2c, true
		} else if a == 0xfb2b {
			return 0xfb2d, true
		}
	case 0x05bf:
		switch a {
		case 0x05d1:
			return 0xfb4c, true
		case 0x05db:
			return 0xfb4d, true
		case 0x05e4:
			return 0xfb4e, true
		}
	case 0x05c1:
		if a == 0x05e9 {
			return 0xfb2a, true
		} else if a == 0xfb49 {
			return 0xfb2c, true
		}
	case 0x05c2:
		if a == 0x05e9 {
			return 0xfb2b, true
		} else if a == 0xfb49 {
			return 0xfb2d, true
		}
	}
	return 0, false
}

func isRegionalIndicator(r rune) bool { return r >= 0x1f1e6 && r <= 0x1f1ff }

// isPictographic approximates Extended_Pictographic.
func isPictographic(r rune) bool {
	return (r >= 0x1f000 && r <= 0x1faff) || (r >= 0x2600 && r <= 0x27bf) || (r >= 0x2300 && r <= 0x23ff) ||
		(r >= 0x2b00 && r <= 0x2bff) || r == 0x00a9 || r == 0x00ae || r == 0x203c || r == 0x2049 || r == 0x2122 ||
		(r >= 0x2190 && r <= 0x21ff) || (r >= 0x1fc00 && r <= 0x1fffd)
}

package otl

// Glyph properties (as HarfBuzz's glyph props).
const (
	propBase = 0x02
	propLig  = 0x04
	propMark = 0x08
)

// Attachment types.
const (
	attachMark    = 1
	attachCursive = 2
)

// Glyph is a glyph of a shaped run. Positions are in units of 1/1024 px
// (Pango units).
type Glyph struct {
	ID uint16
	// Cluster is the index of the first rune of the glyph's cluster in
	// the shaped text.
	Cluster int
	// Positions.
	XAdvance, YAdvance, XOffset, YOffset int32

	cp          rune // character (0 for glyphs without one)
	mask        uint32
	props       uint8
	ccc         uint8
	unicodeMark bool // general category Mn, Mc or Me
	nonSpacing  bool // general category Mn
	ligID       uint8
	ligComp     uint8
	ligComps    uint8
	multiplied  bool
	ignorable   bool
	zwnj, zwj   bool
	attachChain int
	attachType  uint8
}

// Rune returns the character the glyph stands for (0 for ligatures of
// several characters and glyphs added by substitutions).
func (g *Glyph) Rune() rune { return g.cp }

// IsMark reports whether the glyph was classified as a mark.
func (g *Glyph) IsMark() bool { return g.props&propMark != 0 }

// matcher decides which glyphs a lookup skips.
type matcher struct {
	s         *shaper
	flag      uint16
	filterSet int
	mask      uint32 // 0: any
	// ignoreZWNJ makes ZWNJ skippable (positioning and context).
	ignoreZWNJ bool
}

// ignoredByFlag reports whether the lookup flag makes the glyph invisible.
func (m *matcher) ignoredByFlag(g *Glyph) bool {
	p := g.props
	if p&propBase != 0 && m.flag&2 != 0 {
		return true
	}
	if p&propLig != 0 && m.flag&4 != 0 {
		return true
	}
	if p&propMark != 0 {
		if m.flag&8 != 0 {
			return true
		}
		if m.flag&0x10 != 0 {
			return !m.s.gdef.inMarkSet(m.filterSet, g.ID)
		}
		if t := m.flag >> 8; t != 0 && uint16(m.s.gdef.markAttachClass(g.ID)) != t {
			return true
		}
	}
	return false
}

// skip reports whether glyph g is skipped while matching.
func (m *matcher) skip(g *Glyph) bool {
	if m.ignoredByFlag(g) {
		return true
	}
	if g.ignorable && (!g.zwnj || m.ignoreZWNJ) {
		return true
	}
	return false
}

// next returns the next glyph after i that is not skipped, or -1.
func (m *matcher) next(i int) int {
	buf := m.s.buf
	for j := i + 1; j < len(buf); j++ {
		if m.skip(&buf[j]) {
			continue
		}
		return j
	}
	return -1
}

// prev returns the previous glyph before i that is not skipped, or -1.
func (m *matcher) prev(i int) int {
	buf := m.s.buf
	for j := i - 1; j >= 0; j-- {
		if m.skip(&buf[j]) {
			continue
		}
		return j
	}
	return -1
}

// matchFunc tests the k-th glyph of a sequence.
type matchFunc func(g uint16, k int) bool

// matchInput matches count-1 more glyphs after i (the first glyph is at
// i). It returns the positions of the matched glyphs and true.
func (m *matcher) matchInput(i, count int, f matchFunc) ([]int, bool) {
	pos := make([]int, 0, count)
	pos = append(pos, i)
	j := i
	for k := 1; k < count; k++ {
		j = m.next(j)
		if j < 0 {
			return nil, false
		}
		g := &m.s.buf[j]
		if m.mask != 0 && g.mask&m.mask == 0 {
			return nil, false
		}
		if !f(g.ID, k) {
			return nil, false
		}
		pos = append(pos, j)
	}
	return pos, true
}

// matchBacktrack matches count glyphs before i (the k-th one being the
// k-th glyph backwards).
func (m *matcher) matchBacktrack(i, count int, f matchFunc) bool {
	j := i
	for k := 0; k < count; k++ {
		j = m.prev(j)
		if j < 0 || !f(m.s.buf[j].ID, k) {
			return false
		}
	}
	return true
}

// matchLookahead matches count glyphs after end (exclusive position of
// the last input glyph).
func (m *matcher) matchLookahead(last, count int, f matchFunc) bool {
	j := last
	for k := 0; k < count; k++ {
		j = m.next(j)
		if j < 0 || !f(m.s.buf[j].ID, k) {
			return false
		}
	}
	return true
}

// mergeClusters gives the glyphs [start, end) the smallest of their
// clusters.
func (s *shaper) mergeClusters(start, end int) {
	if end-start < 2 {
		return
	}
	c := s.buf[start].Cluster
	for i := start + 1; i < end; i++ {
		if s.buf[i].Cluster < c {
			c = s.buf[i].Cluster
		}
	}
	// Extend to the glyphs of the same clusters around.
	for start > 0 && s.buf[start-1].Cluster == s.buf[start].Cluster {
		start--
	}
	for end < len(s.buf) && s.buf[end].Cluster == s.buf[end-1].Cluster {
		end++
	}
	for i := start; i < end; i++ {
		s.buf[i].Cluster = c
	}
}

// setGlyph replaces the glyph id of buf[i] and updates its properties.
func (s *shaper) setGlyph(i int, id uint16, classGuess uint8) {
	g := &s.buf[i]
	g.ID = id
	s.setProps(g, classGuess)
}

func (s *shaper) setProps(g *Glyph, classGuess uint8) {
	if s.hasGlyphClasses {
		switch s.gdef.glyphClass(g.ID) {
		case 1:
			g.props = propBase
		case 2:
			g.props = propLig
		case 3:
			g.props = propMark
		default:
			g.props = 0
		}
		return
	}
	if classGuess != 0 {
		g.props = classGuess
	}
}

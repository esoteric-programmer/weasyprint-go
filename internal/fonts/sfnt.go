// Package fonts parses OpenType/TrueType fonts, discovers system fonts,
// matches CSS font descriptions and subsets fonts for PDF embedding.
package fonts

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"unicode/utf16"
)

// Font is a parsed OpenType font.
type Font struct {
	Data   []byte // the sfnt data (for collections, the whole file)
	Offset int    // offset of the table directory
	tables map[string][]byte
	tabOff map[string]int

	Family        string
	Subfamily     string
	FullName      string
	PostScript    string
	TypoFamily    string
	TypoSubfamily string

	UnitsPerEm                   float64
	Ascent                       float64 // font units, positive
	Descent                      float64 // font units, positive below baseline
	LineGap                      float64
	CapHeight                    float64
	XHeight                      float64
	BBox                         [4]float64
	ItalicAngle                  float64
	UnderlinePos, UnderlineThick float64
	StrikeoutPos, StrikeoutSize  float64
	Weight                       int
	Italic                       bool
	Stretch                      int // OS/2 usWidthClass 1-9
	FixedPitch                   bool
	IsCFF                        bool
	NumGlyphs                    int
	Embeddable                   bool
	Variable                     bool

	advances   []uint16
	cmap       map[rune]uint16
	kern       map[uint32]int16
	gposKern   *pairKerning
	ligatures  map[uint16][]Ligature
	arabic     *ArabicSubst
	arabicOnce sync.Once
	substs     map[string]map[uint16]uint16
	substMu    sync.Mutex
	locaLong   bool
}

type Ligature struct {
	Components []uint16
	Glyph      uint16
}

func u16(b []byte, off int) uint16 {
	if off+2 > len(b) {
		return 0
	}
	return binary.BigEndian.Uint16(b[off:])
}
func i16(b []byte, off int) int16 { return int16(u16(b, off)) }
func u32(b []byte, off int) uint32 {
	if off+4 > len(b) {
		return 0
	}
	return binary.BigEndian.Uint32(b[off:])
}

// ErrUnsupported is returned for unsupported font formats.
var ErrUnsupported = errors.New("unsupported font format")

// Parse parses a font file. index selects a face in a collection.
func Parse(data []byte, index int) (*Font, error) {
	if len(data) < 12 {
		return nil, ErrUnsupported
	}
	switch string(data[:4]) {
	case "wOFF":
		d, err := decodeWOFF(data)
		if err != nil {
			return nil, err
		}
		data = d
	case "wOF2":
		return nil, fmt.Errorf("WOFF2 fonts are not supported")
	}
	off := 0
	if string(data[:4]) == "ttcf" {
		n := int(u32(data, 8))
		if index >= n {
			return nil, fmt.Errorf("font index %d out of range", index)
		}
		off = int(u32(data, 12+4*index))
	}
	f := &Font{Data: data, Offset: off, tables: map[string][]byte{}, tabOff: map[string]int{}}
	if err := f.readDirectory(); err != nil {
		return nil, err
	}
	if err := f.parse(); err != nil {
		return nil, err
	}
	return f, nil
}

// NumFaces returns the number of faces in a font file.
func NumFaces(data []byte) int {
	if len(data) >= 12 && string(data[:4]) == "ttcf" {
		return int(u32(data, 8))
	}
	return 1
}

func (f *Font) readDirectory() error {
	d := f.Data
	o := f.Offset
	if o+12 > len(d) {
		return ErrUnsupported
	}
	tag := string(d[o : o+4])
	if tag != "\x00\x01\x00\x00" && tag != "OTTO" && tag != "true" {
		return ErrUnsupported
	}
	n := int(u16(d, o+4))
	for i := 0; i < n; i++ {
		e := o + 12 + 16*i
		if e+16 > len(d) {
			return ErrUnsupported
		}
		name := string(d[e : e+4])
		toff := int(u32(d, e+8))
		tlen := int(u32(d, e+12))
		if toff+tlen > len(d) || toff < 0 || tlen < 0 {
			continue
		}
		f.tables[name] = d[toff : toff+tlen]
		f.tabOff[name] = toff
	}
	return nil
}

// Table returns the raw data of a table.
func (f *Font) Table(tag string) []byte { return f.tables[tag] }

func (f *Font) parse() error {
	head := f.tables["head"]
	if len(head) < 54 {
		return errors.New("missing head table")
	}
	f.UnitsPerEm = float64(u16(head, 18))
	if f.UnitsPerEm == 0 {
		f.UnitsPerEm = 1000
	}
	f.BBox = [4]float64{float64(i16(head, 36)), float64(i16(head, 38)), float64(i16(head, 40)), float64(i16(head, 42))}
	macStyle := u16(head, 44)
	f.locaLong = i16(head, 50) == 1

	if maxp := f.tables["maxp"]; len(maxp) >= 6 {
		f.NumGlyphs = int(u16(maxp, 4))
	}
	hhea := f.tables["hhea"]
	numHM := 0
	if len(hhea) >= 36 {
		f.Ascent = float64(i16(hhea, 4))
		f.Descent = -float64(i16(hhea, 6))
		f.LineGap = float64(i16(hhea, 8))
		numHM = int(u16(hhea, 34))
	}
	if hmtx := f.tables["hmtx"]; numHM > 0 {
		f.advances = make([]uint16, numHM)
		for i := 0; i < numHM; i++ {
			f.advances[i] = u16(hmtx, 4*i)
		}
	}
	f.Weight = 400
	f.Stretch = 5
	f.Embeddable = true
	if os2 := f.tables["OS/2"]; len(os2) >= 78 {
		f.Weight = int(u16(os2, 4))
		f.Stretch = int(u16(os2, 6))
		fsType := u16(os2, 8)
		f.Embeddable = fsType&0x000f != 0x0002
		fsSel := u16(os2, 62)
		f.Italic = fsSel&1 != 0 || fsSel&0x200 != 0
		typoAsc := float64(i16(os2, 68))
		typoDesc := -float64(i16(os2, 70))
		typoGap := float64(i16(os2, 72))
		winAsc := float64(u16(os2, 74))
		winDesc := float64(u16(os2, 76))
		f.StrikeoutSize = float64(i16(os2, 26))
		f.StrikeoutPos = float64(i16(os2, 28))
		if fsSel&0x80 != 0 || (f.Ascent == 0 && f.Descent == 0) {
			// USE_TYPO_METRICS
			f.Ascent, f.Descent, f.LineGap = typoAsc, typoDesc, typoGap
		}
		if f.Ascent == 0 && f.Descent == 0 {
			f.Ascent, f.Descent = winAsc, winDesc
		}
		if len(os2) >= 90 && u16(os2, 0) >= 2 {
			f.XHeight = float64(i16(os2, 86))
			f.CapHeight = float64(i16(os2, 88))
		}
	} else {
		f.Italic = macStyle&2 != 0
		if macStyle&1 != 0 {
			f.Weight = 700
		}
	}
	if f.Ascent == 0 && f.Descent == 0 {
		f.Ascent, f.Descent = f.BBox[3], -f.BBox[1]
	}
	if f.XHeight == 0 {
		f.XHeight = f.UnitsPerEm * 0.5
	}
	if f.CapHeight == 0 {
		f.CapHeight = f.UnitsPerEm * 0.7
	}
	if f.StrikeoutSize == 0 {
		f.StrikeoutSize = f.UnitsPerEm * 0.05
	}
	if f.StrikeoutPos == 0 {
		f.StrikeoutPos = f.XHeight / 2
	}
	if post := f.tables["post"]; len(post) >= 16 {
		f.ItalicAngle = float64(int32(u32(post, 4))) / 65536
		f.UnderlinePos = float64(i16(post, 8))
		f.UnderlineThick = float64(i16(post, 10))
		f.FixedPitch = u32(post, 12) != 0
	}
	if f.UnderlineThick == 0 {
		f.UnderlineThick = f.UnitsPerEm * 0.05
		f.UnderlinePos = -f.UnitsPerEm * 0.1
	}
	_, f.IsCFF = f.tables["CFF "]
	if _, ok := f.tables["CFF2"]; ok {
		f.IsCFF = true
	}
	_, f.Variable = f.tables["fvar"]
	f.parseNames()
	return nil
}

func decodeNameString(b []byte, platform uint16) string {
	if platform == 0 || platform == 3 {
		u := make([]uint16, len(b)/2)
		for i := range u {
			u[i] = binary.BigEndian.Uint16(b[2*i:])
		}
		return string(utf16.Decode(u))
	}
	// Mac Roman, treat as Latin-1.
	r := make([]rune, len(b))
	for i, c := range b {
		r[i] = rune(c)
	}
	return string(r)
}

func (f *Font) parseNames() {
	name := f.tables["name"]
	if len(name) < 6 {
		return
	}
	count := int(u16(name, 2))
	strOff := int(u16(name, 4))
	type cand struct {
		s     string
		score int
	}
	best := map[uint16]cand{}
	for i := 0; i < count; i++ {
		r := 6 + 12*i
		if r+12 > len(name) {
			break
		}
		platform := u16(name, r)
		enc := u16(name, r+2)
		lang := u16(name, r+4)
		id := u16(name, r+6)
		l := int(u16(name, r+8))
		o := strOff + int(u16(name, r+10))
		if o+l > len(name) {
			continue
		}
		score := 0
		switch {
		case platform == 3 && (enc == 1 || enc == 10) && lang == 0x409:
			score = 4
		case platform == 3 && (enc == 1 || enc == 10):
			score = 3
		case platform == 0:
			score = 2
		case platform == 1 && enc == 0 && lang == 0:
			score = 1
		default:
			continue
		}
		if c, ok := best[id]; ok && c.score >= score {
			continue
		}
		best[id] = cand{decodeNameString(name[o:o+l], platform), score}
	}
	f.Family = best[1].s
	f.Subfamily = best[2].s
	f.FullName = best[4].s
	f.PostScript = best[6].s
	f.TypoFamily = best[16].s
	f.TypoSubfamily = best[17].s
	if f.PostScript == "" {
		f.PostScript = strings.ReplaceAll(f.FullName, " ", "")
	}
}

// Families returns the family names of the font.
func (f *Font) Families() []string {
	var out []string
	if f.TypoFamily != "" {
		out = append(out, f.TypoFamily)
	}
	if f.Family != "" && f.Family != f.TypoFamily {
		out = append(out, f.Family)
	}
	return out
}

// ---------------------------------------------------------------- cmap

// GlyphIndex returns the glyph for a rune, or 0.
func (f *Font) GlyphIndex(r rune) uint16 {
	if f.cmap == nil {
		f.parseCmap()
	}
	return f.cmap[r]
}

// HasRune reports whether the font maps the rune.
func (f *Font) HasRune(r rune) bool { return f.GlyphIndex(r) != 0 }

// Cmap returns the full character map.
func (f *Font) Cmap() map[rune]uint16 {
	if f.cmap == nil {
		f.parseCmap()
	}
	return f.cmap
}

func (f *Font) parseCmap() {
	f.cmap = map[rune]uint16{}
	cm := f.tables["cmap"]
	if len(cm) < 4 {
		return
	}
	n := int(u16(cm, 2))
	bestScore := -1
	bestOff := -1
	symbol := false
	for i := 0; i < n; i++ {
		r := 4 + 8*i
		platform := u16(cm, r)
		enc := u16(cm, r+2)
		off := int(u32(cm, r+4))
		if off >= len(cm) {
			continue
		}
		format := u16(cm, off)
		score := -1
		switch {
		case platform == 3 && enc == 10 && format == 12:
			score = 6
		case platform == 0 && (enc == 4 || enc == 6) && format == 12:
			score = 5
		case platform == 3 && enc == 1 && format == 4:
			score = 4
		case platform == 0 && format == 4:
			score = 3
		case platform == 3 && enc == 0:
			score = 2
		case platform == 1 && enc == 0:
			score = 1
		}
		if score > bestScore {
			bestScore, bestOff = score, off
			symbol = platform == 3 && enc == 0
		}
	}
	if bestOff < 0 {
		return
	}
	t := cm[bestOff:]
	switch u16(t, 0) {
	case 0:
		for c := 0; c < 256 && 6+c < len(t); c++ {
			if g := t[6+c]; g != 0 {
				f.cmap[rune(c)] = uint16(g)
			}
		}
	case 4:
		segX2 := int(u16(t, 6))
		ends := 14
		starts := ends + segX2 + 2
		deltas := starts + segX2
		ranges := deltas + segX2
		for s := 0; s < segX2/2; s++ {
			end := int(u16(t, ends+2*s))
			start := int(u16(t, starts+2*s))
			delta := int(u16(t, deltas+2*s))
			ro := int(u16(t, ranges+2*s))
			if start > end || end == 0xffff && start == 0xffff {
				continue
			}
			for c := start; c <= end && c < 0xffff; c++ {
				var g int
				if ro == 0 {
					g = (c + delta) & 0xffff
				} else {
					gi := ranges + 2*s + ro + 2*(c-start)
					g = int(u16(t, gi))
					if g != 0 {
						g = (g + delta) & 0xffff
					}
				}
				if g != 0 {
					f.cmap[rune(c)] = uint16(g)
				}
			}
		}
	case 6:
		first := int(u16(t, 6))
		count := int(u16(t, 8))
		for i := 0; i < count; i++ {
			if g := u16(t, 10+2*i); g != 0 {
				f.cmap[rune(first+i)] = g
			}
		}
	case 12:
		ngroups := int(u32(t, 12))
		for i := 0; i < ngroups; i++ {
			o := 16 + 12*i
			start := u32(t, o)
			end := u32(t, o+4)
			g := u32(t, o+8)
			if end-start > 0x10ffff {
				continue
			}
			for c := start; c <= end; c++ {
				if g != 0 && g < 65536 {
					f.cmap[rune(c)] = uint16(g)
				}
				g++
			}
		}
	}
	if symbol {
		// Symbol fonts map U+F0xx; also make ASCII available.
		for c := rune(0x20); c < 0x100; c++ {
			if g, ok := f.cmap[0xf000+c]; ok {
				if _, ok2 := f.cmap[c]; !ok2 {
					f.cmap[c] = g
				}
			}
		}
	}
}

// Advance returns the advance width of a glyph in font units.
func (f *Font) Advance(g uint16) float64 {
	if len(f.advances) == 0 {
		return f.UnitsPerEm / 2
	}
	if int(g) < len(f.advances) {
		return float64(f.advances[g])
	}
	return float64(f.advances[len(f.advances)-1])
}

// ---------------------------------------------------------------- kerning

type pairKerning struct {
	format1 []pairPos1
	format2 []pairPos2
}

type pairPos1 struct {
	coverage map[uint16]int
	sets     map[int]map[uint16]int16
}

type pairPos2 struct {
	coverage map[uint16]int
	class1   map[uint16]int
	class2   map[uint16]int
	values   [][]int16
}

// Kern returns the kerning adjustment between two glyphs in font units.
func (f *Font) Kern(left, right uint16) float64 {
	if f.kern == nil && f.gposKern == nil {
		f.parseKerning()
	}
	if f.gposKern != nil {
		for _, p := range f.gposKern.format1 {
			if idx, ok := p.coverage[left]; ok {
				if v, ok := p.sets[idx][right]; ok {
					return float64(v)
				}
			}
		}
		for _, p := range f.gposKern.format2 {
			if _, ok := p.coverage[left]; ok {
				c1 := p.class1[left]
				c2 := p.class2[right]
				if c1 < len(p.values) && c2 < len(p.values[c1]) {
					if v := p.values[c1][c2]; v != 0 {
						return float64(v)
					}
				}
			}
		}
		return 0
	}
	if v, ok := f.kern[uint32(left)<<16|uint32(right)]; ok {
		return float64(v)
	}
	return 0
}

func (f *Font) parseKerning() {
	f.kern = map[uint32]int16{}
	if gpos := f.tables["GPOS"]; len(gpos) > 10 {
		if pk := parseGPOSKern(gpos); pk != nil {
			f.gposKern = pk
			return
		}
	}
	k := f.tables["kern"]
	if len(k) < 4 {
		return
	}
	if u16(k, 0) != 0 {
		return
	}
	n := int(u16(k, 2))
	off := 4
	for i := 0; i < n && off+6 <= len(k); i++ {
		length := int(u16(k, off+2))
		coverage := u16(k, off+4)
		if coverage>>8 == 0 && coverage&1 != 0 {
			np := int(u16(k, off+6))
			for j := 0; j < np; j++ {
				p := off + 14 + 6*j
				if p+6 > len(k) {
					break
				}
				f.kern[u32(k, p)] = i16(k, p+4)
			}
		}
		if length == 0 {
			break
		}
		off += length
	}
}

func parseCoverage(b []byte, off int) map[uint16]int {
	m := map[uint16]int{}
	if off+4 > len(b) {
		return m
	}
	switch u16(b, off) {
	case 1:
		n := int(u16(b, off+2))
		for i := 0; i < n; i++ {
			m[u16(b, off+4+2*i)] = i
		}
	case 2:
		n := int(u16(b, off+2))
		for i := 0; i < n; i++ {
			r := off + 4 + 6*i
			start, end, idx := u16(b, r), u16(b, r+2), int(u16(b, r+4))
			for g := int(start); g <= int(end); g++ {
				m[uint16(g)] = idx + g - int(start)
			}
		}
	}
	return m
}

func parseClassDef(b []byte, off int) map[uint16]int {
	m := map[uint16]int{}
	if off+4 > len(b) {
		return m
	}
	switch u16(b, off) {
	case 1:
		start := int(u16(b, off+2))
		n := int(u16(b, off+4))
		for i := 0; i < n; i++ {
			m[uint16(start+i)] = int(u16(b, off+6+2*i))
		}
	case 2:
		n := int(u16(b, off+2))
		for i := 0; i < n; i++ {
			r := off + 4 + 6*i
			start, end, c := u16(b, r), u16(b, r+2), int(u16(b, r+4))
			for g := int(start); g <= int(end); g++ {
				m[uint16(g)] = c
			}
		}
	}
	return m
}

func valueRecordSize(format uint16) int {
	n := 0
	for i := 0; i < 8; i++ {
		if format&(1<<i) != 0 {
			n += 2
		}
	}
	return n
}

// xAdvance extracts the XAdvance field of a value record.
func xAdvance(b []byte, off int, format uint16) int16 {
	if format&4 == 0 {
		return 0
	}
	p := off
	if format&1 != 0 {
		p += 2
	}
	if format&2 != 0 {
		p += 2
	}
	return i16(b, p)
}

// lookupIndices returns the lookups for a feature tag in DFLT/latn scripts.
func lookupIndices(t []byte, feature string) []int {
	return lookupIndicesFor(t, feature, map[string]bool{"DFLT": true, "latn": true, "cyrl": true, "grek": true})
}

func lookupIndicesFor(t []byte, feature string, scripts map[string]bool) []int {
	scriptList := int(u16(t, 4))
	featureList := int(u16(t, 6))
	var featIdx []int
	nScripts := int(u16(t, scriptList))
	seenLang := false
	for i := 0; i < nScripts; i++ {
		r := scriptList + 2 + 6*i
		tag := string(t[r : r+4])
		if !scripts[tag] {
			continue
		}
		so := scriptList + int(u16(t, r+4))
		dl := int(u16(t, so))
		if dl == 0 {
			continue
		}
		lo := so + dl
		n := int(u16(t, lo+4))
		for j := 0; j < n; j++ {
			featIdx = append(featIdx, int(u16(t, lo+6+2*j)))
		}
		seenLang = true
	}
	nFeat := int(u16(t, featureList))
	var lookups []int
	seen := map[int]bool{}
	for i := 0; i < nFeat; i++ {
		r := featureList + 2 + 6*i
		if r+6 > len(t) {
			break
		}
		if string(t[r:r+4]) != feature {
			continue
		}
		if seenLang {
			ok := false
			for _, fi := range featIdx {
				if fi == i {
					ok = true
				}
			}
			if !ok {
				continue
			}
		}
		fo := featureList + int(u16(t, r+4))
		n := int(u16(t, fo+2))
		for j := 0; j < n; j++ {
			li := int(u16(t, fo+4+2*j))
			if !seen[li] {
				seen[li] = true
				lookups = append(lookups, li)
			}
		}
	}
	sort.Ints(lookups)
	return lookups
}

func lookupSubtables(t []byte, li int, extType uint16) (uint16, []int) {
	lookupList := int(u16(t, 8))
	n := int(u16(t, lookupList))
	if li >= n {
		return 0, nil
	}
	lo := lookupList + int(u16(t, lookupList+2+2*li))
	lookupType := u16(t, lo)
	typ := lookupType
	ns := int(u16(t, lo+4))
	var subs []int
	for i := 0; i < ns; i++ {
		so := lo + int(u16(t, lo+6+2*i))
		if lookupType == extType {
			// Extension subtable (every subtable has its own header).
			typ = u16(t, so+2)
			so += int(u32(t, so+4))
		}
		subs = append(subs, so)
	}
	return typ, subs
}

func parseGPOSKern(t []byte) (pk *pairKerning) {
	defer func() {
		if recover() != nil {
			pk = nil
		}
	}()
	lookups := lookupIndices(t, "kern")
	if len(lookups) == 0 {
		return nil
	}
	pk = &pairKerning{}
	for _, li := range lookups {
		typ, subs := lookupSubtables(t, li, 9)
		if typ != 2 {
			continue
		}
		for _, so := range subs {
			format := u16(t, so)
			cov := parseCoverage(t, so+int(u16(t, so+2)))
			vf1 := u16(t, so+4)
			vf2 := u16(t, so+6)
			s1 := valueRecordSize(vf1)
			s2 := valueRecordSize(vf2)
			switch format {
			case 1:
				p := pairPos1{coverage: cov, sets: map[int]map[uint16]int16{}}
				n := int(u16(t, so+8))
				for i := 0; i < n; i++ {
					ps := so + int(u16(t, so+10+2*i))
					cnt := int(u16(t, ps))
					m := map[uint16]int16{}
					for j := 0; j < cnt; j++ {
						r := ps + 2 + j*(2+s1+s2)
						second := u16(t, r)
						if v := xAdvance(t, r+2, vf1); v != 0 {
							m[second] = v
						}
					}
					p.sets[i] = m
				}
				pk.format1 = append(pk.format1, p)
			case 2:
				p := pairPos2{coverage: cov}
				p.class1 = parseClassDef(t, so+int(u16(t, so+8)))
				p.class2 = parseClassDef(t, so+int(u16(t, so+10)))
				c1 := int(u16(t, so+12))
				c2 := int(u16(t, so+14))
				p.values = make([][]int16, c1)
				for i := 0; i < c1; i++ {
					p.values[i] = make([]int16, c2)
					for j := 0; j < c2; j++ {
						r := so + 16 + (i*c2+j)*(s1+s2)
						p.values[i][j] = xAdvance(t, r, vf1)
					}
				}
				pk.format2 = append(pk.format2, p)
			}
		}
	}
	if len(pk.format1) == 0 && len(pk.format2) == 0 {
		return nil
	}
	return pk
}

// ArabicSubst holds the GSUB substitutions used to shape Arabic text.
type ArabicSubst struct {
	// Forms are the single substitutions of the isol, fina, init and medi
	// features.
	Forms [4]map[uint16]uint16
	// Ligatures are the required and standard ligatures (rlig, liga).
	Ligatures map[uint16][]Ligature
}

// Arabic returns the Arabic shaping substitutions of the font.
func (f *Font) Arabic() *ArabicSubst {
	f.arabicOnce.Do(func() {
		a := &ArabicSubst{Ligatures: map[uint16][]Ligature{}}
		for i := range a.Forms {
			a.Forms[i] = map[uint16]uint16{}
		}
		t := f.tables["GSUB"]
		if len(t) >= 10 {
			func() {
				defer func() { recover() }()
				scripts := map[string]bool{"arab": true}
				for fi, feat := range []string{"isol", "fina", "init", "medi"} {
					for _, li := range lookupIndicesFor(t, feat, scripts) {
						singleSubst(t, li, a.Forms[fi])
					}
				}
				for _, feat := range []string{"rlig", "liga"} {
					for _, li := range lookupIndicesFor(t, feat, scripts) {
						ligatureSubst(t, li, a.Ligatures)
					}
				}
			}()
		}
		f.arabic = a
	})
	return f.arabic
}

func singleSubst(t []byte, li int, out map[uint16]uint16) {
	typ, subs := lookupSubtables(t, li, 7)
	if typ != 1 {
		return
	}
	for _, so := range subs {
		format := u16(t, so)
		cov := parseCoverage(t, so+int(u16(t, so+2)))
		switch format {
		case 1:
			delta := u16(t, so+4)
			for g := range cov {
				if _, ok := out[g]; !ok {
					out[g] = g + delta
				}
			}
		case 2:
			n := int(u16(t, so+4))
			for g, i := range cov {
				if i < n {
					if _, ok := out[g]; !ok {
						out[g] = u16(t, so+6+2*i)
					}
				}
			}
		}
	}
}

func ligatureSubst(t []byte, li int, out map[uint16][]Ligature) {
	typ, subs := lookupSubtables(t, li, 7)
	if typ != 4 {
		return
	}
	for _, so := range subs {
		cov := parseCoverage(t, so+int(u16(t, so+2)))
		n := int(u16(t, so+4))
		inv := map[int]uint16{}
		for g, i := range cov {
			inv[i] = g
		}
		for i := 0; i < n; i++ {
			first := inv[i]
			ls := so + int(u16(t, so+6+2*i))
			cnt := int(u16(t, ls))
			for j := 0; j < cnt; j++ {
				lo := ls + int(u16(t, ls+2+2*j))
				lig := u16(t, lo)
				cc := int(u16(t, lo+2))
				comps := make([]uint16, cc-1)
				for k := range comps {
					comps[k] = u16(t, lo+4+2*k)
				}
				out[first] = append(out[first], Ligature{comps, lig})
			}
		}
	}
}

// SingleSubstitutions returns the single substitutions of a GSUB feature
// (such as smcp or c2sc) for Latin and default scripts.
func (f *Font) SingleSubstitutions(feature string) map[uint16]uint16 {
	f.substMu.Lock()
	defer f.substMu.Unlock()
	if m, ok := f.substs[feature]; ok {
		return m
	}
	m := map[uint16]uint16{}
	if t := f.tables["GSUB"]; len(t) >= 10 {
		func() {
			defer func() { recover() }()
			for _, li := range lookupIndices(t, feature) {
				singleSubst(t, li, m)
			}
		}()
	}
	if f.substs == nil {
		f.substs = map[string]map[uint16]uint16{}
	}
	f.substs[feature] = m
	return m
}

// Ligatures returns standard ligatures (GSUB liga) starting with a glyph.
func (f *Font) Ligatures(first uint16) []Ligature {
	if f.ligatures == nil {
		f.parseLigatures()
	}
	return f.ligatures[first]
}

func (f *Font) parseLigatures() {
	f.ligatures = map[uint16][]Ligature{}
	t := f.tables["GSUB"]
	if len(t) < 10 {
		return
	}
	defer func() { recover() }()
	for _, li := range lookupIndices(t, "liga") {
		typ, subs := lookupSubtables(t, li, 7)
		if typ != 4 {
			continue
		}
		for _, so := range subs {
			cov := parseCoverage(t, so+int(u16(t, so+2)))
			n := int(u16(t, so+4))
			inv := map[int]uint16{}
			for g, i := range cov {
				inv[i] = g
			}
			for i := 0; i < n; i++ {
				first := inv[i]
				ls := so + int(u16(t, so+6+2*i))
				cnt := int(u16(t, ls))
				for j := 0; j < cnt; j++ {
					lo := ls + int(u16(t, ls+2+2*j))
					lig := u16(t, lo)
					cc := int(u16(t, lo+2))
					comps := make([]uint16, cc-1)
					for k := range comps {
						comps[k] = u16(t, lo+4+2*k)
					}
					f.ligatures[first] = append(f.ligatures[first], Ligature{comps, lig})
				}
			}
		}
	}
}

// ---------------------------------------------------------------- WOFF

func decodeWOFF(d []byte) ([]byte, error) {
	if len(d) < 44 {
		return nil, ErrUnsupported
	}
	flavor := d[4:8]
	n := int(u16(d, 12))
	type entry struct {
		tag             []byte
		off, comp, orig int
		checksum        uint32
	}
	var entries []entry
	for i := 0; i < n; i++ {
		r := 44 + 20*i
		if r+20 > len(d) {
			return nil, ErrUnsupported
		}
		entries = append(entries, entry{d[r : r+4], int(u32(d, r+4)), int(u32(d, r+8)), int(u32(d, r+12)), u32(d, r+16)})
	}
	var out bytes.Buffer
	out.Write(flavor)
	binary.Write(&out, binary.BigEndian, uint16(n))
	es, lg := 1, 0
	for es*2 <= n {
		es *= 2
		lg++
	}
	binary.Write(&out, binary.BigEndian, uint16(es*16))
	binary.Write(&out, binary.BigEndian, uint16(lg))
	binary.Write(&out, binary.BigEndian, uint16(n*16-es*16))
	offset := 12 + 16*n
	var datas [][]byte
	for _, e := range entries {
		if e.off+e.comp > len(d) {
			return nil, ErrUnsupported
		}
		raw := d[e.off : e.off+e.comp]
		if e.comp < e.orig {
			zr, err := zlib.NewReader(bytes.NewReader(raw))
			if err != nil {
				return nil, err
			}
			var b bytes.Buffer
			if _, err := b.ReadFrom(zr); err != nil {
				return nil, err
			}
			raw = b.Bytes()
		}
		datas = append(datas, raw)
	}
	for i, e := range entries {
		out.Write(e.tag)
		binary.Write(&out, binary.BigEndian, e.checksum)
		binary.Write(&out, binary.BigEndian, uint32(offset))
		binary.Write(&out, binary.BigEndian, uint32(len(datas[i])))
		offset += (len(datas[i]) + 3) &^ 3
	}
	for _, b := range datas {
		out.Write(b)
		for out.Len()%4 != 0 {
			out.WriteByte(0)
		}
	}
	return out.Bytes(), nil
}

// Package otl applies OpenType layout tables (GSUB, GPOS, GDEF and the
// legacy kern table) to shape text, following HarfBuzz's shaping model for
// the default (Latin, Greek, Cyrillic…) and Arabic shapers: Unicode
// normalization against the font's cmap, feature selection per script and
// language, substitutions, positioning, mark attachment and fallback mark
// positioning.
//
// The language tag table comes from HarfBuzz (see LICENSE.harfbuzz).
package otl

import (
	"errors"
	"sort"
)

var errBounds = errors.New("otl: table out of bounds")

// data is a font table read with bounds checks; reading past its end
// panics with errBounds, which the shaper recovers from.
type data []byte

func (d data) u8(off int) uint8 {
	if off < 0 || off >= len(d) {
		panic(errBounds)
	}
	return d[off]
}

func (d data) u16(off int) uint16 {
	if off < 0 || off+2 > len(d) {
		panic(errBounds)
	}
	return uint16(d[off])<<8 | uint16(d[off+1])
}

func (d data) i16(off int) int16 { return int16(d.u16(off)) }

func (d data) u32(off int) uint32 {
	if off < 0 || off+4 > len(d) {
		panic(errBounds)
	}
	return uint32(d[off])<<24 | uint32(d[off+1])<<16 | uint32(d[off+2])<<8 | uint32(d[off+3])
}

func (d data) tag(off int) string {
	if off < 0 || off+4 > len(d) {
		panic(errBounds)
	}
	return string(d[off : off+4])
}

// coverage returns the coverage index of g in the coverage table at off,
// or -1.
func (d data) coverage(off int, g uint16) int {
	switch d.u16(off) {
	case 1:
		n := int(d.u16(off + 2))
		lo, hi := 0, n
		for lo < hi {
			m := (lo + hi) / 2
			v := d.u16(off + 4 + 2*m)
			switch {
			case v == g:
				return m
			case v < g:
				lo = m + 1
			default:
				hi = m
			}
		}
	case 2:
		n := int(d.u16(off + 2))
		lo, hi := 0, n
		for lo < hi {
			m := (lo + hi) / 2
			r := off + 4 + 6*m
			start, end := d.u16(r), d.u16(r+2)
			switch {
			case g < start:
				hi = m
			case g > end:
				lo = m + 1
			default:
				return int(d.u16(r+4)) + int(g-start)
			}
		}
	}
	return -1
}

// class returns the class of g in the class definition table at off.
func (d data) class(off int, g uint16) int {
	switch d.u16(off) {
	case 1:
		start := d.u16(off + 2)
		n := d.u16(off + 4)
		if g >= start && g < start+n {
			return int(d.u16(off + 6 + 2*int(g-start)))
		}
	case 2:
		n := int(d.u16(off + 2))
		lo, hi := 0, n
		for lo < hi {
			m := (lo + hi) / 2
			r := off + 4 + 6*m
			start, end := d.u16(r), d.u16(r+2)
			switch {
			case g < start:
				hi = m
			case g > end:
				lo = m + 1
			default:
				return int(d.u16(r + 4))
			}
		}
	}
	return 0
}

// layoutTable is a GSUB or GPOS table.
type layoutTable struct {
	d          data
	gpos       bool
	scriptList int
	featList   int
	lookupList int
	featVars   int // FeatureVariations offset (0 if none)
	lookups    []*lookup
}

func newLayoutTable(d data, gpos bool) *layoutTable {
	if len(d) < 10 {
		return nil
	}
	t := &layoutTable{d: d, gpos: gpos}
	t.scriptList = int(d.u16(4))
	t.featList = int(d.u16(6))
	t.lookupList = int(d.u16(8))
	if d.u16(0) == 1 && d.u16(2) >= 1 && len(d) >= 14 {
		if o := d.u32(10); o != 0 {
			t.featVars = int(o)
		}
	}
	n := int(d.u16(t.lookupList))
	t.lookups = make([]*lookup, n)
	return t
}

// langSys returns the offset of the language system table for a script
// and language tags (or the default one), and whether the script was
// found. Script tags are tried in order, then DFLT, dflt and latn, as
// HarfBuzz does.
func (t *layoutTable) langSys(scripts, langs []string) int {
	d := t.d
	n := int(d.u16(t.scriptList))
	find := func(tag string) int {
		for i := 0; i < n; i++ {
			r := t.scriptList + 2 + 6*i
			if d.tag(r) == tag {
				return t.scriptList + int(d.u16(r+4))
			}
		}
		return 0
	}
	so := 0
	for _, s := range append(append([]string(nil), scripts...), "DFLT", "dflt", "latn") {
		if so = find(s); so != 0 {
			break
		}
	}
	if so == 0 {
		return 0
	}
	nl := int(d.u16(so + 2))
	for _, l := range langs {
		for i := 0; i < nl; i++ {
			r := so + 4 + 6*i
			if d.tag(r) == l {
				return so + int(d.u16(r+4))
			}
		}
	}
	if dl := int(d.u16(so)); dl != 0 {
		return so + dl
	}
	return 0
}

// featureLookups returns the lookups of the features with a tag in a
// language system (required feature: tag ""), with feature variations
// substituted by subst.
func (t *layoutTable) featureLookups(ls int, tag string, subst map[int]int) []int {
	if ls == 0 {
		return nil
	}
	d := t.d
	var idx []int
	if tag == "" {
		if r := d.u16(ls + 2); r != 0xffff {
			idx = append(idx, int(r))
		}
	} else {
		n := int(d.u16(ls + 4))
		nf := int(d.u16(t.featList))
		for i := 0; i < n; i++ {
			fi := int(d.u16(ls + 6 + 2*i))
			if fi < nf && d.tag(t.featList+2+6*fi) == tag {
				idx = append(idx, fi)
			}
		}
	}
	var out []int
	for _, fi := range idx {
		fo := t.featList + int(d.u16(t.featList+2+6*fi+4))
		if s, ok := subst[fi]; ok {
			fo = s
		}
		n := int(d.u16(fo + 2))
		for j := 0; j < n; j++ {
			out = append(out, int(d.u16(fo+4+2*j)))
		}
	}
	return out
}

// hasFeature reports whether the language system has a feature.
func (t *layoutTable) hasFeature(ls int, tag string) bool {
	if ls == 0 {
		return false
	}
	d := t.d
	n := int(d.u16(ls + 4))
	nf := int(d.u16(t.featList))
	for i := 0; i < n; i++ {
		fi := int(d.u16(ls + 6 + 2*i))
		if fi < nf && d.tag(t.featList+2+6*fi) == tag {
			return true
		}
	}
	return false
}

// lookup is a parsed lookup table header.
type lookup struct {
	typ        uint16
	flag       uint16
	filterSet  int
	subtables  []int // absolute offsets, extensions resolved
	coverageOK bool
}

func (t *layoutTable) lookup(i int) *lookup {
	if i < 0 || i >= len(t.lookups) {
		return nil
	}
	if l := t.lookups[i]; l != nil {
		return l
	}
	d := t.d
	lo := t.lookupList + int(d.u16(t.lookupList+2+2*i))
	l := &lookup{typ: d.u16(lo), flag: d.u16(lo + 2)}
	ns := int(d.u16(lo + 4))
	ext := uint16(7)
	if t.gpos {
		ext = 9
	}
	for k := 0; k < ns; k++ {
		so := lo + int(d.u16(lo+6+2*k))
		if l.typ == ext {
			typ := d.u16(so + 2)
			if k == 0 {
				l.typ = typ
			}
			so += int(d.u32(so + 4))
		}
		l.subtables = append(l.subtables, so)
	}
	if l.flag&0x10 != 0 {
		l.filterSet = int(d.u16(lo + 6 + 2*ns))
	}
	if l.typ == ext {
		l.typ = 0
	}
	t.lookups[i] = l
	return l
}

// gdef holds glyph classes and mark sets.
type gdef struct {
	d         data
	classDef  int
	markClass int
	markSets  int
}

func newGDEF(d data) *gdef {
	if len(d) < 12 {
		return nil
	}
	g := &gdef{d: d, classDef: int(d.u16(4)), markClass: int(d.u16(10))}
	if d.u16(2) >= 2 && len(d) >= 14 {
		g.markSets = int(d.u16(12))
	}
	return g
}

func (g *gdef) glyphClass(id uint16) int {
	if g == nil || g.classDef == 0 {
		return 0
	}
	return g.d.class(g.classDef, id)
}

func (g *gdef) markAttachClass(id uint16) int {
	if g == nil || g.markClass == 0 {
		return 0
	}
	return g.d.class(g.markClass, id)
}

func (g *gdef) inMarkSet(set int, id uint16) bool {
	if g == nil || g.markSets == 0 {
		return false
	}
	d := g.d
	if set >= int(d.u16(g.markSets+2)) {
		return false
	}
	off := g.markSets + int(d.u32(g.markSets+4+4*set))
	return d.coverage(off, id) >= 0
}

func sortedUnique(v []int) []int {
	sort.Ints(v)
	out := v[:0]
	for i, x := range v {
		if i == 0 || x != v[i-1] {
			out = append(out, x)
		}
	}
	return out
}

// featureVariations returns the feature table substitutions of a feature
// variation record (feature index → absolute feature table offset).
func (t *layoutTable) featureVariations(record int) map[int]int {
	if t.featVars == 0 || record < 0 {
		return nil
	}
	d := t.d
	fv := t.featVars
	if record >= int(d.u32(fv+4)) {
		return nil
	}
	r := fv + 8 + 8*record
	sub := int(d.u32(r + 4))
	if sub == 0 {
		return nil
	}
	so := fv + sub
	n := int(d.u16(so + 4))
	out := map[int]int{}
	for i := 0; i < n; i++ {
		rr := so + 6 + 6*i
		out[int(d.u16(rr))] = so + int(d.u32(rr+2))
	}
	return out
}

// FeatureVariationRecords returns, for each feature variation record of
// the table, its condition sets as (axis index, min, max) triples in
// normalized F2Dot14 coordinates.
func (t *layoutTable) featureVariationConditions() [][][3]float64 {
	if t == nil || t.featVars == 0 {
		return nil
	}
	d := t.d
	fv := t.featVars
	n := int(d.u32(fv + 4))
	out := make([][][3]float64, n)
	for i := 0; i < n; i++ {
		cs := int(d.u32(fv + 8 + 8*i))
		if cs == 0 {
			continue
		}
		so := fv + cs
		nc := int(d.u16(so))
		conds := [][3]float64{}
		for k := 0; k < nc; k++ {
			co := so + int(d.u32(so+2+4*k))
			if d.u16(co) != 1 {
				conds = append(conds, [3]float64{-1, 0, 0}) // unknown format: never matches
				continue
			}
			conds = append(conds, [3]float64{float64(d.u16(co + 2)), float64(d.i16(co+4)) / 16384, float64(d.i16(co+6)) / 16384})
		}
		out[i] = conds
	}
	return out
}

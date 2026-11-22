package fonts

import (
	"bytes"
	"encoding/binary"
	"errors"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// Variable fonts: axes, coordinate normalization (fvar, avar), item
// variation stores (HVAR, MVAR, GDEF), glyph variations (gvar) and static
// instances.

// Axis is a variation axis of a font (fvar).
type Axis struct {
	Tag               string
	Min, Default, Max float64
	Hidden            bool
}

// NamedInstance is a named instance of a variable font.
type NamedInstance struct {
	Subfamily string
	Coords    map[string]float64
}

type varState struct {
	once      sync.Once
	axes      []Axis
	instances []NamedInstance
	avar      [][][2]float64 // per axis: (from, to) pairs
	mu        sync.Mutex
	cache     map[string]*Font
}

var varStates sync.Map // *Font → *varState

func (f *Font) vars() *varState {
	v, _ := varStates.LoadOrStore(f, &varState{})
	st := v.(*varState)
	st.once.Do(func() { st.parse(f) })
	return st
}

// parseAxes returns the axes of an fvar table.
func parseAxes(fvar []byte) (axes []Axis) {
	defer func() {
		if recover() != nil {
			axes = nil
		}
	}()
	if len(fvar) < 16 {
		return nil
	}
	axesOff := int(u16(fvar, 4))
	n := int(u16(fvar, 8))
	axisSize := int(u16(fvar, 10))
	for i := 0; i < n; i++ {
		p := axesOff + i*axisSize
		axes = append(axes, Axis{
			Tag:     string(fvar[p : p+4]),
			Min:     fixed(fvar, p+4),
			Default: fixed(fvar, p+8),
			Max:     fixed(fvar, p+12),
			Hidden:  u16(fvar, p+16)&1 != 0,
		})
	}
	return axes
}

func (st *varState) parse(f *Font) {
	defer func() { recover() }()
	fvar := f.tables["fvar"]
	if len(fvar) < 16 {
		return
	}
	axesOff := int(u16(fvar, 4))
	n := int(u16(fvar, 8))
	axisSize := int(u16(fvar, 10))
	ni := int(u16(fvar, 12))
	instSize := int(u16(fvar, 14))
	st.axes = parseAxes(fvar)
	for i := 0; i < ni; i++ {
		p := axesOff + n*axisSize + i*instSize
		if p+4+4*n > len(fvar) {
			break
		}
		inst := NamedInstance{Subfamily: f.nameString(int(u16(fvar, p))), Coords: map[string]float64{}}
		for k, a := range st.axes {
			inst.Coords[a.Tag] = fixed(fvar, p+4+4*k)
		}
		st.instances = append(st.instances, inst)
	}
	if avar := f.tables["avar"]; len(avar) >= 8 {
		p := 8
		for k := 0; k < int(u16(avar, 6)) && k < n; k++ {
			cnt := int(u16(avar, p))
			p += 2
			var m [][2]float64
			for j := 0; j < cnt; j++ {
				m = append(m, [2]float64{f2dot14(avar, p), f2dot14(avar, p+2)})
				p += 4
			}
			st.avar = append(st.avar, m)
		}
	}
}

// otRound rounds like fontTools (half up).
func otRound(v float64) float64 { return math.Floor(v + 0.5) }

func fixed(b []byte, off int) float64 { return float64(int32(u32(b, off))) / 65536 }

func f2dot14(b []byte, off int) float64 { return float64(i16(b, off)) / 16384 }

// nameString returns a name of the name table (English, any platform).
func (f *Font) nameString(id int) string {
	name := f.tables["name"]
	if len(name) < 6 {
		return ""
	}
	count := int(u16(name, 2))
	strOff := int(u16(name, 4))
	best := ""
	for i := 0; i < count; i++ {
		r := 6 + 12*i
		if r+12 > len(name) {
			break
		}
		platform, lang, nid := u16(name, r), u16(name, r+4), u16(name, r+6)
		if int(nid) != id {
			continue
		}
		l, o := int(u16(name, r+8)), int(u16(name, r+10))
		if strOff+o+l > len(name) {
			continue
		}
		s := decodeNameString(name[strOff+o:strOff+o+l], platform)
		if (platform == 3 && lang == 0x409) || best == "" {
			best = s
		}
	}
	return best
}

// Axes returns the variation axes of a variable font.
func (f *Font) Axes() []Axis { return f.vars().axes }

// NamedInstances returns the named instances of a variable font.
func (f *Font) NamedInstances() []NamedInstance { return f.vars().instances }

// Normalize returns the normalized coordinates (F2Dot14 precision, avar
// applied) of user axis values; missing axes take their default.
func (f *Font) Normalize(user map[string]float64) []float64 {
	st := f.vars()
	out := make([]float64, len(st.axes))
	for i, a := range st.axes {
		v, ok := user[a.Tag]
		if !ok {
			v = a.Default
		}
		v = math.Max(a.Min, math.Min(a.Max, v))
		var n float64
		switch {
		case v < a.Default && a.Default > a.Min:
			n = (v - a.Default) / (a.Default - a.Min)
		case v > a.Default && a.Max > a.Default:
			n = (v - a.Default) / (a.Max - a.Default)
		}
		n = otRound(n*16384) / 16384
		if i < len(st.avar) && len(st.avar[i]) >= 2 {
			m := st.avar[i]
			for k := 1; k < len(m); k++ {
				if n <= m[k][0] {
					a0, a1 := m[k-1], m[k]
					if a1[0] == a0[0] {
						n = a1[1]
					} else {
						n = a0[1] + (n-a0[0])*(a1[1]-a0[1])/(a1[0]-a0[0])
					}
					break
				}
			}
			n = otRound(n*16384) / 16384
		}
		out[i] = n
	}
	return out
}

// ---------------------------------------------------------- item variation

// itemVarStore evaluates an ItemVariationStore at normalized coordinates.
type itemVarStore struct {
	d       []byte
	off     int
	coords  []float64
	scalars map[int]float64
}

func (s *itemVarStore) regionScalar(r int) float64 {
	if v, ok := s.scalars[r]; ok {
		return v
	}
	d := s.d
	rl := s.off + int(u32(d, s.off+2))
	axes := int(u16(d, rl))
	p := rl + 4 + r*axes*6
	scalar := 1.0
	for a := 0; a < axes; a++ {
		start, peak, end := f2dot14(d, p), f2dot14(d, p+2), f2dot14(d, p+4)
		p += 6
		scalar *= tent(coord(s.coords, a), start, peak, end)
		if scalar == 0 {
			break
		}
	}
	s.scalars[r] = scalar
	return scalar
}

func coord(c []float64, a int) float64 {
	if a < len(c) {
		return c[a]
	}
	return 0
}

// tent returns the scalar of an axis region at coordinate v.
func tent(v, start, peak, end float64) float64 {
	if start > peak || peak > end || peak == 0 || (start < 0 && end > 0) {
		return 1
	}
	if v == peak {
		return 1
	}
	if v <= start || v >= end {
		return 0
	}
	if v < peak {
		return (v - start) / (peak - start)
	}
	return (end - v) / (end - peak)
}

// delta returns the (unrounded) delta of an item.
func (s *itemVarStore) delta(outer, inner int) float64 {
	d := s.d
	if outer >= int(u16(d, s.off+6)) {
		return 0
	}
	ivd := s.off + int(u32(d, s.off+8+4*outer))
	items := int(u16(d, ivd))
	if inner >= items {
		return 0
	}
	wc := int(u16(d, ivd+2))
	long := wc&0x8000 != 0
	wc &= 0x7fff
	nr := int(u16(d, ivd+4))
	big, small := 2, 1
	if long {
		big, small = 4, 2
	}
	rowSize := wc*big + (nr-wc)*small
	p := ivd + 6 + 2*nr + inner*rowSize
	sum := 0.0
	for k := 0; k < nr; k++ {
		var v int
		switch {
		case k < wc && long:
			v = int(int32(u32(d, p)))
			p += 4
		case k < wc, long:
			v = int(i16(d, p))
			p += 2
		default:
			v = int(int8(d[p]))
			p++
		}
		if v != 0 {
			sum += float64(v) * s.regionScalar(int(u16(d, ivd+6+2*k)))
		}
	}
	return sum
}

func newItemVarStore(d []byte, off int, coords []float64) *itemVarStore {
	if off <= 0 || off+8 > len(d) {
		return nil
	}
	return &itemVarStore{d: d, off: off, coords: coords, scalars: map[int]float64{}}
}

// deltaSetIndex maps a glyph or item through a DeltaSetIndexMap.
func deltaSetIndex(d []byte, off, i int) (int, int) {
	format := d[off]
	entry := d[off+1]
	var count, p int
	if format == 0 {
		count, p = int(u16(d, off+2)), off+4
	} else {
		count, p = int(u32(d, off+2)), off+6
	}
	if count == 0 {
		return 0, i
	}
	if i >= count {
		i = count - 1
	}
	size := int(entry>>4&3) + 1
	bits := int(entry&0xf) + 1
	v := 0
	for k := 0; k < size; k++ {
		v = v<<8 | int(d[p+i*size+k])
	}
	return v >> bits, v & (1<<bits - 1)
}

// VarCoords returns the normalized coordinates of a variable font
// instance (nil for other fonts).
func (f *Font) VarCoords() []float64 { return f.varCoords }

// VarDelta returns the delta of an item of the GDEF variation store
// (used by GPOS device tables) for an instance.
func (f *Font) VarDelta(outer, inner int) float64 {
	if f.gdefStore == nil {
		return 0
	}
	f.varMu.Lock()
	defer f.varMu.Unlock()
	return f.gdefStore.delta(outer, inner)
}

// ------------------------------------------------------------- instances

// Instance returns the static instance of a variable font at user axis
// values (missing axes take their default): a font with the glyph
// outlines, advances and metrics of the instance, whose GSUB, GPOS and
// GDEF tables are those of the variable font (with VarCoords and VarDelta
// for their variations). It returns f for fonts that are not variable.
func (f *Font) Instance(user map[string]float64) *Font {
	st := f.vars()
	if len(st.axes) == 0 {
		return f
	}
	coords := f.Normalize(user)
	var kb strings.Builder
	for _, c := range coords {
		kb.WriteString(strconv.FormatFloat(c, 'f', 6, 64))
		kb.WriteByte(',')
	}
	key := kb.String()
	st.mu.Lock()
	defer st.mu.Unlock()
	if inst, ok := st.cache[key]; ok {
		return inst
	}
	inst, err := f.instantiate(coords)
	if err != nil {
		inst = f
	}
	if st.cache == nil {
		st.cache = map[string]*Font{}
	}
	st.cache[key] = inst
	return inst
}

func (f *Font) instantiate(coords []float64) (inst *Font, err error) {
	defer func() {
		if r := recover(); r != nil {
			inst, err = nil, errors.New("fonts: broken variable font")
		}
	}()
	tables := map[string][]byte{}
	for t, d := range f.tables {
		switch t {
		case "fvar", "gvar", "avar", "HVAR", "MVAR", "cvar", "STAT", "VVAR", "DSIG", "hdmx", "LTSH", "VDMX":
			continue
		}
		tables[t] = d
	}
	n := f.NumGlyphs
	// Advances.
	adv := make([]float64, n)
	lsb := make([]int, n)
	hhea, hmtx := f.tables["hhea"], f.tables["hmtx"]
	numH := int(u16(hhea, 34))
	for g := 0; g < n; g++ {
		if g < numH {
			adv[g] = float64(u16(hmtx, 4*g))
			lsb[g] = int(i16(hmtx, 4*g+2))
		} else {
			adv[g] = float64(u16(hmtx, 4*(numH-1)))
			if p := 4*numH + 2*(g-numH); p+2 <= len(hmtx) {
				lsb[g] = int(i16(hmtx, p))
			}
		}
	}
	var hvarStore *itemVarStore
	hvarMap := 0
	if hvar := f.tables["HVAR"]; len(hvar) >= 20 {
		hvarStore = newItemVarStore(hvar, int(u32(hvar, 4)), coords)
		hvarMap = int(u32(hvar, 8))
		for g := 0; g < n; g++ {
			outer, inner := 0, g
			if hvarMap != 0 {
				outer, inner = deltaSetIndex(hvar, hvarMap, g)
			}
			adv[g] += hvarStore.delta(outer, inner)
		}
	}
	// Outlines.
	if glyf := f.tables["glyf"]; glyf != nil && f.tables["gvar"] != nil {
		gv := &gvarReader{f: f, d: f.tables["gvar"], coords: coords}
		newGlyf, loca, newAdv, newLsb := f.instanceGlyf(gv, hvarStore == nil, adv, lsb)
		tables["glyf"], tables["loca"] = newGlyf, loca
		adv, lsb = newAdv, newLsb
		head := append([]byte(nil), tables["head"]...)
		binary.BigEndian.PutUint16(head[50:], 1)
		tables["head"] = head
	} else if cff2 := f.tables["CFF2"]; cff2 != nil {
		cff, err := cff2ToCFF(f, cff2, coords, adv)
		if err != nil {
			return nil, err
		}
		delete(tables, "CFF2")
		tables["CFF "] = cff
	}
	var mtx bytes.Buffer
	for g := 0; g < n; g++ {
		a := otRound(adv[g])
		if a < 0 {
			a = 0
		}
		binary.Write(&mtx, binary.BigEndian, uint16(a))
		binary.Write(&mtx, binary.BigEndian, int16(lsb[g]))
	}
	tables["hmtx"] = mtx.Bytes()
	h := append([]byte(nil), hhea...)
	binary.BigEndian.PutUint16(h[34:], uint16(n))
	tables["hhea"] = h
	// Metrics.
	if mvar := f.tables["MVAR"]; len(mvar) >= 12 {
		f.applyMVAR(tables, mvar, coords)
	}
	version := f.sfntVersion()
	if _, ok := tables["CFF "]; ok {
		version = []byte("OTTO")
	}
	data := buildSFNT(version, tables)
	inst, err = Parse(data, 0)
	if err != nil {
		return nil, err
	}
	inst.varCoords = coords
	inst.VarBase = f
	if gdef := f.tables["GDEF"]; len(gdef) >= 18 && u16(gdef, 2) >= 3 {
		if off := int(u32(gdef, 14)); off != 0 {
			inst.gdefStore = newItemVarStore(gdef, off, coords)
		}
	}
	return inst, nil
}

// applyMVAR applies the metrics variations to the OS/2, hhea and post
// tables.
func (f *Font) applyMVAR(tables map[string][]byte, mvar []byte, coords []float64) {
	recSize := int(u16(mvar, 6))
	count := int(u16(mvar, 8))
	store := newItemVarStore(mvar, int(u16(mvar, 10)), coords)
	if store == nil {
		return
	}
	targets := map[string][][2]any{
		"hasc": {{"OS/2", 68}, {"hhea", 4}},
		"hdsc": {{"OS/2", 70}, {"hhea", 6}},
		"hlgp": {{"OS/2", 72}, {"hhea", 8}},
		"hcla": {{"OS/2", 74}},
		"hcld": {{"OS/2", 76}},
		"xhgt": {{"OS/2", 86}},
		"cpht": {{"OS/2", 88}},
		"strs": {{"OS/2", 26}},
		"stro": {{"OS/2", 28}},
		"sbxs": {{"OS/2", 10}},
		"sbys": {{"OS/2", 12}},
		"sbxo": {{"OS/2", 14}},
		"sbyo": {{"OS/2", 16}},
		"spxs": {{"OS/2", 18}},
		"spys": {{"OS/2", 20}},
		"spxo": {{"OS/2", 22}},
		"spyo": {{"OS/2", 24}},
		"undo": {{"post", 8}},
		"unds": {{"post", 10}},
		"hcrs": {{"hhea", 18}},
		"hcrn": {{"hhea", 20}},
		"hcof": {{"hhea", 22}},
	}
	copied := map[string]bool{}
	for i := 0; i < count; i++ {
		r := 12 + i*recSize
		tag := string(mvar[r : r+4])
		dv := otRound(store.delta(int(u16(mvar, r+4)), int(u16(mvar, r+6))))
		if dv == 0 {
			continue
		}
		for _, t := range targets[tag] {
			name, off := t[0].(string), t[1].(int)
			tb := tables[name]
			if off+2 > len(tb) {
				continue
			}
			if !copied[name] {
				tb = append([]byte(nil), tb...)
				tables[name] = tb
				copied[name] = true
			}
			v := float64(int16(binary.BigEndian.Uint16(tb[off:])))
			if tag == "hcla" || tag == "hcld" {
				v = float64(binary.BigEndian.Uint16(tb[off:]))
			}
			binary.BigEndian.PutUint16(tb[off:], uint16(int16(v+dv)))
		}
	}
}

// ------------------------------------------------------------------ gvar

type gvarReader struct {
	f      *Font
	d      []byte
	coords []float64
}

type point struct {
	x, y    float64
	onCurve bool
}

// glyphDeltas returns the deltas of the n points of a glyph (including
// the four phantom points), with contour ends for interpolation.
func (gv *gvarReader) glyphDeltas(g, n int, pts []point, ends []int) ([]float64, []float64) {
	d := gv.d
	dx := make([]float64, n)
	dy := make([]float64, n)
	axisCount := int(u16(d, 4))
	sharedCount := int(u16(d, 6))
	sharedOff := int(u32(d, 8))
	glyphCount := int(u16(d, 12))
	flags := u16(d, 14)
	dataOff := int(u32(d, 16))
	if g >= glyphCount {
		return dx, dy
	}
	var start, end int
	if flags&1 != 0 {
		start, end = int(u32(d, 20+4*g)), int(u32(d, 24+4*g))
	} else {
		start, end = 2*int(u16(d, 20+2*g)), 2*int(u16(d, 22+2*g))
	}
	if end <= start {
		return dx, dy
	}
	gd := d[dataOff+start : dataOff+end]
	tc := int(u16(gd, 0))
	count := tc & 0xfff
	serial := int(u16(gd, 2))
	p := serial
	var shared []int
	sharedAll := false
	if tc&0x8000 != 0 {
		shared, sharedAll, p = readPoints(gd, p)
	}
	h := 4
	for t := 0; t < count; t++ {
		size := int(u16(gd, h))
		ti := u16(gd, h+2)
		h += 4
		peak := make([]float64, axisCount)
		if ti&0x8000 != 0 {
			for a := 0; a < axisCount; a++ {
				peak[a] = f2dot14(gd, h+2*a)
			}
			h += 2 * axisCount
		} else {
			idx := int(ti & 0xfff)
			if idx < sharedCount {
				for a := 0; a < axisCount; a++ {
					peak[a] = f2dot14(d, sharedOff+(idx*axisCount+a)*2)
				}
			}
		}
		var istart, iend []float64
		if ti&0x4000 != 0 {
			istart = make([]float64, axisCount)
			iend = make([]float64, axisCount)
			for a := 0; a < axisCount; a++ {
				istart[a] = f2dot14(gd, h+2*a)
				iend[a] = f2dot14(gd, h+2*(axisCount+a))
			}
			h += 4 * axisCount
		}
		scalar := 1.0
		for a := 0; a < axisCount && scalar != 0; a++ {
			pk := peak[a]
			if pk == 0 {
				continue
			}
			v := coord(gv.coords, a)
			if v == pk {
				continue
			}
			var s, e float64
			if istart != nil {
				s, e = istart[a], iend[a]
				if s > pk || pk > e {
					continue
				}
			} else {
				s, e = math.Min(0, pk), math.Max(0, pk)
			}
			if v < s || v > e {
				scalar = 0
				break
			}
			if v < pk {
				if pk != s {
					scalar *= (v - s) / (pk - s)
				}
			} else if pk != e {
				scalar *= (e - v) / (e - pk)
			}
		}
		tp := p
		p += size
		if scalar == 0 {
			continue
		}
		pointsIdx, all := shared, sharedAll
		if ti&0x2000 != 0 {
			pointsIdx, all, tp = readPoints(gd, tp)
		}
		np := len(pointsIdx)
		if all {
			np = n
		}
		xs, tp := readDeltas(gd, tp, np)
		ys, _ := readDeltas(gd, tp, np)
		if all {
			for i := 0; i < n && i < len(xs); i++ {
				dx[i] += xs[i] * scalar
				dy[i] += ys[i] * scalar
			}
			continue
		}
		// Explicit points: interpolate the others (simple glyphs).
		tdx := make([]float64, n)
		tdy := make([]float64, n)
		touched := make([]bool, n)
		for k, pi := range pointsIdx {
			if pi < n && k < len(xs) {
				tdx[pi] += xs[k]
				tdy[pi] += ys[k]
				touched[pi] = true
			}
		}
		if pts != nil {
			iup(pts, ends, tdx, tdy, touched)
		}
		for i := 0; i < n; i++ {
			dx[i] += tdx[i] * scalar
			dy[i] += tdy[i] * scalar
		}
	}
	return dx, dy
}

func readPoints(b []byte, p int) ([]int, bool, int) {
	c := int(b[p])
	p++
	if c == 0 {
		return nil, true, p
	}
	if c&0x80 != 0 {
		c = (c&0x7f)<<8 | int(b[p])
		p++
	}
	out := make([]int, 0, c)
	last := 0
	for len(out) < c {
		ctl := b[p]
		p++
		run := int(ctl&0x7f) + 1
		for k := 0; k < run && len(out) < c; k++ {
			if ctl&0x80 != 0 {
				last += int(u16(b, p))
				p += 2
			} else {
				last += int(b[p])
				p++
			}
			out = append(out, last)
		}
	}
	return out, false, p
}

func readDeltas(b []byte, p, n int) ([]float64, int) {
	out := make([]float64, 0, n)
	for len(out) < n {
		ctl := b[p]
		p++
		run := int(ctl&0x3f) + 1
		for k := 0; k < run && len(out) < n; k++ {
			switch {
			case ctl&0x80 != 0:
				out = append(out, 0)
			case ctl&0x40 != 0:
				out = append(out, float64(i16(b, p)))
				p += 2
			default:
				out = append(out, float64(int8(b[p])))
				p++
			}
		}
	}
	return out, p
}

// iup interpolates the deltas of untouched points of each contour.
func iup(pts []point, ends []int, dx, dy []float64, touched []bool) {
	start := 0
	for _, end := range ends {
		if end >= len(pts) {
			break
		}
		var idx []int
		for i := start; i <= end; i++ {
			if touched[i] {
				idx = append(idx, i)
			}
		}
		if len(idx) == 0 {
			start = end + 1
			continue
		}
		if len(idx) == 1 {
			for i := start; i <= end; i++ {
				if !touched[i] {
					dx[i], dy[i] = dx[idx[0]], dy[idx[0]]
				}
			}
			start = end + 1
			continue
		}
		for k := range idx {
			a := idx[k]
			b := idx[(k+1)%len(idx)]
			// Points strictly between a and b (cyclically).
			i := a + 1
			if i > end {
				i = start
			}
			for i != b {
				iupPoint(pts, i, a, b, dx, dy)
				i++
				if i > end {
					i = start
				}
			}
		}
		start = end + 1
	}
}

func iupPoint(pts []point, i, a, b int, dx, dy []float64) {
	interp := func(c, c1, c2, d1, d2 float64) float64 {
		if c1 > c2 {
			c1, c2 = c2, c1
			d1, d2 = d2, d1
		}
		switch {
		case c1 == c2:
			if d1 == d2 {
				return d1
			}
			return 0
		case c <= c1:
			return d1
		case c >= c2:
			return d2
		}
		return d1 + (c-c1)*(d2-d1)/(c2-c1)
	}
	dx[i] = interp(pts[i].x, pts[a].x, pts[b].x, dx[a], dx[b])
	dy[i] = interp(pts[i].y, pts[a].y, pts[b].y, dy[a], dy[b])
}

// simpleGlyph decodes a simple TrueType glyph.
func decodeSimple(g []byte) ([]point, []int, bool) {
	nc := int(i16(g, 0))
	if nc <= 0 {
		return nil, nil, false
	}
	ends := make([]int, nc)
	for i := range ends {
		ends[i] = int(u16(g, 10+2*i))
	}
	n := ends[nc-1] + 1
	p := 10 + 2*nc
	il := int(u16(g, p))
	p += 2 + il
	flags := make([]byte, 0, n)
	for len(flags) < n {
		fl := g[p]
		p++
		flags = append(flags, fl)
		if fl&8 != 0 {
			r := int(g[p])
			p++
			for k := 0; k < r && len(flags) < n; k++ {
				flags = append(flags, fl)
			}
		}
	}
	pts := make([]point, n)
	v := 0
	for i, fl := range flags {
		switch {
		case fl&2 != 0:
			if fl&0x10 != 0 {
				v += int(g[p])
			} else {
				v -= int(g[p])
			}
			p++
		case fl&0x10 == 0:
			v += int(i16(g, p))
			p += 2
		}
		pts[i].x = float64(v)
		pts[i].onCurve = fl&1 != 0
	}
	v = 0
	for i, fl := range flags {
		switch {
		case fl&4 != 0:
			if fl&0x20 != 0 {
				v += int(g[p])
			} else {
				v -= int(g[p])
			}
			p++
		case fl&0x20 == 0:
			v += int(i16(g, p))
			p += 2
		}
		pts[i].y = float64(v)
	}
	return pts, ends, true
}

// encodeSimple encodes a simple glyph (without instructions).
func encodeSimple(pts []point, ends []int) []byte {
	var b bytes.Buffer
	xMin, yMin, xMax, yMax := bboxOf(pts)
	binary.Write(&b, binary.BigEndian, []int16{int16(len(ends)), int16(xMin), int16(yMin), int16(xMax), int16(yMax)})
	for _, e := range ends {
		binary.Write(&b, binary.BigEndian, uint16(e))
	}
	binary.Write(&b, binary.BigEndian, uint16(0))
	var flags, xs, ys []byte
	px, py := 0, 0
	for _, p := range pts {
		var fl byte
		if p.onCurve {
			fl |= 1
		}
		x, y := int(p.x), int(p.y)
		dxv, dyv := x-px, y-py
		px, py = x, y
		switch {
		case dxv == 0:
			fl |= 0x10
		case dxv > -256 && dxv < 256:
			fl |= 2
			if dxv > 0 {
				fl |= 0x10
				xs = append(xs, byte(dxv))
			} else {
				xs = append(xs, byte(-dxv))
			}
		default:
			xs = append(xs, byte(uint16(dxv)>>8), byte(dxv))
		}
		switch {
		case dyv == 0:
			fl |= 0x20
		case dyv > -256 && dyv < 256:
			fl |= 4
			if dyv > 0 {
				fl |= 0x20
				ys = append(ys, byte(dyv))
			} else {
				ys = append(ys, byte(-dyv))
			}
		default:
			ys = append(ys, byte(uint16(dyv)>>8), byte(dyv))
		}
		flags = append(flags, fl)
	}
	b.Write(flags)
	b.Write(xs)
	b.Write(ys)
	for b.Len()%4 != 0 {
		b.WriteByte(0)
	}
	return b.Bytes()
}

func bboxOf(pts []point) (int, int, int, int) {
	if len(pts) == 0 {
		return 0, 0, 0, 0
	}
	x0, y0, x1, y1 := pts[0].x, pts[0].y, pts[0].x, pts[0].y
	for _, p := range pts {
		x0, y0 = math.Min(x0, p.x), math.Min(y0, p.y)
		x1, y1 = math.Max(x1, p.x), math.Max(y1, p.y)
	}
	return int(x0), int(y0), int(x1), int(y1)
}

type component struct {
	flags     uint16
	glyph     uint16
	dx, dy    float64
	transform []byte // scale data, as in the font
}

func decodeComposite(g []byte) []component {
	var out []component
	p := 10
	for {
		fl := u16(g, p)
		c := component{flags: fl, glyph: u16(g, p+2)}
		p += 4
		if fl&1 != 0 {
			c.dx, c.dy = float64(i16(g, p)), float64(i16(g, p+2))
			p += 4
		} else {
			c.dx, c.dy = float64(int8(g[p])), float64(int8(g[p+1]))
			p += 2
		}
		switch {
		case fl&0x8 != 0:
			c.transform = g[p : p+2]
			p += 2
		case fl&0x40 != 0:
			c.transform = g[p : p+4]
			p += 4
		case fl&0x80 != 0:
			c.transform = g[p : p+8]
			p += 8
		}
		out = append(out, c)
		if fl&0x20 == 0 {
			break
		}
	}
	return out
}

func transformOf(c component) [4]float64 {
	t := c.transform
	switch len(t) {
	case 2:
		s := f2dot14(t, 0)
		return [4]float64{s, 0, 0, s}
	case 4:
		return [4]float64{f2dot14(t, 0), 0, 0, f2dot14(t, 2)}
	case 8:
		return [4]float64{f2dot14(t, 0), f2dot14(t, 2), f2dot14(t, 4), f2dot14(t, 6)}
	}
	return [4]float64{1, 0, 0, 1}
}

// instanceGlyf applies gvar to all glyphs. Without HVAR, advances come
// from the phantom points.
func (f *Font) instanceGlyf(gv *gvarReader, phantomAdvances bool, adv []float64, lsb []int) ([]byte, []byte, []float64, []int) {
	glyf := f.tables["glyf"]
	n := f.NumGlyphs
	outlines := make([][]point, n) // instanced simple outlines (resolved composites)
	encoded := make([][]byte, n)
	newAdv := append([]float64(nil), adv...)
	newLsb := append([]int(nil), lsb...)
	var resolve func(g, depth int) []point
	done := make([]bool, n)
	resolve = func(g, depth int) []point {
		if g >= n || depth > 8 {
			return nil
		}
		if done[g] {
			return outlines[g]
		}
		done[g] = true
		s, e := f.glyphRange(g)
		var data []byte
		if e > s && e <= len(glyf) {
			data = glyf[s:e]
		}
		xMin := 0
		if len(data) >= 10 {
			xMin = int(i16(data, 2))
		}
		phantom := func(np int) []point {
			ph := make([]point, 4)
			ph[0].x = float64(xMin - lsb[g])
			ph[1].x = ph[0].x + adv[g]
			return ph
		}
		switch {
		case len(data) < 10:
			dx, _ := gv.glyphDeltas(g, 4, nil, nil)
			if phantomAdvances {
				newAdv[g] = adv[g] + dx[1] - dx[0]
			}
			newLsb[g] = 0
		case i16(data, 0) >= 0:
			pts, ends, ok := decodeSimple(data)
			if !ok {
				encoded[g] = nil
				newLsb[g] = 0
				break
			}
			np := len(pts)
			all := append(pts, phantom(np)...)
			dx, dy := gv.glyphDeltas(g, np+4, all, ends)
			for i := range all {
				all[i].x = otRound(all[i].x + dx[i])
				all[i].y = otRound(all[i].y + dy[i])
			}
			if phantomAdvances {
				newAdv[g] = all[np+1].x - all[np].x
			}
			pts = all[:np]
			outlines[g] = pts
			encoded[g] = encodeSimple(pts, ends)
			x0, _, _, _ := bboxOf(pts)
			newLsb[g] = x0 - int(all[np].x)
		default:
			comps := decodeComposite(data)
			nc := len(comps)
			ph := phantom(nc)
			cp := make([]point, nc)
			for i, c := range comps {
				cp[i] = point{x: c.dx, y: c.dy}
			}
			dx, dy := gv.glyphDeltas(g, nc+4, nil, nil)
			var all []point
			for i := range comps {
				if comps[i].flags&2 != 0 { // ARGS_ARE_XY_VALUES
					comps[i].dx = otRound(comps[i].dx + dx[i])
					comps[i].dy = otRound(comps[i].dy + dy[i])
				}
				sub := resolve(int(comps[i].glyph), depth+1)
				m := transformOf(comps[i])
				for _, p := range sub {
					all = append(all, point{x: m[0]*p.x + m[2]*p.y + comps[i].dx, y: m[1]*p.x + m[3]*p.y + comps[i].dy, onCurve: p.onCurve})
				}
			}
			for i := range ph {
				ph[i].x = otRound(ph[i].x + dx[nc+i])
			}
			if phantomAdvances {
				newAdv[g] = ph[1].x - ph[0].x
			}
			outlines[g] = all
			encoded[g] = encodeComposite(comps, all)
			x0, _, _, _ := bboxOf(all)
			newLsb[g] = x0 - int(ph[0].x)
		}
		return outlines[g]
	}
	for g := 0; g < n; g++ {
		resolve(g, 0)
	}
	var out bytes.Buffer
	loca := make([]byte, 4*(n+1))
	for g := 0; g < n; g++ {
		binary.BigEndian.PutUint32(loca[4*g:], uint32(out.Len()))
		out.Write(encoded[g])
	}
	binary.BigEndian.PutUint32(loca[4*n:], uint32(out.Len()))
	return out.Bytes(), loca, newAdv, newLsb
}

func encodeComposite(comps []component, all []point) []byte {
	var b bytes.Buffer
	xMin, yMin, xMax, yMax := bboxOf(all)
	binary.Write(&b, binary.BigEndian, []int16{-1, int16(xMin), int16(yMin), int16(xMax), int16(yMax)})
	for i, c := range comps {
		fl := c.flags&^(0x100|0x20) | 1 // no instructions, word arguments
		if i < len(comps)-1 {
			fl |= 0x20
		}
		binary.Write(&b, binary.BigEndian, fl)
		binary.Write(&b, binary.BigEndian, c.glyph)
		binary.Write(&b, binary.BigEndian, []int16{int16(c.dx), int16(c.dy)})
		b.Write(c.transform)
	}
	for b.Len()%4 != 0 {
		b.WriteByte(0)
	}
	return b.Bytes()
}

// VariationKey returns a stable key of axis values.
func VariationKey(m map[string]float64) string {
	var tags []string
	for t := range m {
		tags = append(tags, t)
	}
	sort.Strings(tags)
	var b strings.Builder
	for _, t := range tags {
		b.WriteString(t + "=" + strconv.FormatFloat(m[t], 'g', -1, 64) + ",")
	}
	return b.String()
}

// AxisRange returns the range of an axis of a face (ok false if the face
// has no such axis).
func (fi *FaceInfo) AxisRange(tag string) (float64, float64, bool) {
	for _, a := range fi.Axes {
		if a.Tag == tag {
			return a.Min, a.Max, true
		}
	}
	return 0, 0, false
}

// stretchPercent gives the font-stretch percentages of the width classes.
var stretchPercent = [10]float64{100, 50, 62.5, 75, 87.5, 100, 112.5, 125, 150, 200}

// StretchPercent returns the percentage of a width class (1-9).
func StretchPercent(class int) float64 {
	if class < 1 || class > 9 {
		return 100
	}
	return stretchPercent[class]
}

// WithVariations returns a face drawing the instance of a variable face
// at axis values.
func (f *Face) WithVariations(v map[string]float64) *Face {
	nf := &Face{Info: f.Info, lib: f.lib, Variations: v, SynthBold: f.SynthBold, SynthItalic: f.SynthItalic}
	if f.lib == nil && f.Font != nil {
		nf.Font = f.Font.Instance(v)
	}
	return nf
}

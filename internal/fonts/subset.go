package fonts

import (
	"bytes"
	"encoding/binary"
	"sort"
)

func checksum(b []byte) uint32 {
	var sum uint32
	for i := 0; i < len(b); i += 4 {
		var v uint32
		for j := 0; j < 4; j++ {
			v <<= 8
			if i+j < len(b) {
				v |= uint32(b[i+j])
			}
		}
		sum += v
	}
	return sum
}

// buildSFNT assembles an sfnt file from tables.
func buildSFNT(version []byte, tables map[string][]byte) []byte {
	tags := make([]string, 0, len(tables))
	for t := range tables {
		tags = append(tags, t)
	}
	sort.Strings(tags)
	n := len(tags)
	var out bytes.Buffer
	out.Write(version)
	es, lg := 1, 0
	for es*2 <= n {
		es *= 2
		lg++
	}
	binary.Write(&out, binary.BigEndian, uint16(n))
	binary.Write(&out, binary.BigEndian, uint16(es*16))
	binary.Write(&out, binary.BigEndian, uint16(lg))
	binary.Write(&out, binary.BigEndian, uint16(n*16-es*16))
	offset := 12 + 16*n
	headOffset := -1
	for _, t := range tags {
		data := tables[t]
		if t == "head" && len(data) >= 12 {
			d := append([]byte(nil), data...)
			binary.BigEndian.PutUint32(d[8:], 0)
			tables[t] = d
			data = d
		}
		out.WriteString(t)
		binary.Write(&out, binary.BigEndian, checksum(data))
		binary.Write(&out, binary.BigEndian, uint32(offset))
		binary.Write(&out, binary.BigEndian, uint32(len(data)))
		if t == "head" {
			headOffset = offset
		}
		offset += (len(data) + 3) &^ 3
	}
	for _, t := range tags {
		out.Write(tables[t])
		for out.Len()%4 != 0 {
			out.WriteByte(0)
		}
	}
	b := out.Bytes()
	if headOffset >= 0 && headOffset+12 <= len(b) {
		adj := 0xB1B0AFBA - checksum(b)
		binary.BigEndian.PutUint32(b[headOffset+8:], adj)
	}
	return b
}

func (f *Font) sfntVersion() []byte {
	v := make([]byte, 4)
	copy(v, f.Data[f.Offset:f.Offset+4])
	return v
}

// Standalone returns the font as a standalone sfnt (extracting it from a
// collection if needed).
func (f *Font) Standalone() []byte {
	if f.Offset == 0 && string(f.Data[:4]) != "ttcf" {
		return f.Data
	}
	tables := map[string][]byte{}
	for t, d := range f.tables {
		tables[t] = d
	}
	return buildSFNT(f.sfntVersion(), tables)
}

// glyphData returns the glyf data of a glyph.
func (f *Font) glyphRange(g int) (int, int) {
	loca := f.tables["loca"]
	if f.locaLong {
		if 4*g+8 > len(loca) {
			return 0, 0
		}
		return int(u32(loca, 4*g)), int(u32(loca, 4*g+4))
	}
	if 2*g+4 > len(loca) {
		return 0, 0
	}
	return 2 * int(u16(loca, 2*g)), 2 * int(u16(loca, 2*g+2))
}

func (f *Font) compositeComponents(g int) []uint16 {
	glyf := f.tables["glyf"]
	start, end := f.glyphRange(g)
	if end <= start || end > len(glyf) || end-start < 10 {
		return nil
	}
	d := glyf[start:end]
	if i16(d, 0) >= 0 {
		return nil
	}
	var out []uint16
	p := 10
	for p+4 <= len(d) {
		flags := u16(d, p)
		out = append(out, u16(d, p+2))
		p += 4
		if flags&1 != 0 {
			p += 4
		} else {
			p += 2
		}
		switch {
		case flags&0x8 != 0:
			p += 2
		case flags&0x40 != 0:
			p += 4
		case flags&0x80 != 0:
			p += 8
		}
		if flags&0x20 == 0 {
			break
		}
	}
	return out
}

// Subset returns font data suitable for embedding, containing only the
// given glyphs (glyph IDs are preserved). Like HarfBuzz's subsetter used
// by WeasyPrint, glyphs after the last used one are removed, as well as
// hinting unless hinting is set.
func (f *Font) Subset(glyphs map[uint16]bool, hinting ...bool) []byte {
	keepHinting := len(hinting) > 0 && hinting[0]
	if f.IsCFF {
		if cff, ok := f.tables["CFF "]; ok {
			if sub, err := subsetCFF(cff, glyphs); err == nil {
				tables := map[string][]byte{"CFF ": sub}
				for _, t := range []string{"head", "hhea", "hmtx", "maxp", "OS/2", "name", "post", "cmap"} {
					if d, ok := f.tables[t]; ok {
						tables[t] = d
					}
				}
				return buildSFNT([]byte("OTTO"), tables)
			}
		}
		return f.Standalone()
	}
	glyf := f.tables["glyf"]
	if glyf == nil || f.tables["loca"] == nil {
		if f.tables["CBDT"] != nil || f.tables["sbix"] != nil {
			return f.emptyOutlines()
		}
		return f.Standalone()
	}
	keep := map[uint16]bool{0: true}
	var stack []uint16
	for g := range glyphs {
		stack = append(stack, g)
	}
	for len(stack) > 0 {
		g := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if keep[g] && g != 0 {
			continue
		}
		keep[g] = true
		for _, c := range f.compositeComponents(int(g)) {
			if !keep[c] {
				stack = append(stack, c)
			}
		}
	}
	// Glyphs after the last used one are dropped.
	n := 1
	for g := range keep {
		if int(g)+1 > n {
			n = int(g) + 1
		}
	}
	if n > f.NumGlyphs {
		n = f.NumGlyphs
	}
	var newGlyf bytes.Buffer
	loca := make([]byte, 4*(n+1))
	for g := 0; g < n; g++ {
		binary.BigEndian.PutUint32(loca[4*g:], uint32(newGlyf.Len()))
		if keep[uint16(g)] {
			s, e := f.glyphRange(g)
			if e > s && e <= len(glyf) {
				data := glyf[s:e]
				if !keepHinting {
					data = stripInstructions(data)
				}
				newGlyf.Write(data)
				for newGlyf.Len()%4 != 0 {
					newGlyf.WriteByte(0)
				}
			}
		}
	}
	binary.BigEndian.PutUint32(loca[4*n:], uint32(newGlyf.Len()))
	tables := map[string][]byte{
		"glyf": newGlyf.Bytes(),
		"loca": loca,
	}
	copied := []string{"head", "hhea", "OS/2"}
	if keepHinting {
		copied = append(copied, "cvt ", "fpgm", "prep", "gasp")
	}
	for _, t := range copied {
		if d, ok := f.tables[t]; ok {
			tables[t] = d
		}
	}
	if name := subsetName(f.tables["name"]); name != nil {
		tables["name"] = name
	}
	head := append([]byte(nil), tables["head"]...)
	if len(head) >= 52 {
		binary.BigEndian.PutUint16(head[50:], 1)
	}
	tables["head"] = head
	// Metrics of the kept glyphs.
	if hhea, hmtx := f.tables["hhea"], f.tables["hmtx"]; len(hhea) >= 36 && hmtx != nil {
		numH := int(u16(hhea, 34))
		var mtx bytes.Buffer
		for g := 0; g < n; g++ {
			var adv, lsb uint16
			if g < numH {
				if 4*g+4 <= len(hmtx) {
					adv, lsb = u16(hmtx, 4*g), u16(hmtx, 4*g+2)
				}
			} else if numH > 0 {
				adv = u16(hmtx, 4*(numH-1))
				if p := 4*numH + 2*(g-numH); p+2 <= len(hmtx) {
					lsb = u16(hmtx, p)
				}
			}
			binary.Write(&mtx, binary.BigEndian, [2]uint16{adv, lsb})
		}
		tables["hmtx"] = mtx.Bytes()
		h := append([]byte(nil), hhea...)
		binary.BigEndian.PutUint16(h[34:], uint16(n))
		tables["hhea"] = h
	}
	if maxp := f.tables["maxp"]; len(maxp) >= 6 {
		m := append([]byte(nil), maxp...)
		binary.BigEndian.PutUint16(m[4:], uint16(n))
		if !keepHinting && len(m) >= 32 {
			// No instructions are left.
			binary.BigEndian.PutUint16(m[26:], 0) // maxSizeOfInstructions
		}
		tables["maxp"] = m
	}
	if post := f.tables["post"]; len(post) >= 32 {
		p := append([]byte(nil), post[:32]...)
		binary.BigEndian.PutUint32(p[0:], 0x00030000)
		tables["post"] = p
	}
	return buildSFNT([]byte{0, 1, 0, 0}, tables)
}

// stripInstructions removes the hinting instructions of a glyph.
func stripInstructions(g []byte) []byte {
	if len(g) < 10 {
		return g
	}
	nc := int(int16(u16(g, 0)))
	if nc >= 0 {
		p := 10 + 2*nc
		if p+2 > len(g) {
			return g
		}
		il := int(u16(g, p))
		if il == 0 || p+2+il > len(g) {
			return g
		}
		out := make([]byte, 0, len(g)-il)
		out = append(out, g[:p]...)
		out = append(out, 0, 0)
		return append(out, g[p+2+il:]...)
	}
	// Composite glyph: clear WE_HAVE_INSTRUCTIONS and drop them.
	out := append([]byte(nil), g...)
	p := 10
	for p+4 <= len(out) {
		flags := u16(out, p)
		binary.BigEndian.PutUint16(out[p:], flags&^0x100)
		p += 4
		if flags&1 != 0 {
			p += 4
		} else {
			p += 2
		}
		switch {
		case flags&0x8 != 0:
			p += 2
		case flags&0x40 != 0:
			p += 4
		case flags&0x80 != 0:
			p += 8
		}
		if flags&0x20 == 0 {
			if flags&0x100 != 0 && p <= len(out) {
				return out[:p]
			}
			break
		}
	}
	return out
}

// subsetName keeps the English names 0 to 6 of a name table, like
// HarfBuzz's default subsetting.
func subsetName(name []byte) []byte {
	if len(name) < 6 {
		return nil
	}
	count := int(u16(name, 2))
	strOff := int(u16(name, 4))
	type rec struct {
		hdr  [4]uint16
		id   uint16
		data []byte
	}
	var recs []rec
	for i := 0; i < count; i++ {
		p := 6 + 12*i
		if p+12 > len(name) {
			break
		}
		platform, encoding, lang, id := u16(name, p), u16(name, p+2), u16(name, p+4), u16(name, p+6)
		length, off := int(u16(name, p+8)), int(u16(name, p+10))
		if id > 6 || !(platform == 3 && lang == 0x409 || platform == 1 && lang == 0) {
			continue
		}
		if strOff+off+length > len(name) {
			continue
		}
		recs = append(recs, rec{[4]uint16{platform, encoding, lang, id}, id, name[strOff+off : strOff+off+length]})
	}
	var out, strs bytes.Buffer
	binary.Write(&out, binary.BigEndian, [3]uint16{0, uint16(len(recs)), uint16(6 + 12*len(recs))})
	for _, r := range recs {
		binary.Write(&out, binary.BigEndian, r.hdr)
		binary.Write(&out, binary.BigEndian, [2]uint16{uint16(len(r.data)), uint16(strs.Len())})
		strs.Write(r.data)
	}
	out.Write(strs.Bytes())
	return out.Bytes()
}

// CFFCIDs returns, for CID-keyed CFF fonts, the mapping from glyph IDs to
// CIDs. It returns nil for name-keyed fonts (where CID == GID).
func (f *Font) CFFCIDs() map[uint16]uint16 {
	cff := f.tables["CFF "]
	if len(cff) < 4 {
		return nil
	}
	defer func() { recover() }()
	hdrSize := int(cff[2])
	// Name INDEX.
	off := skipIndex(cff, hdrSize)
	// Top DICT INDEX.
	topData := indexItem(cff, off, 0)
	dict := parseDict(topData)
	if _, ok := dict[1230]; !ok { // ROS
		return nil
	}
	charsetOff := 0
	if v, ok := dict[15]; ok && len(v) > 0 {
		charsetOff = int(v[0])
	}
	csOff := 0
	if v, ok := dict[17]; ok && len(v) > 0 {
		csOff = int(v[0])
	}
	nGlyphs := int(u16(cff, csOff))
	out := map[uint16]uint16{0: 0}
	if charsetOff == 0 {
		return out
	}
	p := charsetOff
	format := cff[p]
	p++
	gid := 1
	switch format {
	case 0:
		for gid < nGlyphs {
			out[uint16(gid)] = u16(cff, p)
			p += 2
			gid++
		}
	case 1, 2:
		for gid < nGlyphs {
			first := int(u16(cff, p))
			var left int
			if format == 1 {
				left = int(cff[p+2])
				p += 3
			} else {
				left = int(u16(cff, p+2))
				p += 4
			}
			for i := 0; i <= left && gid < nGlyphs; i++ {
				out[uint16(gid)] = uint16(first + i)
				gid++
			}
		}
	}
	return out
}

func indexInfo(b []byte, off int) (count, offSize, dataStart int) {
	count = int(u16(b, off))
	if count == 0 {
		return 0, 0, off + 2
	}
	offSize = int(b[off+2])
	dataStart = off + 3 + (count+1)*offSize - 1
	return
}

func readOffset(b []byte, p, size int) int {
	v := 0
	for i := 0; i < size; i++ {
		v = v<<8 | int(b[p+i])
	}
	return v
}

func skipIndex(b []byte, off int) int {
	count, offSize, dataStart := indexInfo(b, off)
	if count == 0 {
		return off + 2
	}
	last := readOffset(b, off+3+count*offSize, offSize)
	return dataStart + last
}

func indexItem(b []byte, off, i int) []byte {
	count, offSize, dataStart := indexInfo(b, off)
	if i >= count {
		return nil
	}
	s := readOffset(b, off+3+i*offSize, offSize)
	e := readOffset(b, off+3+(i+1)*offSize, offSize)
	return b[dataStart+s : dataStart+e]
}

func parseDict(d []byte) map[int][]float64 {
	out := map[int][]float64{}
	var operands []float64
	for i := 0; i < len(d); {
		b0 := int(d[i])
		switch {
		case b0 <= 21:
			op := b0
			i++
			if b0 == 12 {
				op = 1200 + int(d[i])
				i++
			}
			out[op] = operands
			operands = nil
		case b0 == 28:
			operands = append(operands, float64(int16(u16(d, i+1))))
			i += 3
		case b0 == 29:
			operands = append(operands, float64(int32(u32(d, i+1))))
			i += 5
		case b0 == 30:
			i++
			for i < len(d) {
				c := d[i]
				i++
				if c&0xf == 0xf || c>>4 == 0xf {
					break
				}
			}
			operands = append(operands, 0)
		case b0 >= 32 && b0 <= 246:
			operands = append(operands, float64(b0-139))
			i++
		case b0 >= 247 && b0 <= 250:
			operands = append(operands, float64((b0-247)*256+int(d[i+1])+108))
			i += 2
		case b0 >= 251 && b0 <= 254:
			operands = append(operands, float64(-(b0-251)*256-int(d[i+1])-108))
			i += 2
		default:
			i++
		}
	}
	return out
}

// emptyOutlines returns a TrueType font with the metrics of a bitmap-only
// font (such as color emoji fonts) and empty outlines: the bitmaps are
// drawn as images.
func (f *Font) emptyOutlines() []byte {
	n := f.NumGlyphs
	tables := map[string][]byte{
		"glyf": {0, 0, 0, 0},
		"loca": make([]byte, 4*(n+1)),
	}
	for _, t := range []string{"head", "hhea", "hmtx", "OS/2", "name"} {
		if d, ok := f.tables[t]; ok {
			tables[t] = d
		}
	}
	head := append([]byte(nil), tables["head"]...)
	if len(head) >= 52 {
		binary.BigEndian.PutUint16(head[50:], 1)
	}
	tables["head"] = head
	maxp := make([]byte, 32)
	binary.BigEndian.PutUint32(maxp[0:], 0x00010000)
	binary.BigEndian.PutUint16(maxp[4:], uint16(n))
	tables["maxp"] = maxp
	post := make([]byte, 32)
	binary.BigEndian.PutUint32(post[0:], 0x00030000)
	tables["post"] = post
	return buildSFNT([]byte{0, 1, 0, 0}, tables)
}

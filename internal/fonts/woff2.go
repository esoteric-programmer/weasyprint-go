package fonts

import (
	"encoding/binary"
	"errors"
	"sort"

	"github.com/esoteric-programmer/weasyprint-go/internal/brotli"
)

// WOFF2 (https://www.w3.org/TR/WOFF2/) fonts are converted back to sfnt
// (or TrueType collection) data, rebuilding the transformed glyf, loca and
// hmtx tables.

var errWOFF2 = errors.New("invalid WOFF2 font")

var woff2Tags = [63]string{
	"cmap", "head", "hhea", "hmtx", "maxp", "name", "OS/2", "post", "cvt ",
	"fpgm", "glyf", "loca", "prep", "CFF ", "VORG", "EBDT", "EBLC", "gasp",
	"hdmx", "kern", "LTSH", "PCLT", "VDMX", "vhea", "vmtx", "BASE", "GDEF",
	"GPOS", "GSUB", "EBSC", "JSTF", "MATH", "CBDT", "CBLC", "COLR", "CPAL",
	"SVG ", "sbix", "acnt", "avar", "bdat", "bloc", "bsln", "cvar", "fdsc",
	"feat", "fmtx", "fvar", "gvar", "hsty", "just", "lcar", "mort", "morx",
	"opbd", "prop", "trak", "Zapf", "Silf", "Glat", "Gloc", "Feat", "Sill",
}

type woff2Reader struct {
	d   []byte
	pos int
	err bool
}

func (r *woff2Reader) u8() int {
	if r.pos+1 > len(r.d) {
		r.err = true
		return 0
	}
	r.pos++
	return int(r.d[r.pos-1])
}

func (r *woff2Reader) u16() int {
	if r.pos+2 > len(r.d) {
		r.err = true
		return 0
	}
	r.pos += 2
	return int(binary.BigEndian.Uint16(r.d[r.pos-2:]))
}

func (r *woff2Reader) i16() int { return int(int16(r.u16())) }

func (r *woff2Reader) u32() int {
	if r.pos+4 > len(r.d) {
		r.err = true
		return 0
	}
	r.pos += 4
	return int(binary.BigEndian.Uint32(r.d[r.pos-4:]))
}

func (r *woff2Reader) bytes(n int) []byte {
	if n < 0 || r.pos+n > len(r.d) {
		r.err = true
		return nil
	}
	r.pos += n
	return r.d[r.pos-n : r.pos]
}

// base128 reads a UIntBase128 value.
func (r *woff2Reader) base128() int {
	v := 0
	for i := 0; i < 5; i++ {
		b := r.u8()
		if i == 0 && b == 0x80 {
			r.err = true
			return 0
		}
		if v&0xfe000000 != 0 {
			r.err = true
			return 0
		}
		v = v<<7 | b&0x7f
		if b&0x80 == 0 {
			return v
		}
	}
	r.err = true
	return 0
}

// u255 reads a 255UInt16 value.
func (r *woff2Reader) u255() int {
	switch c := r.u8(); c {
	case 253:
		return r.u16()
	case 255:
		return r.u8() + 253
	case 254:
		return r.u8() + 506
	default:
		return c
	}
}

type woff2Table struct {
	tag          string
	transformed  bool
	origLen      int
	transformLen int
	data         []byte // decompressed (possibly transformed) data
	out          []byte // reconstructed data
	xMins        []int  // glyph xMin values, for glyf tables
}

type woff2Face struct {
	flavor  int
	indices []int
}

// decodeWOFF2 converts a WOFF2 font to sfnt or TTC data.
func decodeWOFF2(d []byte) ([]byte, error) {
	r := &woff2Reader{d: d}
	if string(r.bytes(4)) != "wOF2" {
		return nil, errWOFF2
	}
	flavor := r.u32()
	r.u32() // length
	numTables := r.u16()
	r.u16() // reserved
	totalSfnt := r.u32()
	compressedLen := r.u32()
	r.pos += 4 + 20 // versions, metadata and private blocks
	tables := make([]*woff2Table, numTables)
	for i := range tables {
		flags := r.u8()
		t := &woff2Table{}
		if idx := flags & 63; idx == 63 {
			t.tag = string(r.bytes(4))
		} else {
			t.tag = woff2Tags[idx]
		}
		version := flags >> 6
		t.origLen = r.base128()
		if t.tag == "glyf" || t.tag == "loca" {
			t.transformed = version == 0
		} else {
			t.transformed = version != 0
		}
		t.transformLen = t.origLen
		if t.transformed {
			t.transformLen = r.base128()
		}
		tables[i] = t
		if r.err {
			return nil, errWOFF2
		}
	}
	// Collection directory.
	type face = woff2Face
	var faces []face
	if flavor == 0x74746366 { // ttcf
		r.u32() // version
		n := r.u255()
		for i := 0; i < n; i++ {
			nt := r.u255()
			f := face{flavor: r.u32()}
			for k := 0; k < nt; k++ {
				idx := r.u255()
				if idx >= numTables {
					return nil, errWOFF2
				}
				f.indices = append(f.indices, idx)
			}
			faces = append(faces, f)
		}
	} else {
		f := face{flavor: flavor}
		for i := range tables {
			f.indices = append(f.indices, i)
		}
		faces = append(faces, f)
	}
	if r.err {
		return nil, errWOFF2
	}
	data, err := brotli.DecodeSize(r.bytes(compressedLen), totalSfnt)
	if err != nil || r.err {
		return nil, errWOFF2
	}
	pos := 0
	for _, t := range tables {
		if pos+t.transformLen > len(data) {
			return nil, errWOFF2
		}
		t.data = data[pos : pos+t.transformLen]
		pos += t.transformLen
		if !t.transformed {
			t.out = t.data
		}
	}
	// Rebuild the transformed tables, face by face (tables may be shared).
	for _, f := range faces {
		get := func(tag string) *woff2Table {
			for _, i := range f.indices {
				if tables[i].tag == tag {
					return tables[i]
				}
			}
			return nil
		}
		glyf, loca := get("glyf"), get("loca")
		var xMins []int
		if glyf != nil && glyf.transformed && glyf.out == nil {
			if loca == nil {
				return nil, errWOFF2
			}
			g, l, mins, err := rebuildGlyf(glyf.data)
			if err != nil {
				return nil, err
			}
			glyf.out, loca.out = g, l
			glyf.xMins = mins
		}
		if glyf != nil {
			xMins = glyf.xMins
		}
		if loca != nil && loca.out == nil {
			return nil, errWOFF2
		}
		if hmtx := get("hmtx"); hmtx != nil && hmtx.out == nil {
			hhea, maxp := get("hhea"), get("maxp")
			if hhea == nil || maxp == nil || len(hhea.out) < 36 || len(maxp.out) < 6 {
				return nil, errWOFF2
			}
			numH := int(binary.BigEndian.Uint16(hhea.out[34:]))
			numGlyphs := int(binary.BigEndian.Uint16(maxp.out[4:]))
			out, err := rebuildHmtx(hmtx.data, numH, numGlyphs, xMins)
			if err != nil {
				return nil, err
			}
			hmtx.out = out
		}
		for _, i := range f.indices {
			if tables[i].out == nil {
				return nil, errWOFF2
			}
		}
	}
	if len(faces) == 1 {
		return buildSfnt(faces[0].flavor, faces[0].indices, tables), nil
	}
	return buildCollection(faces, tables), nil
}

// sfntDirectory writes an sfnt table directory followed by the tables.
func sfntDirectory(flavor int, tabs []*woff2Table, offset int, offsets map[*woff2Table]int) []byte {
	sorted := append([]*woff2Table(nil), tabs...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].tag < sorted[j].tag })
	n := len(sorted)
	out := make([]byte, 12+16*n)
	binary.BigEndian.PutUint32(out, uint32(flavor))
	binary.BigEndian.PutUint16(out[4:], uint16(n))
	es, lg := 1, 0
	for es*2 <= n {
		es *= 2
		lg++
	}
	binary.BigEndian.PutUint16(out[6:], uint16(es*16))
	binary.BigEndian.PutUint16(out[8:], uint16(lg))
	binary.BigEndian.PutUint16(out[10:], uint16(n*16-es*16))
	for i, t := range sorted {
		e := out[12+16*i:]
		copy(e, t.tag)
		binary.BigEndian.PutUint32(e[4:], checksum(t.out))
		binary.BigEndian.PutUint32(e[8:], uint32(offsets[t]))
		binary.BigEndian.PutUint32(e[12:], uint32(len(t.out)))
	}
	return out
}

func pad4(b []byte) []byte {
	for len(b)%4 != 0 {
		b = append(b, 0)
	}
	return b
}

func buildSfnt(flavor int, indices []int, tables []*woff2Table) []byte {
	var tabs []*woff2Table
	for _, i := range indices {
		tabs = append(tabs, tables[i])
	}
	offsets := map[*woff2Table]int{}
	off := 12 + 16*len(tabs)
	for _, t := range tabs {
		offsets[t] = off
		off += (len(t.out) + 3) &^ 3
	}
	out := sfntDirectory(flavor, tabs, 0, offsets)
	for _, t := range tabs {
		out = pad4(append(out, t.out...))
	}
	return out
}

func buildCollection(faces []woff2Face, tables []*woff2Table) []byte {
	hdr := 12 + 4*len(faces)
	dirSize := 0
	for _, f := range faces {
		dirSize += 12 + 16*len(f.indices)
	}
	offsets := map[*woff2Table]int{}
	off := hdr + dirSize
	for _, t := range tables {
		offsets[t] = off
		off += (len(t.out) + 3) &^ 3
	}
	out := make([]byte, hdr)
	copy(out, "ttcf")
	binary.BigEndian.PutUint32(out[4:], 0x00010000)
	binary.BigEndian.PutUint32(out[8:], uint32(len(faces)))
	dirOff := hdr
	for i, f := range faces {
		binary.BigEndian.PutUint32(out[12+4*i:], uint32(dirOff))
		dirOff += 12 + 16*len(f.indices)
	}
	for _, f := range faces {
		var tabs []*woff2Table
		for _, i := range f.indices {
			tabs = append(tabs, tables[i])
		}
		out = append(out, sfntDirectory(f.flavor, tabs, 0, offsets)...)
	}
	for _, t := range tables {
		out = pad4(append(out, t.out...))
	}
	return out
}

// rebuildGlyf rebuilds the glyf and loca tables from the transformed glyf
// table, and returns the xMin of each glyph.
func rebuildGlyf(d []byte) (glyf, loca []byte, xMins []int, err error) {
	r := &woff2Reader{d: d}
	r.u16() // reserved
	options := r.u16()
	numGlyphs := r.u16()
	indexFormat := r.u16()
	var sizes [7]int
	for i := range sizes {
		sizes[i] = r.u32()
	}
	var streams [7]*woff2Reader
	for i := range streams {
		streams[i] = &woff2Reader{d: r.bytes(sizes[i])}
	}
	var overlap []byte
	if options&1 != 0 {
		overlap = r.bytes((numGlyphs + 7) / 8)
	}
	if r.err {
		return nil, nil, nil, errWOFF2
	}
	nContours, nPoints, flagS, glyphS, compS, bboxS, instrS := streams[0], streams[1], streams[2], streams[3], streams[4], streams[5], streams[6]
	bitmapLen := 4 * ((numGlyphs + 31) / 32)
	bboxBitmap := bboxS.bytes(bitmapLen)
	hasBBox := func(i int) bool { return bboxBitmap[i>>3]&(0x80>>uint(i&7)) != 0 }
	offsets := make([]int, numGlyphs+1)
	xMins = make([]int, numGlyphs)
	for i := 0; i < numGlyphs; i++ {
		offsets[i] = len(glyf)
		nc := nContours.i16()
		if nContours.err {
			return nil, nil, nil, errWOFF2
		}
		switch {
		case nc == 0:
			if hasBBox(i) {
				return nil, nil, nil, errWOFF2
			}
		case nc < 0:
			// Composite glyph.
			if !hasBBox(i) {
				return nil, nil, nil, errWOFF2
			}
			bbox := bboxS.bytes(8)
			start := compS.pos
			haveInstr := false
			for {
				flags := compS.u16()
				compS.u16() // glyph index
				n := 2
				if flags&1 != 0 {
					n = 4
				}
				switch {
				case flags&0x08 != 0:
					n += 2
				case flags&0x40 != 0:
					n += 4
				case flags&0x80 != 0:
					n += 8
				}
				compS.bytes(n)
				if flags&0x100 != 0 {
					haveInstr = true
				}
				if flags&0x20 == 0 || compS.err {
					break
				}
			}
			if compS.err || bboxS.err {
				return nil, nil, nil, errWOFF2
			}
			glyf = binary.BigEndian.AppendUint16(glyf, 0xffff)
			glyf = append(glyf, bbox...)
			glyf = append(glyf, compS.d[start:compS.pos]...)
			if haveInstr {
				n := glyphS.u255()
				glyf = binary.BigEndian.AppendUint16(glyf, uint16(n))
				glyf = append(glyf, instrS.bytes(n)...)
			}
			xMins[i] = int(int16(binary.BigEndian.Uint16(bbox)))
		default:
			// Simple glyph.
			ends := make([]int, nc)
			total := 0
			for c := 0; c < nc; c++ {
				total += nPoints.u255()
				ends[c] = total - 1
			}
			if nPoints.err || total > 65535 {
				return nil, nil, nil, errWOFF2
			}
			fl := flagS.bytes(total)
			if flagS.err {
				return nil, nil, nil, errWOFF2
			}
			xs := make([]int, total)
			ys := make([]int, total)
			on := make([]bool, total)
			x, y := 0, 0
			for p := 0; p < total; p++ {
				f := int(fl[p])
				on[p] = f&0x80 == 0
				f &= 0x7f
				sign := func(flag, v int) int {
					if flag&1 != 0 {
						return v
					}
					return -v
				}
				var dx, dy int
				switch {
				case f < 10:
					dy = sign(f, (f&14)<<7+glyphS.u8())
				case f < 20:
					dx = sign(f, ((f-10)&14)<<7+glyphS.u8())
				case f < 84:
					b0, b1 := f-20, glyphS.u8()
					dx = sign(f, 1+(b0&0x30)+b1>>4)
					dy = sign(f>>1, 1+(b0&0x0c)<<2+b1&0x0f)
				case f < 120:
					b0 := f - 84
					dx = sign(f, 1+(b0/12)<<8+glyphS.u8())
					dy = sign(f>>1, 1+((b0%12)>>2)<<8+glyphS.u8())
				case f < 124:
					b0, b1, b2 := glyphS.u8(), glyphS.u8(), glyphS.u8()
					dx = sign(f, b0<<4+b1>>4)
					dy = sign(f>>1, (b1&0x0f)<<8+b2)
				default:
					b0, b1, b2, b3 := glyphS.u8(), glyphS.u8(), glyphS.u8(), glyphS.u8()
					dx = sign(f, b0<<8+b1)
					dy = sign(f>>1, b2<<8+b3)
				}
				x += dx
				y += dy
				xs[p], ys[p] = x, y
			}
			instrLen := glyphS.u255()
			instr := instrS.bytes(instrLen)
			if glyphS.err || instrS.err {
				return nil, nil, nil, errWOFF2
			}
			var bbox [4]int
			if hasBBox(i) {
				b := bboxS.bytes(8)
				if bboxS.err {
					return nil, nil, nil, errWOFF2
				}
				for k := range bbox {
					bbox[k] = int(int16(binary.BigEndian.Uint16(b[2*k:])))
				}
			} else if total > 0 {
				bbox = [4]int{xs[0], ys[0], xs[0], ys[0]}
				for p := range xs {
					bbox[0], bbox[1] = min(bbox[0], xs[p]), min(bbox[1], ys[p])
					bbox[2], bbox[3] = max(bbox[2], xs[p]), max(bbox[3], ys[p])
				}
			}
			xMins[i] = bbox[0]
			glyf = binary.BigEndian.AppendUint16(glyf, uint16(nc))
			for _, v := range bbox {
				glyf = binary.BigEndian.AppendUint16(glyf, uint16(int16(v)))
			}
			for _, e := range ends {
				glyf = binary.BigEndian.AppendUint16(glyf, uint16(e))
			}
			glyf = binary.BigEndian.AppendUint16(glyf, uint16(instrLen))
			glyf = append(glyf, instr...)
			glyf = appendSimpleOutline(glyf, xs, ys, on, overlap != nil && overlap[i>>3]&(0x80>>uint(i&7)) != 0)
		}
		// Glyphs are padded to 4 bytes (valid for both loca formats).
		glyf = pad4(glyf)
	}
	offsets[numGlyphs] = len(glyf)
	for _, o := range offsets {
		if indexFormat == 0 {
			if o/2 > 0xffff {
				return nil, nil, nil, errWOFF2
			}
			loca = binary.BigEndian.AppendUint16(loca, uint16(o/2))
		} else {
			loca = binary.BigEndian.AppendUint32(loca, uint32(o))
		}
	}
	return glyf, loca, xMins, nil
}

// appendSimpleOutline appends the flags and coordinates of a simple glyph.
func appendSimpleOutline(out []byte, xs, ys []int, on []bool, overlap bool) []byte {
	n := len(xs)
	flags := make([]byte, n)
	var xb, yb []byte
	px, py := 0, 0
	for p := 0; p < n; p++ {
		var f byte
		if on[p] {
			f = 0x01
		}
		if p == 0 && overlap {
			f |= 0x40
		}
		dx, dy := xs[p]-px, ys[p]-py
		px, py = xs[p], ys[p]
		switch {
		case dx == 0:
			f |= 0x10
		case dx > -256 && dx < 256:
			f |= 0x02
			if dx > 0 {
				f |= 0x10
			} else {
				dx = -dx
			}
			xb = append(xb, byte(dx))
		default:
			xb = binary.BigEndian.AppendUint16(xb, uint16(int16(dx)))
		}
		switch {
		case dy == 0:
			f |= 0x20
		case dy > -256 && dy < 256:
			f |= 0x04
			if dy > 0 {
				f |= 0x20
			} else {
				dy = -dy
			}
			yb = append(yb, byte(dy))
		default:
			yb = binary.BigEndian.AppendUint16(yb, uint16(int16(dy)))
		}
		flags[p] = f
	}
	// Flags with repeat counts.
	for p := 0; p < n; {
		f := flags[p]
		run := 1
		for p+run < n && flags[p+run] == f && run < 256 {
			run++
		}
		if run > 1 {
			out = append(out, f|0x08, byte(run-1))
		} else {
			out = append(out, f)
		}
		p += run
	}
	out = append(out, xb...)
	return append(out, yb...)
}

// rebuildHmtx rebuilds the hmtx table from its transformed version.
func rebuildHmtx(d []byte, numH, numGlyphs int, xMins []int) ([]byte, error) {
	r := &woff2Reader{d: d}
	flags := r.u8()
	if numH > numGlyphs || numH < 1 {
		return nil, errWOFF2
	}
	adv := make([]int, numH)
	for i := range adv {
		adv[i] = r.u16()
	}
	lsb := make([]int, numGlyphs)
	for i := 0; i < numGlyphs; i++ {
		explicit := (i < numH && flags&1 == 0) || (i >= numH && flags&2 == 0)
		switch {
		case explicit:
			lsb[i] = r.i16()
		case i < len(xMins):
			lsb[i] = xMins[i]
		}
	}
	if r.err {
		return nil, errWOFF2
	}
	var out []byte
	for i := 0; i < numGlyphs; i++ {
		if i < numH {
			out = binary.BigEndian.AppendUint16(out, uint16(adv[i]))
		}
		out = binary.BigEndian.AppendUint16(out, uint16(int16(lsb[i])))
	}
	return out, nil
}

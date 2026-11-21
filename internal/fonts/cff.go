package fonts

import (
	"bytes"
	"encoding/binary"
	"errors"
)

// CFF subsetting. Glyph IDs are preserved: unused charstrings are
// replaced by a single endchar operator, which removes almost all of the
// glyph data while keeping charset, FDSelect and subroutines valid.

type dictEntry struct {
	op       int
	operands [][]byte // raw operand encodings
	values   []float64
}

func parseDictEntries(d []byte) ([]dictEntry, error) {
	var out []dictEntry
	var ops [][]byte
	var vals []float64
	for i := 0; i < len(d); {
		b0 := int(d[i])
		start := i
		switch {
		case b0 <= 21:
			op := b0
			i++
			if b0 == 12 {
				if i >= len(d) {
					return nil, errors.New("bad dict")
				}
				op = 1200 + int(d[i])
				i++
			}
			out = append(out, dictEntry{op, ops, vals})
			ops, vals = nil, nil
			continue
		case b0 == 28:
			if i+3 > len(d) {
				return nil, errors.New("bad dict")
			}
			vals = append(vals, float64(int16(binary.BigEndian.Uint16(d[i+1:]))))
			i += 3
		case b0 == 29:
			if i+5 > len(d) {
				return nil, errors.New("bad dict")
			}
			vals = append(vals, float64(int32(binary.BigEndian.Uint32(d[i+1:]))))
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
			vals = append(vals, 0)
		case b0 >= 32 && b0 <= 246:
			vals = append(vals, float64(b0-139))
			i++
		case b0 >= 247 && b0 <= 250:
			if i+2 > len(d) {
				return nil, errors.New("bad dict")
			}
			vals = append(vals, float64((b0-247)*256+int(d[i+1])+108))
			i += 2
		case b0 >= 251 && b0 <= 254:
			if i+2 > len(d) {
				return nil, errors.New("bad dict")
			}
			vals = append(vals, float64(-(b0-251)*256-int(d[i+1])-108))
			i += 2
		default:
			return nil, errors.New("bad dict operand")
		}
		ops = append(ops, d[start:i])
	}
	return out, nil
}

func encInt32(v int) []byte {
	b := []byte{29, 0, 0, 0, 0}
	binary.BigEndian.PutUint32(b[1:], uint32(int32(v)))
	return b
}

func encodeDict(entries []dictEntry, override map[int][]int) []byte {
	var b bytes.Buffer
	for _, e := range entries {
		if vals, ok := override[e.op]; ok {
			for _, v := range vals {
				b.Write(encInt32(v))
			}
		} else {
			for _, o := range e.operands {
				b.Write(o)
			}
		}
		if e.op >= 1200 {
			b.WriteByte(12)
			b.WriteByte(byte(e.op - 1200))
		} else {
			b.WriteByte(byte(e.op))
		}
	}
	return b.Bytes()
}

func dictLookup(entries []dictEntry, op int) ([]float64, bool) {
	for _, e := range entries {
		if e.op == op {
			return e.values, true
		}
	}
	return nil, false
}

type cffIndex struct {
	items [][]byte
	end   int // offset after the INDEX
}

func readIndex(b []byte, off int) (cffIndex, error) {
	if off+2 > len(b) {
		return cffIndex{}, errors.New("index out of range")
	}
	count := int(binary.BigEndian.Uint16(b[off:]))
	if count == 0 {
		return cffIndex{end: off + 2}, nil
	}
	if off+3 > len(b) {
		return cffIndex{}, errors.New("index out of range")
	}
	offSize := int(b[off+2])
	if offSize < 1 || offSize > 4 {
		return cffIndex{}, errors.New("bad offSize")
	}
	base := off + 3 + (count+1)*offSize - 1
	if off+3+(count+1)*offSize > len(b) {
		return cffIndex{}, errors.New("index out of range")
	}
	idx := cffIndex{items: make([][]byte, count)}
	prev := readOffset(b, off+3, offSize)
	for i := 0; i < count; i++ {
		next := readOffset(b, off+3+(i+1)*offSize, offSize)
		if base+next > len(b) || next < prev {
			return cffIndex{}, errors.New("bad index offsets")
		}
		idx.items[i] = b[base+prev : base+next]
		prev = next
	}
	idx.end = base + prev
	return idx, nil
}

func writeIndex(items [][]byte) []byte {
	var b bytes.Buffer
	binary.Write(&b, binary.BigEndian, uint16(len(items)))
	if len(items) == 0 {
		return b.Bytes()
	}
	total := 1
	for _, it := range items {
		total += len(it)
	}
	offSize := 1
	switch {
	case total > 0xffffff:
		offSize = 4
	case total > 0xffff:
		offSize = 3
	case total > 0xff:
		offSize = 2
	}
	b.WriteByte(byte(offSize))
	off := 1
	put := func(v int) {
		for i := offSize - 1; i >= 0; i-- {
			b.WriteByte(byte(v >> (8 * i)))
		}
	}
	put(off)
	for _, it := range items {
		off += len(it)
		put(off)
	}
	for _, it := range items {
		b.Write(it)
	}
	return b.Bytes()
}

func charsetSize(b []byte, off, nGlyphs int) (int, error) {
	if off >= len(b) {
		return 0, errors.New("charset out of range")
	}
	switch b[off] {
	case 0:
		return 1 + 2*(nGlyphs-1), nil
	case 1, 2:
		p := off + 1
		covered := 1
		for covered < nGlyphs {
			if p+3 > len(b) {
				return 0, errors.New("charset out of range")
			}
			var left int
			if b[off] == 1 {
				left = int(b[p+2])
				p += 3
			} else {
				left = int(binary.BigEndian.Uint16(b[p+2:]))
				p += 4
			}
			covered += left + 1
		}
		return p - off, nil
	}
	return 0, errors.New("bad charset format")
}

func fdSelectSize(b []byte, off, nGlyphs int) (int, error) {
	if off >= len(b) {
		return 0, errors.New("fdselect out of range")
	}
	switch b[off] {
	case 0:
		return 1 + nGlyphs, nil
	case 3:
		if off+3 > len(b) {
			return 0, errors.New("fdselect out of range")
		}
		n := int(binary.BigEndian.Uint16(b[off+1:]))
		return 3 + 3*n + 2, nil
	}
	return 0, errors.New("bad fdselect format")
}

func encodingSize(b []byte, off int) (int, error) {
	if off >= len(b) {
		return 0, errors.New("encoding out of range")
	}
	f := b[off]
	size := 0
	switch f & 0x7f {
	case 0:
		size = 2 + int(b[off+1])
	case 1:
		size = 2 + 2*int(b[off+1])
	default:
		return 0, errors.New("bad encoding format")
	}
	if f&0x80 != 0 {
		if off+size >= len(b) {
			return 0, errors.New("encoding out of range")
		}
		size += 1 + 3*int(b[off+size])
	}
	return size, nil
}

// privateBlock returns the private DICT entries and its local subrs.
func privateBlock(cff []byte, size, off int) ([]dictEntry, []byte, error) {
	if off < 0 || size < 0 || off+size > len(cff) {
		return nil, nil, errors.New("private out of range")
	}
	entries, err := parseDictEntries(cff[off : off+size])
	if err != nil {
		return nil, nil, err
	}
	var subrs []byte
	if v, ok := dictLookup(entries, 19); ok && len(v) == 1 {
		so := off + int(v[0])
		idx, err := readIndex(cff, so)
		if err != nil {
			return nil, nil, err
		}
		subrs = cff[so:idx.end]
	}
	return entries, subrs, nil
}

// subsetCFF returns a CFF table keeping only the charstrings of glyphs.
func subsetCFF(cff []byte, keep map[uint16]bool) ([]byte, error) {
	if len(cff) < 4 {
		return nil, errors.New("short CFF")
	}
	hdrSize := int(cff[2])
	nameIdx, err := readIndex(cff, hdrSize)
	if err != nil {
		return nil, err
	}
	topIdx, err := readIndex(cff, nameIdx.end)
	if err != nil || len(topIdx.items) != 1 {
		return nil, errors.New("unsupported top dict")
	}
	strIdx, err := readIndex(cff, topIdx.end)
	if err != nil {
		return nil, err
	}
	gsubrs, err := readIndex(cff, strIdx.end)
	if err != nil {
		return nil, err
	}
	top, err := parseDictEntries(topIdx.items[0])
	if err != nil {
		return nil, err
	}
	csv, ok := dictLookup(top, 17)
	if !ok || len(csv) != 1 {
		return nil, errors.New("no charstrings")
	}
	cs, err := readIndex(cff, int(csv[0]))
	if err != nil {
		return nil, err
	}
	nGlyphs := len(cs.items)
	charstringType := 2
	if v, ok := dictLookup(top, 1206); ok && len(v) == 1 {
		charstringType = int(v[0])
	}
	endchar := []byte{14}
	if charstringType == 1 {
		endchar = []byte{0x8b, 0x8b, 13, 14} // 0 0 hsbw endchar
	}
	newCS := make([][]byte, nGlyphs)
	for i := range cs.items {
		if i == 0 || keep[uint16(i)] {
			newCS[i] = cs.items[i]
		} else {
			newCS[i] = endchar
		}
	}

	// Raw blocks to copy.
	var charset, encoding, fdselect []byte
	if v, ok := dictLookup(top, 15); ok && len(v) == 1 && v[0] > 2 {
		n, err := charsetSize(cff, int(v[0]), nGlyphs)
		if err != nil {
			return nil, err
		}
		charset = cff[int(v[0]) : int(v[0])+n]
	}
	if v, ok := dictLookup(top, 16); ok && len(v) == 1 && v[0] > 1 {
		n, err := encodingSize(cff, int(v[0]))
		if err != nil {
			return nil, err
		}
		encoding = cff[int(v[0]) : int(v[0])+n]
	}
	cidKeyed := false
	if _, ok := dictLookup(top, 1230); ok {
		cidKeyed = true
	}
	var fdDicts [][]dictEntry
	var fdPrivates []struct {
		entries []dictEntry
		subrs   []byte
	}
	var topPrivate []dictEntry
	var topSubrs []byte
	if cidKeyed {
		fv, ok := dictLookup(top, 1236)
		if !ok || len(fv) != 1 {
			return nil, errors.New("no FDArray")
		}
		fdIdx, err := readIndex(cff, int(fv[0]))
		if err != nil {
			return nil, err
		}
		for _, item := range fdIdx.items {
			e, err := parseDictEntries(item)
			if err != nil {
				return nil, err
			}
			fdDicts = append(fdDicts, e)
			pv, ok := dictLookup(e, 18)
			var pe []dictEntry
			var ps []byte
			if ok && len(pv) == 2 {
				pe, ps, err = privateBlock(cff, int(pv[0]), int(pv[1]))
				if err != nil {
					return nil, err
				}
			}
			fdPrivates = append(fdPrivates, struct {
				entries []dictEntry
				subrs   []byte
			}{pe, ps})
		}
		sv, ok := dictLookup(top, 1237)
		if !ok || len(sv) != 1 {
			return nil, errors.New("no FDSelect")
		}
		n, err := fdSelectSize(cff, int(sv[0]), nGlyphs)
		if err != nil {
			return nil, err
		}
		fdselect = cff[int(sv[0]) : int(sv[0])+n]
	} else if pv, ok := dictLookup(top, 18); ok && len(pv) == 2 {
		topPrivate, topSubrs, err = privateBlock(cff, int(pv[0]), int(pv[1]))
		if err != nil {
			return nil, err
		}
	}

	csData := writeIndex(newCS)
	encPrivate := func(entries []dictEntry, subrs []byte) []byte {
		ov := map[int][]int{}
		if subrs != nil {
			// The Subrs offset is relative to the private dict: put the
			// subrs right after it. Its size doesn't depend on the value.
			size := len(encodeDict(entries, map[int][]int{19: {0}}))
			ov[19] = []int{size}
		}
		d := encodeDict(entries, ov)
		return append(d, subrs...)
	}

	// Build the font, iterating until the top dict size is stable.
	header := cff[:hdrSize]
	nameData := writeIndex(nameIdx.items)
	strData := writeIndex(strIdx.items)
	gsubrData := writeIndex(gsubrs.items)
	topOverride := map[int][]int{}
	for _, op := range []int{15, 16, 17, 1236, 1237} {
		if _, ok := dictLookup(top, op); ok {
			topOverride[op] = []int{0}
		}
	}
	if _, ok := dictLookup(top, 18); ok && !cidKeyed {
		topOverride[18] = []int{0, 0}
	}
	if v, ok := dictLookup(top, 15); ok && len(v) == 1 && v[0] <= 2 {
		delete(topOverride, 15)
	}
	if v, ok := dictLookup(top, 16); ok && len(v) == 1 && v[0] <= 1 {
		delete(topOverride, 16)
	}
	topSize := len(writeIndex([][]byte{encodeDict(top, topOverride)}))
	base := len(header) + len(nameData) + topSize + len(strData) + len(gsubrData)
	pos := base
	if charset != nil {
		topOverride[15] = []int{pos}
		pos += len(charset)
	}
	if encoding != nil {
		topOverride[16] = []int{pos}
		pos += len(encoding)
	}
	if fdselect != nil {
		topOverride[1237] = []int{pos}
		pos += len(fdselect)
	}
	topOverride[17] = []int{pos}
	pos += len(csData)
	var tail bytes.Buffer
	if cidKeyed {
		// Private dicts first (after the FDArray), so compute FDArray size.
		var privBlocks [][]byte
		for _, p := range fdPrivates {
			if p.entries == nil {
				privBlocks = append(privBlocks, nil)
				continue
			}
			privBlocks = append(privBlocks, encPrivate(p.entries, p.subrs))
		}
		fdItems := make([][]byte, len(fdDicts))
		for i, d := range fdDicts {
			fdItems[i] = encodeDict(d, map[int][]int{18: {0, 0}})
		}
		fdArraySize := len(writeIndex(fdItems))
		topOverride[1236] = []int{pos}
		privPos := pos + fdArraySize
		for i, d := range fdDicts {
			if privBlocks[i] == nil {
				fdItems[i] = encodeDict(d, nil)
				continue
			}
			plen := len(encodeDict(fdPrivates[i].entries, map[int][]int{19: {0}}))
			if fdPrivates[i].subrs == nil {
				plen = len(encodeDict(fdPrivates[i].entries, nil))
			}
			fdItems[i] = encodeDict(d, map[int][]int{18: {plen, privPos}})
			privPos += len(privBlocks[i])
		}
		tail.Write(writeIndex(fdItems))
		for _, pb := range privBlocks {
			tail.Write(pb)
		}
	} else if topPrivate != nil {
		pb := encPrivate(topPrivate, topSubrs)
		plen := len(encodeDict(topPrivate, map[int][]int{19: {0}}))
		if topSubrs == nil {
			plen = len(encodeDict(topPrivate, nil))
		}
		topOverride[18] = []int{plen, pos}
		tail.Write(pb)
	}
	topData := writeIndex([][]byte{encodeDict(top, topOverride)})
	if len(topData) != topSize {
		return nil, errors.New("unstable top dict size")
	}
	var out bytes.Buffer
	out.Write(header)
	out.Write(nameData)
	out.Write(topData)
	out.Write(strData)
	out.Write(gsubrData)
	out.Write(charset)
	out.Write(encoding)
	out.Write(fdselect)
	out.Write(csData)
	out.Write(tail.Bytes())
	return out.Bytes(), nil
}

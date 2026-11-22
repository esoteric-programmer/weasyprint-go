package fonts

import (
	"bytes"
	"encoding/binary"
	"errors"
	"math"
	"strconv"
)

// CFF2 variable fonts: instances are converted to CID-keyed CFF tables
// with the blends evaluated, subroutines flattened and hints dropped.

// index2 is a CFF2 INDEX (32-bit count).
type index2 struct {
	b         []byte
	count     int
	offSize   int
	offs      int
	dataStart int
}

func readIndex2(b []byte, off int) index2 {
	ix := index2{b: b, count: int(u32(b, off))}
	if ix.count == 0 {
		ix.dataStart = off + 4
		return ix
	}
	ix.offSize = int(b[off+4])
	ix.offs = off + 5
	ix.dataStart = off + 5 + (ix.count+1)*ix.offSize - 1
	return ix
}

func (ix index2) item(i int) []byte {
	if i < 0 || i >= ix.count {
		return nil
	}
	s := readOffset(ix.b, ix.offs+i*ix.offSize, ix.offSize)
	e := readOffset(ix.b, ix.offs+(i+1)*ix.offSize, ix.offSize)
	return ix.b[ix.dataStart+s : ix.dataStart+e]
}

// parseDict2 parses a CFF2 DICT (operators up to 24, blend in values
// ignored: the default values are kept).
func parseDict2(d []byte) map[int][]float64 {
	out := map[int][]float64{}
	var operands []float64
	for i := 0; i < len(d); {
		b0 := int(d[i])
		switch {
		case b0 <= 24 && b0 != 23 || b0 == 12:
			op := b0
			i++
			if b0 == 12 {
				op = 1200 + int(d[i])
				i++
			}
			out[op] = operands
			operands = nil
		case b0 == 23: // blend (only in hint values, which are dropped)
			i++
			if len(operands) > 0 {
				operands = operands[:len(operands)-1]
			}
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

type cff2Font struct {
	b         []byte
	gsubrs    index2
	charStr   index2
	lsubrs    []index2 // per font DICT (count 0 when absent)
	vsindex   []int    // per font DICT
	fdSelect  func(g int) int
	store     *itemVarStore
	storeOff  int
	scalarsOf map[int][]float64
}

// regionScalars returns the scalars of the regions of an ItemVariationData.
func (c *cff2Font) regionScalars(vs int) []float64 {
	if s, ok := c.scalarsOf[vs]; ok {
		return s
	}
	var out []float64
	if c.store != nil {
		d := c.store.d
		o := c.store.off
		if vs < int(u16(d, o+6)) {
			ivd := o + int(u32(d, o+8+4*vs))
			nr := int(u16(d, ivd+4))
			for k := 0; k < nr; k++ {
				out = append(out, c.store.regionScalar(int(u16(d, ivd+6+2*k))))
			}
		}
	}
	c.scalarsOf[vs] = out
	return out
}

func parseCFF2(b []byte, coords []float64) (*cff2Font, error) {
	if len(b) < 5 || b[0] != 2 {
		return nil, errors.New("fonts: not a CFF2 table")
	}
	hdr := int(b[2])
	topLen := int(u16(b, 3))
	top := parseDict2(b[hdr : hdr+topLen])
	c := &cff2Font{b: b, scalarsOf: map[int][]float64{}}
	c.gsubrs = readIndex2(b, hdr+topLen)
	cs, ok := top[17]
	if !ok || len(cs) == 0 {
		return nil, errors.New("fonts: CFF2 without CharStrings")
	}
	c.charStr = readIndex2(b, int(cs[0]))
	if vs, ok := top[24]; ok && len(vs) > 0 {
		c.storeOff = int(vs[0]) + 2
		c.store = newItemVarStore(b, c.storeOff, coords)
	}
	fda, ok := top[1236]
	if !ok || len(fda) == 0 {
		return nil, errors.New("fonts: CFF2 without FDArray")
	}
	fdArray := readIndex2(b, int(fda[0]))
	for i := 0; i < fdArray.count; i++ {
		fd := parseDict2(fdArray.item(i))
		var subrs index2
		vsi := 0
		if priv := fd[18]; len(priv) >= 2 {
			size, po := int(priv[0]), int(priv[1])
			pd := parseDict2(b[po : po+size])
			if s, ok := pd[19]; ok && len(s) > 0 {
				subrs = readIndex2(b, po+int(s[0]))
			}
			if v, ok := pd[22]; ok && len(v) > 0 {
				vsi = int(v[0])
			}
		}
		c.lsubrs = append(c.lsubrs, subrs)
		c.vsindex = append(c.vsindex, vsi)
	}
	c.fdSelect = func(int) int { return 0 }
	if fds, ok := top[1237]; ok && len(fds) > 0 {
		p := int(fds[0])
		switch b[p] {
		case 0:
			c.fdSelect = func(g int) int { return int(b[p+1+g]) }
		case 3:
			nr := int(u16(b, p+1))
			c.fdSelect = func(g int) int {
				for i := 0; i < nr; i++ {
					r := p + 3 + 3*i
					if g >= int(u16(b, r)) && g < int(u16(b, r+3)) {
						return int(b[r+2])
					}
				}
				return 0
			}
		case 4:
			nr := int(u32(b, p+1))
			c.fdSelect = func(g int) int {
				for i := 0; i < nr; i++ {
					r := p + 5 + 6*i
					if g >= int(u32(b, r)) && g < int(u32(b, r+6)) {
						return int(u16(b, r+4))
					}
				}
				return 0
			}
		}
	}
	return c, nil
}

// pathOp is a drawing operation in absolute coordinates.
type pathOp struct {
	op  byte // 'm', 'l', 'c'
	pts [3][2]float64
}

type cff2Interp struct {
	c       *cff2Font
	lsubrs  index2
	vsindex int
	stack   []float64
	x, y    float64
	nStems  int
	ops     []pathOp
}

func bias(n int) int { return subrBias(n) }

func (t *cff2Interp) moveTo(dx, dy float64) {
	t.x += dx
	t.y += dy
	t.ops = append(t.ops, pathOp{op: 'm', pts: [3][2]float64{{t.x, t.y}}})
}

func (t *cff2Interp) lineTo(dx, dy float64) {
	t.x += dx
	t.y += dy
	t.ops = append(t.ops, pathOp{op: 'l', pts: [3][2]float64{{t.x, t.y}}})
}

func (t *cff2Interp) curveTo(dx1, dy1, dx2, dy2, dx3, dy3 float64) {
	x1, y1 := t.x+dx1, t.y+dy1
	x2, y2 := x1+dx2, y1+dy2
	t.x, t.y = x2+dx3, y2+dy3
	t.ops = append(t.ops, pathOp{op: 'c', pts: [3][2]float64{{x1, y1}, {x2, y2}, {t.x, t.y}}})
}

func (t *cff2Interp) exec(cs []byte, depth int) {
	if depth > 10 {
		return
	}
	for i := 0; i < len(cs); {
		b0 := int(cs[i])
		switch {
		case b0 == 28:
			t.stack = append(t.stack, float64(int16(u16(cs, i+1))))
			i += 3
			continue
		case b0 >= 32 && b0 <= 246:
			t.stack = append(t.stack, float64(b0-139))
			i++
			continue
		case b0 >= 247 && b0 <= 250:
			t.stack = append(t.stack, float64((b0-247)*256+int(cs[i+1])+108))
			i += 2
			continue
		case b0 >= 251 && b0 <= 254:
			t.stack = append(t.stack, float64(-(b0-251)*256-int(cs[i+1])-108))
			i += 2
			continue
		case b0 == 255:
			t.stack = append(t.stack, float64(int32(u32(cs, i+1)))/65536)
			i += 5
			continue
		}
		i++
		s := t.stack
		switch b0 {
		case 1, 3, 18, 23:
			t.nStems += len(s) / 2
			t.stack = s[:0]
		case 19, 20:
			t.nStems += len(s) / 2
			t.stack = s[:0]
			i += (t.nStems + 7) / 8
		case 15: // vsindex
			if len(s) > 0 {
				t.vsindex = int(s[len(s)-1])
			}
			t.stack = s[:0]
		case 16: // blend
			if len(s) == 0 {
				return
			}
			n := int(s[len(s)-1])
			s = s[:len(s)-1]
			sc := t.c.regionScalars(t.vsindex)
			k := len(sc)
			total := n * (k + 1)
			if n < 0 || total > len(s) {
				return
			}
			base := len(s) - total
			for j := 0; j < n; j++ {
				v := s[base+j]
				for r := 0; r < k; r++ {
					v += s[base+n+j*k+r] * sc[r]
				}
				s[base+j] = v
			}
			t.stack = s[:base+n]
		case 21:
			if len(s) >= 2 {
				t.moveTo(s[len(s)-2], s[len(s)-1])
			}
			t.stack = s[:0]
		case 22:
			if len(s) >= 1 {
				t.moveTo(s[len(s)-1], 0)
			}
			t.stack = s[:0]
		case 4:
			if len(s) >= 1 {
				t.moveTo(0, s[len(s)-1])
			}
			t.stack = s[:0]
		case 5:
			for k := 0; k+1 < len(s); k += 2 {
				t.lineTo(s[k], s[k+1])
			}
			t.stack = s[:0]
		case 6, 7:
			h := b0 == 6
			for k := 0; k < len(s); k++ {
				if h {
					t.lineTo(s[k], 0)
				} else {
					t.lineTo(0, s[k])
				}
				h = !h
			}
			t.stack = s[:0]
		case 8:
			for k := 0; k+5 < len(s); k += 6 {
				t.curveTo(s[k], s[k+1], s[k+2], s[k+3], s[k+4], s[k+5])
			}
			t.stack = s[:0]
		case 24:
			k := 0
			for ; k+5 < len(s)-2; k += 6 {
				t.curveTo(s[k], s[k+1], s[k+2], s[k+3], s[k+4], s[k+5])
			}
			if k+1 < len(s) {
				t.lineTo(s[k], s[k+1])
			}
			t.stack = s[:0]
		case 25:
			k := 0
			for ; k+1 < len(s)-6; k += 2 {
				t.lineTo(s[k], s[k+1])
			}
			if k+5 < len(s) {
				t.curveTo(s[k], s[k+1], s[k+2], s[k+3], s[k+4], s[k+5])
			}
			t.stack = s[:0]
		case 26:
			k, dx1 := 0, 0.0
			if len(s)%2 == 1 {
				dx1, k = s[0], 1
			}
			for ; k+3 < len(s); k += 4 {
				t.curveTo(dx1, s[k], s[k+1], s[k+2], 0, s[k+3])
				dx1 = 0
			}
			t.stack = s[:0]
		case 27:
			k, dy1 := 0, 0.0
			if len(s)%2 == 1 {
				dy1, k = s[0], 1
			}
			for ; k+3 < len(s); k += 4 {
				t.curveTo(s[k], dy1, s[k+1], s[k+2], s[k+3], 0)
				dy1 = 0
			}
			t.stack = s[:0]
		case 30, 31:
			h := b0 == 31
			for k := 0; k+3 < len(s); k += 4 {
				last := 0.0
				if k+5 == len(s) {
					last = s[k+4]
				}
				if h {
					t.curveTo(s[k], 0, s[k+1], s[k+2], last, s[k+3])
				} else {
					t.curveTo(0, s[k], s[k+1], s[k+2], s[k+3], last)
				}
				h = !h
			}
			t.stack = s[:0]
		case 10, 29:
			if len(s) == 0 {
				return
			}
			n := int(s[len(s)-1])
			t.stack = s[:len(s)-1]
			ix := t.c.gsubrs
			if b0 == 10 {
				ix = t.lsubrs
			}
			sub := ix.item(n + bias(ix.count))
			if sub == nil {
				return
			}
			t.exec(sub, depth+1)
		case 12:
			op := int(cs[i])
			i++
			switch op {
			case 35:
				if len(s) >= 13 {
					t.curveTo(s[0], s[1], s[2], s[3], s[4], s[5])
					t.curveTo(s[6], s[7], s[8], s[9], s[10], s[11])
				}
			case 34:
				if len(s) >= 7 {
					t.curveTo(s[0], 0, s[1], s[2], s[3], 0)
					t.curveTo(s[4], 0, s[5], -s[2], s[6], 0)
				}
			case 36:
				if len(s) >= 9 {
					t.curveTo(s[0], s[1], s[2], s[3], s[4], 0)
					t.curveTo(s[5], 0, s[6], s[7], s[8], -(s[1] + s[3] + s[7]))
				}
			case 37:
				if len(s) >= 11 {
					dx := s[0] + s[2] + s[4] + s[6] + s[8]
					dy := s[1] + s[3] + s[5] + s[7] + s[9]
					t.curveTo(s[0], s[1], s[2], s[3], s[4], s[5])
					if math.Abs(dx) > math.Abs(dy) {
						t.curveTo(s[6], s[7], s[8], s[9], s[10], -dy)
					} else {
						t.curveTo(s[6], s[7], s[8], s[9], -dx, s[10])
					}
				}
			}
			t.stack = t.stack[:0]
		default:
			t.stack = s[:0]
		}
	}
}

// t2num appends a Type 2 charstring number.
func t2num(b *bytes.Buffer, v float64) {
	if v == math.Trunc(v) && v >= -32768 && v <= 32767 {
		n := int(v)
		switch {
		case n >= -107 && n <= 107:
			b.WriteByte(byte(n + 139))
		case n >= 108 && n <= 1131:
			n -= 108
			b.WriteByte(byte(n/256 + 247))
			b.WriteByte(byte(n % 256))
		case n >= -1131 && n <= -108:
			n = -n - 108
			b.WriteByte(byte(n/256 + 251))
			b.WriteByte(byte(n % 256))
		default:
			b.WriteByte(28)
			binary.Write(b, binary.BigEndian, int16(n))
		}
		return
	}
	b.WriteByte(255)
	binary.Write(b, binary.BigEndian, int32(math.Round(v*65536)))
}

// encodeT2 encodes a path as a Type 2 charstring with a width.
func encodeT2(width float64, ops []pathOp) []byte {
	var b bytes.Buffer
	t2num(&b, width)
	first := true
	x, y := 0.0, 0.0
	for _, o := range ops {
		switch o.op {
		case 'm':
			p := o.pts[0]
			px, py := otRound(p[0]), otRound(p[1])
			t2num(&b, px-x)
			t2num(&b, py-y)
			b.WriteByte(21)
			x, y = px, py
		case 'l':
			if first {
				t2num(&b, 0)
				t2num(&b, 0)
				b.WriteByte(21)
			}
			p := o.pts[0]
			px, py := otRound(p[0]), otRound(p[1])
			t2num(&b, px-x)
			t2num(&b, py-y)
			b.WriteByte(5)
			x, y = px, py
		case 'c':
			if first {
				t2num(&b, 0)
				t2num(&b, 0)
				b.WriteByte(21)
			}
			for _, p := range o.pts {
				px, py := otRound(p[0]), otRound(p[1])
				t2num(&b, px-x)
				t2num(&b, py-y)
				x, y = px, py
			}
			b.WriteByte(8)
		}
		first = false
	}
	b.WriteByte(14)
	return b.Bytes()
}

// cffIndex encodes a CFF INDEX.
func makeCFFIndex(items [][]byte) []byte {
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
	for total >= 1<<(8*offSize) {
		offSize++
	}
	b.WriteByte(byte(offSize))
	off := 1
	writeOff := func(v int) {
		for k := offSize - 1; k >= 0; k-- {
			b.WriteByte(byte(v >> (8 * k)))
		}
	}
	writeOff(off)
	for _, it := range items {
		off += len(it)
		writeOff(off)
	}
	for _, it := range items {
		b.Write(it)
	}
	return b.Bytes()
}

func dictInt(b *bytes.Buffer, v int) {
	b.WriteByte(29)
	binary.Write(b, binary.BigEndian, int32(v))
}

func dictReal(b *bytes.Buffer, v float64) {
	s := []byte(trimFloat(v))
	b.WriteByte(30)
	var nib []byte
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c >= '0' && c <= '9':
			nib = append(nib, c-'0')
		case c == '.':
			nib = append(nib, 0xa)
		case c == '-':
			nib = append(nib, 0xe)
		case c == 'e' || c == 'E':
			if i+1 < len(s) && s[i+1] == '-' {
				nib = append(nib, 0xc)
				i++
			} else {
				if i+1 < len(s) && s[i+1] == '+' {
					i++
				}
				nib = append(nib, 0xb)
			}
		}
	}
	nib = append(nib, 0xf)
	if len(nib)%2 == 1 {
		nib = append(nib, 0xf)
	}
	for i := 0; i < len(nib); i += 2 {
		b.WriteByte(nib[i]<<4 | nib[i+1])
	}
}

func trimFloat(v float64) string { return strconv.FormatFloat(v, 'g', -1, 64) }

// cff2ToCFF converts a CFF2 table to a CID-keyed CFF table for an
// instance (advances from the instance's hmtx).
func cff2ToCFF(f *Font, cff2 []byte, coords []float64, adv []float64) (out []byte, err error) {
	defer func() {
		if r := recover(); r != nil {
			out, err = nil, errors.New("fonts: broken CFF2 table")
		}
	}()
	c, err := parseCFF2(cff2, coords)
	if err != nil {
		return nil, err
	}
	n := c.charStr.count
	charStrings := make([][]byte, n)
	for g := 0; g < n; g++ {
		fd := c.fdSelect(g)
		in := &cff2Interp{c: c}
		if fd < len(c.lsubrs) {
			in.lsubrs = c.lsubrs[fd]
			in.vsindex = c.vsindex[fd]
		}
		in.exec(c.charStr.item(g), 0)
		w := 0.0
		if g < len(adv) {
			w = otRound(adv[g])
		}
		charStrings[g] = encodeT2(w, in.ops)
	}
	name := f.nameString(6)
	if name == "" {
		name = "Instance"
	}
	// Private DICT: default and nominal widths 0.
	var priv bytes.Buffer
	dictInt(&priv, 0)
	priv.WriteByte(20)
	dictInt(&priv, 0)
	priv.WriteByte(21)
	upem := f.UnitsPerEm
	build := func(charsetOff, fdSelectOff, charStringsOff, fdArrayOff int) []byte {
		var top bytes.Buffer
		dictInt(&top, 391) // "Adobe"
		dictInt(&top, 392) // "Identity"
		dictInt(&top, 0)
		top.Write([]byte{12, 30})
		if upem != 1000 {
			dictReal(&top, 1/upem)
			dictInt(&top, 0)
			dictInt(&top, 0)
			dictReal(&top, 1/upem)
			dictInt(&top, 0)
			dictInt(&top, 0)
			top.Write([]byte{12, 7})
		}
		dictInt(&top, n)
		top.Write([]byte{12, 34})
		dictInt(&top, charsetOff)
		top.WriteByte(15)
		dictInt(&top, charStringsOff)
		top.WriteByte(17)
		dictInt(&top, fdArrayOff)
		top.Write([]byte{12, 36})
		dictInt(&top, fdSelectOff)
		top.Write([]byte{12, 37})
		return top.Bytes()
	}
	header := []byte{1, 0, 4, 4}
	nameIndex := makeCFFIndex([][]byte{[]byte(name)})
	stringIndex := makeCFFIndex([][]byte{[]byte("Adobe"), []byte("Identity")})
	gsubrIndex := makeCFFIndex(nil)
	var charset bytes.Buffer
	if n > 1 {
		charset.WriteByte(2)
		binary.Write(&charset, binary.BigEndian, uint16(1))
		binary.Write(&charset, binary.BigEndian, uint16(n-2))
	} else {
		charset.WriteByte(0)
	}
	var fdSelect bytes.Buffer
	fdSelect.WriteByte(3)
	binary.Write(&fdSelect, binary.BigEndian, uint16(1))
	binary.Write(&fdSelect, binary.BigEndian, uint16(0))
	fdSelect.WriteByte(0)
	binary.Write(&fdSelect, binary.BigEndian, uint16(n))
	csIndex := makeCFFIndex(charStrings)
	// The Top DICT has a fixed size (5-byte integers).
	topLen := len(makeCFFIndex([][]byte{build(0, 0, 0, 0)}))
	base := len(header) + len(nameIndex) + topLen + len(stringIndex) + len(gsubrIndex)
	charsetOff := base
	fdSelectOff := charsetOff + charset.Len()
	csOff := fdSelectOff + fdSelect.Len()
	fdArrayOff := csOff + len(csIndex)
	var fdDict bytes.Buffer
	privOff := fdArrayOff + len(makeCFFIndex([][]byte{make([]byte, 11)}))
	dictInt(&fdDict, priv.Len())
	dictInt(&fdDict, privOff)
	fdDict.WriteByte(18)
	fdArray := makeCFFIndex([][]byte{fdDict.Bytes()})
	var b bytes.Buffer
	b.Write(header)
	b.Write(nameIndex)
	b.Write(makeCFFIndex([][]byte{build(charsetOff, fdSelectOff, csOff, fdArrayOff)}))
	b.Write(stringIndex)
	b.Write(gsubrIndex)
	b.Write(charset.Bytes())
	b.Write(fdSelect.Bytes())
	b.Write(csIndex)
	b.Write(fdArray)
	b.Write(priv.Bytes())
	return b.Bytes(), nil
}

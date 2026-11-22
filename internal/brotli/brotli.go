// Package brotli decodes Brotli compressed data (RFC 7932), as used by
// WOFF2 fonts.
package brotli

import (
	_ "embed"
	"errors"
)

//go:embed dictionary.bin
var dictionary []byte

// ErrFormat is returned for invalid Brotli streams.
var ErrFormat = errors.New("brotli: invalid data")

var (
	dictNBits = [25]uint{0, 0, 0, 0, 10, 10, 11, 11, 10, 10, 10, 10, 10, 9, 9, 8, 7, 7, 8, 7, 7, 6, 6, 5, 5}
	dictOff   [25]int
)

func init() {
	o := 0
	for l := 4; l <= 24; l++ {
		dictOff[l] = o
		o += l << dictNBits[l]
	}
}

// ---------------------------------------------------------------- bits

type bitReader struct {
	data []byte
	pos  int // byte position
	bit  uint
	err  bool
}

func (br *bitReader) read(n uint) uint32 {
	var v uint32
	for i := uint(0); i < n; i++ {
		if br.pos >= len(br.data) {
			br.err = true
			return 0
		}
		v |= uint32(br.data[br.pos]>>br.bit&1) << i
		br.bit++
		if br.bit == 8 {
			br.bit = 0
			br.pos++
		}
	}
	return v
}

func (br *bitReader) align() bool {
	if br.bit != 0 {
		pad := br.read(8 - br.bit)
		return pad == 0
	}
	return true
}

// ---------------------------------------------------------------- prefix codes

// huffman is a canonical prefix code decoded bit by bit.
type huffman struct {
	count  [16]uint16
	symbol []uint16
	single int // symbol of a one-symbol code, or -1
}

func newHuffman(lengths []uint8) *huffman {
	h := &huffman{single: -1}
	n := 0
	last := 0
	for s, l := range lengths {
		if l > 0 {
			h.count[l]++
			n++
			last = s
		}
	}
	if n == 1 {
		h.single = last
		return h
	}
	var offs [16]uint16
	for l := 2; l < 16; l++ {
		offs[l] = offs[l-1] + h.count[l-1]
	}
	h.symbol = make([]uint16, n)
	for s, l := range lengths {
		if l > 0 {
			h.symbol[offs[l]] = uint16(s)
			offs[l]++
		}
	}
	return h
}

func (h *huffman) decode(br *bitReader) int {
	if h.single >= 0 {
		return h.single
	}
	code, first, index := 0, 0, 0
	for l := 1; l < 16; l++ {
		code |= int(br.read(1))
		count := int(h.count[l])
		if code-count < first {
			return int(h.symbol[index+(code-first)])
		}
		index += count
		first += count
		first <<= 1
		code <<= 1
		if br.err {
			return 0
		}
	}
	br.err = true
	return 0
}

var codeLengthOrder = [18]int{1, 2, 3, 4, 0, 5, 17, 6, 16, 7, 8, 9, 10, 11, 12, 13, 14, 15}

// readPrefixCode reads a prefix code over an alphabet (RFC 7932 §3.4–3.5).
func readPrefixCode(br *bitReader, alphabet int) (*huffman, error) {
	lengths := make([]uint8, alphabet)
	hskip := br.read(2)
	if hskip == 1 {
		// Simple prefix code.
		nsym := int(br.read(2)) + 1
		bits := uint(0)
		for (1 << bits) < alphabet {
			bits++
		}
		syms := make([]int, nsym)
		for i := range syms {
			syms[i] = int(br.read(bits))
			if syms[i] >= alphabet {
				return nil, ErrFormat
			}
			for j := 0; j < i; j++ {
				if syms[j] == syms[i] {
					return nil, ErrFormat
				}
			}
		}
		switch nsym {
		case 1:
			return &huffman{single: syms[0]}, nil
		case 2:
			lengths[syms[0]], lengths[syms[1]] = 1, 1
		case 3:
			lengths[syms[0]], lengths[syms[1]], lengths[syms[2]] = 1, 2, 2
		case 4:
			if br.read(1) == 0 {
				for _, s := range syms {
					lengths[s] = 2
				}
			} else {
				lengths[syms[0]], lengths[syms[1]], lengths[syms[2]], lengths[syms[3]] = 1, 2, 3, 3
			}
		}
		return newHuffman(lengths), nil
	}
	// Complex prefix code: first the code length code lengths.
	var clLengths [18]uint8
	space := 32
	num := 0
	for i := int(hskip); i < 18; i++ {
		// Static code: 0:00 1:0111 2:011 3:10 4:01 5:1111 (bit order of
		// the stream).
		var v uint8
		switch br.read(2) {
		case 0:
			v = 0
		case 1:
			v = 4
		case 2:
			v = 3
		case 3: // bits "11..."
			if br.read(1) == 0 {
				v = 2
			} else if br.read(1) == 0 {
				v = 1
			} else {
				v = 5
			}
		}
		clLengths[codeLengthOrder[i]] = v
		if v != 0 {
			space -= 32 >> v
			num++
			if space <= 0 {
				break
			}
		}
	}
	if br.err || (num != 1 && space != 0) {
		return nil, ErrFormat
	}
	clCode := newHuffman(clLengths[:])
	symbol := 0
	prevLen := uint8(8)
	repeat := 0
	repeatLen := uint8(0)
	space = 32768
	for symbol < alphabet && space > 0 {
		c := clCode.decode(br)
		if br.err {
			return nil, ErrFormat
		}
		if c < 16 {
			repeat = 0
			lengths[symbol] = uint8(c)
			if c != 0 {
				prevLen = uint8(c)
				space -= 32768 >> c
			}
			symbol++
			continue
		}
		extra := uint(2)
		newLen := prevLen
		if c == 17 {
			extra, newLen = 3, 0
		}
		if repeatLen != newLen {
			repeat = 0
			repeatLen = newLen
		}
		old := repeat
		if repeat > 0 {
			repeat = (repeat - 2) << extra
		}
		repeat += int(br.read(extra)) + 3
		delta := repeat - old
		if symbol+delta > alphabet {
			return nil, ErrFormat
		}
		for k := 0; k < delta; k++ {
			lengths[symbol+k] = repeatLen
		}
		symbol += delta
		if repeatLen != 0 {
			space -= delta << (15 - repeatLen)
		}
	}
	if space != 0 || br.err {
		return nil, ErrFormat
	}
	return newHuffman(lengths), nil
}

// ---------------------------------------------------------------- tables

var blockLenBase = [26]int{1, 5, 9, 13, 17, 25, 33, 41, 49, 65, 81, 97, 113, 145, 177, 209, 241, 305, 369, 497, 753, 1265, 2289, 4337, 8433, 16625}
var blockLenExtra = [26]uint{2, 2, 2, 2, 3, 3, 3, 3, 4, 4, 4, 4, 5, 5, 5, 5, 6, 6, 7, 8, 9, 10, 11, 12, 13, 24}

var insBase = [24]int{0, 1, 2, 3, 4, 5, 6, 8, 10, 14, 18, 26, 34, 50, 66, 98, 130, 194, 322, 578, 1090, 2114, 6210, 22594}
var insExtra = [24]uint{0, 0, 0, 0, 0, 0, 1, 1, 2, 2, 3, 3, 4, 4, 5, 5, 6, 7, 8, 9, 10, 12, 14, 24}
var copyBase = [24]int{2, 3, 4, 5, 6, 7, 8, 9, 10, 12, 14, 18, 22, 30, 38, 54, 70, 102, 134, 198, 326, 582, 1094, 2118}
var copyExtra = [24]uint{0, 0, 0, 0, 0, 0, 0, 0, 1, 1, 2, 2, 3, 3, 4, 4, 5, 5, 6, 7, 8, 9, 10, 24}

// Insert and copy code ranges of the 64-symbol cells of the command
// alphabet.
var cellInsert = [11]int{0, 0, 0, 0, 8, 8, 0, 16, 8, 16, 16}
var cellCopy = [11]int{0, 8, 0, 8, 0, 8, 16, 0, 16, 8, 16}

// ---------------------------------------------------------------- decoder

func readVarLen8(br *bitReader) int {
	if br.read(1) == 0 {
		return 1
	}
	n := uint(br.read(3))
	if n == 0 {
		return 2
	}
	return (1 << n) + int(br.read(n)) + 1
}

type blockCategory struct {
	n        int // number of block types
	types    *huffman
	lens     *huffman
	cur      int
	prev     int
	remain   int
	disabled bool
}

func (bc *blockCategory) read(br *bitReader, max int) error {
	bc.n = readVarLen8(br)
	bc.cur, bc.prev = 0, 1
	if bc.n < 2 {
		bc.remain = 1 << 30
		bc.disabled = true
		return nil
	}
	var err error
	if bc.types, err = readPrefixCode(br, bc.n+2); err != nil {
		return err
	}
	if bc.lens, err = readPrefixCode(br, 26); err != nil {
		return err
	}
	bc.remain = readBlockLen(br, bc.lens)
	return nil
}

func readBlockLen(br *bitReader, h *huffman) int {
	s := h.decode(br)
	return blockLenBase[s] + int(br.read(blockLenExtra[s]))
}

func (bc *blockCategory) next(br *bitReader) {
	if bc.disabled {
		return
	}
	s := bc.types.decode(br)
	var t int
	switch s {
	case 0:
		t = bc.prev
	case 1:
		t = bc.cur + 1
	default:
		t = s - 2
	}
	if t >= bc.n {
		t -= bc.n
	}
	bc.prev, bc.cur = bc.cur, t
	bc.remain = readBlockLen(br, bc.lens)
}

func readContextMap(br *bitReader, size int) ([]byte, int, error) {
	ntrees := readVarLen8(br)
	m := make([]byte, size)
	if ntrees < 2 {
		return m, ntrees, nil
	}
	rle := 0
	if br.read(1) == 1 {
		rle = int(br.read(4)) + 1
	}
	h, err := readPrefixCode(br, ntrees+rle)
	if err != nil {
		return nil, 0, err
	}
	for i := 0; i < size; {
		s := h.decode(br)
		if br.err {
			return nil, 0, ErrFormat
		}
		switch {
		case s == 0:
			m[i] = 0
			i++
		case s <= rle:
			n := (1 << uint(s)) + int(br.read(uint(s)))
			if i+n > size {
				return nil, 0, ErrFormat
			}
			i += n // zeros
		default:
			m[i] = byte(s - rle)
			i++
		}
	}
	if br.read(1) == 1 {
		// Inverse move-to-front transform.
		var mtf [256]byte
		for i := range mtf {
			mtf[i] = byte(i)
		}
		for i, v := range m {
			idx := int(v)
			val := mtf[idx]
			m[i] = val
			copy(mtf[1:idx+1], mtf[:idx])
			mtf[0] = val
		}
	}
	for _, v := range m {
		if int(v) >= ntrees {
			return nil, 0, ErrFormat
		}
	}
	return m, ntrees, nil
}

// Decode decompresses a complete Brotli stream.
func Decode(data []byte) ([]byte, error) {
	return DecodeSize(data, 0)
}

// DecodeSize decompresses a Brotli stream, with a hint of the output size.
func DecodeSize(data []byte, sizeHint int) ([]byte, error) {
	br := &bitReader{data: data}
	// Window size.
	var wbits uint
	if br.read(1) == 0 {
		wbits = 16
	} else if n := br.read(3); n != 0 {
		wbits = 17 + uint(n)
	} else if n := br.read(3); n != 0 {
		if n == 1 {
			return nil, ErrFormat // large window extension
		}
		wbits = 8 + uint(n)
	} else {
		wbits = 17
	}
	maxBackward := (1 << wbits) - 16
	out := make([]byte, 0, sizeHint)
	dist := [4]int{16, 15, 11, 4} // ring buffer, dist[idx&3] is the last
	distIdx := 3
	for {
		if br.err {
			return nil, ErrFormat
		}
		isLast := br.read(1) == 1
		if isLast && br.read(1) == 1 {
			break
		}
		nibbles := br.read(2)
		if nibbles == 3 {
			// Metadata block.
			if br.read(1) != 0 {
				return nil, ErrFormat
			}
			skipBytes := br.read(2)
			skip := 0
			if skipBytes > 0 {
				skip = int(br.read(8*uint(skipBytes))) + 1
			}
			if !br.align() {
				return nil, ErrFormat
			}
			br.pos += skip
			if br.pos > len(br.data) {
				return nil, ErrFormat
			}
			if isLast {
				break
			}
			continue
		}
		mlen := int(br.read(4*(uint(nibbles)+4))) + 1
		if !isLast && br.read(1) == 1 {
			// Uncompressed block.
			if !br.align() || br.pos+mlen > len(br.data) {
				return nil, ErrFormat
			}
			out = append(out, br.data[br.pos:br.pos+mlen]...)
			br.pos += mlen
			continue
		}
		var cats [3]blockCategory // literals, commands, distances
		for i := range cats {
			if err := cats[i].read(br, 0); err != nil {
				return nil, err
			}
		}
		npostfix := uint(br.read(2))
		ndirect := int(br.read(4)) << npostfix
		cmodes := make([]byte, cats[0].n)
		for i := range cmodes {
			cmodes[i] = byte(br.read(2))
		}
		litMap, nLitTrees, err := readContextMap(br, 64*cats[0].n)
		if err != nil {
			return nil, err
		}
		distMap, nDistTrees, err := readContextMap(br, 4*cats[2].n)
		if err != nil {
			return nil, err
		}
		litTrees := make([]*huffman, nLitTrees)
		for i := range litTrees {
			if litTrees[i], err = readPrefixCode(br, 256); err != nil {
				return nil, err
			}
		}
		cmdTrees := make([]*huffman, cats[1].n)
		for i := range cmdTrees {
			if cmdTrees[i], err = readPrefixCode(br, 704); err != nil {
				return nil, err
			}
		}
		distAlphabet := 16 + ndirect + (48 << npostfix)
		distTrees := make([]*huffman, nDistTrees)
		for i := range distTrees {
			if distTrees[i], err = readPrefixCode(br, distAlphabet); err != nil {
				return nil, err
			}
		}
		end := len(out) + mlen
		for len(out) < end {
			if br.err {
				return nil, ErrFormat
			}
			// Command.
			if cats[1].remain == 0 {
				cats[1].next(br)
			}
			cats[1].remain--
			cmd := cmdTrees[cats[1].cur].decode(br)
			cell := cmd >> 6
			insCode := cellInsert[cell] + (cmd>>3)&7
			copyCode := cellCopy[cell] + cmd&7
			insLen := insBase[insCode] + int(br.read(insExtra[insCode]))
			copyLen := copyBase[copyCode] + int(br.read(copyExtra[copyCode]))
			// Literals.
			for k := 0; k < insLen; k++ {
				if cats[0].remain == 0 {
					cats[0].next(br)
				}
				cats[0].remain--
				var p1, p2 byte
				if n := len(out); n > 0 {
					p1 = out[n-1]
					if n > 1 {
						p2 = out[n-2]
					}
				}
				mode := int(cmodes[cats[0].cur])
				ctx := contextLUT[mode*512+int(p1)] | contextLUT[mode*512+256+int(p2)]
				tree := litTrees[litMap[64*cats[0].cur+int(ctx)]]
				out = append(out, byte(tree.decode(br)))
				if br.err {
					return nil, ErrFormat
				}
			}
			if len(out) >= end {
				if len(out) > end {
					return nil, ErrFormat
				}
				break
			}
			// Distance.
			var distance int
			if cmd < 128 {
				distance = dist[distIdx&3]
			} else {
				if cats[2].remain == 0 {
					cats[2].next(br)
				}
				cats[2].remain--
				dctx := copyLen - 2
				if dctx > 3 {
					dctx = 3
				}
				dcode := distTrees[distMap[4*cats[2].cur+dctx]].decode(br)
				push := true
				switch {
				case dcode < 16:
					last, second := dist[distIdx&3], dist[(distIdx-1)&3]
					switch dcode {
					case 0:
						distance = last
						push = false
					case 1:
						distance = second
					case 2:
						distance = dist[(distIdx-2)&3]
					case 3:
						distance = dist[(distIdx-3)&3]
					default:
						base := last
						if dcode >= 10 {
							base = second
						}
						d := [16]int{4: -1, 5: 1, 6: -2, 7: 2, 8: -3, 9: 3, 10: -1, 11: 1, 12: -2, 13: 2, 14: -3, 15: 3}[dcode]
						distance = base + d
					}
					if distance <= 0 {
						return nil, ErrFormat
					}
				case dcode < 16+ndirect:
					distance = dcode - 15
				default:
					x := dcode - ndirect - 16
					ndistbits := uint(1 + (x >> (npostfix + 1)))
					hcode := x >> npostfix
					lcode := x & (1<<npostfix - 1)
					offset := ((2 + (hcode & 1)) << ndistbits) - 4
					distance = ((offset + int(br.read(ndistbits))) << npostfix) + lcode + ndirect + 1
				}
				maxDist := len(out)
				if maxDist > maxBackward {
					maxDist = maxBackward
				}
				if push && distance <= maxDist {
					distIdx++
					dist[distIdx&3] = distance
				}
			}
			maxDist := len(out)
			if maxDist > maxBackward {
				maxDist = maxBackward
			}
			if distance > maxDist {
				// Static dictionary reference.
				if copyLen < 4 || copyLen > 24 {
					return nil, ErrFormat
				}
				wordID := distance - maxDist - 1
				nbits := dictNBits[copyLen]
				idx := wordID & (1<<nbits - 1)
				tid := wordID >> nbits
				if tid >= 121 {
					return nil, ErrFormat
				}
				w := dictionary[dictOff[copyLen]+idx*copyLen : dictOff[copyLen]+(idx+1)*copyLen]
				out = transformWord(out, w, tid)
				if len(out) > end {
					return nil, ErrFormat
				}
				continue
			}
			if len(out)+copyLen > end {
				return nil, ErrFormat
			}
			src := len(out) - distance
			for k := 0; k < copyLen; k++ {
				out = append(out, out[src+k])
			}
		}
		if isLast {
			break
		}
	}
	if br.err {
		return nil, ErrFormat
	}
	return out, nil
}

func prefixSuffixString(id byte) []byte {
	o := int(prefixSuffixMap[id])
	n := int(prefixSuffix[o])
	return prefixSuffix[o+1 : o+1+n]
}

// transformWord appends a dictionary word transformed by a transform.
func transformWord(out []byte, w []byte, tid int) []byte {
	pre, typ, suf := transformsData[3*tid], int(transformsData[3*tid+1]), transformsData[3*tid+2]
	out = append(out, prefixSuffixString(pre)...)
	switch {
	case typ >= 1 && typ <= 9:
		if typ > len(w) {
			w = nil
		} else {
			w = w[:len(w)-typ]
		}
	case typ >= 12 && typ <= 20:
		n := typ - 11
		if n > len(w) {
			w = nil
		} else {
			w = w[n:]
		}
	}
	start := len(out)
	out = append(out, w...)
	word := out[start:]
	switch typ {
	case 10:
		toUpper(word)
	case 11:
		for len(word) > 0 {
			word = word[toUpper(word):]
		}
	}
	return append(out, prefixSuffixString(suf)...)
}

// toUpper uppercases the first character of p like the reference decoder
// and returns its length.
func toUpper(p []byte) int {
	if p[0] < 0xC0 {
		if p[0] >= 'a' && p[0] <= 'z' {
			p[0] ^= 32
		}
		return 1
	}
	if p[0] < 0xE0 {
		if len(p) > 1 {
			p[1] ^= 32
		}
		return min(2, len(p))
	}
	if len(p) > 2 {
		p[2] ^= 5
	}
	return min(3, len(p))
}

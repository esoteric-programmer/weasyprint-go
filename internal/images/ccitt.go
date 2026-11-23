package images

import (
	"errors"
	"strings"
)

// CCITT fax decoding for TIFF (Modified Huffman, Group 3 and Group 4).

type faxCode struct {
	bits string
	run  int
}

var whiteCodes = parseFaxCodes(`
00110101 0 000111 1 0111 2 1000 3 1011 4 1100 5 1110 6 1111 7 10011 8 10100 9
00111 10 01000 11 001000 12 000011 13 110100 14 110101 15 101010 16 101011 17
0100111 18 0001100 19 0001000 20 0010111 21 0000011 22 0000100 23 0101000 24
0101011 25 0010011 26 0100100 27 0011000 28 00000010 29 00000011 30 00011010 31
00011011 32 00010010 33 00010011 34 00010100 35 00010101 36 00010110 37
00010111 38 00101000 39 00101001 40 00101010 41 00101011 42 00101100 43
00101101 44 00000100 45 00000101 46 00001010 47 00001011 48 01010010 49
01010011 50 01010100 51 01010101 52 00100100 53 00100101 54 01011000 55
01011001 56 01011010 57 01011011 58 01001010 59 01001011 60 00110010 61
00110011 62 00110100 63
11011 64 10010 128 010111 192 0110111 256 00110110 320 00110111 384
01100100 448 01100101 512 01101000 576 01100111 640 011001100 704
011001101 768 011010010 832 011010011 896 011010100 960 011010101 1024
011010110 1088 011010111 1152 011011000 1216 011011001 1280 011011010 1344
011011011 1408 010011000 1472 010011001 1536 010011010 1600 011000 1664
010011011 1728`)

var blackCodes = parseFaxCodes(`
0000110111 0 010 1 11 2 10 3 011 4 0011 5 0010 6 00011 7 000101 8 000100 9
0000100 10 0000101 11 0000111 12 00000100 13 00000111 14 000011000 15
0000010111 16 0000011000 17 0000001000 18 00001100111 19 00001101000 20
00001101100 21 00000110111 22 00000101000 23 00000010111 24 00000011000 25
000011001010 26 000011001011 27 000011001100 28 000011001101 29
000001101000 30 000001101001 31 000001101010 32 000001101011 33
000011010010 34 000011010011 35 000011010100 36 000011010101 37
000011010110 38 000011010111 39 000001101100 40 000001101101 41
000011011010 42 000011011011 43 000001010100 44 000001010101 45
000001010110 46 000001010111 47 000001100100 48 000001100101 49
000001010010 50 000001010011 51 000000100100 52 000000110111 53
000000111000 54 000000100111 55 000000101000 56 000001011000 57
000001011001 58 000000101011 59 000000101100 60 000001011010 61
000001100110 62 000001100111 63
0000001111 64 000011001000 128 000011001001 192 000001011011 256
000000110011 320 000000110100 384 000000110101 448 0000001101100 512
0000001101101 576 0000001001010 640 0000001001011 704 0000001001100 768
0000001001101 832 0000001110010 896 0000001110011 960 0000001110100 1024
0000001110101 1088 0000001110110 1152 0000001110111 1216 0000001010010 1280
0000001010011 1344 0000001010100 1408 0000001010101 1472 0000001011010 1536
0000001011011 1600 0000001100100 1664 0000001100101 1728`)

var extendedCodes = parseFaxCodes(`
00000001000 1792 00000001100 1856 00000001101 1920 000000010010 1984
000000010011 2048 000000010100 2112 000000010101 2176 000000010110 2240
000000010111 2304 000000011100 2368 000000011101 2432 000000011110 2496
000000011111 2560`)

// faxTable maps (length << 16 | code) to run lengths.
type faxTable map[uint32]int

func parseFaxCodes(s string) []faxCode {
	f := strings.Fields(s)
	var out []faxCode
	for i := 0; i+1 < len(f); i += 2 {
		run := 0
		for _, c := range f[i+1] {
			run = run*10 + int(c-'0')
		}
		out = append(out, faxCode{f[i], run})
	}
	return out
}

func makeFaxTable(sets ...[]faxCode) faxTable {
	t := faxTable{}
	for _, set := range sets {
		for _, c := range set {
			code := uint32(0)
			for _, b := range c.bits {
				code = code<<1 | uint32(b-'0')
			}
			t[uint32(len(c.bits))<<16|code] = c.run
		}
	}
	return t
}

var (
	whiteTable = makeFaxTable(whiteCodes, extendedCodes)
	blackTable = makeFaxTable(blackCodes, extendedCodes)
)

type faxReader struct {
	data []byte
	pos  int // bit position
}

func (r *faxReader) bit() (uint32, bool) {
	if r.pos >= 8*len(r.data) {
		return 0, false
	}
	b := uint32(r.data[r.pos/8]>>(7-r.pos%8)) & 1
	r.pos++
	return b, true
}

func (r *faxReader) peek(n int) (uint32, bool) {
	v := uint32(0)
	for i := 0; i < n; i++ {
		p := r.pos + i
		if p >= 8*len(r.data) {
			return 0, false
		}
		v = v<<1 | uint32(r.data[p/8]>>(7-p%8))&1
	}
	return v, true
}

func (r *faxReader) align() { r.pos = (r.pos + 7) &^ 7 }

// run reads a run length (makeup codes followed by a terminating code).
func (r *faxReader) run(black bool) (int, error) {
	t := whiteTable
	if black {
		t = blackTable
	}
	total := 0
	for {
		code := uint32(0)
		found := false
		for n := 1; n <= 13; n++ {
			b, ok := r.bit()
			if !ok {
				return total, errors.New("ccitt: truncated")
			}
			code = code<<1 | b
			if v, ok := t[uint32(n)<<16|code]; ok {
				total += v
				found = true
				if v < 64 {
					return total, nil
				}
				break
			}
		}
		if !found {
			return total, errors.New("ccitt: invalid code")
		}
	}
}

// skipEOL skips an end-of-line code (with fill bits) if present.
func (r *faxReader) skipEOL() bool {
	save := r.pos
	zeros := 0
	for {
		b, ok := r.bit()
		if !ok {
			r.pos = save
			return false
		}
		if b == 1 {
			if zeros >= 11 {
				return true
			}
			r.pos = save
			return false
		}
		zeros++
	}
}

// decodeFax decodes a CCITT-compressed bilevel image into rows of bytes,
// 1 for black. compression is 2 (Modified Huffman), 3 (T.4) or 4 (T.6).
func decodeFax(data []byte, width, height, compression, options int) ([][]byte, error) {
	r := &faxReader{data: data}
	rows := make([][]byte, 0, height)
	ref := []int{width, width} // changing elements of the reference line
	twoD := compression == 4 || (compression == 3 && options&1 != 0)
	for y := 0; y < height; y++ {
		row := make([]byte, width)
		var changes []int
		mode2D := compression == 4
		if compression == 3 {
			r.skipEOL()
			if twoD {
				b, ok := r.bit()
				if !ok {
					break
				}
				mode2D = b == 0
			}
		}
		fill := func(a, b int, black bool) {
			if a < 0 {
				a = 0
			}
			if b > width {
				b = width
			}
			if black {
				for i := a; i < b; i++ {
					row[i] = 1
				}
			}
		}
		var err error
		if !mode2D {
			x := 0
			black := false
			for x < width {
				var n int
				n, err = r.run(black)
				if err != nil {
					break
				}
				fill(x, x+n, black)
				x += n
				if x < width {
					changes = append(changes, x)
				}
				black = !black
			}
		} else {
			a0 := -1
			black := false
			for a0 < width {
				// b1: first changing element on the reference line after
				// a0 of the opposite color; b2 the next one.
				i := 0
				for i < len(ref) && (ref[i] <= a0 || (i%2 == 1) != black) {
					i++
				}
				b1, b2 := width, width
				if i < len(ref) {
					b1 = ref[i]
				}
				if i+1 < len(ref) {
					b2 = ref[i+1]
				}
				var code uint32
				var ok bool
				switch {
				case peekIs(r, "1", &code, &ok): // V0
					fill(a0, b1, black)
					changes = append(changes, b1)
					a0, black = b1, !black
				case peekIs(r, "011", &code, &ok): // VR1
					fill(a0, b1+1, black)
					changes = append(changes, b1+1)
					a0, black = b1+1, !black
				case peekIs(r, "000011", &code, &ok):
					fill(a0, b1+2, black)
					changes = append(changes, b1+2)
					a0, black = b1+2, !black
				case peekIs(r, "0000011", &code, &ok):
					fill(a0, b1+3, black)
					changes = append(changes, b1+3)
					a0, black = b1+3, !black
				case peekIs(r, "010", &code, &ok): // VL1
					fill(a0, b1-1, black)
					changes = append(changes, b1-1)
					a0, black = b1-1, !black
				case peekIs(r, "000010", &code, &ok):
					fill(a0, b1-2, black)
					changes = append(changes, b1-2)
					a0, black = b1-2, !black
				case peekIs(r, "0000010", &code, &ok):
					fill(a0, b1-3, black)
					changes = append(changes, b1-3)
					a0, black = b1-3, !black
				case peekIs(r, "0001", &code, &ok): // pass
					fill(a0, b2, black)
					a0 = b2
				case peekIs(r, "001", &code, &ok): // horizontal
					start := max(a0, 0)
					var n1, n2 int
					n1, err = r.run(black)
					if err == nil {
						n2, err = r.run(!black)
					}
					fill(start, start+n1, black)
					fill(start+n1, start+n1+n2, !black)
					changes = append(changes, min(start+n1, width), min(start+n1+n2, width))
					a0 = start + n1 + n2
				default:
					err = errors.New("ccitt: invalid mode")
				}
				if err != nil {
					break
				}
			}
		}
		if err != nil {
			if y == 0 {
				return nil, err
			}
			break
		}
		rows = append(rows, row)
		ref = append(changes, width, width)
		if compression == 2 {
			r.align()
		}
		if compression == 4 {
			if v, ok := r.peek(24); ok && v == 0x001001 {
				break // EOFB
			}
		}
	}
	for len(rows) < height {
		rows = append(rows, make([]byte, width))
	}
	return rows, nil
}

func peekIs(r *faxReader, bits string, _ *uint32, _ *bool) bool {
	v, ok := r.peek(len(bits))
	if !ok {
		return false
	}
	want := uint32(0)
	for _, b := range bits {
		want = want<<1 | uint32(b-'0')
	}
	if v != want {
		return false
	}
	r.pos += len(bits)
	return true
}

// DecodeFax decodes CCITT fax data (compression 2: modified Huffman, 3:
// T.4 with options bit 0 for 2D, 4: T.6) into rows of pixels, 1 for black.
func DecodeFax(data []byte, width, height, compression, options int) ([][]byte, error) {
	return decodeFax(data, width, height, compression, options)
}

package ps

import (
	"bytes"
	"io"
	"math"
	"strconv"
)

// errEOF is returned by token at the end of the file.
var errEOF = io.EOF

// token reads the next object of a file. Procedures are read whole.
// Immediately evaluated names (//name) are looked up.
func (in *Interp) token(f *File) (Object, error) {
	for {
		c, err := f.ReadByte()
		if err != nil {
			return null, errEOF
		}
		switch {
		case isSpace(c):
			if c == '\n' {
				f.lines++
			}
			continue
		case c == '%':
			for {
				c, err = f.ReadByte()
				if err != nil || c == '\n' || c == '\r' {
					break
				}
			}
			continue
		}
		return in.tokenFrom(f, c)
	}
}

func (in *Interp) tokenFrom(f *File, c byte) (Object, error) {
	switch c {
	case '(':
		return String(readString(f)), nil
	case '<':
		c2, err := f.ReadByte()
		if err == nil {
			if c2 == '<' {
				return XName("<<"), nil
			}
			if c2 == '~' {
				return String(readASCII85(f)), nil
			}
			f.UnreadByte()
		}
		return String(readHexString(f)), nil
	case '>':
		c2, err := f.ReadByte()
		if err == nil && c2 == '>' {
			return XName(">>"), nil
		}
		if err == nil {
			f.UnreadByte()
		}
		panic(psError{"syntaxerror", ">"})
	case '[', ']':
		return XName(string(c)), nil
	case '{':
		var elems []Object
		for {
			o, err := in.token(f)
			if err != nil {
				panic(psError{"syntaxerror", "{"})
			}
			if o.Kind == KName && o.Exec && o.NameV() == "}" {
				break
			}
			elems = append(elems, o)
		}
		a := Array(elems)
		a.Exec = true
		return a, nil
	case '}':
		return XName("}"), nil
	case '/':
		c2, err := f.ReadByte()
		immediate := false
		if err == nil {
			if c2 == '/' {
				immediate = true
			} else {
				f.UnreadByte()
			}
		}
		name := readRegular(f, 0)
		if immediate {
			v, ok := in.lookup(name)
			if !ok {
				panic(psError{"undefined", name})
			}
			return v, nil
		}
		return Name(name), nil
	}
	s := readRegular(f, c)
	if o, ok := parseNumber(s); ok {
		return o, nil
	}
	return XName(s), nil
}

// readRegular reads the rest of a regular token starting with first (0
// if none). A whitespace terminating it is consumed (CR LF as one).
func readRegular(f *File, first byte) string {
	var b []byte
	if first != 0 {
		b = append(b, first)
	}
	for {
		c, err := f.ReadByte()
		if err != nil {
			break
		}
		if isSpace(c) {
			if c == '\r' {
				if c2, err := f.ReadByte(); err == nil && c2 != '\n' {
					f.UnreadByte()
				}
			}
			if c == '\n' || c == '\r' {
				f.lines++
			}
			break
		}
		if isDelim(c) {
			f.UnreadByte()
			break
		}
		b = append(b, c)
	}
	return string(b)
}

func parseNumber(s string) (Object, bool) {
	if s == "" {
		return null, false
	}
	c := s[0]
	if !(c >= '0' && c <= '9' || c == '-' || c == '+' || c == '.') {
		return null, false
	}
	if i, err := strconv.ParseInt(s, 10, 64); err == nil {
		if i > math.MaxInt32 || i < math.MinInt32 {
			return Real(float64(i)), true
		}
		return Int(int(i)), true
	}
	if k := bytes.IndexByte([]byte(s), '#'); k > 0 {
		base, err := strconv.Atoi(s[:k])
		if err == nil && base >= 2 && base <= 36 {
			if v, err := strconv.ParseUint(s[k+1:], base, 32); err == nil {
				return Int(int(int32(uint32(v)))), true
			}
		}
		return null, false
	}
	// Reals: no hex floats, infinities or underscores.
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c >= '0' && c <= '9', c == '.', c == '-', c == '+', c == 'e', c == 'E':
		default:
			return null, false
		}
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return Real(f), true
	} else if ne, ok := err.(*strconv.NumError); ok && ne.Err == strconv.ErrRange {
		return Real(f), true
	}
	return null, false
}

func readString(f *File) []byte {
	var b []byte
	depth := 1
	for {
		c, err := f.ReadByte()
		if err != nil {
			return b
		}
		switch c {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return b
			}
		case '\r':
			// CR and CR LF are newlines.
			if c2, err := f.ReadByte(); err == nil && c2 != '\n' {
				f.UnreadByte()
			}
			c = '\n'
		case '\\':
			c2, err := f.ReadByte()
			if err != nil {
				return b
			}
			switch c2 {
			case 'n':
				c = '\n'
			case 'r':
				c = '\r'
			case 't':
				c = '\t'
			case 'b':
				c = '\b'
			case 'f':
				c = '\f'
			case '\r':
				if c3, err := f.ReadByte(); err == nil && c3 != '\n' {
					f.UnreadByte()
				}
				continue
			case '\n':
				continue
			default:
				if c2 >= '0' && c2 <= '7' {
					v := int(c2 - '0')
					for k := 0; k < 2; k++ {
						c3, err := f.ReadByte()
						if err != nil {
							break
						}
						if c3 < '0' || c3 > '7' {
							f.UnreadByte()
							break
						}
						v = v*8 + int(c3-'0')
					}
					c = byte(v)
				} else {
					c = c2
				}
			}
		}
		b = append(b, c)
	}
}

func readHexString(f *File) []byte {
	var b []byte
	h := -1
	for {
		c, err := f.ReadByte()
		if err != nil || c == '>' {
			break
		}
		v := hexVal(c)
		if v < 0 {
			continue
		}
		if h < 0 {
			h = v
		} else {
			b = append(b, byte(h<<4|v))
			h = -1
		}
	}
	if h >= 0 {
		b = append(b, byte(h<<4))
	}
	return b
}

func readASCII85(f *File) []byte {
	var raw []byte
	for {
		c, err := f.ReadByte()
		if err != nil {
			break
		}
		if c == '~' {
			f.ReadByte() // '>'
			break
		}
		raw = append(raw, c)
	}
	out, _ := io.ReadAll(&a85Reader{src: memFile(raw)})
	return out
}

package ps

import (
	"bufio"
	"io"
)

// File is a readable file: the program, a string or a filter.
type File struct {
	data []byte // memory source
	pos  int
	rd   *bufio.Reader // buffered stream source (nil for memory)
	// bs is an unbuffered stream source (eexec, filters on the program:
	// reading must not go past what the decoder needs).
	bs     io.ByteReader
	last   byte
	unread bool
	closed bool
	// close is called when the file is closed (filters).
	close func()
	// lines counts newlines read, for error messages.
	lines int
	// dct keeps the JPEG data of a DCTDecode filter (once read, see
	// dctInit).
	dct     *dctData
	dctInit func()
	// skipEOD consumes the end-of-data marker of an ASCII filter after
	// its data was read.
	skipEOD func()
}

func memFile(b []byte) *File { return &File{data: b} }

func readerFile(r io.Reader) *File { return &File{rd: bufio.NewReaderSize(r, 4096)} }

// byteFile reads a source one byte at a time.
func byteFile(r io.ByteReader) *File { return &File{bs: r} }

// ReadByte reads a byte.
func (f *File) ReadByte() (byte, error) {
	if f.closed {
		return 0, io.EOF
	}
	if f.bs != nil {
		if f.unread {
			f.unread = false
			return f.last, nil
		}
		c, err := f.bs.ReadByte()
		if err == nil {
			f.last = c
			f.pos++
		}
		return c, err
	}
	if f.rd == nil {
		if f.pos >= len(f.data) {
			return 0, io.EOF
		}
		c := f.data[f.pos]
		f.pos++
		return c, nil
	}
	return f.rd.ReadByte()
}

// UnreadByte puts back the last byte read.
func (f *File) UnreadByte() error {
	if f.bs != nil {
		f.unread = true
		return nil
	}
	if f.rd == nil {
		if f.pos > 0 {
			f.pos--
		}
		return nil
	}
	return f.rd.UnreadByte()
}

// Read implements io.Reader.
func (f *File) Read(p []byte) (int, error) {
	if f.closed {
		return 0, io.EOF
	}
	if f.bs != nil {
		for i := range p {
			c, err := f.ReadByte()
			if err != nil {
				if i == 0 {
					return 0, err
				}
				return i, nil
			}
			p[i] = c
		}
		return len(p), nil
	}
	if f.rd == nil {
		if f.pos >= len(f.data) {
			return 0, io.EOF
		}
		n := copy(p, f.data[f.pos:])
		f.pos += n
		return n, nil
	}
	return f.rd.Read(p)
}

// readFull reads up to len(p) bytes, fewer only at the end of the file.
func (f *File) readFull(p []byte) int {
	n, _ := io.ReadFull(f, p)
	return n
}

// Close closes the file.
func (f *File) Close() {
	if f.closed {
		return
	}
	f.closed = true
	if f.close != nil {
		f.close()
	}
}

// eexecReader decrypts eexec-encrypted data (binary or hexadecimal).
type eexecReader struct {
	src  *File
	r    uint16
	hex  bool
	err  error
	init bool
}

func newEexec(src *File) *eexecReader {
	e := &eexecReader{src: src, r: 55665}
	// Skip leading whitespace, then decide between hex and binary on the
	// first four bytes.
	var head [4]byte
	n := 0
	for n < 4 {
		c, err := src.ReadByte()
		if err != nil {
			break
		}
		if n == 0 && (c == ' ' || c == '\t' || c == '\r' || c == '\n') {
			continue
		}
		head[n] = c
		n++
	}
	e.hex = true
	for _, c := range head[:n] {
		if hexVal(c) < 0 {
			e.hex = false
		}
	}
	if e.hex {
		// The four hex digits are two bytes; four random bytes in all.
		b0 := byte(hexVal(head[0])<<4 | hexVal(head[1]))
		b1 := byte(hexVal(head[2])<<4 | hexVal(head[3]))
		e.dec(b0)
		e.dec(b1)
		for i := 0; i < 2; i++ {
			e.readByte()
		}
	} else {
		for _, c := range head[:n] {
			e.dec(c)
		}
	}
	return e
}

func (e *eexecReader) dec(c byte) byte {
	p := c ^ byte(e.r>>8)
	e.r = (uint16(c)+e.r)*52845 + 22719
	return p
}

func (e *eexecReader) raw() (byte, error) {
	if !e.hex {
		return e.src.ReadByte()
	}
	var v [2]int
	for i := 0; i < 2; {
		c, err := e.src.ReadByte()
		if err != nil {
			return 0, err
		}
		h := hexVal(c)
		if h < 0 {
			if isSpace(c) {
				continue
			}
			return 0, io.EOF
		}
		v[i] = h
		i++
	}
	return byte(v[0]<<4 | v[1]), nil
}

func (e *eexecReader) readByte() (byte, error) {
	c, err := e.raw()
	if err != nil {
		return 0, err
	}
	return e.dec(c), nil
}

// ReadByte implements io.ByteReader.
func (e *eexecReader) ReadByte() (byte, error) { return e.readByte() }

func (e *eexecReader) Read(p []byte) (int, error) {
	for i := range p {
		c, err := e.readByte()
		if err != nil {
			if i == 0 {
				return 0, err
			}
			return i, nil
		}
		p[i] = c
	}
	return len(p), nil
}

func hexVal(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10
	case c >= 'A' && c <= 'F':
		return int(c-'A') + 10
	}
	return -1
}

func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' || c == 0
}

func isDelim(c byte) bool {
	switch c {
	case '(', ')', '<', '>', '[', ']', '{', '}', '/', '%':
		return true
	}
	return false
}

// decryptCharString decrypts a Type 1 charstring (or Subrs entry).
func decryptCharString(b []byte, lenIV int) []byte {
	if lenIV < 0 {
		return b
	}
	r := uint16(4330)
	out := make([]byte, len(b))
	for i, c := range b {
		out[i] = c ^ byte(r>>8)
		r = (uint16(c)+r)*52845 + 22719
	}
	if lenIV > len(out) {
		return nil
	}
	return out[lenIV:]
}

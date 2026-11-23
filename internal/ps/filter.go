package ps

import (
	"bufio"
	"bytes"
	"compress/zlib"
	"errors"
	"image"
	"image/jpeg"
	"io"
)

// a85Reader decodes ASCII85 up to "~>".
type a85Reader struct {
	src     io.ByteReader
	out     []byte
	done    bool
	started bool
}

func (r *a85Reader) fill() {
	var group [5]byte
	n := 0
	for n < 5 {
		c, err := r.src.ReadByte()
		if err != nil {
			r.done = true
			break
		}
		if isSpace(c) {
			continue
		}
		if !r.started && c == '<' {
			// An optional "<~" prefix.
			if c2, err := r.src.ReadByte(); err == nil && c2 == '~' {
				r.started = true
				continue
			}
			r.done = true
			break
		}
		r.started = true
		if c == '~' {
			r.src.ReadByte()
			r.done = true
			break
		}
		if c == 'z' && n == 0 {
			r.out = append(r.out, 0, 0, 0, 0)
			return
		}
		if c < '!' || c > 'u' {
			r.done = true
			break
		}
		group[n] = c - '!'
		n++
	}
	if n == 0 {
		return
	}
	for i := n; i < 5; i++ {
		group[i] = 84
	}
	var v uint32
	for _, d := range group {
		v = v*85 + uint32(d)
	}
	b := []byte{byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)}
	r.out = append(r.out, b[:n-1]...)
}

func (r *a85Reader) Read(p []byte) (int, error) {
	// Decode ahead (up to the end-of-data marker), like a buffered
	// filter.
	for len(r.out) < 1024 && !r.done {
		r.fill()
	}
	if len(r.out) == 0 {
		return 0, io.EOF
	}
	n := copy(p, r.out)
	r.out = r.out[n:]
	return n, nil
}

// hexReader decodes ASCIIHex up to ">".
type hexReader struct {
	src  io.ByteReader
	done bool
}

func (r *hexReader) Read(p []byte) (int, error) {
	n := 0
	if len(p) > 1024 {
		p = p[:1024]
	}
	for n < len(p) && !r.done {
		h := -1
		for {
			c, err := r.src.ReadByte()
			if err != nil || c == '>' {
				r.done = true
				break
			}
			v := hexVal(c)
			if v < 0 {
				continue
			}
			if h < 0 {
				h = v
				continue
			}
			p[n] = byte(h<<4 | v)
			n++
			h = -1
			break
		}
		if r.done && h >= 0 {
			p[n] = byte(h << 4)
			n++
		}
	}
	if n == 0 {
		return 0, io.EOF
	}
	return n, nil
}

// rlReader decodes RunLengthDecode.
type rlReader struct {
	src  io.ByteReader
	out  []byte
	done bool
}

func (r *rlReader) Read(p []byte) (int, error) {
	for len(r.out) == 0 {
		if r.done {
			return 0, io.EOF
		}
		l, err := r.src.ReadByte()
		if err != nil || l == 128 {
			r.done = true
			continue
		}
		if l < 128 {
			for i := 0; i <= int(l); i++ {
				c, err := r.src.ReadByte()
				if err != nil {
					r.done = true
					break
				}
				r.out = append(r.out, c)
			}
		} else {
			c, err := r.src.ReadByte()
			if err != nil {
				r.done = true
				continue
			}
			for i := 0; i < 257-int(l); i++ {
				r.out = append(r.out, c)
			}
		}
	}
	n := copy(p, r.out)
	r.out = r.out[n:]
	return n, nil
}

// lzwReader decodes LZWDecode (with EarlyChange).
type lzwReader struct {
	src   io.ByteReader
	early int
	bits  uint32
	nbits int
	width int
	table [][]byte
	prev  []byte
	out   []byte
	done  bool
}

func newLZW(src io.ByteReader, early int) *lzwReader {
	r := &lzwReader{src: src, early: early}
	r.reset()
	return r
}

func (r *lzwReader) reset() {
	r.table = r.table[:0]
	for i := 0; i < 256; i++ {
		r.table = append(r.table, []byte{byte(i)})
	}
	r.table = append(r.table, nil, nil) // 256 clear, 257 EOD
	r.width = 9
	r.prev = nil
}

func (r *lzwReader) code() (int, bool) {
	for r.nbits < r.width {
		c, err := r.src.ReadByte()
		if err != nil {
			return 0, false
		}
		r.bits = r.bits<<8 | uint32(c)
		r.nbits += 8
	}
	v := int(r.bits>>(r.nbits-r.width)) & (1<<r.width - 1)
	r.nbits -= r.width
	return v, true
}

func (r *lzwReader) Read(p []byte) (int, error) {
	for len(r.out) == 0 {
		if r.done {
			return 0, io.EOF
		}
		c, ok := r.code()
		if !ok || c == 257 {
			r.done = true
			continue
		}
		if c == 256 {
			r.reset()
			continue
		}
		var entry []byte
		switch {
		case c < len(r.table) && r.table[c] != nil:
			entry = r.table[c]
		case c == len(r.table) && r.prev != nil:
			entry = append(append([]byte{}, r.prev...), r.prev[0])
		default:
			r.done = true
			continue
		}
		if r.prev != nil && len(r.table) < 4096 {
			r.table = append(r.table, append(append([]byte{}, r.prev...), entry[0]))
		}
		r.prev = entry
		r.out = append(r.out, entry...)
		if n := len(r.table) + r.early; n >= 1<<r.width && r.width < 12 {
			r.width++
		}
	}
	n := copy(p, r.out)
	r.out = r.out[n:]
	return n, nil
}

// subFileReader reads up to a count of occurrences of an end string.
type subFileReader struct {
	src   io.ByteReader
	count int
	eod   []byte
	pend  []byte
	out   []byte
	done  bool
	bytes int // remaining bytes when eod is empty (-1: unlimited)
}

func (r *subFileReader) Read(p []byte) (int, error) {
	if len(r.eod) == 0 {
		if r.bytes == 0 {
			return 0, io.EOF
		}
		n := 0
		for n < len(p) && r.bytes != 0 {
			c, err := r.src.ReadByte()
			if err != nil {
				r.bytes = 0
				break
			}
			p[n] = c
			n++
			if r.bytes > 0 {
				r.bytes--
			}
		}
		if n == 0 {
			return 0, io.EOF
		}
		return n, nil
	}
	for len(r.out) == 0 {
		if r.done {
			return 0, io.EOF
		}
		c, err := r.src.ReadByte()
		if err != nil {
			r.out = append(r.out, r.pend...)
			r.pend = nil
			r.done = true
			continue
		}
		r.pend = append(r.pend, c)
		for len(r.pend) > 0 && !bytes.HasPrefix(r.eod, r.pend) {
			r.out = append(r.out, r.pend[0])
			r.pend = r.pend[1:]
		}
		if len(r.pend) == len(r.eod) {
			r.count--
			if r.count < 0 {
				r.done = true
				r.pend = nil
				continue
			}
			r.out = append(r.out, r.pend...)
			r.pend = nil
		}
	}
	n := copy(p, r.out)
	r.out = r.out[n:]
	return n, nil
}

// procReader reads the strings returned by a procedure until an empty
// one.
type procReader struct {
	in   *Interp
	proc Object
	out  []byte
	done bool
}

func (r *procReader) Read(p []byte) (int, error) {
	for len(r.out) == 0 {
		if r.done {
			return 0, io.EOF
		}
		r.in.execObj(r.proc)
		s := r.in.pop()
		if s.Kind != KString || s.Len == 0 {
			r.done = true
			continue
		}
		r.out = append(r.out[:0], s.Bytes()...)
	}
	n := copy(p, r.out)
	r.out = r.out[n:]
	return n, nil
}

// predictorReader undoes PNG and TIFF predictors.
type predictorReader struct {
	src            io.Reader
	pred, colors   int
	bpc, cols      int
	row, prev, out []byte
	done           bool
}

func newPredictor(src io.Reader, parms *Dict) io.Reader {
	geti := func(k string, def int) int {
		if v, ok := parms.GetS(k); ok && v.IsNum() {
			return int(v.Num())
		}
		return def
	}
	pred := geti("Predictor", 1)
	if pred < 2 {
		return src
	}
	r := &predictorReader{src: src, pred: pred, colors: geti("Colors", 1), bpc: geti("BitsPerComponent", 8), cols: geti("Columns", 1)}
	rowLen := (r.colors*r.bpc*r.cols + 7) / 8
	r.prev = make([]byte, rowLen)
	return r
}

func (r *predictorReader) Read(p []byte) (int, error) {
	for len(r.out) == 0 {
		if r.done {
			return 0, io.EOF
		}
		rowLen := len(r.prev)
		bpp := (r.colors*r.bpc + 7) / 8
		if r.pred >= 10 {
			buf := make([]byte, rowLen+1)
			if _, err := io.ReadFull(r.src, buf); err != nil {
				r.done = true
				continue
			}
			ft, row := buf[0], buf[1:]
			for i := range row {
				var a, b, c byte
				if i >= bpp {
					a = row[i-bpp]
					c = r.prev[i-bpp]
				}
				b = r.prev[i]
				switch ft {
				case 1:
					row[i] += a
				case 2:
					row[i] += b
				case 3:
					row[i] += byte((int(a) + int(b)) / 2)
				case 4:
					row[i] += paeth(a, b, c)
				}
			}
			copy(r.prev, row)
			r.out = append(r.out, row...)
		} else {
			row := make([]byte, rowLen)
			if _, err := io.ReadFull(r.src, row); err != nil {
				r.done = true
				continue
			}
			if r.bpc == 8 {
				for i := bpp; i < len(row); i++ {
					row[i] += row[i-bpp]
				}
			}
			r.out = append(r.out, row...)
		}
	}
	n := copy(p, r.out)
	r.out = r.out[n:]
	return n, nil
}

func paeth(a, b, c byte) byte {
	p := int(a) + int(b) - int(c)
	pa, pb, pc := iabs(p-int(a)), iabs(p-int(b)), iabs(p-int(c))
	switch {
	case pa <= pb && pa <= pc:
		return a
	case pb <= pc:
		return b
	}
	return c
}

func iabs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// dctData decodes a JPEG read from src into samples (gray, RGB or CMYK)
// and keeps the encoded data.
type dctData struct {
	jpeg       []byte
	samples    []byte
	w, h, comp int
}

var errNotJPEG = errors.New("ps: bad DCT data")

// readJPEG reads a JPEG stream up to its EOI marker.
func readJPEG(src io.ByteReader) []byte {
	var b []byte
	prevFF := false
	for {
		c, err := src.ReadByte()
		if err != nil {
			return b
		}
		b = append(b, c)
		if prevFF && c == 0xD9 {
			return b
		}
		if prevFF && c >= 0xC0 && c != 0xD0 && !(c >= 0xD0 && c <= 0xD7) && c != 0xD8 && c != 0xFF && c != 0x01 {
			// A marker segment with a length: copy it whole (it may
			// contain FF D9 bytes, e.g. in thumbnails).
			hi, err1 := src.ReadByte()
			lo, err2 := src.ReadByte()
			if err1 != nil || err2 != nil {
				return b
			}
			b = append(b, hi, lo)
			n := int(hi)<<8 | int(lo)
			for i := 2; i < n; i++ {
				d, err := src.ReadByte()
				if err != nil {
					return b
				}
				b = append(b, d)
			}
			prevFF = false
			if c == 0xDA {
				// Entropy-coded data follows: scan for markers.
			}
			continue
		}
		prevFF = c == 0xFF
	}
}

func decodeDCT(data []byte) (*dctData, error) {
	img, err := jpeg.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	b := img.Bounds()
	d := &dctData{jpeg: data, w: b.Dx(), h: b.Dy()}
	switch m := img.(type) {
	case *image.Gray:
		d.comp = 1
		for y := b.Min.Y; y < b.Max.Y; y++ {
			d.samples = append(d.samples, m.Pix[(y-b.Min.Y)*m.Stride:][:d.w]...)
		}
	case *image.CMYK:
		d.comp = 4
		for y := b.Min.Y; y < b.Max.Y; y++ {
			d.samples = append(d.samples, m.Pix[(y-b.Min.Y)*m.Stride:][:4*d.w]...)
		}
		// image/jpeg undoes the inversion of Adobe CMYK files; the
		// DCTDecode filter gives the stored (inverted) values, which
		// programs invert with their Decode arrays.
		if bytes.Contains(data[:min(len(data), 65536)], []byte("Adobe")) {
			for i := range d.samples {
				d.samples[i] = 255 - d.samples[i]
			}
		}
	default:
		d.comp = 3
		d.samples = make([]byte, 0, 3*d.w*d.h)
		for y := b.Min.Y; y < b.Max.Y; y++ {
			for x := b.Min.X; x < b.Max.X; x++ {
				r, g, bl, _ := img.At(x, y).RGBA()
				d.samples = append(d.samples, byte(r>>8), byte(g>>8), byte(bl>>8))
			}
		}
	}
	return d, nil
}

// filter creates a filter file of the given name on a source.
func (in *Interp) makeFilter(name string, src io.ByteReader, parms *Dict) *File {
	if parms == nil {
		parms = NewDict(0)
	}
	var r io.Reader
	switch name {
	case "ASCIIHexDecode":
		r = &hexReader{src: src}
	case "ASCII85Decode":
		r = &a85Reader{src: src}
	case "RunLengthDecode":
		r = &rlReader{src: src}
	case "LZWDecode":
		early := 1
		if v, ok := parms.GetS("EarlyChange"); ok && v.IsNum() {
			early = int(v.Num())
		}
		r = newPredictor(newLZW(src, early), parms)
	case "FlateDecode":
		// Lazily: the zlib header must not be read before the data is.
		r = &lazyReader{init: func() io.Reader {
			zr, err := zlib.NewReader(byteReaderAdapter{src})
			if err != nil {
				return bytes.NewReader(nil)
			}
			return newPredictor(zr, parms)
		}}
	case "SubFileDecode":
		sf := &subFileReader{src: src, bytes: -1}
		if v, ok := parms.GetS("EODCount"); ok && v.IsNum() {
			sf.count = int(v.Num())
		}
		if v, ok := parms.GetS("EODString"); ok {
			sf.eod = []byte(v.Text())
		}
		if len(sf.eod) == 0 && sf.count > 0 {
			sf.bytes = sf.count
		} else if len(sf.eod) == 0 {
			sf.bytes = 0
		}
		r = sf
	case "DCTDecode":
		f := &File{}
		f.data = nil
		lr := &lazyReader{init: func() io.Reader {
			d, err := decodeDCT(readJPEG(src))
			if err != nil {
				return bytes.NewReader(nil)
			}
			f.dct = d
			return bytes.NewReader(d.samples)
		}}
		f.rd = bufio.NewReaderSize(lr, 4096)
		f.dctInit = func() {
			if lr.r == nil {
				lr.r = lr.init()
			}
		}
		return f
	case "NullEncode", "NullDecode":
		r = byteReaderAdapter{src}
	case "CCITTFaxDecode":
		r = newCCITT(src, parms)
	default:
		// Encoding filters and unknown filters: unsupported.
		panic(psError{"undefined", name})
	}
	// Like Ghostscript, filters read ahead in buffers (the ASCII
	// decoders stop at their end-of-data marker).
	f := readerFile(r)
	if e, ok := r.(interface{ skipEOD() }); ok {
		f.skipEOD = e.skipEOD
	}
	return f
}

// skipEODMarker consumes whitespace and an end-of-data marker that
// follows the data (when the reader of the filter stopped just before).
func skipEODMarker(src io.ByteReader, marker string) {
	u, ok := src.(interface{ UnreadByte() error })
	if !ok {
		return
	}
	for {
		c, err := src.ReadByte()
		if err != nil {
			return
		}
		if isSpace(c) {
			continue
		}
		if c != marker[0] {
			u.UnreadByte()
			return
		}
		for i := 1; i < len(marker); i++ {
			src.ReadByte()
		}
		return
	}
}

func (r *hexReader) skipEOD() {
	if !r.done {
		skipEODMarker(r.src, ">")
		r.done = true
	}
}

func (r *a85Reader) skipEOD() {
	if !r.done && len(r.out) == 0 {
		skipEODMarker(r.src, "~>")
		r.done = true
	}
}

type oneByte struct {
	r   io.Reader
	buf [1]byte
}

func (o *oneByte) ReadByte() (byte, error) {
	for {
		n, err := o.r.Read(o.buf[:])
		if n == 1 {
			return o.buf[0], nil
		}
		if err != nil {
			return 0, err
		}
	}
}

// lazyReader creates its reader on the first read.
type lazyReader struct {
	init func() io.Reader
	r    io.Reader
}

func (l *lazyReader) Read(p []byte) (int, error) {
	if l.r == nil {
		l.r = l.init()
	}
	return l.r.Read(p)
}

type byteReaderAdapter struct{ r io.ByteReader }

// ReadByte makes byteReaderAdapter a flate.Reader (no read-ahead).
func (a byteReaderAdapter) ReadByte() (byte, error) { return a.r.ReadByte() }

func (a byteReaderAdapter) Read(p []byte) (int, error) {
	for i := range p {
		c, err := a.r.ReadByte()
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

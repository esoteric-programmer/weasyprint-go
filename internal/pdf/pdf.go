// Package pdf is a small low-level PDF writer, similar in spirit to pydyf,
// the library used by WeasyPrint. It knows how to serialize PDF objects,
// build content streams and write a complete file with a cross-reference
// table.
package pdf

import (
	"bytes"
	"compress/zlib"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"
)

// Name is a PDF name object (/Name).
type Name string

// String is a PDF literal string, written with escapes.
type String string

// HexString is a PDF hexadecimal string.
type HexString []byte

// Ref is an indirect reference to an object of the document.
type Ref int

// Raw is written verbatim.
type Raw string

// Array is a PDF array.
type Array []any

// Dict is a PDF dictionary. Keys are written in sorted order to keep the
// output deterministic.
type Dict map[string]any

// Stream is a PDF stream object.
type Stream struct {
	Dict     Dict
	Data     []byte
	Compress bool
}

// Document holds all indirect objects of a PDF file.
type Document struct {
	objects []any
	Version string
}

// NewDocument returns an empty document.
func NewDocument() *Document {
	return &Document{objects: []any{nil}, Version: "1.7"}
}

// Add adds an indirect object and returns its reference.
func (d *Document) Add(obj any) Ref {
	d.objects = append(d.objects, obj)
	return Ref(len(d.objects) - 1)
}

// Reserve reserves an object number to be set later with Set.
func (d *Document) Reserve() Ref { return d.Add(nil) }

// Set replaces the object for a reserved reference.
func (d *Document) Set(r Ref, obj any) { d.objects[r] = obj }

// Get returns the object of a reference.
func (d *Document) Get(r Ref) any { return d.objects[r] }

// FormatNumber formats a float the way PDF expects it: no exponent, up to
// 4 decimal places, no trailing zeros.
func FormatNumber(f float64) string {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return "0"
	}
	if f == math.Trunc(f) && math.Abs(f) < 1e15 {
		return strconv.FormatInt(int64(f), 10)
	}
	s := strconv.FormatFloat(f, 'f', 4, 64)
	s = strings.TrimRight(s, "0")
	s = strings.TrimSuffix(s, ".")
	if s == "-0" || s == "" {
		return "0"
	}
	return s
}

func writeName(w *bytes.Buffer, n string) {
	w.WriteByte('/')
	for i := 0; i < len(n); i++ {
		c := n[i]
		if c < 0x21 || c > 0x7e || strings.IndexByte("#()<>[]{}/%", c) >= 0 {
			fmt.Fprintf(w, "#%02X", c)
		} else {
			w.WriteByte(c)
		}
	}
}

func writeString(w *bytes.Buffer, s string) {
	// Use UTF-16BE for non-ASCII text strings.
	ascii := true
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			ascii = false
			break
		}
	}
	if !ascii {
		var b []byte
		b = append(b, 0xfe, 0xff)
		for _, r := range s {
			if r > 0xffff {
				r -= 0x10000
				hi := 0xd800 + (r >> 10)
				lo := 0xdc00 + (r & 0x3ff)
				b = append(b, byte(hi>>8), byte(hi), byte(lo>>8), byte(lo))
			} else {
				b = append(b, byte(r>>8), byte(r))
			}
		}
		writeValue(w, HexString(b))
		return
	}
	w.WriteByte('(')
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '(', ')', '\\':
			w.WriteByte('\\')
			w.WriteByte(c)
		case '\n':
			w.WriteString("\\n")
		case '\r':
			w.WriteString("\\r")
		default:
			w.WriteByte(c)
		}
	}
	w.WriteByte(')')
}

func writeValue(w *bytes.Buffer, v any) {
	switch v := v.(type) {
	case nil:
		w.WriteString("null")
	case bool:
		if v {
			w.WriteString("true")
		} else {
			w.WriteString("false")
		}
	case int:
		w.WriteString(strconv.Itoa(v))
	case int64:
		w.WriteString(strconv.FormatInt(v, 10))
	case float64:
		w.WriteString(FormatNumber(v))
	case Name:
		writeName(w, string(v))
	case String:
		writeString(w, string(v))
	case HexString:
		w.WriteByte('<')
		fmt.Fprintf(w, "%X", []byte(v))
		w.WriteByte('>')
	case Ref:
		fmt.Fprintf(w, "%d 0 R", int(v))
	case Raw:
		w.WriteString(string(v))
	case Array:
		w.WriteByte('[')
		for i, item := range v {
			if i > 0 {
				w.WriteByte(' ')
			}
			writeValue(w, item)
		}
		w.WriteByte(']')
	case []float64:
		w.WriteByte('[')
		for i, item := range v {
			if i > 0 {
				w.WriteByte(' ')
			}
			w.WriteString(FormatNumber(item))
		}
		w.WriteByte(']')
	case Dict:
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		w.WriteString("<<")
		for _, k := range keys {
			writeName(w, k)
			w.WriteByte(' ')
			writeValue(w, v[k])
		}
		w.WriteString(">>")
	case *Stream:
		data := v.Data
		dict := Dict{}
		for k, val := range v.Dict {
			dict[k] = val
		}
		if v.Compress {
			var buf bytes.Buffer
			zw, _ := zlib.NewWriterLevel(&buf, zlib.BestCompression)
			zw.Write(data)
			zw.Close()
			data = buf.Bytes()
			if f, ok := dict["Filter"]; ok {
				dict["Filter"] = Array{Name("FlateDecode"), f}
			} else {
				dict["Filter"] = Name("FlateDecode")
			}
		}
		dict["Length"] = len(data)
		writeValue(w, dict)
		w.WriteString("\nstream\n")
		w.Write(data)
		w.WriteString("\nendstream")
	case *Content:
		writeValue(w, &Stream{Dict: v.Dict, Data: v.Bytes(), Compress: v.Compress})
	default:
		panic(fmt.Sprintf("pdf: cannot serialize %T", v))
	}
}

// Serialize returns the PDF syntax for a value.
func Serialize(v any) []byte {
	var b bytes.Buffer
	writeValue(&b, v)
	return b.Bytes()
}

// WriteOptions control how a document is written.
type WriteOptions struct {
	// Identifier is the first file identifier. When nil and AutoID is
	// set, the hash of the file is used. No identifier is written when
	// both are unset.
	Identifier []byte
	AutoID     bool
	// Compress stores objects in a compressed object stream with a
	// cross-reference stream when the version is 1.5 or later, like
	// pydyf.
	Compress bool
}

// VersionAtLeast reports whether version v is at least min ("1.5"...).
func VersionAtLeast(v, min string) bool {
	parse := func(s string) (int, int) {
		maj, minr, _ := strings.Cut(s, ".")
		a, _ := strconv.Atoi(maj)
		b, _ := strconv.Atoi(minr)
		return a, b
	}
	a1, b1 := parse(v)
	a2, b2 := parse(min)
	return a1 > a2 || (a1 == a2 && b1 >= b2)
}

// Write writes the complete PDF file. root and info are references to the
// catalog and information dictionaries (info may be 0).
func (d *Document) Write(out io.Writer, root, info Ref, opts WriteOptions) error {
	var w bytes.Buffer
	version := d.Version
	if version == "" {
		version = "1.7"
	}
	fmt.Fprintf(&w, "%%PDF-%s\n%%\xf0\x9f\x96\xa4\n", version)
	n := len(d.objects)
	data := make([][]byte, n)
	for i := 1; i < n; i++ {
		data[i] = Serialize(d.objects[i])
	}
	var id Array
	if opts.Identifier != nil || opts.AutoID {
		h := md5.New()
		for i := 1; i < n; i++ {
			h.Write(data[i])
		}
		hash := []byte(hex.EncodeToString(h.Sum(nil)))
		first := opts.Identifier
		if first == nil {
			first = hash
		}
		id = Array{String(first), String(hash)}
	}
	if opts.Compress && VersionAtLeast(version, "1.5") {
		return d.writeCompressed(out, &w, data, root, info, id)
	}
	offsets := make([]int, n)
	for i := 1; i < n; i++ {
		offsets[i] = w.Len()
		fmt.Fprintf(&w, "%d 0 obj\n", i)
		w.Write(data[i])
		w.WriteString("\nendobj\n")
	}
	xref := w.Len()
	fmt.Fprintf(&w, "xref\n0 %d\n0000000000 65535 f \n", n)
	for i := 1; i < n; i++ {
		fmt.Fprintf(&w, "%010d 00000 n \n", offsets[i])
	}
	trailer := Dict{"Size": n, "Root": root}
	if info != 0 {
		trailer["Info"] = info
	}
	if id != nil {
		trailer["ID"] = id
	}
	w.WriteString("trailer\n")
	writeValue(&w, trailer)
	fmt.Fprintf(&w, "\nstartxref\n%d\n%%%%EOF\n", xref)
	_, err := out.Write(w.Bytes())
	return err
}

// writeCompressed writes the objects that are not streams in an object
// stream, and a cross-reference stream.
func (d *Document) writeCompressed(out io.Writer, w *bytes.Buffer, data [][]byte, root, info Ref, id Array) error {
	n := len(data)
	type entry struct{ kind, a, b int }
	xref := make([]entry, n+2)
	xref[0] = entry{0, 0, 65535}
	var objStm bytes.Buffer
	var header []string
	k := 0
	for i := 1; i < n; i++ {
		switch d.objects[i].(type) {
		case *Stream, *Content:
			xref[i] = entry{1, w.Len(), 0}
			fmt.Fprintf(w, "%d 0 obj\n", i)
			w.Write(data[i])
			w.WriteString("\nendobj\n")
		default:
			header = append(header, strconv.Itoa(i), strconv.Itoa(objStm.Len()))
			objStm.Write(data[i])
			objStm.WriteByte('\n')
			xref[i] = entry{2, n, k}
			k++
		}
	}
	// Object stream (number n), then cross-reference stream (n + 1).
	head := strings.Join(header, " ")
	stm := &Stream{Dict: Dict{"Type": Name("ObjStm"), "N": k, "First": len(head) + 1},
		Data: append([]byte(head+"\n"), objStm.Bytes()...), Compress: true}
	xref[n] = entry{1, w.Len(), 0}
	fmt.Fprintf(w, "%d 0 obj\n", n)
	writeValue(w, stm)
	w.WriteString("\nendobj\n")
	xrefPos := w.Len()
	xref[n+1] = entry{1, xrefPos, 0}
	size := func(v int) int {
		s := 1
		for v >= 256 {
			v >>= 8
			s++
		}
		return s
	}
	w2, w3 := size(xrefPos), size(max(k, 65535))
	var rows bytes.Buffer
	for _, e := range xref {
		rows.WriteByte(byte(e.kind))
		for s := w2 - 1; s >= 0; s-- {
			rows.WriteByte(byte(e.a >> (8 * s)))
		}
		for s := w3 - 1; s >= 0; s-- {
			rows.WriteByte(byte(e.b >> (8 * s)))
		}
	}
	dict := Dict{"Type": Name("XRef"), "Index": Array{0, n + 2}, "W": Array{1, w2, w3}, "Size": n + 2, "Root": root}
	if info != 0 {
		dict["Info"] = info
	}
	if id != nil {
		dict["ID"] = id
	}
	fmt.Fprintf(w, "%d 0 obj\n", n+1)
	writeValue(w, &Stream{Dict: dict, Data: rows.Bytes(), Compress: true})
	fmt.Fprintf(w, "\nendobj\nstartxref\n%d\n%%%%EOF\n", xrefPos)
	_, err := out.Write(w.Bytes())
	return err
}

// Content is a content stream builder.
type Content struct {
	bytes.Buffer
	Dict     Dict
	Compress bool
	ctm      [6]float64 // current transformation matrix, relative to base
	ctmSet   bool
	ctmStack [][6]float64
}

var identity = [6]float64{1, 0, 0, 1, 0, 0}

// CTM returns the current transformation matrix (a, b, c, d, e, f) of the
// stream, including the base set by SetBaseCTM.
func (c *Content) CTM() [6]float64 {
	if !c.ctmSet {
		return identity
	}
	return c.ctm
}

// SetBaseCTM sets the matrix the stream is drawn with (for form XObjects
// drawn in another stream).
func (c *Content) SetBaseCTM(m [6]float64) {
	c.ctm, c.ctmSet = m, true
}

func mulMatrix(m, n [6]float64) [6]float64 {
	return [6]float64{
		m[0]*n[0] + m[1]*n[2], m[0]*n[1] + m[1]*n[3],
		m[2]*n[0] + m[3]*n[2], m[2]*n[1] + m[3]*n[3],
		m[4]*n[0] + m[5]*n[2] + n[4], m[4]*n[1] + m[5]*n[3] + n[5],
	}
}

// NewContent returns an empty content stream.
func NewContent(compress bool) *Content {
	return &Content{Dict: Dict{}, Compress: compress}
}

func (c *Content) nums(ops string, fs ...float64) {
	for _, f := range fs {
		c.WriteString(FormatNumber(f))
		c.WriteByte(' ')
	}
	c.WriteString(ops)
	c.WriteByte('\n')
}

// Op writes a raw operator.
func (c *Content) Op(s string) { c.WriteString(s); c.WriteByte('\n') }

func (c *Content) Save() {
	c.ctmStack = append(c.ctmStack, c.CTM())
	c.Op("q")
}

func (c *Content) Restore() {
	if n := len(c.ctmStack); n > 0 {
		c.ctm, c.ctmSet = c.ctmStack[n-1], true
		c.ctmStack = c.ctmStack[:n-1]
	}
	c.Op("Q")
}

// Transform concatenates a matrix to the CTM.
func (c *Content) Transform(a, b, cc, d, e, f float64) {
	c.ctm, c.ctmSet = mulMatrix([6]float64{a, b, cc, d, e, f}, c.CTM()), true
	c.nums("cm", a, b, cc, d, e, f)
}

func (c *Content) Rect(x, y, w, h float64) { c.nums("re", x, y, w, h) }
func (c *Content) MoveTo(x, y float64)     { c.nums("m", x, y) }
func (c *Content) LineTo(x, y float64)     { c.nums("l", x, y) }
func (c *Content) CurveTo(x1, y1, x2, y2, x3, y3 float64) {
	c.nums("c", x1, y1, x2, y2, x3, y3)
}
func (c *Content) ClosePath()   { c.Op("h") }
func (c *Content) Fill()        { c.Op("f") }
func (c *Content) FillEvenOdd() { c.Op("f*") }
func (c *Content) Stroke()      { c.Op("S") }
func (c *Content) EndPath()     { c.Op("n") }
func (c *Content) Clip()        { c.Op("W n") }
func (c *Content) ClipEvenOdd() { c.Op("W* n") }

func (c *Content) SetFillRGB(r, g, b float64)   { c.nums("rg", r, g, b) }
func (c *Content) SetStrokeRGB(r, g, b float64) { c.nums("RG", r, g, b) }
func (c *Content) SetLineWidth(w float64)       { c.nums("w", w) }
func (c *Content) SetLineCap(v int)             { c.nums("J", float64(v)) }
func (c *Content) SetDash(dash []float64, phase float64) {
	c.WriteString(string(Serialize(dash)))
	c.WriteByte(' ')
	c.nums("d", phase)
}
func (c *Content) SetState(name string) {
	c.WriteString(string(Serialize(Name(name))))
	c.Op(" gs")
}
func (c *Content) DrawXObject(name string) {
	c.WriteString(string(Serialize(Name(name))))
	c.Op(" Do")
}
func (c *Content) ShFill(name string) {
	c.WriteString(string(Serialize(Name(name))))
	c.Op(" sh")
}
func (c *Content) BeginText() { c.Op("BT") }
func (c *Content) EndText()   { c.Op("ET") }
func (c *Content) SetFont(name string, size float64) {
	c.WriteString(string(Serialize(Name(name))))
	c.WriteByte(' ')
	c.nums("Tf", size)
}
func (c *Content) SetTextMatrix(a, b, cc, d, e, f float64) { c.nums("Tm", a, b, cc, d, e, f) }
func (c *Content) SetTextRise(r float64)                   { c.nums("Ts", r) }
func (c *Content) SetTextRender(mode int)                  { c.nums("Tr", float64(mode)) }

// ShowGlyphs writes a TJ operator. Items are either []byte (glyph codes,
// written as hex strings) or float64 (position adjustments).
func (c *Content) ShowGlyphs(items []any) {
	c.WriteByte('[')
	for _, it := range items {
		switch it := it.(type) {
		case []byte:
			c.WriteByte('<')
			fmt.Fprintf(c, "%X", it)
			c.WriteByte('>')
		case float64:
			c.WriteString(FormatNumber(it))
		}
	}
	c.Op("] TJ")
}

// TextString returns the PDF syntax of a text string (UTF-16BE when not
// ASCII), for use in content streams.
func TextString(s string) string {
	var b bytes.Buffer
	writeString(&b, s)
	return b.String()
}

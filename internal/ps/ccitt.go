package ps

import (
	"bytes"
	"io"

	"github.com/esoteric-programmer/weasyprint-go/internal/images"
)

// newCCITT decodes CCITTFaxDecode data (read whole).
func newCCITT(src io.ByteReader, parms *Dict) io.Reader {
	geti := func(k string, def int) int {
		if v, ok := parms.GetS(k); ok && v.IsNum() {
			return int(v.Num())
		}
		return def
	}
	getb := func(k string) bool {
		v, ok := parms.GetS(k)
		return ok && v.Kind == KBool && v.BoolV()
	}
	data, _ := io.ReadAll(byteReaderAdapter{src})
	k := geti("K", 0)
	cols := geti("Columns", 1728)
	rows := geti("Rows", 0)
	if rows <= 0 {
		rows = 1 << 20
	}
	comp, opts := 3, 0
	switch {
	case k < 0:
		comp = 4
	case k > 0:
		opts = 1
	}
	if !getb("EndOfLine") && k == 0 {
		comp = 2
	}
	lines, _ := images.DecodeFax(data, cols, rows, comp, opts)
	black1 := getb("BlackIs1")
	rb := (cols + 7) / 8
	out := make([]byte, rb*len(lines))
	for y, row := range lines {
		for x, v := range row {
			if (v == 1) == black1 {
				out[y*rb+x/8] |= 0x80 >> (x % 8)
			}
		}
	}
	return bytes.NewReader(out)
}

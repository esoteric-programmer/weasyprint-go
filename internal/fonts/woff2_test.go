package fonts

import (
	"bytes"
	"encoding/binary"
	"os"
	"reflect"
	"testing"
)

// glyphPoints decodes a TrueType glyph into comparable data: contours,
// instructions and absolute points, or the components of composite glyphs.
func glyphPoints(g []byte) any {
	if len(g) < 10 {
		return nil
	}
	nc := int(int16(binary.BigEndian.Uint16(g)))
	if nc < 0 {
		return g[:10] // bbox, then components
	}
	p := 10
	ends := make([]int, nc)
	for i := range ends {
		ends[i] = int(binary.BigEndian.Uint16(g[p:]))
		p += 2
	}
	il := int(binary.BigEndian.Uint16(g[p:]))
	instr := g[p+2 : p+2+il]
	p += 2 + il
	n := 0
	if nc > 0 {
		n = ends[nc-1] + 1
	}
	flags := make([]byte, 0, n)
	for len(flags) < n {
		f := g[p]
		p++
		flags = append(flags, f)
		if f&8 != 0 {
			for k := 0; k < int(g[p]); k++ {
				flags = append(flags, f)
			}
			p++
		}
	}
	coords := func(short, same byte) []int {
		out := make([]int, n)
		v := 0
		for i, f := range flags {
			switch {
			case f&short != 0:
				d := int(g[p])
				p++
				if f&same == 0 {
					d = -d
				}
				v += d
			case f&same == 0:
				v += int(int16(binary.BigEndian.Uint16(g[p:])))
				p += 2
			}
			out[i] = v
		}
		return out
	}
	xs := coords(2, 0x10)
	ys := coords(4, 0x20)
	on := make([]byte, n)
	for i, f := range flags {
		on[i] = f & 1
	}
	return []any{g[:10], ends, instr, xs, ys, on}
}

func glyphs(t *testing.T, f *Font) []any {
	loca, glyf, head := f.Table("loca"), f.Table("glyf"), f.Table("head")
	long := binary.BigEndian.Uint16(head[50:]) == 1
	var out []any
	n := len(loca)/2 - 1
	if long {
		n = len(loca)/4 - 1
	}
	for i := 0; i < n; i++ {
		var a, b int
		if long {
			a, b = int(binary.BigEndian.Uint32(loca[4*i:])), int(binary.BigEndian.Uint32(loca[4*i+4:]))
		} else {
			a, b = 2*int(binary.BigEndian.Uint16(loca[2*i:])), 2*int(binary.BigEndian.Uint16(loca[2*i+2:]))
		}
		out = append(out, glyphPoints(glyf[a:b]))
	}
	return out
}

func TestWOFF2(t *testing.T) {
	orig, _ := os.ReadFile("testdata/sub.ttf")
	w2, _ := os.ReadFile("testdata/sub.woff2")
	a, err := Parse(orig, 0)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Parse(w2, 0)
	if err != nil {
		t.Fatal(err)
	}
	for tag := range a.tables {
		switch tag {
		case "glyf", "loca", "head":
			continue
		}
		if !bytes.Equal(a.Table(tag), b.Table(tag)) {
			t.Errorf("table %q differs", tag)
		}
	}
	ga, gb := glyphs(t, a), glyphs(t, b)
	if len(ga) != len(gb) {
		t.Fatalf("%d glyphs instead of %d", len(gb), len(ga))
	}
	for i := range ga {
		if !reflect.DeepEqual(ga[i], gb[i]) {
			t.Errorf("glyph %d differs", i)
		}
	}
}

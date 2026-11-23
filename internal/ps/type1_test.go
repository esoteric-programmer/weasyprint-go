package ps

import (
	"fmt"
	"testing"
)

func t1num(v int) []byte {
	switch {
	case v >= -107 && v <= 107:
		return []byte{byte(v + 139)}
	case v >= 108 && v <= 1131:
		v -= 108
		return []byte{byte(v/256 + 247), byte(v % 256)}
	case v >= -1131 && v <= -108:
		v = -v - 108
		return []byte{byte(v/256 + 251), byte(v % 256)}
	}
	return []byte{255, byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)}
}

func encrypt(b []byte, r uint16, prefix int) []byte {
	in := append(make([]byte, prefix), b...)
	out := make([]byte, len(in))
	for i, p := range in {
		c := p ^ byte(r>>8)
		r = (uint16(c)+r)*52845 + 22719
		out[i] = c
	}
	return out
}

func TestType1Font(t *testing.T) {
	// A square glyph: hsbw, rmoveto, rlineto, closepath, endchar; and an
	// accented glyph with seac (StandardEncoding: A = 65, acute = 194).
	var cs []byte
	for _, v := range []int{0, 600} {
		cs = append(cs, t1num(v)...)
	}
	cs = append(cs, 13)
	cs = append(append(append(cs, t1num(100)...), t1num(0)...), 21)
	cs = append(append(append(cs, t1num(400)...), t1num(0)...), 5)
	cs = append(append(append(cs, t1num(0)...), t1num(500)...), 5)
	cs = append(cs, 9, 14)
	var seac []byte
	for _, v := range []int{0, 600, 0, 50, 300, 65, 194} {
		seac = append(seac, t1num(v)...)
	}
	seac = append(seac[:len(t1num(0))+len(t1num(600))], 13)
	for _, v := range []int{0, 50, 300, 65, 194} {
		seac = append(seac, t1num(v)...)
	}
	seac = append(seac, 12, 6)
	body := []byte(`dup /Private 8 dict dup begin /RD {string currentfile exch readstring pop} executeonly def
/ND {noaccess def} executeonly def /NP {noaccess put} executeonly def /lenIV 4 def /Subrs 0 array def
2 index /CharStrings 4 dict dup begin
`)
	nd := encrypt([]byte{139, 139, 13, 14}, 4330, 4)
	body = append(body, []byte(fmt.Sprintf("/.notdef %d RD ", len(nd)))...)
	body = append(body, nd...)
	body = append(body, []byte(" ND\n")...)
	sq := encrypt(cs, 4330, 4)
	body = append(body, []byte(fmt.Sprintf("/A %d RD ", len(sq)))...)
	body = append(body, sq...)
	body = append(body, []byte(" ND\n")...)
	var acs []byte
	acs = append(append(append(acs, t1num(0)...), t1num(200)...), 13)
	acs = append(append(append(acs, t1num(0)...), t1num(600)...), 21)
	acs = append(append(append(acs, t1num(100)...), t1num(0)...), 5)
	acs = append(append(append(acs, t1num(0)...), t1num(100)...), 5)
	acs = append(acs, 9, 14)
	ac := encrypt(acs, 4330, 4)
	body = append(body, []byte(fmt.Sprintf("/acute %d RD ", len(ac)))...)
	body = append(body, ac...)
	body = append(body, []byte(" ND\n")...)
	sc := encrypt(seac, 4330, 4)
	body = append(body, []byte(fmt.Sprintf("/Aacute %d RD ", len(sc)))...)
	body = append(body, sc...)
	body = append(body, []byte(" ND\nend\nend\nreadonly put noaccess put\ndup /FontName get exch definefont pop\nmark currentfile closefile\n")...)
	prog := []byte(`%!PS-AdobeFont-1.0: Test
12 dict begin /FontName /Test def /FontType 1 def /PaintType 0 def /FontMatrix [0.001 0 0 0.001 0 0] readonly def
/FontBBox {0 0 600 600} readonly def /Encoding StandardEncoding def
currentdict end
currentfile eexec
`)
	prog = append(prog, encrypt(body, 55665, 4)...)
	prog = append(prog, []byte("\n0000000000000000000000000000000000000000000000000000000000000000\ncleartomark\n/Test findfont 10 scalefont setfont 0 0 moveto (A) show /Aacute glyphshow currentpoint\n")...)
	in := New(Options{})
	l, err := in.Run(prog)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(l.Ops) != 2 {
		t.Fatalf("%d ops", len(l.Ops))
	}
	x0, y0, x1, y1, _ := l.Ops[0].Path.BBox()
	if x0 != 1 || y0 != 0 || x1 != 5 || y1 != 5 {
		t.Errorf("glyph bbox %v %v %v %v", x0, y0, x1, y1)
	}
	// seac: the accent at (adx, ady) = (50, 300) from the base.
	x0, y0, x1, y1, _ = l.Ops[1].Path.BBox()
	if x0 != 6.5 || y0 != 0 || x1 != 11 || y1 != 10 {
		t.Errorf("seac glyph bbox %v %v %v %v", x0, y0, x1, y1)
	}
	if s := in.ostack; len(s) != 2 || s[0].Num() != 12 {
		t.Errorf("stack %v", s)
	}
}

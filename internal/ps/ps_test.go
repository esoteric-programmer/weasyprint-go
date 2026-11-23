package ps

import (
	"testing"
)

func run(t *testing.T, prog string) (*Interp, *List) {
	t.Helper()
	in := New(Options{Warn: func(s string) { t.Log(s) }})
	l, err := in.Run([]byte(prog))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	return in, l
}

func TestBasics(t *testing.T) {
	in, _ := run(t, `
/sq { dup mul } bind def
3 sq 4 sq add sqrt
[1 2 3] { 2 mul } forall add add
0 1 1 10 { add } for
(abc) length
<< /a 1 /b (x) >> /a get
16#ff 8#10 add
1 2 3 3 1 roll
{ 1 0 div } stopped
(  12.5 ) cvr
/x 7 def x
mark 1 2 counttomark exch pop exch pop exch pop
`)
	want := []float64{5, 12, 55, 3, 1, 263, 3, 1, 2}
	if len(in.ostack) < len(want) {
		t.Fatalf("stack %v", in.ostack)
	}
	for i, w := range want {
		if got := in.ostack[i].Num(); got != w {
			t.Errorf("item %d: %v, want %v", i, got, w)
		}
	}
	rest := in.ostack[len(want):]
	if len(rest) != 4 || !rest[0].BoolV() || rest[1].Num() != 12.5 || rest[2].Num() != 7 || rest[3].Num() != 2 {
		t.Errorf("rest %v", rest)
	}
}

func TestPaint(t *testing.T) {
	_, l := run(t, `
1 0 0 setrgbcolor
newpath 10 10 moveto 100 0 rlineto 0 50 rlineto closepath fill
gsave 2 2 scale 0 setgray 3 setlinewidth 5 5 moveto 20 20 lineto stroke grestore
0 0 1 setrgbcolor 50 50 20 0 360 arc fill
showpage
`)
	if len(l.Ops) != 3 {
		t.Fatalf("%d ops", len(l.Ops))
	}
	if l.Ops[0].Kind != OpFill || l.Ops[0].Paint.Color.V[0] != 1 {
		t.Errorf("op 0: %+v", l.Ops[0])
	}
	if l.Ops[1].Kind != OpStroke || l.Ops[1].CTM[0] != 2 || l.Ops[1].Stroke.Width != 3 {
		t.Errorf("op 1: %+v", l.Ops[1])
	}
	if p := l.Ops[1].Path; p[1].P[0].X != 40 {
		t.Errorf("stroke path in device space: %+v", p)
	}
}

func TestImageAndFilters(t *testing.T) {
	in, l := run(t, `
gsave 10 10 scale
2 2 8 [2 0 0 -2 0 2] currentfile /ASCIIHexDecode filter image
00ff80 40>
grestore
gsave 4 4 true [4 0 0 4 0 0] (\377\000\377\000) imagemask grestore
/s 100 string def currentfile /ASCII85Decode filter s readstring
87cURD]i,"Ebo80~>
pop length
`)
	if len(l.Ops) != 2 || l.Ops[0].Image == nil || l.Ops[0].Image.Data[1] != 255 || !l.Ops[1].Image.Mask {
		t.Fatalf("ops %+v", l.Ops)
	}
	if n := in.ostack[len(in.ostack)-1]; n.Num() != 12 {
		t.Errorf("ASCII85 data: %v bytes", n)
	}
}

func TestType3Font(t *testing.T) {
	in, l := run(t, `
/T3 8 dict begin
/FontType 3 def /FontMatrix [0.001 0 0 0.001 0 0] def /FontBBox [0 0 1000 1000] def
/Encoding 256 array def 0 1 255 { Encoding exch /.notdef put } for Encoding 65 /A put
/BuildChar { 1000 0 0 0 1000 1000 setcachedevice pop pop 0 0 moveto 1000 0 lineto 500 1000 lineto closepath fill } def
currentdict end definefont pop
/T3 findfont 10 scalefont setfont
20 20 moveto (AA) show currentpoint
(A) stringwidth
`)
	if len(l.Ops) != 2 {
		t.Fatalf("%d ops", len(l.Ops))
	}
	s := in.ostack
	if len(s) != 4 || s[0].Num() != 40 || s[2].Num() != 10 {
		t.Errorf("stack %v", s)
	}
}

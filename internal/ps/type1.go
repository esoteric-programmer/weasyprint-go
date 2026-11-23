package ps

import "math"

// t1glyph interprets Type 1 charstrings into a path (glyph space).
type t1glyph struct {
	font       *fontInfo
	path       Path
	x, y       float64
	sbx, sby   float64
	wx, wy     float64
	stack      []float64
	psStack    []float64 // results of othersubrs, for pop
	flex       []Point
	inFlex     bool
	ended      bool
	open       bool
	seacDepth  int
	offX, offY float64 // seac accent offset
}

func (t *t1glyph) moveTo(x, y float64) {
	t.x, t.y = x, y
	if t.inFlex {
		t.flex = append(t.flex, Point{x, y})
		return
	}
	if t.open {
		t.path = append(t.path, Seg{Op: 'Z'})
	}
	t.path = append(t.path, Seg{Op: 'M', P: [3]Point{{x + t.offX, y + t.offY}}})
	t.open = true
}

func (t *t1glyph) lineTo(x, y float64) {
	t.x, t.y = x, y
	t.path = append(t.path, Seg{Op: 'L', P: [3]Point{{x + t.offX, y + t.offY}}})
}

func (t *t1glyph) curveTo(x1, y1, x2, y2, x3, y3 float64) {
	t.x, t.y = x3, y3
	t.path = append(t.path, Seg{Op: 'C', P: [3]Point{{x1 + t.offX, y1 + t.offY}, {x2 + t.offX, y2 + t.offY}, {x3 + t.offX, y3 + t.offY}}})
}

func (t *t1glyph) run(cs []byte, depth int) {
	if depth > 20 || t.ended {
		return
	}
	for i := 0; i < len(cs) && !t.ended; {
		v := int(cs[i])
		i++
		switch {
		case v >= 32 && v <= 246:
			t.stack = append(t.stack, float64(v-139))
			continue
		case v >= 247 && v <= 250:
			if i >= len(cs) {
				return
			}
			t.stack = append(t.stack, float64((v-247)*256+int(cs[i])+108))
			i++
			continue
		case v >= 251 && v <= 254:
			if i >= len(cs) {
				return
			}
			t.stack = append(t.stack, float64(-(v-251)*256-int(cs[i])-108))
			i++
			continue
		case v == 255:
			if i+4 > len(cs) {
				return
			}
			n := int32(uint32(cs[i])<<24 | uint32(cs[i+1])<<16 | uint32(cs[i+2])<<8 | uint32(cs[i+3]))
			t.stack = append(t.stack, float64(n))
			i += 4
			continue
		}
		s := t.stack
		arg := func(k int) float64 {
			if k < len(s) {
				return s[k]
			}
			return 0
		}
		clear := true
		switch v {
		case 1, 3: // hstem, vstem
		case 4: // vmoveto
			t.moveTo(t.x, t.y+arg(0))
		case 5: // rlineto
			t.lineTo(t.x+arg(0), t.y+arg(1))
		case 6: // hlineto
			t.lineTo(t.x+arg(0), t.y)
		case 7: // vlineto
			t.lineTo(t.x, t.y+arg(0))
		case 8: // rrcurveto
			x1, y1 := t.x+arg(0), t.y+arg(1)
			x2, y2 := x1+arg(2), y1+arg(3)
			t.curveTo(x1, y1, x2, y2, x2+arg(4), y2+arg(5))
		case 9: // closepath
			if t.open {
				t.path = append(t.path, Seg{Op: 'Z'})
				t.open = false
			}
		case 10: // callsubr
			if len(s) == 0 {
				break
			}
			n := int(s[len(s)-1])
			t.stack = s[:len(s)-1]
			if n >= 0 && n < len(t.font.subrs) {
				t.run(t.font.subrs[n], depth+1)
			}
			clear = false
		case 11: // return
			return
		case 12:
			if i >= len(cs) {
				return
			}
			e := int(cs[i])
			i++
			switch e {
			case 0, 1, 2: // dotsection, vstem3, hstem3
			case 6: // seac
				t.seac(arg(0), arg(1), arg(2), int(arg(3)), int(arg(4)))
				return
			case 7: // sbw
				t.sbx, t.sby = arg(0), arg(1)
				t.wx, t.wy = arg(2), arg(3)
				t.x, t.y = t.sbx, t.sby
			case 12: // div
				if len(s) >= 2 {
					b := s[len(s)-1]
					a := s[len(s)-2]
					r := 0.0
					if b != 0 {
						r = a / b
					}
					t.stack = append(s[:len(s)-2], r)
				}
				clear = false
			case 16: // callothersubr
				clear = false
				if len(s) < 2 {
					break
				}
				oth := int(s[len(s)-1])
				n := int(s[len(s)-2])
				s = s[:len(s)-2]
				if n < 0 || n > len(s) {
					n = len(s)
				}
				args := append([]float64(nil), s[len(s)-n:]...)
				t.stack = s[:len(s)-n]
				t.othersubr(oth, args)
			case 17: // pop
				clear = false
				if len(t.psStack) > 0 {
					t.stack = append(t.stack, t.psStack[len(t.psStack)-1])
					t.psStack = t.psStack[:len(t.psStack)-1]
				} else {
					t.stack = append(t.stack, 0)
				}
			case 33: // setcurrentpoint
				t.x, t.y = arg(0), arg(1)
			}
		case 13: // hsbw
			t.sbx, t.sby = arg(0), 0
			t.wx, t.wy = arg(1), 0
			t.x, t.y = t.sbx, 0
		case 14: // endchar
			if t.open {
				t.path = append(t.path, Seg{Op: 'Z'})
				t.open = false
			}
			t.ended = true
		case 21: // rmoveto
			t.moveTo(t.x+arg(0), t.y+arg(1))
		case 22: // hmoveto
			t.moveTo(t.x+arg(0), t.y)
		case 30: // vhcurveto
			x1, y1 := t.x, t.y+arg(0)
			x2, y2 := x1+arg(1), y1+arg(2)
			t.curveTo(x1, y1, x2, y2, x2+arg(3), y2)
		case 31: // hvcurveto
			x1, y1 := t.x+arg(0), t.y
			x2, y2 := x1+arg(1), y1+arg(2)
			t.curveTo(x1, y1, x2, y2, x2, y2+arg(3))
		default:
		}
		if clear {
			t.stack = t.stack[:0]
		}
	}
}

func (t *t1glyph) othersubr(n int, args []float64) {
	switch n {
	case 1: // start flex
		t.inFlex = true
		t.flex = t.flex[:0]
	case 2: // flex point: recorded by the moveto
	case 0: // end flex
		t.inFlex = false
		if len(t.flex) >= 7 {
			f := t.flex
			// f[0] is the reference point.
			t.curveTo(f[1].X, f[1].Y, f[2].X, f[2].Y, f[3].X, f[3].Y)
			t.curveTo(f[4].X, f[4].Y, f[5].X, f[5].Y, f[6].X, f[6].Y)
		}
		ex, ey := t.x, t.y
		t.psStack = append(t.psStack, ey, ex)
	case 3: // hint replacement
		t.psStack = append(t.psStack, 3)
	default:
		for i := len(args) - 1; i >= 0; i-- {
			t.psStack = append(t.psStack, args[i])
		}
	}
}

// seac draws an accented character from StandardEncoding components.
func (t *t1glyph) seac(asb, adx, ady float64, bchar, achar int) {
	if t.seacDepth > 1 || bchar < 0 || bchar > 255 || achar < 0 || achar > 255 {
		t.ended = true
		return
	}
	wx, wy := t.wx, t.wy
	base := t.font.charString(standardEncoding[bchar])
	accent := t.font.charString(standardEncoding[achar])
	if t.open {
		t.path = append(t.path, Seg{Op: 'Z'})
		t.open = false
	}
	b := &t1glyph{font: t.font, seacDepth: t.seacDepth + 1}
	if base != nil {
		b.run(base, 0)
	}
	a := &t1glyph{font: t.font, seacDepth: t.seacDepth + 1, offX: adx - asb, offY: ady}
	if accent != nil {
		a.run(accent, 0)
	}
	t.path = append(t.path, b.path...)
	t.path = append(t.path, a.path...)
	t.wx, t.wy = wx, wy
	if base != nil {
		t.wx, t.wy = b.wx, b.wy
	}
	t.ended = true
}

// type1Glyph returns the outline and width of a Type 1 glyph.
func (f *fontInfo) type1Glyph(name string) (*glyph, bool) {
	cs := f.charString(name)
	if cs == nil {
		return nil, false
	}
	t := &t1glyph{font: f}
	t.run(cs, 0)
	if t.open {
		t.path = append(t.path, Seg{Op: 'Z'})
	}
	g := &glyph{path: t.path, wx: t.wx, wy: t.wy}
	if math.IsNaN(g.wx) {
		g.wx = 0
	}
	return g, true
}

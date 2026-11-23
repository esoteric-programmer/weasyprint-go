package ps

import (
	"math"
	"strconv"
	"strings"

	"github.com/esoteric-programmer/weasyprint-go/internal/fonts"
)

// SystemFont is a font of the system used for a font name that the
// program does not define.
type SystemFont = *fonts.Font

// glyph is a glyph outline in glyph space, with its width.
type glyph struct {
	path   Path
	wx, wy float64
}

// fontInfo is the decoded form of a font dictionary.
type fontInfo struct {
	typ  int // FontType (sysFont for system fonts)
	dict *Dict
	// Type 1
	charStrings *Dict
	subrs       [][]byte
	lenIV       int
	paintType   int
	strokeWidth float64
	// Type 42, CIDFontType 2 and system fonts
	sfnt  *fonts.Font
	scale float64 // glyph space units per font unit
	// CIDFontType 0: binary glyph data, CIDMap and font dicts.
	glyphData    []byte
	cidMapOffset int
	fdBytes      int
	fds          []*fontInfo
	cidCount     int
	// CIDFontType 2
	cidMap    Object
	gdBytes   int
	cidOffset int
	glyphs    map[string]*glyph
	gidGlyphs map[int]*glyph
}

const sysFont = 100

type fontBase struct{ info *fontInfo }

// fontOf returns the decoded font of a font dictionary.
func (in *Interp) fontOf(d *Dict) *fontInfo {
	if v, ok := d.GetS("FID"); ok {
		if b, ok := v.Ref.(*fontBase); ok {
			return b.info
		}
	}
	if fi, ok := in.fonts[d]; ok {
		return fi
	}
	fi := in.decodeFont(d)
	in.fonts[d] = fi
	return fi
}

func (in *Interp) decodeFont(d *Dict) *fontInfo {
	fi := &fontInfo{dict: d, lenIV: 4, glyphs: map[string]*glyph{}, gidGlyphs: map[int]*glyph{}, scale: 1}
	if v, ok := d.GetS("FontType"); ok && v.IsNum() {
		fi.typ = int(v.Num())
	}
	if v, ok := d.GetS("CIDFontType"); ok && v.IsNum() {
		fi.typ = 100 + int(v.Num()) // 100 + CIDFontType, below sysFont+
		if fi.typ == 100 {
			fi.typ = 109 // CIDFontType 0
		}
	}
	if v, ok := d.GetS("WGO.sys"); ok {
		if sf, ok := v.Ref.(*fonts.Font); ok {
			fi.typ = sysFont
			fi.sfnt = sf
			fi.scale = 1000 / sf.UnitsPerEm
		}
	}
	if v, ok := d.GetS("PaintType"); ok && v.IsNum() {
		fi.paintType = int(v.Num())
	}
	if v, ok := d.GetS("StrokeWidth"); ok && v.IsNum() {
		fi.strokeWidth = v.Num()
	}
	switch fi.typ {
	case 1:
		if v, ok := d.GetS("CharStrings"); ok && v.Kind == KDict {
			fi.charStrings = v.DictV()
		}
		if v, ok := d.GetS("Private"); ok && v.Kind == KDict {
			p := v.DictV()
			if l, ok := p.GetS("lenIV"); ok && l.IsNum() {
				fi.lenIV = int(l.Num())
			}
			if s, ok := p.GetS("Subrs"); ok && s.Kind == KArray {
				for _, e := range s.Elems() {
					if e.Kind == KString {
						fi.subrs = append(fi.subrs, decryptCharString(e.Bytes(), fi.lenIV))
					} else {
						fi.subrs = append(fi.subrs, nil)
					}
				}
			}
		}
	case 109:
		geti := func(dd *Dict, k string, def int) int {
			if v, ok := dd.GetS(k); ok && v.IsNum() {
				return int(v.Num())
			}
			return def
		}
		if v, ok := d.GetS("GlyphData"); ok && v.Kind == KString {
			fi.glyphData = v.Bytes()
		}
		fi.cidMapOffset = geti(d, "CIDMapOffset", 0)
		fi.fdBytes = geti(d, "FDBytes", 1)
		fi.gdBytes = geti(d, "GDBytes", 2)
		fi.cidCount = geti(d, "CIDCount", 0)
		if v, ok := d.GetS("FDArray"); ok && v.Kind == KArray {
			for _, e := range v.Elems() {
				fd := &fontInfo{typ: 1, lenIV: 4, glyphs: map[string]*glyph{}, gidGlyphs: map[int]*glyph{}}
				if e.Kind == KDict {
					fdd := e.DictV()
					fd.dict = fdd
					if p, ok := fdd.GetS("Private"); ok && p.Kind == KDict {
						fd.lenIV = geti(p.DictV(), "lenIV", 4)
					}
					// Subrs in the glyph data.
					off, sd, n := geti(fdd, "SubrMapOffset", 0), geti(fdd, "SDBytes", 0), geti(fdd, "SubrCount", 0)
					get := func(o int) int {
						v := 0
						for i := 0; i < sd; i++ {
							if o+i < len(fi.glyphData) {
								v = v<<8 | int(fi.glyphData[o+i])
							}
						}
						return v
					}
					for i := 0; i < n && sd > 0; i++ {
						a, b := get(off+i*sd), get(off+(i+1)*sd)
						if a < b && b <= len(fi.glyphData) {
							fd.subrs = append(fd.subrs, decryptCharString(fi.glyphData[a:b], fd.lenIV))
						} else {
							fd.subrs = append(fd.subrs, nil)
						}
					}
				}
				fi.fds = append(fi.fds, fd)
			}
		}
	case 42, 102:
		if v, ok := d.GetS("sfnts"); ok && v.Kind == KArray {
			var data []byte
			for _, e := range v.Elems() {
				b := e.Bytes()
				if len(b)%2 == 1 {
					b = b[:len(b)-1]
				}
				data = append(data, b...)
			}
			if f, err := fonts.Parse(data, 0); err == nil {
				fi.sfnt = f
				fi.scale = 1 / f.UnitsPerEm
			} else {
				in.warn("ps: bad Type 42 font: %v", err)
			}
		}
		if v, ok := d.GetS("CharStrings"); ok && v.Kind == KDict {
			fi.charStrings = v.DictV()
		}
		if fi.typ == 102 {
			fi.gdBytes = 2
			if v, ok := d.GetS("GDBytes"); ok && v.IsNum() {
				fi.gdBytes = int(v.Num())
			}
			if v, ok := d.GetS("CIDMap"); ok {
				fi.cidMap = v
			}
		}
	}
	return fi
}

// charString returns the decrypted charstring of a Type 1 glyph.
func (f *fontInfo) charString(name string) []byte {
	if f.charStrings == nil {
		return nil
	}
	v, ok := f.charStrings.GetS(name)
	if !ok || v.Kind != KString {
		return nil
	}
	return decryptCharString(v.Bytes(), f.lenIV)
}

// sfntGlyph returns the outline of a TrueType/CFF glyph in glyph space.
func (f *fontInfo) sfntGlyph(gid int) *glyph {
	if g, ok := f.gidGlyphs[gid]; ok {
		return g
	}
	g := &glyph{}
	if f.sfnt != nil && gid >= 0 && gid < 65536 {
		segs, _ := f.sfnt.GlyphOutline(uint16(gid))
		g.path = convertOutline(segs, f.scale)
		g.wx = f.sfnt.Advance(uint16(gid)) * f.scale
	}
	f.gidGlyphs[gid] = g
	return g
}

func convertOutline(segs []fonts.OutlineSeg, k float64) Path {
	var p Path
	var cur Point
	for _, s := range segs {
		pt := func(i int) Point { return Point{s.P[i][0] * k, s.P[i][1] * k} }
		switch s.Op {
		case 'M':
			cur = pt(0)
			p = append(p, Seg{Op: 'M', P: [3]Point{cur}})
		case 'L':
			cur = pt(0)
			p = append(p, Seg{Op: 'L', P: [3]Point{cur}})
		case 'Q':
			c, e := pt(0), pt(1)
			p = append(p, Seg{Op: 'C', P: [3]Point{
				{cur.X + 2.0/3*(c.X-cur.X), cur.Y + 2.0/3*(c.Y-cur.Y)},
				{e.X + 2.0/3*(c.X-e.X), e.Y + 2.0/3*(c.Y-e.Y)}, e}})
			cur = e
		case 'C':
			cur = pt(2)
			p = append(p, Seg{Op: 'C', P: [3]Point{pt(0), pt(1), cur}})
		case 'Z':
			p = append(p, Seg{Op: 'Z'})
		}
	}
	return p
}

// nameToRune maps a glyph name to Unicode (Adobe Glyph List rules).
func nameToRune(name string) (rune, bool) {
	if r, ok := aglNames[name]; ok {
		return r, true
	}
	base := name
	if i := strings.IndexByte(base, '.'); i > 0 {
		base = base[:i]
		if r, ok := aglNames[base]; ok {
			return r, true
		}
	}
	if strings.HasPrefix(base, "uni") && len(base) >= 7 {
		if v, err := strconv.ParseUint(base[3:7], 16, 32); err == nil {
			return rune(v), true
		}
	}
	if strings.HasPrefix(base, "u") && len(base) >= 5 && len(base) <= 7 {
		if v, err := strconv.ParseUint(base[1:], 16, 32); err == nil {
			return rune(v), true
		}
	}
	return 0, false
}

// glyphByName returns a glyph of a simple font by name (Type 1, 42 and
// system fonts).
func (in *Interp) glyphByName(f *fontInfo, name string) (*glyph, bool) {
	if g, ok := f.glyphs[name]; ok {
		return g, g != nil
	}
	var g *glyph
	switch f.typ {
	case 1:
		if gg, ok := f.type1Glyph(name); ok {
			g = gg
		} else if gg, ok := f.type1Glyph(".notdef"); ok {
			g = gg
		}
	case 42:
		gid := 0
		if f.charStrings != nil {
			if v, ok := f.charStrings.GetS(name); ok && v.IsNum() {
				gid = int(v.Num())
			} else if r, ok := nameToRune(name); ok && f.sfnt != nil {
				gid = int(f.sfnt.GlyphIndex(r))
			}
		}
		g = f.sfntGlyph(gid)
	case sysFont:
		if name == ".notdef" {
			// Like Type 1 fonts: .notdef draws nothing.
			g = &glyph{}
			break
		}
		gid := 0
		if r, ok := nameToRune(name); ok {
			gid = int(f.sfnt.GlyphIndex(r))
			if gid == 0 && r < 0x100 {
				gid = int(f.sfnt.GlyphIndex(0xF000 + r))
			}
		}
		if gid == 0 && name == "space" {
			gid = int(f.sfnt.GlyphIndex(' '))
		}
		g = f.sfntGlyph(gid)
	}
	f.glyphs[name] = g
	return g, g != nil
}

// ---------------------------------------------------------------- operators

func (in *Interp) registerFonts() {
	d := in.def
	d("definefont", func(in *Interp) {
		font := in.pop()
		key := in.pop()
		v := in.defineFont(key, font)
		in.resourceDict("Font").Put(key, v)
		in.push(v)
	})
	d("undefinefont", func(in *Interp) { in.fontDir.Delete(in.pop()) })
	d("findfont", func(in *Interp) { in.push(in.findFont(in.pop())) })
	d("scalefont", func(in *Interp) {
		s := in.popNum()
		f := in.popDict()
		in.push(DictObj(in.makeFont(f, Matrix{s, 0, 0, s, 0, 0})))
	})
	d("makefont", func(in *Interp) {
		_, m := in.popMatrix()
		f := in.popDict()
		in.push(DictObj(in.makeFont(f, m)))
	})
	d("setfont", func(in *Interp) {
		f := in.pop()
		if f.Kind != KDict {
			typecheck("setfont")
		}
		in.gs.Font = f
	})
	d("currentfont", func(in *Interp) { in.push(in.currentFont()) })
	d("rootfont", func(in *Interp) { in.push(in.currentFont()) })
	d("selectfont", func(in *Interp) {
		var m Matrix
		if t := in.top(); t.Kind == KArray {
			_, m = in.popMatrix()
		} else {
			s := in.popNum()
			m = Matrix{s, 0, 0, s, 0, 0}
		}
		f := in.findFont(in.pop())
		in.gs.Font = DictObj(in.makeFont(f.DictV(), m))
	})
	d("composefont", func(in *Interp) {
		descs := in.popArray()
		cm := in.pop()
		key := in.pop()
		f := NewDict(8)
		f.PutS("FontType", Int(0))
		f.PutS("FMapType", Int(9))
		f.PutS("FontMatrix", matrixArray(identity))
		var cmap Object
		if cm.Kind == KDict {
			cmap = cm
		} else if c, ok := in.findResource("CMap", cm); ok {
			cmap = c
		} else {
			panic(psError{"undefinedresource", cm.Text()})
		}
		f.PutS("CMap", cmap)
		var dep []Object
		for _, e := range descs.Elems() {
			if e.Kind == KDict {
				dep = append(dep, e)
			} else if v, ok := in.findResource("CIDFont", e); ok {
				dep = append(dep, v)
			} else {
				dep = append(dep, in.findFont(e))
			}
		}
		f.PutS("FDepVector", Array(dep))
		enc := make([]Object, len(dep))
		for i := range enc {
			enc[i] = Int(i)
		}
		f.PutS("Encoding", Array(enc))
		v := in.defineFont(key, DictObj(f))
		in.resourceDict("Font").Put(key, v)
		in.push(v)
	})
	d("show", func(in *Interp) { in.showString(in.popString().Bytes(), showOpts{}) })
	d("ashow", func(in *Interp) {
		s := in.popString()
		ax, ay := in.popXY()
		in.showString(s.Bytes(), showOpts{ax: ax, ay: ay})
	})
	d("widthshow", func(in *Interp) {
		s := in.popString()
		c := in.popInt()
		cx, cy := in.popXY()
		in.showString(s.Bytes(), showOpts{cx: cx, cy: cy, char: c, hasChar: true})
	})
	d("awidthshow", func(in *Interp) {
		s := in.popString()
		ax, ay := in.popXY()
		c := in.popInt()
		cx, cy := in.popXY()
		in.showString(s.Bytes(), showOpts{ax: ax, ay: ay, cx: cx, cy: cy, char: c, hasChar: true})
	})
	d("kshow", func(in *Interp) {
		s := in.popString()
		p := in.popProc()
		in.showString(s.Bytes(), showOpts{kproc: &p})
	})
	xyshow := func(mode int) func(*Interp) {
		return func(in *Interp) {
			o := in.pop()
			var nums []float64
			switch o.Kind {
			case KArray:
				nums = numsOf(o)
			case KString:
				nums = decodeNumString(o.Bytes())
			default:
				typecheck("xyshow")
			}
			s := in.popString()
			in.showString(s.Bytes(), showOpts{displ: nums, displMode: mode})
		}
	}
	d("xshow", xyshow(1))
	d("yshow", xyshow(2))
	d("xyshow", xyshow(3))
	d("cshow", func(in *Interp) {
		s := in.popString()
		p := in.popProc()
		in.showString(s.Bytes(), showOpts{cproc: &p})
	})
	d("glyphshow", func(in *Interp) {
		n := in.pop()
		in.showGlyphName(n)
	})
	d("stringwidth", func(in *Interp) {
		s := in.popString()
		w := in.showString(s.Bytes(), showOpts{measure: true})
		in.push(Real(w.X))
		in.push(Real(w.Y))
	})
	d("setcachedevice", func(in *Interp) {
		for i := 0; i < 4; i++ {
			in.pop()
		}
		wx, wy := in.popXY()
		in.setCharWidth(wx, wy, true)
	})
	d("setcachedevice2", func(in *Interp) {
		for i := 0; i < 8; i++ {
			in.pop()
		}
		wx, wy := in.popXY()
		in.setCharWidth(wx, wy, true)
	})
	d("setcharwidth", func(in *Interp) {
		wx, wy := in.popXY()
		in.setCharWidth(wx, wy, false)
	})
	// CMaps (only what CMap resources need).
	d("begincmap", func(in *Interp) {})
	d("usefont", func(in *Interp) { in.pop() })
	d("StartData", func(in *Interp) {
		n := in.popInt()
		kind := in.pop().Text()
		f := in.files[len(in.files)-1]
		data := make([]byte, 0, n)
		if kind == "Hex" {
			h := -1
			for len(data) < n {
				c, err := f.ReadByte()
				if err != nil {
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
				data = append(data, byte(h<<4|v))
				h = -1
			}
			skipEODMarker(f, ">")
		} else {
			// One whitespace precedes the binary data.
			buf := make([]byte, n)
			k := f.readFull(buf)
			data = buf[:k]
		}
		cd := in.currentDict()
		cd.PutS("GlyphData", String(data))
		// StartData ends the CIDFont definition and the CIDInit procset.
		name, _ := cd.GetS("CIDFontName")
		if len(in.dstack) > 3 {
			in.dstack = in.dstack[:len(in.dstack)-1]
		}
		v := in.defineFont(name, DictObj(cd))
		in.resourceDict("CIDFont").Put(name, v)
		if len(in.dstack) > 3 {
			in.dstack = in.dstack[:len(in.dstack)-1]
		}
	})
	d("endcmap", func(in *Interp) {})
	d("usecmap", func(in *Interp) {
		k := in.pop()
		if c, ok := in.findResource("CMap", k); ok && c.Kind == KDict {
			cd := in.currentDict()
			if v, ok := c.DictV().GetS("WGO.ranges"); ok {
				cd.PutS("WGO.ranges", v)
			}
			if v, ok := c.DictV().GetS("WGO.space"); ok {
				cd.PutS("WGO.space", v)
			}
		}
	})
	d("begincodespacerange", func(in *Interp) { in.pop() })
	d("endcodespacerange", func(in *Interp) {
		k := in.markIndexOrBottom()
		vals := append([]Object{}, in.ostack[k:]...)
		in.ostack = in.ostack[:k]
		cd := in.currentDict()
		old, _ := cd.GetS("WGO.space")
		cd.PutS("WGO.space", Array(append(old.Elems(), vals...)))
	})
	for _, n := range []string{"begincidrange", "begincidchar", "beginnotdefrange", "beginnotdefchar", "beginbfrange", "beginbfchar", "beginusematrix"} {
		d(n, func(in *Interp) { in.pop(); in.push(mark) })
	}
	cidRanges := func(char bool) func(*Interp) {
		return func(in *Interp) {
			k := in.markIndex()
			vals := append([]Object{}, in.ostack[k+1:]...)
			in.ostack = in.ostack[:k]
			cd := in.currentDict()
			old, _ := cd.GetS("WGO.ranges")
			el := append([]Object(nil), old.Elems()...)
			if char {
				for i := 0; i+1 < len(vals); i += 2 {
					el = append(el, vals[i], vals[i], vals[i+1])
				}
			} else {
				el = append(el, vals...)
			}
			cd.PutS("WGO.ranges", Array(el))
		}
	}
	d("endcidrange", cidRanges(false))
	d("endcidchar", cidRanges(true))
	for _, n := range []string{"endnotdefrange", "endnotdefchar", "endbfrange", "endbfchar", "endusematrix"} {
		d(n, func(in *Interp) { k := in.markIndex(); in.ostack = in.ostack[:k] })
	}
	// Ghostscript-style "fix" operators some prologs use.
	d(".notdef", func(in *Interp) {})
}

// markIndexOrBottom returns the index of the top mark, or of the bottom
// of the stack (begincodespacerange pops its count, not a mark).
func (in *Interp) markIndexOrBottom() int {
	for i := len(in.ostack) - 1; i >= 0; i-- {
		if in.ostack[i].Kind == KMark {
			return i + 1
		}
	}
	return 0
}

func identityCMap(name string) *Dict {
	if name != "Identity-H" && name != "Identity-V" {
		return nil
	}
	d := NewDict(4)
	d.PutS("CMapName", Name(name))
	d.PutS("CMapType", Int(1))
	d.PutS("WGO.space", Array([]Object{String([]byte{0, 0}), String([]byte{0xff, 0xff})}))
	d.PutS("WGO.ranges", Array([]Object{String([]byte{0, 0}), String([]byte{0xff, 0xff}), Int(0)}))
	if name == "Identity-V" {
		d.PutS("WMode", Int(1))
	}
	return d
}

func (in *Interp) currentFont() Object {
	if in.gs.Font.Kind == KDict {
		return in.gs.Font
	}
	return in.findFont(Name("Courier"))
}

// defineFont registers a font.
func (in *Interp) defineFont(key, font Object) Object {
	if font.Kind != KDict {
		typecheck("definefont")
	}
	d := font.DictV()
	if _, ok := d.GetS("FontMatrix"); !ok {
		if _, cid := d.GetS("CIDFontType"); !cid {
			d.PutS("FontMatrix", matrixArray(Matrix{0.001, 0, 0, 0.001, 0, 0}))
		}
	}
	if _, ok := d.GetS("FID"); !ok {
		d.PutS("FID", Object{Kind: KFontID, Ref: &fontBase{info: in.decodeFont(d)}})
	}
	in.fontDir.Put(key, font)
	return font
}

// findFont returns a font: a defined one, or a system font.
func (in *Interp) findFont(key Object) Object {
	name := key.Text()
	if v, ok := in.fontDir.Get(key); ok {
		return v
	}
	if v, ok := in.resourceDict("Font").Get(key); ok {
		return v
	}
	var sf *fonts.Font
	if in.opts.Font != nil {
		sf = in.opts.Font(name)
		if sf == nil {
			sub := substituteFont(name)
			in.warn("ps: font %s not found, using %s", name, sub)
			sf = in.opts.Font(sub)
		}
	}
	d := NewDict(12)
	d.PutS("FontType", Int(1))
	d.PutS("FontName", Name(name))
	d.PutS("FontMatrix", matrixArray(Matrix{0.001, 0, 0, 0.001, 0, 0}))
	d.PutS("FontBBox", Array([]Object{Int(-200), Int(-250), Int(1200), Int(950)}))
	d.PutS("PaintType", Int(0))
	enc := standardEncoding[:]
	if strings.HasPrefix(name, "Symbol") {
		enc = symbolEncoding[:]
	}
	d.PutS("Encoding", encodingArray(enc))
	cs := NewDict(1)
	cs.PutS(".notdef", String(nil))
	d.PutS("CharStrings", DictObj(cs))
	if sf != nil {
		d.PutS("WGO.sys", internal(sf))
	} else {
		in.warn("ps: font %s not found", name)
	}
	return in.defineFont(key, DictObj(d))
}

// substituteFont chooses a standard font for an unknown font name.
func substituteFont(name string) string {
	l := strings.ToLower(name)
	style := ""
	bold := strings.Contains(l, "bold") || strings.Contains(l, "black") || strings.Contains(l, "heavy")
	italic := strings.Contains(l, "italic") || strings.Contains(l, "oblique")
	switch {
	case strings.Contains(l, "courier") || strings.Contains(l, "mono") || strings.Contains(l, "typewriter"):
		name = "Courier"
		if bold {
			style = "Bold"
		}
		if italic {
			style += "Oblique"
		}
	case strings.Contains(l, "times") || strings.Contains(l, "roman") || strings.Contains(l, "serif") && !strings.Contains(l, "sans"):
		name = "Times"
		if bold {
			style = "Bold"
		}
		if italic {
			style += "Italic"
		}
		if style == "" {
			style = "Roman"
		}
	default:
		name = "Helvetica"
		if bold {
			style = "Bold"
		}
		if italic {
			style += "Oblique"
		}
	}
	if style != "" {
		return name + "-" + style
	}
	return name
}

func encodingArray(names []string) Object {
	el := make([]Object, len(names))
	for i, n := range names {
		if n == "" {
			n = ".notdef"
		}
		el[i] = Name(n)
	}
	return Array(el)
}

// makeFont returns a copy of a font with its matrix transformed.
func (in *Interp) makeFont(f *Dict, m Matrix) *Dict {
	c := f.Copy()
	fm := identity
	if v, ok := f.GetS("FontMatrix"); ok && v.Kind == KArray && v.Len == 6 {
		fm = matrixOf(v)
	}
	c.PutS("FontMatrix", matrixArray(fm.Mul(m)))
	if v, ok := f.GetS("FID"); ok {
		c.PutS("FID", v)
	}
	if v, ok := c.GetS("FontType"); ok && intOf(v) == 0 {
		// Composite fonts: the descendants get the matrix at show time.
	}
	return c
}

// ---------------------------------------------------------------- show

type showOpts struct {
	ax, ay, cx, cy float64
	char           int
	hasChar        bool
	kproc, cproc   *Object
	displ          []float64
	displMode      int // 1 x, 2 y, 3 xy
	charpath       bool
	measure        bool
}

type buildState struct {
	wx, wy  float64
	set     bool
	capture *Path // charpath and stringwidth: collect painted paths
}

func (in *Interp) currentBuild() *buildState {
	if len(in.building) == 0 {
		return nil
	}
	return in.building[len(in.building)-1]
}

func (in *Interp) setCharWidth(wx, wy float64, cache bool) {
	b := in.currentBuild()
	if b == nil {
		return
	}
	b.wx, b.wy, b.set = wx, wy, true
	if cache {
		in.gs.noColor = true
	}
}

func fontMatrix(d *Dict) Matrix {
	if v, ok := d.GetS("FontMatrix"); ok && v.Kind == KArray && v.Len == 6 {
		return matrixOf(v)
	}
	return Matrix{0.001, 0, 0, 0.001, 0, 0}
}

// showString paints (or measures) a string with the current font and
// returns the total displacement (user space).
func (in *Interp) showString(s []byte, o showOpts) Point {
	fontObj := in.currentFont()
	font := fontObj.DictV()
	if !o.measure && !in.gs.HasCP {
		panic(psError{"nocurrentpoint", "show"})
	}
	var start Point
	if in.gs.HasCP {
		x, y := in.userPoint()
		start = Point{x, y}
	}
	pos := start
	items := in.decodeShow(font, s, fontMatrix(font))
	for i, it := range items {
		w := in.drawGlyph(it, pos, o)
		dx, dy := w.X, w.Y
		if o.hasChar && it.code == o.char {
			dx += o.cx
			dy += o.cy
		}
		dx += o.ax
		dy += o.ay
		if o.displ != nil {
			dx, dy = 0, 0
			switch o.displMode {
			case 1:
				if i < len(o.displ) {
					dx = o.displ[i]
				}
			case 2:
				if i < len(o.displ) {
					dy = o.displ[i]
				}
			case 3:
				if 2*i+1 < len(o.displ) {
					dx, dy = o.displ[2*i], o.displ[2*i+1]
				}
			}
		}
		if o.cproc != nil {
			// cshow: the procedure positions and paints the character.
			continue
		}
		pos.X += dx
		pos.Y += dy
		if !o.measure {
			in.moveTo(pos.X, pos.Y)
		}
		if o.kproc != nil && i+1 < len(items) {
			in.push(Int(it.code))
			in.push(Int(items[i+1].code))
			in.execObj(*o.kproc)
			x, y := in.userPoint()
			pos = Point{x, y}
		}
	}
	return Point{pos.X - start.X, pos.Y - start.Y}
}

// showItem is a character to show: a font, its matrix, and a code, a
// glyph name or a CID.
type showItem struct {
	font  *Dict
	fm    Matrix
	code  int
	name  string
	cid   int
	isCID bool
}

// decodeShow splits a string into characters, through composite fonts.
func (in *Interp) decodeShow(font *Dict, s []byte, fm Matrix) []showItem {
	ft := 1
	if v, ok := font.GetS("FontType"); ok && v.IsNum() {
		ft = int(v.Num())
	}
	if ft != 0 {
		enc := encodingOf(font)
		var items []showItem
		for _, c := range s {
			name := ""
			if int(c) < len(enc) {
				name = enc[c]
			}
			items = append(items, showItem{font: font, fm: fm, code: int(c), name: name})
		}
		return items
	}
	// Composite font.
	fmap := 2
	if v, ok := font.GetS("FMapType"); ok && v.IsNum() {
		fmap = int(v.Num())
	}
	var dep []Object
	if v, ok := font.GetS("FDepVector"); ok {
		dep = v.Elems()
	}
	var encIdx []int
	if v, ok := font.GetS("Encoding"); ok && v.Kind == KArray {
		for _, e := range v.Elems() {
			if e.IsNum() {
				encIdx = append(encIdx, int(e.Num()))
			} else {
				encIdx = append(encIdx, 0)
			}
		}
	}
	descend := func(fontNum int) (*Dict, Matrix) {
		k := fontNum
		if k >= 0 && k < len(encIdx) {
			k = encIdx[k]
		}
		if k < 0 || k >= len(dep) || dep[k].Kind != KDict {
			return nil, fm
		}
		dd := dep[k].DictV()
		return dd, fontMatrix(dd).Mul(fm)
	}
	var items []showItem
	switch fmap {
	case 9:
		cm, _ := font.GetS("CMap")
		var cmd *Dict
		if cm.Kind == KDict {
			cmd = cm.DictV()
		}
		dd, dfm := descend(0)
		if dd == nil {
			return nil
		}
		for i := 0; i < len(s); {
			code, n := cmapCode(cmd, s[i:])
			cid := cmapCID(cmd, s[i:i+n])
			items = append(items, showItem{font: dd, fm: dfm, code: code, cid: cid, isCID: true})
			i += n
		}
		return items
	case 2, 4, 5:
		for i := 0; i < len(s); {
			var fontNum, code int
			switch fmap {
			case 2:
				if i+1 >= len(s) {
					return items
				}
				fontNum, code = int(s[i]), int(s[i+1])
				i += 2
			case 4:
				fontNum, code = int(s[i]>>7), int(s[i]&0x7f)
				i++
			case 5:
				if i+1 >= len(s) {
					return items
				}
				v := int(s[i])<<8 | int(s[i+1])
				fontNum, code = v>>7, v&0x7f
				i += 2
			}
			dd, dfm := descend(fontNum)
			if dd == nil {
				continue
			}
			items = append(items, in.decodeShow(dd, []byte{byte(code)}, dfm)...)
		}
		return items
	case 3, 7, 8:
		// Escape and shift mappings.
		cur := 0
		esc := 255
		if v, ok := font.GetS("EscChar"); ok && v.IsNum() {
			esc = int(v.Num())
		}
		for i := 0; i < len(s); i++ {
			c := int(s[i])
			if c == esc && i+1 < len(s) {
				cur = int(s[i+1])
				i++
				continue
			}
			dd, dfm := descend(cur)
			if dd == nil {
				continue
			}
			items = append(items, in.decodeShow(dd, []byte{byte(c)}, dfm)...)
		}
		return items
	}
	in.warn("ps: FMapType %d is not supported", fmap)
	return nil
}

func cmapCode(cm *Dict, s []byte) (int, int) {
	n := 2
	if cm != nil {
		if v, ok := cm.GetS("WGO.space"); ok {
			el := v.Elems()
			for k := 1; k <= 4 && k <= len(s); k++ {
				for j := 0; j+1 < len(el); j += 2 {
					lo, hi := el[j].Bytes(), el[j+1].Bytes()
					if len(lo) != k {
						continue
					}
					in := true
					for b := 0; b < k; b++ {
						if s[b] < lo[b] || s[b] > hi[b] {
							in = false
						}
					}
					if in {
						n = k
						goto found
					}
				}
			}
		}
	}
found:
	if n > len(s) {
		n = len(s)
	}
	code := 0
	for _, b := range s[:n] {
		code = code<<8 | int(b)
	}
	return code, n
}

func cmapCID(cm *Dict, c []byte) int {
	code := 0
	for _, b := range c {
		code = code<<8 | int(b)
	}
	if cm == nil {
		return code
	}
	v, ok := cm.GetS("WGO.ranges")
	if !ok {
		return code
	}
	el := v.Elems()
	for j := 0; j+2 < len(el); j += 3 {
		lo, hi := el[j].Bytes(), el[j+1].Bytes()
		if len(lo) != len(c) {
			continue
		}
		l, h := 0, 0
		for i := range lo {
			l = l<<8 | int(lo[i])
			h = h<<8 | int(hi[i])
		}
		if code >= l && code <= h && el[j+2].IsNum() {
			return int(el[j+2].Num()) + code - l
		}
	}
	return 0
}

func encodingOf(font *Dict) []string {
	v, ok := font.GetS("Encoding")
	if !ok || v.Kind != KArray {
		return standardEncoding[:]
	}
	el := v.Elems()
	out := make([]string, len(el))
	for i, e := range el {
		out[i] = e.Text()
	}
	return out
}

// drawGlyph paints one character at user position pos and returns its
// advance in user space.
func (in *Interp) drawGlyph(it showItem, pos Point, o showOpts) Point {
	font := it.font
	fi := in.fontOf(font)
	fm := it.fm
	g := in.gs
	lin := g.CTM
	lin[4], lin[5] = 0, 0
	dx, dy := g.CTM.Apply(pos.X, pos.Y)
	trm := fm.Mul(lin)
	trm[4] += dx
	trm[5] += dy
	adv := func(wx, wy float64) Point {
		x, y := fm.ApplyD(wx, wy)
		return Point{x, y}
	}
	if o.cproc != nil {
		in.push(Int(it.code))
		w := in.glyphWidth(it, fi)
		x, y := fm.ApplyD(w.X, w.Y)
		in.push(Real(x))
		in.push(Real(y))
		saved := in.gs.Font
		in.gs.Font = DictObj(font)
		in.execObj(*o.cproc)
		in.gs.Font = saved
		return Point{}
	}
	switch {
	case fi.typ == 3 || fi.typ == 101:
		return in.buildType3(it, fi, trm, fm, o)
	case it.isCID && fi.typ == 109:
		gl, fdm := fi.cid0Glyph(it.cid)
		if gl == nil {
			return Point{}
		}
		m := fdm.Mul(trm)
		in.paintGlyph(gl, fi, m, o)
		x, y := fdm.ApplyD(gl.wx, gl.wy)
		return adv(x, y)
	case it.isCID && fi.typ == 102:
		gl := fi.sfntGlyph(fi.cidToGID(it.cid))
		in.paintGlyph(gl, fi, trm, o)
		return adv(gl.wx, gl.wy)
	case it.isCID:
		in.warn("ps: CIDFontType %d is not supported", fi.typ-100)
		return Point{}
	}
	name := it.name
	if name == "" {
		name = ".notdef"
	}
	gl, ok := in.glyphByName(fi, name)
	if !ok {
		return Point{}
	}
	w := Point{gl.wx, gl.wy}
	if md, ok := font.GetS("Metrics"); ok && md.Kind == KDict {
		if v, ok := md.DictV().GetS(name); ok {
			switch {
			case v.IsNum():
				w = Point{v.Num(), 0}
			case v.Kind == KArray && v.Len == 2:
				w = Point{v.Elems()[1].Num(), 0}
			case v.Kind == KArray && v.Len == 4:
				w = Point{v.Elems()[2].Num(), v.Elems()[3].Num()}
			}
		}
	}
	in.paintGlyph(gl, fi, trm, o)
	return adv(w.X, w.Y)
}

// glyphWidth returns the width of a character in glyph space.
func (in *Interp) glyphWidth(it showItem, fi *fontInfo) Point {
	if fi.typ == 3 {
		b := &buildState{capture: &Path{}}
		in.building = append(in.building, b)
		in.gsave()
		func() {
			defer func() {
				in.grestore()
				in.building = in.building[:len(in.building)-1]
			}()
			in.runBuild(it, fi)
		}()
		return Point{b.wx, b.wy}
	}
	if gl, ok := in.glyphByName(fi, it.name); ok {
		return Point{gl.wx, gl.wy}
	}
	return Point{}
}

// cid0Glyph returns a glyph of a CIDFontType 0 font and the matrix of
// its font dict.
func (fi *fontInfo) cid0Glyph(cid int) (*glyph, Matrix) {
	if cid < 0 || cid >= fi.cidCount && fi.cidCount > 0 {
		cid = 0
	}
	n := fi.fdBytes + fi.gdBytes
	entry := func(c int) (int, int, bool) {
		o := fi.cidMapOffset + c*n
		if o+n > len(fi.glyphData) {
			return 0, 0, false
		}
		fd, off := 0, 0
		for i := 0; i < fi.fdBytes; i++ {
			fd = fd<<8 | int(fi.glyphData[o+i])
		}
		for i := 0; i < fi.gdBytes; i++ {
			off = off<<8 | int(fi.glyphData[o+fi.fdBytes+i])
		}
		return fd, off, true
	}
	fdIdx, a, ok1 := entry(cid)
	_, b, ok2 := entry(cid + 1)
	if !ok1 || !ok2 || a >= b || b > len(fi.glyphData) || fdIdx >= len(fi.fds) {
		return nil, identity
	}
	fd := fi.fds[fdIdx]
	fdm := identity
	if fd.dict != nil {
		fdm = fontMatrix(fd.dict)
		if v, ok := fd.dict.GetS("FontMatrix"); !ok || v.Kind != KArray {
			fdm = identity
		}
	}
	if g, ok := fd.gidGlyphs[cid]; ok {
		return g, fdm
	}
	cs := decryptCharString(fi.glyphData[a:b], fd.lenIV)
	t := &t1glyph{font: fd}
	t.run(cs, 0)
	if t.open {
		t.path = append(t.path, Seg{Op: 'Z'})
	}
	g := &glyph{path: t.path, wx: t.wx, wy: t.wy}
	fd.gidGlyphs[cid] = g
	return g, fdm
}

func (fi *fontInfo) cidToGID(cid int) int {
	m := fi.cidMap
	switch m.Kind {
	case KInt:
		return cid + m.Int
	case KString:
		b := m.Bytes()
		off := cid * fi.gdBytes
		if off+fi.gdBytes > len(b) {
			return 0
		}
		g := 0
		for i := 0; i < fi.gdBytes; i++ {
			g = g<<8 | int(b[off+i])
		}
		return g
	case KArray:
		// An array of strings, concatenated.
		off := cid * fi.gdBytes
		for _, e := range m.Elems() {
			b := e.Bytes()
			if off+fi.gdBytes <= len(b) {
				g := 0
				for i := 0; i < fi.gdBytes; i++ {
					g = g<<8 | int(b[off+i])
				}
				return g
			}
			off -= len(b)
			if off < 0 {
				return 0
			}
		}
	case KDict:
		if v, ok := m.DictV().Get(Int(cid)); ok && v.IsNum() {
			return int(v.Num())
		}
		return 0
	}
	return cid
}

// paintGlyph fills (or strokes, or appends to the path) a glyph outline.
func (in *Interp) paintGlyph(gl *glyph, fi *fontInfo, trm Matrix, o showOpts) {
	if o.measure || len(gl.path) == 0 {
		return
	}
	dev := gl.path.Transform(trm)
	if o.charpath {
		in.gs.Path = append(in.gs.Path, dev...)
		return
	}
	if b := in.currentBuild(); b != nil && b.capture != nil {
		*b.capture = append(*b.capture, dev...)
		return
	}
	if fi.paintType == 2 {
		st := in.gs.Stroke
		st.Width = fi.strokeWidth
		st.Dash = nil
		in.emit(Op{Kind: OpStroke, Path: dev, Paint: in.gs.Paint, CTM: trm, Stroke: st})
		return
	}
	in.emit(Op{Kind: OpFill, Path: dev, Paint: in.gs.Paint, CTM: trm})
}

// buildType3 runs the BuildGlyph or BuildChar procedure of a Type 3
// font.
func (in *Interp) buildType3(it showItem, fi *fontInfo, trm, fm Matrix, o showOpts) Point {
	b := &buildState{}
	var captured Path
	if o.measure || o.charpath {
		b.capture = &captured
	}
	in.building = append(in.building, b)
	in.gsave()
	in.gs.CTM = trm
	in.newPath()
	savedFont := in.gs.Font
	in.gs.Font = DictObj(it.font)
	func() {
		defer func() {
			in.grestore()
			in.building = in.building[:len(in.building)-1]
		}()
		in.runBuild(it, fi)
	}()
	in.gs.Font = savedFont
	if o.charpath {
		in.gs.Path = append(in.gs.Path, captured...)
	}
	x, y := fm.ApplyD(b.wx, b.wy)
	return Point{x, y}
}

func (in *Interp) runBuild(it showItem, fi *fontInfo) {
	d := it.font
	if p, ok := d.GetS("BuildGlyph"); ok {
		in.push(DictObj(d))
		if it.isCID {
			in.push(Int(it.cid))
		} else {
			name := it.name
			if name == "" {
				name = ".notdef"
			}
			in.push(Name(name))
		}
		in.execObj(p)
		return
	}
	if p, ok := d.GetS("BuildChar"); ok {
		in.push(DictObj(d))
		in.push(Int(it.code))
		in.execObj(p)
	}
}

// showGlyphName shows a glyph by name (glyphshow).
func (in *Interp) showGlyphName(n Object) {
	fontObj := in.currentFont()
	font := fontObj.DictV()
	x, y := in.userPoint()
	it := showItem{font: font, fm: fontMatrix(font), name: n.Text(), code: 0}
	if n.IsNum() {
		it.cid, it.isCID = int(n.Num()), true
	}
	w := in.drawGlyph(it, Point{x, y}, showOpts{})
	in.moveTo(x+w.X, y+w.Y)
}

var _ = math.Abs

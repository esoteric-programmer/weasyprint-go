package ps

import "math"

// colorSpace is a color space.
type colorSpace struct {
	family string
	n      int
	base   *colorSpace
	hival  int
	lookup []byte // Indexed table
	proc   Object // Indexed lookup procedure, Separation/DeviceN tint transform
	obj    Object // as given to setcolorspace
	all    bool   // Separation /All
	none   bool   // Separation /None
	// range of the components (CIE and ICC spaces), pairs.
	rng []float64
}

var (
	deviceGray = &colorSpace{family: "DeviceGray", n: 1, obj: Name("DeviceGray")}
	deviceRGB  = &colorSpace{family: "DeviceRGB", n: 3, obj: Name("DeviceRGB")}
	deviceCMYK = &colorSpace{family: "DeviceCMYK", n: 4, obj: Name("DeviceCMYK")}
)

// parseColorSpace interprets a color space operand.
func (in *Interp) parseColorSpace(o Object) *colorSpace {
	switch o.Kind {
	case KName:
		switch o.NameV() {
		case "DeviceGray", "CalGray", "G":
			return deviceGray
		case "DeviceRGB", "CalRGB", "RGB":
			return deviceRGB
		case "DeviceCMYK", "CMYK":
			return deviceCMYK
		case "Pattern":
			return &colorSpace{family: "Pattern", n: 1, obj: o}
		}
		if v, ok := in.resourceDict("ColorSpace").Get(o); ok {
			return in.parseColorSpace(v)
		}
		panic(psError{"undefined", o.NameV()})
	case KArray:
		el := o.Elems()
		if len(el) == 0 {
			rangecheck("setcolorspace")
		}
		fam := el[0].Text()
		cs := &colorSpace{family: fam, obj: o}
		switch fam {
		case "DeviceGray", "DeviceRGB", "DeviceCMYK":
			return in.parseColorSpace(el[0])
		case "CIEBasedA", "CalGray":
			cs.n = 1
			cs.rng = dictRange(el, "RangeA", 1)
		case "CIEBasedABC", "CalRGB", "Lab":
			cs.n = 3
			cs.rng = dictRange(el, "RangeABC", 3)
			if fam == "Lab" {
				cs.rng = []float64{0, 100, -100, 100, -100, 100}
				if len(el) > 1 && el[1].Kind == KDict {
					if r, ok := el[1].DictV().GetS("Range"); ok && r.Kind == KArray && r.Len == 4 {
						re := r.Elems()
						cs.rng = []float64{0, 100, re[0].Num(), re[1].Num(), re[2].Num(), re[3].Num()}
					}
				}
			}
		case "CIEBasedDEF":
			cs.n = 3
			cs.rng = dictRange(el, "RangeDEF", 3)
		case "CIEBasedDEFG":
			cs.n = 4
			cs.rng = dictRange(el, "RangeDEFG", 4)
		case "ICCBased":
			cs.n = 3
			if len(el) > 1 {
				var d *Dict
				switch el[1].Kind {
				case KDict:
					d = el[1].DictV()
				}
				if d != nil {
					if v, ok := d.GetS("N"); ok && v.IsNum() {
						cs.n = int(v.Num())
					}
					if v, ok := d.GetS("Range"); ok && v.Kind == KArray {
						for _, e := range v.Elems() {
							cs.rng = append(cs.rng, e.Num())
						}
					}
					if v, ok := d.GetS("Alternate"); ok {
						cs.base = in.parseColorSpace(v)
					}
				}
			}
		case "Indexed":
			if len(el) < 4 {
				rangecheck("setcolorspace")
			}
			cs.n = 1
			cs.base = in.parseColorSpace(el[1])
			cs.hival = intOf(el[2])
			switch el[3].Kind {
			case KString:
				cs.lookup = el[3].Bytes()
			default:
				cs.proc = el[3]
			}
		case "Separation":
			if len(el) < 4 {
				rangecheck("setcolorspace")
			}
			cs.n = 1
			switch el[1].Text() {
			case "All":
				cs.all = true
			case "None":
				cs.none = true
			}
			cs.base = in.parseColorSpace(el[2])
			cs.proc = el[3]
		case "DeviceN":
			if len(el) < 4 {
				rangecheck("setcolorspace")
			}
			cs.n = el[1].Len
			cs.base = in.parseColorSpace(el[2])
			cs.proc = el[3]
			allNone := true
			for _, e := range el[1].Elems() {
				if e.Text() != "None" {
					allNone = false
				}
			}
			cs.none = allNone
		case "Pattern":
			cs.n = 1
			if len(el) > 1 {
				cs.base = in.parseColorSpace(el[1])
			}
		case "DevicePixel":
			cs.n = 1
		default:
			panic(psError{"undefined", fam})
		}
		return cs
	}
	typecheck("setcolorspace")
	return nil
}

func dictRange(el []Object, key string, n int) []float64 {
	if len(el) > 1 && el[1].Kind == KDict {
		if v, ok := el[1].DictV().GetS(key); ok && v.Kind == KArray && v.Len == 2*n {
			var r []float64
			for _, e := range v.Elems() {
				r = append(r, e.Num())
			}
			return r
		}
	}
	return nil
}

// initialColor returns the initial components of a space.
func (cs *colorSpace) initial() []float64 {
	switch cs.family {
	case "DeviceCMYK":
		return []float64{0, 0, 0, 1}
	case "Separation":
		return []float64{1}
	case "DeviceN":
		c := make([]float64, cs.n)
		for i := range c {
			c[i] = 1
		}
		return c
	case "Pattern":
		return nil
	}
	c := make([]float64, cs.n)
	if cs.rng != nil {
		for i := range c {
			if 2*i < len(cs.rng) {
				c[i] = math.Max(0, cs.rng[2*i])
			}
		}
	}
	return c
}

func clamp01(v float64) float64 {
	if v < 0 || math.IsNaN(v) {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// toDevice converts the components of a color to a device color. N = 0
// means that nothing is painted (Separation /None).
func (in *Interp) toDevice(cs *colorSpace, c []float64) Color {
	get := func(i int) float64 {
		if i < len(c) {
			return c[i]
		}
		return 0
	}
	norm := func(i int) float64 {
		v := get(i)
		if 2*i+1 < len(cs.rng) {
			lo, hi := cs.rng[2*i], cs.rng[2*i+1]
			if hi > lo {
				v = (v - lo) / (hi - lo)
			}
		}
		return clamp01(v)
	}
	switch cs.family {
	case "DeviceGray", "CIEBasedA", "CalGray", "DevicePixel":
		return Color{N: 1, V: [4]float64{norm(0)}}
	case "DeviceRGB", "CIEBasedABC", "CalRGB", "CIEBasedDEF":
		return Color{N: 3, V: [4]float64{norm(0), norm(1), norm(2)}}
	case "DeviceCMYK", "CIEBasedDEFG":
		return Color{N: 4, V: [4]float64{norm(0), norm(1), norm(2), norm(3)}}
	case "Lab":
		r, g, b := labToRGB(get(0), get(1), get(2))
		return Color{N: 3, V: [4]float64{r, g, b}}
	case "ICCBased":
		switch cs.n {
		case 1:
			return Color{N: 1, V: [4]float64{norm(0)}}
		case 4:
			return Color{N: 4, V: [4]float64{norm(0), norm(1), norm(2), norm(3)}}
		}
		return Color{N: 3, V: [4]float64{norm(0), norm(1), norm(2)}}
	case "Indexed":
		i := int(math.Floor(get(0) + 0.5))
		if i < 0 {
			i = 0
		}
		if i > cs.hival {
			i = cs.hival
		}
		bn := cs.base.n
		comps := make([]float64, bn)
		if cs.lookup != nil {
			for k := 0; k < bn; k++ {
				if j := i*bn + k; j < len(cs.lookup) {
					comps[k] = float64(cs.lookup[j]) / 255
					if 2*k+1 < len(cs.base.rng) {
						lo, hi := cs.base.rng[2*k], cs.base.rng[2*k+1]
						comps[k] = lo + comps[k]*(hi-lo)
					}
				}
			}
		} else {
			comps = in.callFunc(cs.proc, []float64{float64(i)}, bn)
		}
		return in.toDevice(cs.base, comps)
	case "Separation", "DeviceN":
		if cs.none {
			return Color{}
		}
		if cs.all {
			return Color{N: 1, V: [4]float64{1 - clamp01(get(0))}}
		}
		return in.toDevice(cs.base, in.callFunc(cs.proc, c, cs.base.n))
	}
	return Color{N: 1}
}

// callFunc runs a procedure (or a function dictionary) on inputs and
// returns n outputs.
func (in *Interp) callFunc(f Object, args []float64, n int) []float64 {
	if f.Kind == KDict {
		fn := in.parseFunction(f)
		if fn != nil {
			return fn.eval(args)
		}
		return make([]float64, n)
	}
	base := len(in.ostack)
	for _, a := range args {
		in.push(Real(a))
	}
	in.execObj(f)
	out := make([]float64, n)
	if len(in.ostack) < base+n {
		in.ostack = in.ostack[:min(base, len(in.ostack))]
		return out
	}
	for i := 0; i < n; i++ {
		o := in.ostack[len(in.ostack)-n+i]
		if o.IsNum() {
			out[i] = o.Num()
		}
	}
	in.ostack = in.ostack[:base]
	return out
}

func labToRGB(l, a, b float64) (float64, float64, float64) {
	fy := (l + 16) / 116
	fx := fy + a/500
	fz := fy - b/200
	finv := func(t float64) float64 {
		if t > 6.0/29 {
			return t * t * t
		}
		return 3 * (6.0 / 29) * (6.0 / 29) * (t - 4.0/29)
	}
	// D50 white point, then Bradford to D65 and sRGB.
	x, y, z := 0.9642*finv(fx), finv(fy), 0.8249*finv(fz)
	x, y, z = 0.9555766*x-0.0230393*y+0.0631636*z, -0.0282895*x+1.0099416*y+0.0210077*z, 0.0122982*x-0.0204830*y+1.3299098*z
	r := 3.2404542*x - 1.5371385*y - 0.4985314*z
	g := -0.9692660*x + 1.8760108*y + 0.0415560*z
	bl := 0.0556434*x - 0.2040259*y + 1.0572252*z
	gamma := func(v float64) float64 {
		if v <= 0.0031308 {
			return clamp01(12.92 * v)
		}
		return clamp01(1.055*math.Pow(v, 1/2.4) - 0.055)
	}
	return gamma(r), gamma(g), gamma(bl)
}

// gray, rgb and cmyk conversions of device colors (for current*color).
func (c Color) gray() float64 {
	switch c.N {
	case 3:
		return 0.3*c.V[0] + 0.59*c.V[1] + 0.11*c.V[2]
	case 4:
		return 1 - math.Min(1, 0.3*c.V[0]+0.59*c.V[1]+0.11*c.V[2]+c.V[3])
	}
	return c.V[0]
}

func (c Color) rgb() (float64, float64, float64) {
	switch c.N {
	case 3:
		return c.V[0], c.V[1], c.V[2]
	case 4:
		k := c.V[3]
		return 1 - math.Min(1, c.V[0]+k), 1 - math.Min(1, c.V[1]+k), 1 - math.Min(1, c.V[2]+k)
	}
	return c.V[0], c.V[0], c.V[0]
}

func (c Color) cmyk() (float64, float64, float64, float64) {
	switch c.N {
	case 4:
		return c.V[0], c.V[1], c.V[2], c.V[3]
	case 3:
		cc, m, y := 1-c.V[0], 1-c.V[1], 1-c.V[2]
		k := math.Min(cc, math.Min(m, y))
		return cc - k, m - k, y - k, k
	}
	return 0, 0, 0, 1 - c.V[0]
}

func hsbToRGB(h, s, v float64) (float64, float64, float64) {
	h = clamp01(h) * 6
	s, v = clamp01(s), clamp01(v)
	i := math.Floor(h)
	f := h - i
	p, q, t := v*(1-s), v*(1-s*f), v*(1-s*(1-f))
	switch int(i) % 6 {
	case 0:
		return v, t, p
	case 1:
		return q, v, p
	case 2:
		return p, v, t
	case 3:
		return p, q, v
	case 4:
		return t, p, v
	}
	return v, p, q
}

func rgbToHSB(r, g, b float64) (float64, float64, float64) {
	mx := math.Max(r, math.Max(g, b))
	mn := math.Min(r, math.Min(g, b))
	v := mx
	if mx == 0 {
		return 0, 0, 0
	}
	s := (mx - mn) / mx
	if s == 0 {
		return 0, 0, v
	}
	var h float64
	switch mx {
	case r:
		h = (g - b) / (mx - mn)
	case g:
		h = 2 + (b-r)/(mx-mn)
	default:
		h = 4 + (r-g)/(mx-mn)
	}
	h /= 6
	if h < 0 {
		h++
	}
	return h, s, v
}

// setColor sets the current color in the current space.
func (in *Interp) setColor(c []float64) {
	g := in.gs
	if g.noColor {
		return
	}
	g.Comps = c
	g.Paint = Paint{Color: in.toDevice(g.Space, c)}
}

func (in *Interp) setSpace(cs *colorSpace) {
	g := in.gs
	if g.noColor {
		return
	}
	g.Space = cs
	if cs.family == "Pattern" {
		g.Comps = nil
		g.Paint = Paint{Color: Color{N: 1}}
		return
	}
	in.setColor(cs.initial())
}

package css

import (
	"math"
	"strings"
)

// CSS Color 4 and 5 functions, like tinycss2's color4 and color5 used by
// WeasyPrint: lab(), lch(), oklab(), oklch(), color(), device-cmyk() and
// light-dark().

// LightDark is a light-dark() color, chosen with color-scheme.
type LightDark struct{ Light, Dark Value }

// ColorScheme is the computed value of color-scheme (keywords, without
// "only"); "normal" is the keyword Normal.
type ColorScheme []string

// Choose returns the color of a light-dark() value for a color-scheme.
func (ld LightDark) Choose(scheme Value) Value {
	if cs, ok := scheme.(ColorScheme); ok {
		for _, k := range cs {
			if k == "dark" {
				return ld.Dark
			}
			if k == "light" {
				return ld.Light
			}
		}
	}
	return ld.Light
}

func parseColorScheme(ts []Token, _ *ParseCtx) Value {
	ts = RemoveWS(ts)
	if t, ok := single(ts); ok && t.Type == TIdent {
		switch t.Value {
		case "normal":
			return Keyword("normal")
		case "only":
			return nil
		}
		return ColorScheme{t.Value}
	}
	var out ColorScheme
	only := false
	for i, t := range ts {
		if t.Type != TIdent || t.Value == "normal" {
			return nil
		}
		if t.Value == "only" {
			if only || (i != 0 && i != len(ts)-1) {
				return nil
			}
			only = true
			continue
		}
		out = append(out, t.Value)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// colorFunctionArgs splits the arguments of the space-separated syntax
// into components and an optional alpha value.
func colorFunctionArgs(ts []Token) (args []Token, alpha float64, ok bool) {
	ts = RemoveWS(ts)
	alpha = 1
	for i, t := range ts {
		if t.IsDelim("/") {
			if len(ts) != i+2 {
				return nil, 0, false
			}
			if alpha, ok = alphaValue(ts[i+1]); !ok {
				return nil, 0, false
			}
			return ts[:i], alpha, true
		}
		if t.Type == TComma {
			return nil, 0, false
		}
	}
	return ts, alpha, true
}

// component returns a number or percentage (scaled by pct), 0 for none.
func component(t Token, pct float64) (float64, bool) {
	switch t.Type {
	case TNumber:
		return t.Num, true
	case TPercentage:
		return t.Num * pct, true
	case TIdent:
		return 0, t.Value == "none"
	}
	return 0, false
}

func hueComponent(t Token) (float64, bool) {
	if t.Type == TIdent && t.Value == "none" {
		return 0, true
	}
	h, ok := parseHue(t)
	if !ok {
		return 0, false
	}
	return math.Mod(math.Mod(h, 360)+360, 360), true
}

// parseColor4Function parses the Color 4 and 5 functions, or returns
// false for other functions.
func parseColor4Function(t Token) (Value, bool, bool) {
	name := strings.ToLower(t.Value)
	switch name {
	case "lab", "lch", "oklab", "oklch":
		args, alpha, ok := colorFunctionArgs(t.Children)
		if !ok || len(args) != 3 {
			return nil, false, true
		}
		var c [3]float64
		switch name {
		case "lab", "oklab":
			lp, abp := 1.0, 1.25
			if name == "oklab" {
				lp, abp = 0.01, 0.004
			}
			for i, a := range args {
				p := abp
				if i == 0 {
					p = lp
				}
				if c[i], ok = component(a, p); !ok {
					return nil, false, true
				}
			}
			if name == "lab" {
				return labColor(c[0], c[1], c[2], alpha), true, true
			}
			return oklabColor(c[0], c[1], c[2], alpha), true, true
		default:
			lp, cp := 1.0, 1.5
			if name == "oklch" {
				lp, cp = 0.01, 0.004
			}
			if c[0], ok = component(args[0], lp); !ok {
				return nil, false, true
			}
			if c[1], ok = component(args[1], cp); !ok {
				return nil, false, true
			}
			if c[2], ok = hueComponent(args[2]); !ok {
				return nil, false, true
			}
			a := c[1] * math.Cos(c[2]*math.Pi/180)
			b := c[1] * math.Sin(c[2]*math.Pi/180)
			if name == "lch" {
				return labColor(c[0], a, b, alpha), true, true
			}
			return oklabColor(c[0], a, b, alpha), true, true
		}
	case "color":
		args, alpha, ok := colorFunctionArgs(t.Children)
		if !ok || len(args) < 2 || args[0].Type != TIdent {
			return nil, false, true
		}
		space := strings.ToLower(args[0].Value)
		var c []float64
		for _, a := range args[1:] {
			v, ok := component(a, 0.01)
			if !ok {
				return nil, false, true
			}
			c = append(c, v)
		}
		if strings.HasPrefix(args[0].Value, "--") {
			// Color of an @color-profile, with an sRGB approximation.
			col := Color{A: alpha, Space: args[0].Value}
			copy(col.C[:], c)
			if len(c) >= 3 {
				col.R, col.G, col.B = clamp01(c[0]), clamp01(c[1]), clamp01(c[2])
			}
			return col, true, true
		}
		if len(c) != 3 {
			return nil, false, true
		}
		if col, ok := predefinedColor(space, c[0], c[1], c[2], alpha); ok {
			return col, true, true
		}
		return nil, false, true
	case "device-cmyk":
		ts := RemoveWS(t.Children)
		var args []Token
		alpha := 1.0
		ok := true
		if len(ts) == 7 && ts[1].Type == TComma && ts[3].Type == TComma && ts[5].Type == TComma {
			// Legacy syntax: numbers only.
			args = []Token{ts[0], ts[2], ts[4], ts[6]}
			for _, a := range args {
				if a.Type != TNumber {
					return nil, false, true
				}
			}
		} else if args, alpha, ok = colorFunctionArgs(t.Children); !ok || len(args) != 4 {
			return nil, false, true
		}
		var c [4]float64
		for i, a := range args {
			if a.Type == TIdent {
				return nil, false, true
			}
			v, ok := component(a, 0.01)
			if !ok {
				return nil, false, true
			}
			c[i] = clamp01(v)
		}
		k := c[3]
		return Color{
			R: (1 - c[0]) * (1 - k), G: (1 - c[1]) * (1 - k), B: (1 - c[2]) * (1 - k), A: alpha,
			Space: "device-cmyk", C: c,
		}, true, true
	case "light-dark":
		parts := SplitOnComma(t.Children)
		if len(parts) != 2 {
			return nil, false, true
		}
		var colors [2]Value
		for i, p := range parts {
			p = RemoveWS(p)
			if len(p) != 1 {
				return nil, false, true
			}
			c, ok := parseColorToken(p[0])
			if !ok {
				return nil, false, true
			}
			colors[i] = c
		}
		return LightDark{colors[0], colors[1]}, true, true
	}
	return nil, false, false
}

// Conversions, from CSS Color 4 sample code (as in tinycss2).

var (
	d50 = [3]float64{0.3457 / 0.3585, 1, (1 - 0.3457 - 0.3585) / 0.3585}
	d65 = [3]float64{0.3127 / 0.3290, 1, (1 - 0.3127 - 0.3290) / 0.3290}
)

const (
	labKappa   = 24389.0 / 27
	labEpsilon = 216.0 / 24389
)

func labToXYZ(l, a, b float64, d [3]float64) [3]float64 {
	f1 := (l + 16) / 116
	f0 := a/500 + f1
	f2 := f1 - b/200
	x := (116*f0 - 16) / labKappa
	if f0*f0*f0 > labEpsilon {
		x = f0 * f0 * f0
	}
	y := l / labKappa
	if l > labKappa*labEpsilon {
		y = math.Pow((l+16)/116, 3)
	}
	z := (116*f2 - 16) / labKappa
	if f2*f2*f2 > labEpsilon {
		z = f2 * f2 * f2
	}
	return [3]float64{x * d[0], y * d[1], z * d[2]}
}

func xyzToLab(xyz [3]float64, d [3]float64) (float64, float64, float64) {
	f := func(v float64) float64 {
		if v > labEpsilon {
			return math.Cbrt(v)
		}
		return (labKappa*v + 16) / 116
	}
	f0, f1, f2 := f(xyz[0]/d[0]), f(xyz[1]/d[1]), f(xyz[2]/d[2])
	return 116*f1 - 16, 500 * (f0 - f1), 200 * (f1 - f2)
}

func oklabToXYZ(l, a, b float64) [3]float64 {
	toLMS := [3][3]float64{
		{0.99999999845051981432, 0.39633779217376785678, 0.21580375806075880339},
		{1.0000000088817607767, -0.1055613423236563494, -0.063854174771705903402},
		{1.0000000546724109177, -0.089484182094965759684, -1.2914855378640917399},
	}
	toXYZ := [3][3]float64{
		{1.2268798733741557, -0.5578149965554813, 0.28139105017721583},
		{-0.04057576262431372, 1.1122868293970594, -0.07171106666151701},
		{-0.07637294974672142, -0.4214933239627914, 1.5869240244272418},
	}
	lab := [3]float64{l, a, b}
	var lms [3]float64
	for i := range lms {
		for j := range lab {
			lms[i] += toLMS[i][j] * lab[j]
		}
		lms[i] = lms[i] * lms[i] * lms[i]
	}
	var xyz [3]float64
	for i := range xyz {
		for j := range lms {
			xyz[i] += toXYZ[i][j] * lms[j]
		}
	}
	return xyz
}

func mulMat(m [3][3]float64, v [3]float64) [3]float64 {
	return [3]float64{
		m[0][0]*v[0] + m[0][1]*v[1] + m[0][2]*v[2],
		m[1][0]*v[0] + m[1][1]*v[1] + m[1][2]*v[2],
		m[2][0]*v[0] + m[2][1]*v[1] + m[2][2]*v[2],
	}
}

func invMat(m [3][3]float64) [3][3]float64 {
	a, b, c := m[0][0], m[0][1], m[0][2]
	d, e, f := m[1][0], m[1][1], m[1][2]
	g, h, i := m[2][0], m[2][1], m[2][2]
	det := a*(e*i-f*h) - b*(d*i-f*g) + c*(d*h-e*g)
	return [3][3]float64{
		{(e*i - f*h) / det, (c*h - b*i) / det, (b*f - c*e) / det},
		{(f*g - d*i) / det, (a*i - c*g) / det, (c*d - a*f) / det},
		{(d*h - e*g) / det, (b*g - a*h) / det, (a*e - b*d) / det},
	}
}

// rgbToXYZMatrix computes the RGB → XYZ matrix of primaries and white.
func rgbToXYZMatrix(rx, ry, gx, gy, bx, by float64, white [3]float64) [3][3]float64 {
	col := func(x, y float64) [3]float64 { return [3]float64{x / y, 1, (1 - x - y) / y} }
	r, g, b := col(rx, ry), col(gx, gy), col(bx, by)
	p := [3][3]float64{{r[0], g[0], b[0]}, {r[1], g[1], b[1]}, {r[2], g[2], b[2]}}
	s := mulMat(invMat(p), white)
	for i := 0; i < 3; i++ {
		for j := 0; j < 3; j++ {
			p[i][j] *= s[j]
		}
	}
	return p
}

// bradford adapts XYZ from one white point to another.
func bradford(xyz, from, to [3]float64) [3]float64 {
	m := [3][3]float64{{0.8951, 0.2664, -0.1614}, {-0.7502, 1.7135, 0.0367}, {0.0389, -0.0685, 1.0296}}
	src, dst := mulMat(m, from), mulMat(m, to)
	cone := mulMat(m, xyz)
	for i := range cone {
		cone[i] *= dst[i] / src[i]
	}
	return mulMat(invMat(m), cone)
}

var srgbToXYZ = rgbToXYZMatrix(0.64, 0.33, 0.30, 0.60, 0.15, 0.06, d65)

func srgbGamma(v float64) float64 {
	sign := 1.0
	if v < 0 {
		sign, v = -1, -v
	}
	if v > 0.0031308 {
		return sign * (1.055*math.Pow(v, 1/2.4) - 0.055)
	}
	return sign * 12.92 * v
}

func srgbLinear(v float64) float64 {
	sign := 1.0
	if v < 0 {
		sign, v = -1, -v
	}
	if v > 0.04045 {
		return sign * math.Pow((v+0.055)/1.055, 2.4)
	}
	return sign * v / 12.92
}

// xyzToSRGB converts D65 XYZ to clamped sRGB.
func xyzToSRGB(xyz [3]float64) (float64, float64, float64) {
	lin := mulMat(invMat(srgbToXYZ), xyz)
	return clamp01(srgbGamma(lin[0])), clamp01(srgbGamma(lin[1])), clamp01(srgbGamma(lin[2]))
}

func labColor(l, a, b, alpha float64) Color {
	xyz := bradford(labToXYZ(l, a, b, d50), d50, d65)
	r, g, bl := xyzToSRGB(xyz)
	return Color{R: r, G: g, B: bl, A: alpha, Space: "lab-d50", C: [4]float64{l, a, b}}
}

func oklabColor(l, a, b, alpha float64) Color {
	xyz := oklabToXYZ(l, a, b)
	r, g, bl := xyzToSRGB(xyz)
	L, A, B := xyzToLab(xyz, d65)
	return Color{R: r, G: g, B: bl, A: alpha, Space: "lab-d65", C: [4]float64{L, A, B}}
}

// predefinedColor converts color() in a predefined color space.
func predefinedColor(space string, c0, c1, c2, alpha float64) (Color, bool) {
	var xyz [3]float64
	switch space {
	case "srgb":
		return Color{R: clamp01(c0), G: clamp01(c1), B: clamp01(c2), A: alpha}, true
	case "srgb-linear":
		xyz = mulMat(srgbToXYZ, [3]float64{c0, c1, c2})
	case "display-p3":
		m := rgbToXYZMatrix(0.680, 0.320, 0.265, 0.690, 0.150, 0.060, d65)
		xyz = mulMat(m, [3]float64{srgbLinear(c0), srgbLinear(c1), srgbLinear(c2)})
	case "a98-rgb":
		f := func(v float64) float64 { return math.Copysign(math.Pow(math.Abs(v), 563.0/256), v) }
		m := rgbToXYZMatrix(0.64, 0.33, 0.21, 0.71, 0.15, 0.06, d65)
		xyz = mulMat(m, [3]float64{f(c0), f(c1), f(c2)})
	case "prophoto-rgb":
		f := func(v float64) float64 {
			if math.Abs(v) <= 16.0/512 {
				return v / 16
			}
			return math.Copysign(math.Pow(math.Abs(v), 1.8), v)
		}
		m := rgbToXYZMatrix(0.734699, 0.265301, 0.159597, 0.840403, 0.036598, 0.000105, d50)
		xyz = bradford(mulMat(m, [3]float64{f(c0), f(c1), f(c2)}), d50, d65)
	case "rec2020":
		const a, b = 1.09929682680944, 0.018053968510807
		f := func(v float64) float64 {
			if math.Abs(v) < b*4.5 {
				return v / 4.5
			}
			return math.Copysign(math.Pow((math.Abs(v)+a-1)/a, 1/0.45), v)
		}
		m := rgbToXYZMatrix(0.708, 0.292, 0.170, 0.797, 0.131, 0.046, d65)
		xyz = mulMat(m, [3]float64{f(c0), f(c1), f(c2)})
	case "xyz", "xyz-d65":
		xyz = [3]float64{c0, c1, c2}
		r, g, bl := xyzToSRGB(xyz)
		L, A, B := xyzToLab(xyz, d65)
		return Color{R: r, G: g, B: bl, A: alpha, Space: "lab-d65", C: [4]float64{L, A, B}}, true
	case "xyz-d50":
		r, g, bl := xyzToSRGB(bradford([3]float64{c0, c1, c2}, d50, d65))
		L, A, B := xyzToLab([3]float64{c0, c1, c2}, d50)
		return Color{R: r, G: g, B: bl, A: alpha, Space: "lab-d50", C: [4]float64{L, A, B}}, true
	default:
		return Color{}, false
	}
	r, g, bl := xyzToSRGB(xyz)
	return Color{R: r, G: g, B: bl, A: alpha}, true
}

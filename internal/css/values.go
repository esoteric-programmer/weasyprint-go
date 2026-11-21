package css

import (
	"math"
	"strconv"
	"strings"

	"github.com/esoteric-programmer/weasyprint-go/internal/fetch"
)

// Value is a specified or computed CSS value.
type Value any

// Keyword is an identifier value.
type Keyword string

// Number is a unitless number.
type Number float64

// Str is a string value.
type Str string

// URL is an absolute URL value.
type URL string

// Length is a dimension. After computation Unit is "px", "%" or "calc"
// (V px + Pct percent).
type Length struct {
	V    float64
	Unit string
	Pct  float64
	calc *calcNode
}

// Px returns a computed pixel length.
func Px(v float64) Length { return Length{V: v, Unit: "px"} }

// Pct returns a percentage.
func Percent(v float64) Length { return Length{V: v, Unit: "%"} }

// IsPercent returns whether the length depends on a percentage base.
func (l Length) IsPercent() bool { return l.Unit == "%" || l.Unit == "calc" }

// Resolve returns the length in px for a given percentage base.
func (l Length) Resolve(base float64) float64 {
	switch l.Unit {
	case "%":
		return l.V * base / 100
	case "calc":
		if l.calc != nil {
			// min(), max() or clamp() mixing percentages and lengths,
			// with lengths already computed to px.
			lin, _ := l.calc.evalBase(func(x Length) float64 { return x.V }, base, true)
			return lin.px + lin.num
		}
		return l.V + l.Pct*base/100
	}
	return l.V
}

// Color is an RGBA color with components in [0, 1].
type Color struct{ R, G, B, A float64 }

// Transparent is the transparent color.
var Transparent = Color{0, 0, 0, 0}

// IsTransparent reports whether the color is fully transparent.
func (c Color) IsTransparent() bool { return c.A <= 0 }

// ContentItem is an item of the content, bookmark-label or string-set
// properties.
type ContentItem struct {
	Kind  string // string, url, counter, counters, attr, open-quote, close-quote, no-open-quote, no-close-quote, string-ref, content, target-counter, target-counters, target-text, leader, element, image
	Str   string // string, url, separator, leader
	Name  string // counter name, attribute, string name
	Style Value  // counter style (Keyword or *CounterStyle)
	Which string // for string() and content()
	Image Value  // gradient
	Ref   []Token
}

// CounterOp is an item of counter-reset/increment/set.
type CounterOp struct {
	Name  string
	Value int
}

// StringSet is an item of string-set.
type StringSet struct {
	Name    string
	Content []ContentItem
}

// Position is a background-position-like value.
type Position struct {
	XOrigin string // left or right
	X       Length
	YOrigin string // top or bottom
	Y       Length
}

// BgSize is a background-size value: Keyword (cover/contain) or a pair.
type BgSize struct {
	Keyword string
	W, H    Value // Length or Keyword("auto")
}

// Repeat is a background-repeat value.
type Repeat [2]string

// Gradient is a linear or radial gradient.
type Gradient struct {
	Radial    bool
	Repeating bool
	Angle     float64 // radians, for linear gradients
	ToCorner  [2]string
	Shape     string // circle or ellipse
	Size      string // closest-side, farthest-corner, ...
	SizeLen   [2]Length
	SizeIsLen bool
	Position  Position
	Stops     []ColorStop
}

// ColorStop is a gradient color stop.
type ColorStop struct {
	Color Value // Color or Keyword("currentcolor")
	Pos   *Length
	// Hint is the color hint between the previous stop and this one.
	Hint *Length
}

// Shadow is a box-shadow / text-shadow item.
type Shadow struct {
	X, Y, Blur, Spread Length
	Color              Value
	Inset              bool
}

// Transform is a transform function.
type Transform struct {
	Name string
	Args []Value
}

// FontFeature is a font-feature-settings item.
type FontFeature struct {
	Tag   string
	Value int
}

// ---------------------------------------------------------------- parsing

// ParseCtx carries information needed to validate values.
type ParseCtx struct {
	BaseURL string
}

var lengthUnits = map[string]bool{
	"px": true, "pt": true, "pc": true, "in": true, "cm": true, "mm": true,
	"q": true, "em": true, "ex": true, "ch": true, "rem": true, "vw": true,
	"vh": true, "vmin": true, "vmax": true, "lh": true, "rlh": true,
	"cap": true, "ic": true, "rex": true, "rch": true,
}

// absolute unit conversion to px
var unitToPx = map[string]float64{
	"px": 1, "pt": 4.0 / 3, "pc": 16, "in": 96, "cm": 96 / 2.54,
	"mm": 96 / 25.4, "q": 96 / 101.6,
}

func parseLengthToken(t Token, negative, percent bool) (Length, bool) {
	switch t.Type {
	case TDimension:
		if !lengthUnits[t.Value] {
			return Length{}, false
		}
		if !negative && t.Num < 0 {
			return Length{}, false
		}
		return Length{V: t.Num, Unit: t.Value}, true
	case TPercentage:
		if !percent || (!negative && t.Num < 0) {
			return Length{}, false
		}
		return Length{V: t.Num, Unit: "%"}, true
	case TNumber:
		if t.Num == 0 {
			return Length{V: 0, Unit: "px"}, true
		}
	case TFunction:
		if t.Value == "calc" || t.Value == "min" || t.Value == "max" || t.Value == "clamp" {
			n := parseCalc(t)
			if n == nil {
				return Length{}, false
			}
			return Length{Unit: "calc", calc: n}, true
		}
	}
	return Length{}, false
}

func single(ts []Token) (Token, bool) {
	if len(ts) == 1 {
		return ts[0], true
	}
	return Token{}, false
}

func parseColorToken(t Token) (Value, bool) {
	switch t.Type {
	case TIdent:
		switch t.Value {
		case "currentcolor":
			return Keyword("currentcolor"), true
		case "transparent":
			return Transparent, true
		}
		if c, ok := namedColors[t.Value]; ok {
			return Color{float64(c[0]) / 255, float64(c[1]) / 255, float64(c[2]) / 255, 1}, true
		}
		if c, ok := systemColors[t.Value]; ok {
			return c, true
		}
	case THash:
		return parseHexColor(t.Value)
	case TFunction:
		return parseColorFunction(t)
	}
	return nil, false
}

var systemColors = map[string]Color{
	"canvas": {1, 1, 1, 1}, "canvastext": {0, 0, 0, 1}, "linktext": {0, 0, 0.933, 1},
	"visitedtext": {0.333, 0.102, 0.545, 1}, "activetext": {1, 0, 0, 1},
	"buttonface": {0.941, 0.941, 0.941, 1}, "buttontext": {0, 0, 0, 1},
	"buttonborder": {0.463, 0.463, 0.463, 1}, "field": {1, 1, 1, 1},
	"fieldtext": {0, 0, 0, 1}, "highlight": {0.71, 0.835, 1, 1},
	"highlighttext": {0, 0, 0, 1}, "graytext": {0.427, 0.427, 0.427, 1},
	"mark": {1, 1, 0, 1}, "marktext": {0, 0, 0, 1},
}

func parseHexColor(h string) (Value, bool) {
	hexv := func(s string) (float64, bool) {
		v, err := strconv.ParseUint(s, 16, 8)
		return float64(v) / 255, err == nil
	}
	switch len(h) {
	case 3, 4:
		var c [4]float64
		c[3] = 1
		for i := 0; i < len(h); i++ {
			v, ok := hexv(string([]byte{h[i], h[i]}))
			if !ok {
				return nil, false
			}
			c[i] = v
		}
		return Color{c[0], c[1], c[2], c[3]}, true
	case 6, 8:
		var c [4]float64
		c[3] = 1
		for i := 0; i < len(h)/2; i++ {
			v, ok := hexv(h[2*i : 2*i+2])
			if !ok {
				return nil, false
			}
			c[i] = v
		}
		return Color{c[0], c[1], c[2], c[3]}, true
	}
	return nil, false
}

func colorArgs(ts []Token) ([]Token, bool) {
	// Returns 3 or 4 tokens, supporting both comma and space syntax.
	ts = StripWS(ts)
	hasComma := false
	for _, t := range ts {
		if t.Type == TComma {
			hasComma = true
		}
	}
	var out []Token
	if hasComma {
		for _, part := range SplitOnComma(ts) {
			p := RemoveWS(part)
			if len(p) != 1 {
				return nil, false
			}
			out = append(out, p[0])
		}
	} else {
		var alpha []Token
		main := RemoveWS(ts)
		for i, t := range main {
			if t.IsDelim("/") {
				alpha = main[i+1:]
				main = main[:i]
				break
			}
		}
		out = append(out, main...)
		if alpha != nil {
			if len(alpha) != 1 {
				return nil, false
			}
			out = append(out, alpha[0])
		}
	}
	if len(out) != 3 && len(out) != 4 {
		return nil, false
	}
	return out, true
}

func alphaValue(t Token) (float64, bool) {
	switch t.Type {
	case TNumber:
		return clamp01(t.Num), true
	case TPercentage:
		return clamp01(t.Num / 100), true
	case TIdent:
		if t.Value == "none" {
			return 0, true
		}
	}
	return 0, false
}

func clamp01(f float64) float64 { return math.Max(0, math.Min(1, f)) }

func parseColorFunction(t Token) (Value, bool) {
	args, ok := colorArgs(t.Children)
	if !ok {
		return nil, false
	}
	a := 1.0
	if len(args) == 4 {
		a, ok = alphaValue(args[3])
		if !ok {
			return nil, false
		}
	}
	switch t.Value {
	case "rgb", "rgba":
		var c [3]float64
		for i := 0; i < 3; i++ {
			switch args[i].Type {
			case TNumber:
				c[i] = clamp01(args[i].Num / 255)
			case TPercentage:
				c[i] = clamp01(args[i].Num / 100)
			case TIdent:
				if args[i].Value != "none" {
					return nil, false
				}
			default:
				return nil, false
			}
		}
		return Color{c[0], c[1], c[2], a}, true
	case "hsl", "hsla":
		h, ok := parseHue(args[0])
		if !ok {
			return nil, false
		}
		var sl [2]float64
		for i := 1; i < 3; i++ {
			switch args[i].Type {
			case TPercentage, TNumber:
				sl[i-1] = clamp01(args[i].Num / 100)
			default:
				return nil, false
			}
		}
		r, g, b := hslToRGB(h, sl[0], sl[1])
		return Color{r, g, b, a}, true
	case "hwb":
		h, ok := parseHue(args[0])
		if !ok {
			return nil, false
		}
		var wb [2]float64
		for i := 1; i < 3; i++ {
			switch args[i].Type {
			case TPercentage, TNumber:
				wb[i-1] = clamp01(args[i].Num / 100)
			default:
				return nil, false
			}
		}
		w, bl := wb[0], wb[1]
		if w+bl >= 1 {
			g := w / (w + bl)
			return Color{g, g, g, a}, true
		}
		r, g, b := hslToRGB(h, 1, 0.5)
		f := func(c float64) float64 { return c*(1-w-bl) + w }
		return Color{f(r), f(g), f(b), a}, true
	}
	return nil, false
}

func parseHue(t Token) (float64, bool) {
	switch t.Type {
	case TNumber:
		return t.Num, true
	case TDimension:
		if v, ok := angleToRad(t); ok {
			return v * 180 / math.Pi, true
		}
	}
	return 0, false
}

func hslToRGB(h, s, l float64) (float64, float64, float64) {
	h = math.Mod(h, 360)
	if h < 0 {
		h += 360
	}
	h /= 360
	var m2 float64
	if l <= 0.5 {
		m2 = l * (s + 1)
	} else {
		m2 = l + s - l*s
	}
	m1 := l*2 - m2
	hue := func(h float64) float64 {
		if h < 0 {
			h++
		}
		if h > 1 {
			h--
		}
		switch {
		case h*6 < 1:
			return m1 + (m2-m1)*h*6
		case h*2 < 1:
			return m2
		case h*3 < 2:
			return m1 + (m2-m1)*(2.0/3-h)*6
		}
		return m1
	}
	return hue(h + 1.0/3), hue(h), hue(h - 1.0/3)
}

func angleToRad(t Token) (float64, bool) {
	if t.Type == TNumber && t.Num == 0 {
		return 0, true
	}
	if t.Type != TDimension {
		return 0, false
	}
	switch t.Value {
	case "deg":
		return t.Num * math.Pi / 180, true
	case "rad":
		return t.Num, true
	case "grad":
		return t.Num * math.Pi / 200, true
	case "turn":
		return t.Num * 2 * math.Pi, true
	}
	return 0, false
}

func parseURLToken(t Token, ctx *ParseCtx) (string, bool) {
	switch t.Type {
	case TURL:
		return fetch.Resolve(ctx.BaseURL, t.Value), true
	case TFunction:
		if t.Value == "url" {
			args := RemoveWS(t.Children)
			if len(args) >= 1 && args[0].Type == TString {
				return fetch.Resolve(ctx.BaseURL, args[0].Value), true
			}
		}
	}
	return "", false
}

func parseImage(t Token, ctx *ParseCtx) (Value, bool) {
	if t.IsIdent("none") {
		return Keyword("none"), true
	}
	if u, ok := parseURLToken(t, ctx); ok {
		return URL(u), true
	}
	if t.Type == TFunction {
		switch t.Value {
		case "linear-gradient", "repeating-linear-gradient":
			g, ok := parseLinearGradient(t.Children)
			if ok {
				g.Repeating = strings.HasPrefix(t.Value, "repeating")
				return g, true
			}
		case "radial-gradient", "repeating-radial-gradient":
			g, ok := parseRadialGradient(t.Children)
			if ok {
				g.Repeating = strings.HasPrefix(t.Value, "repeating")
				return g, true
			}
		}
	}
	return nil, false
}

func parseColorStops(groups [][]Token) ([]ColorStop, bool) {
	var stops []ColorStop
	var hint *Length
	for _, g := range groups {
		g = RemoveWS(g)
		if len(g) == 0 {
			return nil, false
		}
		// Color hint: a lone length between two color stops.
		if len(g) == 1 {
			if l, ok := parseLengthToken(g[0], true, true); ok {
				if len(stops) == 0 || hint != nil {
					return nil, false
				}
				hint = &l
				continue
			}
		}
		c, ok := parseColorToken(g[0])
		rest := g[1:]
		if !ok && len(g) >= 2 {
			c, ok = parseColorToken(g[len(g)-1])
			rest = g[:len(g)-1]
		}
		if !ok {
			return nil, false
		}
		if len(rest) == 0 {
			stops = append(stops, ColorStop{Color: c, Hint: hint})
			hint = nil
			continue
		}
		for _, r := range rest {
			l, ok := parseLengthToken(r, true, true)
			if !ok {
				return nil, false
			}
			ll := l
			stops = append(stops, ColorStop{Color: c, Pos: &ll, Hint: hint})
			hint = nil
		}
	}
	return stops, len(stops) >= 1 && hint == nil
}

func parseLinearGradient(args []Token) (*Gradient, bool) {
	groups := SplitOnComma(args)
	g := &Gradient{Angle: math.Pi} // to bottom
	first := RemoveWS(groups[0])
	if len(first) >= 1 {
		if a, ok := angleToRad(first[0]); ok && len(first) == 1 {
			g.Angle = a
			groups = groups[1:]
		} else if first[0].IsIdent("to") {
			var sides []string
			for _, t := range first[1:] {
				if t.Type != TIdent {
					return nil, false
				}
				sides = append(sides, t.Value)
			}
			if len(sides) == 1 {
				switch sides[0] {
				case "top":
					g.Angle = 0
				case "right":
					g.Angle = math.Pi / 2
				case "bottom":
					g.Angle = math.Pi
				case "left":
					g.Angle = 3 * math.Pi / 2
				default:
					return nil, false
				}
			} else if len(sides) == 2 {
				var h, v string
				for _, s := range sides {
					switch s {
					case "top", "bottom":
						v = s
					case "left", "right":
						h = s
					}
				}
				if h == "" || v == "" {
					return nil, false
				}
				g.ToCorner = [2]string{h, v}
			} else {
				return nil, false
			}
			groups = groups[1:]
		}
	}
	stops, ok := parseColorStops(groups)
	if !ok {
		return nil, false
	}
	g.Stops = stops
	return g, true
}

func parseRadialGradient(args []Token) (*Gradient, bool) {
	groups := SplitOnComma(args)
	g := &Gradient{Radial: true, Shape: "ellipse", Size: "farthest-corner",
		Position: Position{"left", Percent(50), "top", Percent(50)}}
	first := RemoveWS(groups[0])
	isConfig := false
	if len(first) > 0 {
		if _, ok := parseColorToken(first[0]); !ok {
			isConfig = true
		} else if len(first) > 0 && first[0].Type == TIdent && (first[0].Value == "circle" || first[0].Value == "ellipse") {
			isConfig = true
		}
	}
	if isConfig {
		toks := first
		var posToks []Token
		for i, t := range toks {
			if t.IsIdent("at") {
				posToks = toks[i+1:]
				toks = toks[:i]
				break
			}
		}
		var lens []Length
		for _, t := range toks {
			if t.Type == TIdent {
				switch t.Value {
				case "circle", "ellipse":
					g.Shape = t.Value
				case "closest-side", "closest-corner", "farthest-side", "farthest-corner":
					g.Size = t.Value
				default:
					return nil, false
				}
			} else if l, ok := parseLengthToken(t, false, true); ok {
				lens = append(lens, l)
			} else {
				return nil, false
			}
		}
		if len(lens) == 1 {
			g.Shape = "circle"
			g.SizeIsLen = true
			g.SizeLen = [2]Length{lens[0], lens[0]}
		} else if len(lens) == 2 {
			g.Shape = "ellipse"
			g.SizeIsLen = true
			g.SizeLen = [2]Length{lens[0], lens[1]}
		}
		if posToks != nil {
			p, ok := parsePosition(posToks)
			if !ok {
				return nil, false
			}
			g.Position = p
		}
		groups = groups[1:]
	}
	stops, ok := parseColorStops(groups)
	if !ok {
		return nil, false
	}
	g.Stops = stops
	return g, true
}

// parsePosition parses a <position> (1 to 4 values).
func parsePosition(ts []Token) (Position, bool) {
	ts = RemoveWS(ts)
	center := Percent(50)
	kwPos := map[string]Length{"left": Percent(0), "center": center, "right": Percent(100), "top": Percent(0), "bottom": Percent(100)}
	switch len(ts) {
	case 1:
		t := ts[0]
		if l, ok := parseLengthToken(t, true, true); ok {
			return Position{"left", l, "top", center}, true
		}
		if t.Type == TIdent {
			switch t.Value {
			case "left", "right":
				return Position{"left", kwPos[t.Value], "top", center}, true
			case "top", "bottom":
				return Position{"left", center, "top", kwPos[t.Value]}, true
			case "center":
				return Position{"left", center, "top", center}, true
			}
		}
	case 2:
		a, b := ts[0], ts[1]
		var x, y Length
		var okx, oky bool
		// Keywords may be in either order.
		if a.Type == TIdent && (a.Value == "top" || a.Value == "bottom") {
			a, b = b, a
		} else if b.Type == TIdent && (b.Value == "left" || b.Value == "right") {
			a, b = b, a
		}
		if l, ok := parseLengthToken(a, true, true); ok {
			x, okx = l, true
		} else if a.Type == TIdent && (a.Value == "left" || a.Value == "right" || a.Value == "center") {
			x, okx = kwPos[a.Value], true
		}
		if l, ok := parseLengthToken(b, true, true); ok {
			y, oky = l, true
		} else if b.Type == TIdent && (b.Value == "top" || b.Value == "bottom" || b.Value == "center") {
			y, oky = kwPos[b.Value], true
		}
		if okx && oky {
			return Position{"left", x, "top", y}, true
		}
	case 3, 4:
		// Keyword + offset pairs.
		p := Position{"left", center, "top", center}
		i := 0
		seenX, seenY := false, false
		for i < len(ts) {
			t := ts[i]
			if t.Type != TIdent {
				return Position{}, false
			}
			var off *Length
			if i+1 < len(ts) {
				if l, ok := parseLengthToken(ts[i+1], true, true); ok {
					off = &l
				}
			}
			switch t.Value {
			case "left", "right":
				if seenX {
					return Position{}, false
				}
				seenX = true
				p.XOrigin = t.Value
				if off != nil {
					p.X = *off
					i++
				} else {
					p.X = Percent(0)
				}
			case "top", "bottom":
				if seenY {
					return Position{}, false
				}
				seenY = true
				p.YOrigin = t.Value
				if off != nil {
					p.Y = *off
					i++
				} else {
					p.Y = Percent(0)
				}
			case "center":
			default:
				return Position{}, false
			}
			i++
		}
		return p, true
	}
	return Position{}, false
}

// ------------------------------------------------------------------- calc

type calcNode struct {
	op          byte // '+', '-', '*', '/', 0 for leaf, 'm' min, 'M' max, 'c' clamp
	left, right *calcNode
	args        []*calcNode
	leaf        Token
}

func parseCalc(t Token) *calcNode {
	switch t.Value {
	case "calc":
		return parseCalcSum(StripWS(t.Children))
	case "min", "max", "clamp":
		var args []*calcNode
		for _, part := range SplitOnComma(t.Children) {
			n := parseCalcSum(StripWS(part))
			if n == nil {
				return nil
			}
			args = append(args, n)
		}
		op := byte('m')
		if t.Value == "max" {
			op = 'M'
		} else if t.Value == "clamp" {
			op = 'c'
			if len(args) != 3 {
				return nil
			}
		}
		return &calcNode{op: op, args: args}
	}
	return nil
}

func parseCalcSum(ts []Token) *calcNode {
	// Split on top-level + and - surrounded by whitespace.
	var terms [][]Token
	var ops []byte
	start := 0
	for i := 0; i < len(ts); i++ {
		if ts[i].Type == TDelim && (ts[i].Value == "+" || ts[i].Value == "-") && i > 0 && ts[i-1].Type == TWhitespace {
			terms = append(terms, StripWS(ts[start:i]))
			ops = append(ops, ts[i].Value[0])
			start = i + 1
		}
	}
	terms = append(terms, StripWS(ts[start:]))
	node := parseCalcProduct(terms[0])
	if node == nil {
		return nil
	}
	for i, op := range ops {
		r := parseCalcProduct(terms[i+1])
		if r == nil {
			return nil
		}
		node = &calcNode{op: op, left: node, right: r}
	}
	return node
}

func parseCalcProduct(ts []Token) *calcNode {
	ts = RemoveWS(ts)
	if len(ts) == 0 {
		return nil
	}
	node := parseCalcValue(ts[0])
	if node == nil {
		return nil
	}
	for i := 1; i+1 < len(ts); i += 2 {
		if !(ts[i].IsDelim("*") || ts[i].IsDelim("/")) {
			return nil
		}
		r := parseCalcValue(ts[i+1])
		if r == nil {
			return nil
		}
		node = &calcNode{op: ts[i].Value[0], left: node, right: r}
	}
	if len(ts)%2 == 0 {
		return nil
	}
	return node
}

func parseCalcValue(t Token) *calcNode {
	switch t.Type {
	case TNumber, TPercentage:
		return &calcNode{leaf: t}
	case TDimension:
		if lengthUnits[t.Value] {
			return &calcNode{leaf: t}
		}
	case TParenBlock:
		return parseCalcSum(StripWS(t.Children))
	case TFunction:
		return parseCalc(t)
	}
	return nil
}

// linear is px + pct% (+ number for pure numbers)
type linear struct {
	px, pct, num float64
	isNum        bool
	// deferred is set when min(), max() or clamp() compare values with
	// percentages, which can only be resolved with the percentage base.
	deferred bool
}

func (n *calcNode) eval(toPx func(Length) float64) (linear, bool) {
	return n.evalBase(toPx, 0, false)
}

// withPx returns a copy of the node with lengths converted to px.
func (n *calcNode) withPx(toPx func(Length) float64) *calcNode {
	c := *n
	if n.op == 0 {
		if n.leaf.Type == TDimension {
			c.leaf = Token{Type: TDimension, Num: toPx(Length{V: n.leaf.Num, Unit: n.leaf.Value}), Value: "px"}
		}
		return &c
	}
	if n.left != nil {
		c.left = n.left.withPx(toPx)
	}
	if n.right != nil {
		c.right = n.right.withPx(toPx)
	}
	c.args = nil
	for _, a := range n.args {
		c.args = append(c.args, a.withPx(toPx))
	}
	return &c
}

// evalBase evaluates the node; percentages are resolved against base when
// hasBase is set.
func (n *calcNode) evalBase(toPx func(Length) float64, base float64, hasBase bool) (linear, bool) {
	if n.op == 0 {
		t := n.leaf
		switch t.Type {
		case TNumber:
			return linear{num: t.Num, isNum: true}, true
		case TPercentage:
			if hasBase {
				return linear{px: t.Num * base / 100}, true
			}
			return linear{pct: t.Num}, true
		case TDimension:
			return linear{px: toPx(Length{V: t.Num, Unit: t.Value})}, true
		}
		return linear{}, false
	}
	if n.op == 'm' || n.op == 'M' || n.op == 'c' {
		// Only resolvable without percentages.
		var vals []linear
		deferred := false
		for _, a := range n.args {
			v, ok := a.evalBase(toPx, base, hasBase)
			if !ok {
				return linear{}, false
			}
			deferred = deferred || v.deferred || v.pct != 0
			vals = append(vals, v)
		}
		if deferred {
			return linear{deferred: true}, true
		}
		pick := vals[0]
		switch n.op {
		case 'm':
			for _, v := range vals[1:] {
				if v.px+v.num < pick.px+pick.num {
					pick = v
				}
			}
		case 'M':
			for _, v := range vals[1:] {
				if v.px+v.num > pick.px+pick.num {
					pick = v
				}
			}
		case 'c':
			lo, val, hi := vals[0], vals[1], vals[2]
			pick = val
			if val.px+val.num > hi.px+hi.num {
				pick = hi
			}
			if pick.px+pick.num < lo.px+lo.num {
				pick = lo
			}
		}
		return pick, true
	}
	l, ok1 := n.left.evalBase(toPx, base, hasBase)
	r, ok2 := n.right.evalBase(toPx, base, hasBase)
	if !ok1 || !ok2 {
		return linear{}, false
	}
	if l.deferred || r.deferred {
		return linear{deferred: true, isNum: l.isNum && r.isNum}, true
	}
	switch n.op {
	case '+':
		return linear{l.px + r.px, l.pct + r.pct, l.num + r.num, l.isNum && r.isNum, false}, true
	case '-':
		return linear{l.px - r.px, l.pct - r.pct, l.num - r.num, l.isNum && r.isNum, false}, true
	case '*':
		if r.isNum {
			return linear{l.px * r.num, l.pct * r.num, l.num * r.num, l.isNum, false}, true
		}
		if l.isNum {
			return linear{r.px * l.num, r.pct * l.num, r.num * l.num, r.isNum, false}, true
		}
	case '/':
		if r.isNum && r.num != 0 {
			return linear{l.px / r.num, l.pct / r.num, l.num / r.num, l.isNum, false}, true
		}
	}
	return linear{}, false
}

// ParseColor parses a CSS color string. currentColor is reported with ok
// true and current set.
func ParseColor(s string) (c Color, current bool, ok bool) {
	ts := RemoveWS(StripWS(Tokenize(s)))
	if len(ts) != 1 {
		return Color{}, false, false
	}
	v, ok := parseColorToken(ts[0])
	if !ok {
		return Color{}, false, false
	}
	if k, isKw := v.(Keyword); isKw && k == "currentcolor" {
		return Color{}, true, true
	}
	return v.(Color), false, true
}

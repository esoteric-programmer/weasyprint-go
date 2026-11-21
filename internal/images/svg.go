package images

import (
	"errors"
	"math"
	"strconv"
	"strings"

	"github.com/esoteric-programmer/weasyprint-go/internal/css"
	"github.com/esoteric-programmer/weasyprint-go/internal/html"
)

// SVG is a parsed SVG image.
type SVG struct {
	Root          *html.Node
	Width, Height float64 // intrinsic size in px (0 if unknown)
	ViewBox       [4]float64
	HasViewBox    bool
	URL           string
	IDs           map[string]*html.Node
	Rules         []SVGRule
	fetch         func(string) *Image
}

// SVGRule is a CSS rule of an SVG <style> element.
type SVGRule struct {
	Selectors []*css.Selector
	Decls     []css.Declaration
	Order     int
}

// Fetch loads an image referenced by the SVG.
func (s *SVG) Fetch(url string) *Image {
	if s.fetch == nil {
		return nil
	}
	return s.fetch(url)
}

// SetFetcher sets the function used to load referenced images.
func (s *SVG) SetFetcher(f func(string) *Image) { s.fetch = f }

// ParseSVG parses an SVG document.
func ParseSVG(data []byte, url string) (*SVG, error) {
	doc := html.Parse(html.DecodeBytes(data, ""))
	var root *html.Node
	doc.Walk(func(n *html.Node) bool {
		if root != nil {
			return false
		}
		if n.Type == html.ElementNode && strings.EqualFold(n.Tag, "svg") {
			root = n
			return false
		}
		return true
	})
	if root == nil {
		return nil, errors.New("no <svg> element")
	}
	s := &SVG{Root: root, URL: url, IDs: map[string]*html.Node{}}
	root.Walk(func(n *html.Node) bool {
		if n.Type == html.ElementNode {
			if id := n.Get("id"); id != "" {
				if _, ok := s.IDs[id]; !ok {
					s.IDs[id] = n
				}
			}
			if strings.EqualFold(n.Tag, "style") {
				for _, r := range css.ParseStylesheetRules(css.Tokenize(n.TextContent())) {
					if r.AtKeyword != "" {
						continue
					}
					sels := css.ParseSelectorList(css.StripWS(r.Prelude))
					if sels == nil {
						continue
					}
					decls, _ := css.ParseDeclarations(r.Content)
					s.Rules = append(s.Rules, SVGRule{sels, decls, len(s.Rules)})
				}
			}
		}
		return true
	})
	if vb := Attr(root, "viewBox"); vb != "" {
		nums := ParseNumbers(vb)
		if len(nums) == 4 && nums[2] >= 0 && nums[3] >= 0 {
			copy(s.ViewBox[:], nums)
			s.HasViewBox = true
		}
	}
	w, wok := absLength(Attr(root, "width"))
	h, hok := absLength(Attr(root, "height"))
	switch {
	case wok && hok:
		s.Width, s.Height = w, h
	case wok && s.HasViewBox && s.ViewBox[2] > 0:
		s.Width, s.Height = w, w*s.ViewBox[3]/s.ViewBox[2]
	case hok && s.HasViewBox && s.ViewBox[3] > 0:
		s.Width, s.Height = h*s.ViewBox[2]/s.ViewBox[3], h
	case !wok && !hok && s.HasViewBox && Attr(root, "width") == "" && Attr(root, "height") == "":
		// Only a ratio.
	}
	return s, nil
}

// Attr returns an attribute, matching the name case-insensitively.
func Attr(n *html.Node, name string) string {
	if v, ok := n.Attr(name); ok {
		return v
	}
	lower := strings.ToLower(name)
	for _, a := range n.Attrs {
		if strings.ToLower(a.Name) == lower {
			return a.Value
		}
	}
	return ""
}

func absLength(s string) (float64, bool) {
	s = strings.TrimSpace(s)
	if s == "" || strings.HasSuffix(s, "%") {
		return 0, false
	}
	v, ok := ParseLength(s, 0, 16)
	return v, ok
}

// ParseLength parses an SVG length; percentages refer to ref.
func ParseLength(s string, ref, fontSize float64) (float64, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	units := []struct {
		suffix string
		f      float64
	}{{"px", 1}, {"pt", 4.0 / 3}, {"pc", 16}, {"mm", 96 / 25.4}, {"cm", 96 / 2.54}, {"in", 96}, {"em", 0}, {"ex", 0}, {"%", 0}}
	for _, u := range units {
		if strings.HasSuffix(strings.ToLower(s), u.suffix) {
			v, err := strconv.ParseFloat(strings.TrimSpace(s[:len(s)-len(u.suffix)]), 64)
			if err != nil {
				return 0, false
			}
			switch u.suffix {
			case "em":
				return v * fontSize, true
			case "ex":
				return v * fontSize / 2, true
			case "%":
				return v * ref / 100, true
			}
			return v * u.f, true
		}
	}
	v, err := strconv.ParseFloat(s, 64)
	return v, err == nil
}

// ParseNumbers parses a list of numbers separated by spaces/commas.
func ParseNumbers(s string) []float64 {
	var out []float64
	i := 0
	for i < len(s) {
		for i < len(s) && (s[i] == ' ' || s[i] == ',' || s[i] == '\t' || s[i] == '\n' || s[i] == '\r') {
			i++
		}
		if i >= len(s) {
			break
		}
		j := scanNumber(s, i)
		if j == i {
			i++
			continue
		}
		v, err := strconv.ParseFloat(s[i:j], 64)
		if err == nil {
			out = append(out, v)
		}
		i = j
	}
	return out
}

func scanNumber(s string, i int) int {
	j := i
	if j < len(s) && (s[j] == '+' || s[j] == '-') {
		j++
	}
	digits := false
	for j < len(s) && s[j] >= '0' && s[j] <= '9' {
		j++
		digits = true
	}
	if j < len(s) && s[j] == '.' {
		j++
		for j < len(s) && s[j] >= '0' && s[j] <= '9' {
			j++
			digits = true
		}
	}
	if !digits {
		return i
	}
	if j < len(s) && (s[j] == 'e' || s[j] == 'E') {
		k := j + 1
		if k < len(s) && (s[k] == '+' || s[k] == '-') {
			k++
		}
		if k < len(s) && s[k] >= '0' && s[k] <= '9' {
			for k < len(s) && s[k] >= '0' && s[k] <= '9' {
				k++
			}
			j = k
		}
	}
	return j
}

// PathSeg is an absolute path segment: M, L, C or Z.
type PathSeg struct {
	Op  byte
	Pts []float64
}

// ParsePath parses SVG path data into absolute M/L/C/Z segments.
func ParsePath(d string) []PathSeg {
	var out []PathSeg
	var cx, cy, sx, sy float64 // current point and subpath start
	var lastCtrl [2]float64
	var lastOp byte
	i := 0
	var cmd byte
	readNums := func(n int) ([]float64, bool) {
		vals := make([]float64, 0, n)
		for len(vals) < n {
			for i < len(d) && (d[i] == ' ' || d[i] == ',' || d[i] == '\t' || d[i] == '\n' || d[i] == '\r') {
				i++
			}
			if i >= len(d) {
				return nil, false
			}
			// Arc flags may be written without separators.
			if cmd == 'a' || cmd == 'A' {
				if (len(vals) == 3 || len(vals) == 4) && (d[i] == '0' || d[i] == '1') {
					vals = append(vals, float64(d[i]-'0'))
					i++
					continue
				}
			}
			j := scanNumber(d, i)
			if j == i {
				return nil, false
			}
			v, err := strconv.ParseFloat(d[i:j], 64)
			if err != nil {
				return nil, false
			}
			vals = append(vals, v)
			i = j
		}
		return vals, true
	}
	for i < len(d) {
		for i < len(d) && (d[i] == ' ' || d[i] == ',' || d[i] == '\t' || d[i] == '\n' || d[i] == '\r') {
			i++
		}
		if i >= len(d) {
			break
		}
		c := d[i]
		if strings.IndexByte("MmLlHhVvCcSsQqTtAaZz", c) >= 0 {
			cmd = c
			i++
		} else if cmd == 0 {
			break
		}
		rel := cmd >= 'a'
		var ox, oy float64
		if rel {
			ox, oy = cx, cy
		}
		switch cmd {
		case 'Z', 'z':
			out = append(out, PathSeg{Op: 'Z'})
			cx, cy = sx, sy
			lastOp = 'Z'
			cmd = 0
			continue
		case 'M', 'm':
			v, ok := readNums(2)
			if !ok {
				return out
			}
			cx, cy = ox+v[0], oy+v[1]
			sx, sy = cx, cy
			out = append(out, PathSeg{'M', []float64{cx, cy}})
			if cmd == 'M' {
				cmd = 'L'
			} else {
				cmd = 'l'
			}
			lastOp = 'M'
			continue
		case 'L', 'l':
			v, ok := readNums(2)
			if !ok {
				return out
			}
			cx, cy = ox+v[0], oy+v[1]
			out = append(out, PathSeg{'L', []float64{cx, cy}})
		case 'H', 'h':
			v, ok := readNums(1)
			if !ok {
				return out
			}
			cx = ox + v[0]
			if !rel {
				cx = v[0]
			}
			out = append(out, PathSeg{'L', []float64{cx, cy}})
		case 'V', 'v':
			v, ok := readNums(1)
			if !ok {
				return out
			}
			if rel {
				cy = oy + v[0]
			} else {
				cy = v[0]
			}
			out = append(out, PathSeg{'L', []float64{cx, cy}})
		case 'C', 'c':
			v, ok := readNums(6)
			if !ok {
				return out
			}
			x1, y1, x2, y2, x, y := ox+v[0], oy+v[1], ox+v[2], oy+v[3], ox+v[4], oy+v[5]
			out = append(out, PathSeg{'C', []float64{x1, y1, x2, y2, x, y}})
			lastCtrl = [2]float64{x2, y2}
			cx, cy = x, y
			lastOp = 'C'
			continue
		case 'S', 's':
			v, ok := readNums(4)
			if !ok {
				return out
			}
			x1, y1 := cx, cy
			if lastOp == 'C' {
				x1, y1 = 2*cx-lastCtrl[0], 2*cy-lastCtrl[1]
			}
			x2, y2, x, y := ox+v[0], oy+v[1], ox+v[2], oy+v[3]
			out = append(out, PathSeg{'C', []float64{x1, y1, x2, y2, x, y}})
			lastCtrl = [2]float64{x2, y2}
			cx, cy = x, y
			lastOp = 'C'
			continue
		case 'Q', 'q':
			v, ok := readNums(4)
			if !ok {
				return out
			}
			qx, qy, x, y := ox+v[0], oy+v[1], ox+v[2], oy+v[3]
			out = append(out, quadToCubic(cx, cy, qx, qy, x, y))
			lastCtrl = [2]float64{qx, qy}
			cx, cy = x, y
			lastOp = 'Q'
			continue
		case 'T', 't':
			v, ok := readNums(2)
			if !ok {
				return out
			}
			qx, qy := cx, cy
			if lastOp == 'Q' {
				qx, qy = 2*cx-lastCtrl[0], 2*cy-lastCtrl[1]
			}
			x, y := ox+v[0], oy+v[1]
			out = append(out, quadToCubic(cx, cy, qx, qy, x, y))
			lastCtrl = [2]float64{qx, qy}
			cx, cy = x, y
			lastOp = 'Q'
			continue
		case 'A', 'a':
			v, ok := readNums(7)
			if !ok {
				return out
			}
			x, y := ox+v[5], oy+v[6]
			out = append(out, arcToCubic(cx, cy, v[0], v[1], v[2], v[3] != 0, v[4] != 0, x, y)...)
			cx, cy = x, y
		default:
			return out
		}
		lastOp = 'L'
	}
	return out
}

func quadToCubic(x0, y0, qx, qy, x, y float64) PathSeg {
	return PathSeg{'C', []float64{x0 + 2.0/3*(qx-x0), y0 + 2.0/3*(qy-y0), x + 2.0/3*(qx-x), y + 2.0/3*(qy-y), x, y}}
}

// arcToCubic converts an elliptical arc to cubic Bezier segments.
func arcToCubic(x1, y1, rx, ry, angle float64, large, sweep bool, x2, y2 float64) []PathSeg {
	if rx == 0 || ry == 0 || (x1 == x2 && y1 == y2) {
		return []PathSeg{{'L', []float64{x2, y2}}}
	}
	rx, ry = math.Abs(rx), math.Abs(ry)
	phi := angle * math.Pi / 180
	cosPhi, sinPhi := math.Cos(phi), math.Sin(phi)
	dx, dy := (x1-x2)/2, (y1-y2)/2
	x1p := cosPhi*dx + sinPhi*dy
	y1p := -sinPhi*dx + cosPhi*dy
	lambda := x1p*x1p/(rx*rx) + y1p*y1p/(ry*ry)
	if lambda > 1 {
		s := math.Sqrt(lambda)
		rx, ry = rx*s, ry*s
	}
	num := rx*rx*ry*ry - rx*rx*y1p*y1p - ry*ry*x1p*x1p
	den := rx*rx*y1p*y1p + ry*ry*x1p*x1p
	coef := 0.0
	if den != 0 && num > 0 {
		coef = math.Sqrt(num / den)
	}
	if large == sweep {
		coef = -coef
	}
	cxp := coef * rx * y1p / ry
	cyp := -coef * ry * x1p / rx
	cx := cosPhi*cxp - sinPhi*cyp + (x1+x2)/2
	cy := sinPhi*cxp + cosPhi*cyp + (y1+y2)/2
	ang := func(ux, uy, vx, vy float64) float64 {
		a := math.Atan2(ux*vy-uy*vx, ux*vx+uy*vy)
		return a
	}
	theta1 := ang(1, 0, (x1p-cxp)/rx, (y1p-cyp)/ry)
	dtheta := ang((x1p-cxp)/rx, (y1p-cyp)/ry, (-x1p-cxp)/rx, (-y1p-cyp)/ry)
	if !sweep && dtheta > 0 {
		dtheta -= 2 * math.Pi
	} else if sweep && dtheta < 0 {
		dtheta += 2 * math.Pi
	}
	n := int(math.Ceil(math.Abs(dtheta) / (math.Pi / 2)))
	if n < 1 {
		n = 1
	}
	delta := dtheta / float64(n)
	t := 4.0 / 3 * math.Tan(delta/4)
	var out []PathSeg
	th := theta1
	for i := 0; i < n; i++ {
		c1, s1 := math.Cos(th), math.Sin(th)
		c2, s2 := math.Cos(th+delta), math.Sin(th+delta)
		p := func(x, y float64) (float64, float64) {
			return cosPhi*rx*x - sinPhi*ry*y + cx, sinPhi*rx*x + cosPhi*ry*y + cy
		}
		ax, ay := p(c1-t*s1, s1+t*c1)
		bx, by := p(c2+t*s2, s2-t*c2)
		ex, ey := p(c2, s2)
		if i == n-1 {
			ex, ey = x2, y2
		}
		out = append(out, PathSeg{'C', []float64{ax, ay, bx, by, ex, ey}})
		th += delta
	}
	return out
}

package draw

import (
	"fmt"
	"math"

	"github.com/esoteric-programmer/weasyprint-go/internal/css"
	"github.com/esoteric-programmer/weasyprint-go/internal/layout"
	"github.com/esoteric-programmer/weasyprint-go/internal/pdf"
)

type rect struct{ x, y, w, h float64 }

func borderRect(b *layout.Box) rect {
	return rect{b.BorderBoxX(), b.BorderBoxY(), b.BorderWidth(), b.BorderHeight()}
}

func paddingRect(b *layout.Box) rect {
	return rect{b.PaddingBoxX(), b.PaddingBoxY(), b.PaddingWidth(), b.PaddingHeight()}
}

func contentRect(b *layout.Box) rect {
	return rect{b.ContentX(), b.ContentY(), b.W, b.H}
}

func boxRect(b *layout.Box, which string) rect {
	switch which {
	case "padding-box":
		return paddingRect(b)
	case "content-box":
		return contentRect(b)
	}
	return borderRect(b)
}

// outerRadii returns the border radii (corner order TL, TR, BR, BL; each
// [horizontal, vertical]) clamped to fit the border box.
func outerRadii(b *layout.Box) [4][2]float64 {
	var r [4][2]float64
	if b.Style == nil || b.Kind == layout.KLine || b.Kind == layout.KText {
		return r
	}
	br := borderRect(b)
	for i := 0; i < 4; i++ {
		rr := b.Style.Radius(i)
		r[i] = [2]float64{rr[0].Resolve(br.w), rr[1].Resolve(br.h)}
	}
	// Fragmented boxes have square corners on the cut side.
	if b.NoStartDecor {
		if b.Kind == layout.KInline {
			r[0], r[3] = [2]float64{}, [2]float64{}
		} else {
			r[0], r[1] = [2]float64{}, [2]float64{}
		}
	}
	if b.NoEndDecor {
		if b.Kind == layout.KInline {
			r[1], r[2] = [2]float64{}, [2]float64{}
		} else {
			r[2], r[3] = [2]float64{}, [2]float64{}
		}
	}
	f := 1.0
	check := func(sum, side float64) {
		if sum > 0 && side/sum < f {
			f = side / sum
		}
	}
	check(r[0][0]+r[1][0], br.w)
	check(r[3][0]+r[2][0], br.w)
	check(r[0][1]+r[3][1], br.h)
	check(r[1][1]+r[2][1], br.h)
	if f < 1 {
		for i := range r {
			r[i][0] *= f
			r[i][1] *= f
		}
	}
	return r
}

func shrinkRadii(r [4][2]float64, t, rt, bt, l float64) [4][2]float64 {
	m := func(v float64) float64 { return math.Max(0, v) }
	return [4][2]float64{
		{m(r[0][0] - l), m(r[0][1] - t)},
		{m(r[1][0] - rt), m(r[1][1] - t)},
		{m(r[2][0] - rt), m(r[2][1] - bt)},
		{m(r[3][0] - l), m(r[3][1] - bt)},
	}
}

func innerRadii(b *layout.Box, r [4][2]float64) [4][2]float64 {
	return shrinkRadii(r, b.BT, b.BR, b.BB, b.BL)
}

func radiiFor(b *layout.Box, which string) [4][2]float64 {
	r := outerRadii(b)
	switch which {
	case "padding-box":
		return shrinkRadii(r, b.BT, b.BR, b.BB, b.BL)
	case "content-box":
		return shrinkRadii(r, b.BT+b.PT, b.BR+b.PR, b.BB+b.PB, b.BL+b.PL)
	}
	return r
}

func hasRadii(r [4][2]float64) bool {
	for _, c := range r {
		if c[0] > 0 && c[1] > 0 {
			return true
		}
	}
	return false
}

const kappa = 0.5522847498

// roundedCtrl places the Bézier control points of rounded corners, as in
// WeasyPrint's rounded_box.
const roundedCtrl = 0.45

// pathRounded adds a (rounded) rectangle path.
func pathRounded(c *pdf.Content, rc rect, r [4][2]float64) {
	if !hasRadii(r) {
		c.Rect(rc.x, rc.y, rc.w, rc.h)
		return
	}
	x0, y0, x1, y1 := rc.x, rc.y, rc.x+rc.w, rc.y+rc.h
	c.MoveTo(x0+r[0][0], y0)
	c.LineTo(x1-r[1][0], y0)
	if r[1][0] > 0 && r[1][1] > 0 {
		c.CurveTo(x1-r[1][0]*roundedCtrl, y0, x1, y0+r[1][1]*roundedCtrl, x1, y0+r[1][1])
	}
	c.LineTo(x1, y1-r[2][1])
	if r[2][0] > 0 && r[2][1] > 0 {
		c.CurveTo(x1, y1-r[2][1]*roundedCtrl, x1-r[2][0]*roundedCtrl, y1, x1-r[2][0], y1)
	}
	c.LineTo(x0+r[3][0], y1)
	if r[3][0] > 0 && r[3][1] > 0 {
		c.CurveTo(x0+r[3][0]*roundedCtrl, y1, x0, y1-r[3][1]*roundedCtrl, x0, y1-r[3][1])
	}
	c.LineTo(x0, y0+r[0][1])
	if r[0][0] > 0 && r[0][1] > 0 {
		c.CurveTo(x0, y0+r[0][1]*roundedCtrl, x0+r[0][0]*roundedCtrl, y0, x0+r[0][0], y0)
	}
	c.ClosePath()
}

func layerValue[T any](list []T, i int) T {
	var zero T
	if len(list) == 0 {
		return zero
	}
	return list[i%len(list)]
}

func (cv *canvas) drawBackground(b *layout.Box) {
	cv.drawBackgroundStyle(b.Style, b, nil, nil)
}

// drawBackgroundStyle paints the background of style for box b. If area is
// given, it is the painting area (canvas backgrounds) and positioning uses
// posBox (or b).
func (cv *canvas) drawBackgroundStyle(s *css.Style, b *layout.Box, area *rect, page *layout.Box) {
	if s == nil {
		return
	}
	color := s.Color(css.PBackgroundColor)
	imgs, _ := s.Get(css.PBackgroundImage).([]css.Value)
	hasImage := false
	for _, im := range imgs {
		if k, ok := im.(css.Keyword); !ok || k != "none" {
			hasImage = true
		}
	}
	if color.IsTransparent() && !hasImage {
		return
	}
	clips, _ := s.Get(css.PBackgroundClip).([]string)
	origins, _ := s.Get(css.PBackgroundOrigin).([]string)
	repeats, _ := s.Get(css.PBackgroundRepeat).([]css.Repeat)
	positions, _ := s.Get(css.PBackgroundPosition).([]css.Position)
	sizes, _ := s.Get(css.PBackgroundSize).([]css.BgSize)
	n := len(imgs)
	clipOf := func(i int) (rect, [4][2]float64) {
		if area != nil {
			return *area, [4][2]float64{}
		}
		which := layerValue(clips, i)
		if which == "text" {
			which = "border-box"
		}
		return boxRect(b, which), radiiFor(b, which)
	}
	// Color, under the bottom layer.
	if !color.IsTransparent() {
		rc, radii := clipOf(n - 1)
		cv.c.Save()
		cv.setFill(color)
		pathRounded(cv.c, rc, radii)
		cv.c.Fill()
		cv.c.Restore()
	}
	for i := n - 1; i >= 0; i-- {
		im := imgs[i]
		if k, ok := im.(css.Keyword); ok && k == "none" {
			continue
		}
		clip, radii := clipOf(i)
		origin := boxRect(b, layerValue(origins, i))
		if area != nil && page != nil {
			// Canvas background: positioned against the root box.
			origin = boxRect(b, layerValue(origins, i))
		}
		cv.c.Save()
		pathRounded(cv.c, clip, radii)
		cv.c.Clip()
		cv.drawImageLayer(im, origin, clip, layerValue(repeats, i), layerValue(positions, i), layerValue(sizes, i))
		cv.c.Restore()
	}
}

func (cv *canvas) drawImageLayer(im css.Value, origin, clip rect, rep css.Repeat, pos css.Position, size css.BgSize) {
	var iw, ih, ratio float64
	var draw func(x, y, w, h float64)
	switch v := im.(type) {
	case css.URL:
		img := cv.imageFor(string(v))
		if img == nil {
			return
		}
		w, h, ok := img.IntrinsicSize()
		ratio = img.Ratio()
		if ok {
			iw, ih = w, h
		}
		draw = func(x, y, w, h float64) { cv.paintImage(img, x, y, w, h) }
	case *css.Gradient:
		draw = func(x, y, w, h float64) { cv.drawGradient(v, rect{x, y, w, h}) }
	default:
		return
	}
	// Size.
	var w, h float64
	switch size.Keyword {
	case "cover", "contain":
		if iw == 0 || ih == 0 {
			w, h = origin.w, origin.h
		} else {
			sx, sy := origin.w/iw, origin.h/ih
			s := math.Max(sx, sy)
			if size.Keyword == "contain" {
				s = math.Min(sx, sy)
			}
			w, h = iw*s, ih*s
		}
	default:
		wl, wok := size.W.(css.Length)
		hl, hok := size.H.(css.Length)
		switch {
		case wok && hok:
			w, h = wl.Resolve(origin.w), hl.Resolve(origin.h)
		case wok:
			w = wl.Resolve(origin.w)
			if ratio > 0 && iw > 0 {
				h = w / ratio
			} else {
				h = origin.h
			}
		case hok:
			h = hl.Resolve(origin.h)
			if ratio > 0 && ih > 0 {
				w = h * ratio
			} else {
				w = origin.w
			}
		default:
			if iw > 0 && ih > 0 {
				w, h = iw, ih
			} else {
				w, h = origin.w, origin.h
			}
		}
	}
	if w <= 0 || h <= 0 {
		return
	}
	// Round.
	if rep[0] == "round" {
		n := math.Max(1, math.Round(origin.w/w))
		nw := origin.w / n
		if !(size.H != nil && isLen(size.H)) && rep[1] != "round" {
			h = h * nw / w
		}
		w = nw
	}
	if rep[1] == "round" {
		n := math.Max(1, math.Round(origin.h/h))
		h = origin.h / n
	}
	// Position.
	px := pos.X.Resolve(origin.w - w)
	py := pos.Y.Resolve(origin.h - h)
	if pos.XOrigin == "right" {
		px = origin.w - w - px
	}
	if pos.YOrigin == "bottom" {
		py = origin.h - h - py
	}
	x0, y0 := origin.x+px, origin.y+py
	// Tiling.
	xs := tiles(rep[0], x0, w, clip.x, clip.w, origin)
	ys := tiles(rep[1], y0, h, clip.y, clip.h, rect{origin.y, origin.x, origin.h, origin.w})
	if len(xs)*len(ys) > 4000 {
		// Too many tiles: draw once.
		xs, ys = []float64{x0}, []float64{y0}
	}
	for _, y := range ys {
		for _, x := range xs {
			draw(x, y, w, h)
		}
	}
}

func isLen(v css.Value) bool { _, ok := v.(css.Length); return ok }

// tiles returns tile start positions along one axis.
func tiles(mode string, start, size, clipStart, clipSize float64, origin rect) []float64 {
	switch mode {
	case "no-repeat":
		return []float64{start}
	case "space":
		n := math.Floor(origin.w / size)
		if n < 2 {
			return []float64{start}
		}
		gap := (origin.w - n*size) / (n - 1)
		var out []float64
		for i := 0.0; i < n; i++ {
			out = append(out, origin.x+i*(size+gap))
		}
		return out
	}
	first := start - math.Ceil((start-clipStart)/size)*size
	var out []float64
	for x := first; x < clipStart+clipSize; x += size {
		out = append(out, x)
		if len(out) > 2000 {
			break
		}
	}
	return out
}

// ---------------------------------------------------------------- gradients

func (cv *canvas) drawGradient(g *css.Gradient, rc rect) {
	if len(g.Stops) == 0 || rc.w <= 0 || rc.h <= 0 {
		return
	}
	cv.c.Save()
	cv.c.Rect(rc.x, rc.y, rc.w, rc.h)
	cv.c.Clip()
	defer cv.c.Restore()
	var length float64
	var coords []float64
	shType := 2
	if !g.Radial {
		angle := g.Angle
		if g.ToCorner[0] != "" {
			// Angle such that the corner's perpendicular passes through the
			// other corners.
			a := math.Atan2(rc.w, rc.h)
			switch {
			case g.ToCorner[0] == "right" && g.ToCorner[1] == "top":
				angle = math.Pi/2 - math.Atan2(rc.h, rc.w)
			case g.ToCorner[0] == "right" && g.ToCorner[1] == "bottom":
				angle = math.Pi/2 + math.Atan2(rc.h, rc.w)
			case g.ToCorner[0] == "left" && g.ToCorner[1] == "bottom":
				angle = 3*math.Pi/2 - math.Atan2(rc.h, rc.w)
			default:
				angle = 3*math.Pi/2 + math.Atan2(rc.h, rc.w)
			}
			_ = a
		}
		dx, dy := math.Sin(angle), -math.Cos(angle)
		length = math.Abs(rc.w*dx) + math.Abs(rc.h*dy)
		cx, cy := rc.x+rc.w/2, rc.y+rc.h/2
		coords = []float64{cx - dx*length/2, cy - dy*length/2, cx + dx*length/2, cy + dy*length/2}
	} else {
		shType = 3
		cx := g.Position.X.Resolve(rc.w)
		cy := g.Position.Y.Resolve(rc.h)
		if g.Position.XOrigin == "right" {
			cx = rc.w - cx
		}
		if g.Position.YOrigin == "bottom" {
			cy = rc.h - cy
		}
		var rx, ry float64
		if g.SizeIsLen {
			rx, ry = g.SizeLen[0].Resolve(rc.w), g.SizeLen[1].Resolve(rc.h)
		} else {
			dl, dr, dt, db := cx, rc.w-cx, cy, rc.h-cy
			switch g.Size {
			case "closest-side":
				rx, ry = math.Min(dl, dr), math.Min(dt, db)
			case "farthest-side":
				rx, ry = math.Max(dl, dr), math.Max(dt, db)
			case "closest-corner":
				rx, ry = math.Min(dl, dr)*math.Sqrt2, math.Min(dt, db)*math.Sqrt2
			default:
				rx, ry = math.Max(dl, dr)*math.Sqrt2, math.Max(dt, db)*math.Sqrt2
			}
			if g.Shape == "circle" {
				var r float64
				switch g.Size {
				case "closest-side":
					r = math.Min(math.Min(dl, dr), math.Min(dt, db))
				case "farthest-side":
					r = math.Max(math.Max(dl, dr), math.Max(dt, db))
				case "closest-corner":
					r = math.Min(math.Hypot(math.Min(dl, dr), math.Min(dt, db)), math.Hypot(math.Min(dl, dr), math.Min(dt, db)))
				default:
					r = math.Hypot(math.Max(dl, dr), math.Max(dt, db))
				}
				rx, ry = r, r
			}
		}
		if rx <= 0 || ry <= 0 {
			return
		}
		length = rx
		// Draw as a circle scaled vertically.
		cv.c.Save()
		cv.c.Transform(1, 0, 0, ry/rx, rc.x+cx, rc.y+cy)
		coords = []float64{0, 0, 0, 0, 0, rx}
		defer cv.c.Restore()
	}
	positions, colors := resolveStops(g.Stops, length)
	if g.Repeating && len(positions) > 1 {
		first, last := positions[0], positions[len(positions)-1]
		period := last - first
		if period > 0 {
			var np []float64
			var nc []css.Color
			for k := math.Floor(-first / period); first+k*period < length && len(np) < 2000; k++ {
				for i := range positions {
					np = append(np, positions[i]+k*period)
					nc = append(nc, colors[i])
				}
			}
			positions, colors = np, nc
		}
	}
	// Normalize to [0, 1] over the gradient line.
	t0, t1 := positions[0], positions[len(positions)-1]
	if t1-t0 < 1e-9 {
		// Single color.
		cv.c.Save()
		cv.setFill(colors[len(colors)-1])
		cv.c.Rect(rc.x, rc.y, rc.w, rc.h)
		cv.c.Fill()
		cv.c.Restore()
		return
	}
	if !g.Radial {
		x0, y0, x1, y1 := coords[0], coords[1], coords[2], coords[3]
		f0, f1 := t0/length, t1/length
		coords = []float64{x0 + (x1-x0)*f0, y0 + (y1-y0)*f0, x0 + (x1-x0)*f1, y0 + (y1-y0)*f1}
	} else {
		if t0 < 0 {
			// Radial gradients can't have negative radii; clamp.
			t0 = 0
		}
		coords = []float64{0, 0, t0, 0, 0, t1}
	}
	var fns pdf.Array
	var bounds, encode []float64
	alpha := false
	for i := 0; i+1 < len(positions); i++ {
		c0, c1 := colors[i], colors[i+1]
		if c0.A < 1 || c1.A < 1 {
			alpha = true
		}
		fns = append(fns, pdf.Dict{"FunctionType": 2, "Domain": []float64{0, 1}, "C0": []float64{c0.R, c0.G, c0.B}, "C1": []float64{c1.R, c1.G, c1.B}, "N": 1})
		if i > 0 {
			bounds = append(bounds, (positions[i]-t0)/(t1-t0))
		}
		encode = append(encode, 0, 1)
	}
	var fn any
	if len(fns) == 1 {
		fn = fns[0]
	} else {
		fn = pdf.Dict{"FunctionType": 3, "Domain": []float64{0, 1}, "Functions": fns, "Bounds": bounds, "Encode": encode}
	}
	sh := pdf.Dict{"ShadingType": shType, "ColorSpace": pdf.Name("DeviceRGB"), "Coords": coords, "Function": fn, "Extend": pdf.Array{true, true}}
	name := fmt.Sprintf("Sh%d", cv.r.nextID())
	cv.shadings[name] = cv.r.doc.Add(sh)
	if alpha {
		// Approximate alpha by the average stop opacity.
		sum := 0.0
		for _, c := range colors {
			sum += c.A
		}
		cv.c.Save()
		cv.c.SetState(cv.alphaState(sum/float64(len(colors)), 1))
		defer cv.c.Restore()
	}
	cv.c.ShFill(name)
}

// resolveStops computes stop positions (CSS Images 3 §3.4.3).
func resolveStops(stops []css.ColorStop, length float64) ([]float64, []css.Color) {
	n := len(stops)
	pos := make([]float64, n)
	set := make([]bool, n)
	colors := make([]css.Color, n)
	for i, s := range stops {
		if c, ok := s.Color.(css.Color); ok {
			colors[i] = c
		} else {
			colors[i] = css.Color{A: 1}
		}
		if s.Pos != nil {
			pos[i] = s.Pos.Resolve(length)
			set[i] = true
		}
	}
	if !set[0] {
		pos[0], set[0] = 0, true
	}
	if !set[n-1] {
		pos[n-1], set[n-1] = math.Max(length, pos[0]), true
	}
	maxSoFar := pos[0]
	for i := range pos {
		if set[i] {
			if pos[i] < maxSoFar {
				pos[i] = maxSoFar
			}
			maxSoFar = pos[i]
		}
	}
	for i := 1; i < n; {
		if set[i] {
			i++
			continue
		}
		j := i
		for !set[j] {
			j++
		}
		a, b := pos[i-1], pos[j]
		for k := i; k < j; k++ {
			pos[k] = a + (b-a)*float64(k-i+1)/float64(j-i+1)
			set[k] = true
		}
		i = j
	}
	if n == 1 {
		pos = append(pos, pos[0])
		colors = append(colors, colors[0])
	}
	return pos, colors
}

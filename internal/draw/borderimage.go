package draw

import (
	"fmt"
	"math"

	"github.com/esoteric-programmer/weasyprint-go/internal/css"
	"github.com/esoteric-programmer/weasyprint-go/internal/layout"
	"github.com/esoteric-programmer/weasyprint-go/internal/pdf"
)

// borderImageProps are the longhands of border-image or mask-border.
type borderImageProps struct {
	source, slice, width, outset, repeat css.PropID
}

var (
	borderImage = borderImageProps{css.PBorderImageSource, css.PBorderImageSlice, css.PBorderImageWidth, css.PBorderImageOutset, css.PBorderImageRepeat}
	maskBorder  = borderImageProps{css.PMaskBorderSource, css.PMaskBorderSlice, css.PMaskBorderWidth, css.PMaskBorderOutset, css.PMaskBorderRepeat}
)

// borderImageSource returns how to draw the image of a border-image-source
// or mask-border-source value, with its natural size (0 when it has none)
// and ratio.
func (cv *canvas) borderImageSource(b *layout.Box, v css.Value) (draw func(x, y, w, h float64), iw, ih float64, ok bool) {
	switch v := v.(type) {
	case css.URL:
		saved := cv.orientation
		cv.orientation = layout.Orientation(b.Style)
		img := cv.imageFor(string(v))
		cv.orientation = saved
		if img == nil {
			return nil, 0, 0, false
		}
		w, h, hasSize := img.IntrinsicSize()
		bw, bh := b.BorderWidth(), b.BorderHeight()
		switch {
		case hasSize:
			iw, ih = w, h
		case img.Ratio() > 0:
			// Contain the ratio in the border box, like WeasyPrint's
			// default sizing.
			r := img.Ratio()
			if bw/r <= bh {
				iw, ih = bw, bw/r
			} else {
				iw, ih = bh*r, bh
			}
		default:
			iw, ih = bw, bh
		}
		return func(x, y, w, h float64) { cv.paintImage(img, x, y, w, h) }, iw, ih, true
	case *css.Gradient:
		return func(x, y, w, h float64) { cv.drawGradient(v, rect{x, y, w, h}) }, b.BorderWidth(), b.BorderHeight(), true
	}
	return nil, 0, 0, false
}

// drawBorderImage draws the border image (or mask border image) of a box
// as described by CSS Backgrounds 3 §6. It returns false when the box has
// no usable image.
func (cv *canvas) drawBorderImage(b *layout.Box, p borderImageProps) bool {
	s := b.Style
	draw, iw, ih, ok := cv.borderImageSource(b, s.Get(p.source))
	if !ok || iw <= 0 || ih <= 0 {
		return false
	}
	slice, _ := s.Get(p.slice).(css.BorderImageSlice)
	sliceDim := func(v css.Value, natural float64) float64 {
		switch v := v.(type) {
		case css.Number:
			return math.Min(float64(v), natural)
		case css.Length:
			return math.Min(v.V, 100) / 100 * natural
		}
		return natural
	}
	st := sliceDim(slice.V[0], ih)
	sr := sliceDim(slice.V[1], iw)
	sb := sliceDim(slice.V[2], ih)
	sl := sliceDim(slice.V[3], iw)

	// Border image area: the border box extended by the outsets.
	area := borderRect(b)
	borders := [4]float64{b.BT, b.BR, b.BB, b.BL}
	outsets, _ := s.Get(p.outset).([]css.Value)
	var out [4]float64
	for i := 0; i < 4 && i < len(outsets); i++ {
		switch v := outsets[i].(type) {
		case css.Number:
			out[i] = float64(v) * borders[i]
		case css.Length:
			out[i] = v.V
		}
	}
	area.x -= out[3]
	area.y -= out[0]
	area.w += out[1] + out[3]
	area.h += out[0] + out[2]

	// Widths of the border image regions.
	widths, _ := s.Get(p.width).([]css.Value)
	slices := [4]float64{st, sr, sb, sl}
	var wd [4]float64
	for i := 0; i < 4; i++ {
		var v css.Value = css.Keyword("auto")
		if i < len(widths) {
			v = widths[i]
		}
		switch v := v.(type) {
		case css.Number:
			wd[i] = float64(v) * borders[i]
		case css.Length:
			if i%2 == 0 {
				wd[i] = v.Resolve(area.h)
			} else {
				wd[i] = v.Resolve(area.w)
			}
		default:
			wd[i] = slices[i]
		}
	}
	// Opposite regions must not overlap.
	f := 1.0
	if sum := wd[1] + wd[3]; sum > area.w && sum > 0 {
		f = math.Min(f, area.w/sum)
	}
	if sum := wd[0] + wd[2]; sum > area.h && sum > 0 {
		f = math.Min(f, area.h/sum)
	}
	for i := range wd {
		wd[i] *= f
	}
	bt, br, bb, bl := wd[0], wd[1], wd[2], wd[3]
	rep, _ := s.Get(p.repeat).(css.Repeat)
	if rep[0] == "" {
		rep = css.Repeat{"stretch", "stretch"}
	}

	x, y, w, h := area.x, area.y, area.w, area.h
	midW, midH := iw-sl-sr, ih-st-sb
	// Corners.
	cv.borderImageRegion(draw, iw, ih, rect{x, y, bl, bt}, rect{0, 0, sl, st}, "stretch", "stretch", 0, 0)
	cv.borderImageRegion(draw, iw, ih, rect{x + w - br, y, br, bt}, rect{iw - sr, 0, sr, st}, "stretch", "stretch", 0, 0)
	cv.borderImageRegion(draw, iw, ih, rect{x + w - br, y + h - bb, br, bb}, rect{iw - sr, ih - sb, sr, sb}, "stretch", "stretch", 0, 0)
	cv.borderImageRegion(draw, iw, ih, rect{x, y + h - bb, bl, bb}, rect{0, ih - sb, sl, sb}, "stretch", "stretch", 0, 0)
	ratio := func(a, b float64) float64 {
		if a > 0 && b > 0 {
			return a / b
		}
		return 0
	}
	if midW > 0 {
		// Top and bottom edges keep the ratio of their slice.
		cv.borderImageRegion(draw, iw, ih, rect{x + bl, y, w - bl - br, bt}, rect{sl, 0, midW, st}, rep[0], "stretch", ratio(bt, st), 0)
		cv.borderImageRegion(draw, iw, ih, rect{x + bl, y + h - bb, w - bl - br, bb}, rect{sl, ih - sb, midW, sb}, rep[0], "stretch", ratio(bb, sb), 0)
	}
	if midH > 0 {
		cv.borderImageRegion(draw, iw, ih, rect{x + w - br, y + bt, br, h - bt - bb}, rect{iw - sr, st, sr, midH}, "stretch", rep[1], 0, ratio(br, sr))
		cv.borderImageRegion(draw, iw, ih, rect{x, y + bt, bl, h - bt - bb}, rect{0, st, sl, midH}, "stretch", rep[1], 0, ratio(bl, sl))
	}
	if slice.Fill && midW > 0 && midH > 0 {
		// The middle is scaled like the top (or bottom) and left (or right)
		// edges.
		fx := ratio(bt, st)
		if fx == 0 {
			fx = ratio(bb, sb)
		}
		if fx == 0 {
			fx = 1
		}
		fy := ratio(bl, sl)
		if fy == 0 {
			fy = ratio(br, sr)
		}
		if fy == 0 {
			fy = 1
		}
		cv.borderImageRegion(draw, iw, ih, rect{x + bl, y + bt, w - bl - br, h - bt - bb}, rect{sl, st, midW, midH}, rep[0], rep[1], fx, fy)
	}
	return true
}

// borderImageRegion draws the part src of an image of size iw×ih in the
// region dst, repeated as given; scaleX and scaleY are the scale factors
// used before repeating (0 to keep the ratio of the slice).
func (cv *canvas) borderImageRegion(draw func(x, y, w, h float64), iw, ih float64, dst, src rect, repX, repY string, scaleX, scaleY float64) {
	if dst.w <= 0 || dst.h <= 0 || src.w <= 0 || src.h <= 0 {
		return
	}
	if repX == "stretch" {
		scaleX = dst.w / src.w
	}
	if repY == "stretch" {
		scaleY = dst.h / src.h
	}
	if scaleX == 0 {
		scaleX = scaleY
	}
	if scaleY == 0 {
		scaleY = scaleX
	}
	if scaleX <= 0 || scaleY <= 0 {
		return
	}
	xs, tw := borderImageTiles(repX, dst.x, dst.w, src.w*scaleX)
	ys, th := borderImageTiles(repY, dst.y, dst.h, src.h*scaleY)
	if len(xs) == 0 || len(ys) == 0 {
		return
	}
	if len(xs)*len(ys) > 4000 {
		xs, ys = xs[:1], ys[:1]
	}
	scaleX, scaleY = tw/src.w, th/src.h
	cv.c.Save()
	defer cv.c.Restore()
	cv.c.Rect(dst.x, dst.y, dst.w, dst.h)
	cv.c.Clip()
	for _, ty := range ys {
		for _, tx := range xs {
			cv.c.Save()
			cv.c.Rect(tx, ty, tw, th)
			cv.c.Clip()
			draw(tx-src.x*scaleX, ty-src.y*scaleY, iw*scaleX, ih*scaleY)
			cv.c.Restore()
		}
	}
}

// borderImageTiles returns the positions and the size of the tiles of a
// border image region along one axis.
func borderImageTiles(mode string, start, length, size float64) ([]float64, float64) {
	if size <= 0 {
		return nil, 0
	}
	switch mode {
	case "repeat":
		// Tiles are centered in the region.
		first := start + (length-size)/2
		first -= math.Ceil((first-start)/size-1e-9) * size
		var out []float64
		for p := first; p < start+length-1e-9; p += size {
			out = append(out, p)
		}
		return out, size
	case "round":
		n := math.Max(1, math.Round(length/size))
		size = length / n
		out := make([]float64, int(n))
		for i := range out {
			out[i] = start + float64(i)*size
		}
		return out, size
	case "space":
		n := math.Floor(length/size + 1e-9)
		if n < 1 {
			return nil, size
		}
		gap := (length - n*size) / (n + 1)
		out := make([]float64, int(n))
		for i := range out {
			out[i] = start + gap + float64(i)*(size+gap)
		}
		return out, size
	}
	return []float64{start}, length
}

// maskBorderState returns the graphics state applying the mask-border of
// a box as a soft mask, or "" when it has none.
func (cv *canvas) maskBorderState(b *layout.Box) string {
	if b.Style == nil {
		return ""
	}
	if k, ok := b.Style.Get(css.PMaskBorderSource).(css.Keyword); ok && k == "none" {
		return ""
	}
	cv.pushGroup()
	drawn := cv.drawBorderImage(b, maskBorder)
	group := cv.c
	cv.c = cv.stack[len(cv.stack)-1]
	cv.stack = cv.stack[:len(cv.stack)-1]
	if !drawn {
		return ""
	}
	group.Dict["Type"] = pdf.Name("XObject")
	group.Dict["Subtype"] = pdf.Name("Form")
	group.Dict["BBox"] = []float64{-1e5, -1e5, 1e5, 1e5}
	group.Dict["Group"] = pdf.Dict{"Type": pdf.Name("Group"), "S": pdf.Name("Transparency"), "CS": pdf.Name("DeviceRGB")}
	group.Dict["Resources"] = cv.resources()
	mode := pdf.Name("Alpha")
	if b.Style.Kw(css.PMaskBorderMode) == "luminance" {
		mode = pdf.Name("Luminosity")
	}
	state := pdf.Dict{"Type": pdf.Name("ExtGState"), "SMask": pdf.Dict{"Type": pdf.Name("Mask"), "S": mode, "G": cv.r.doc.Add(group)}, "ca": 1, "AIS": false}
	name := fmt.Sprintf("MB%d", cv.r.nextID())
	cv.states[name] = cv.r.doc.Add(state)
	return name
}

// hasMaskBorder reports whether a box has a mask-border.
func hasMaskBorder(b *layout.Box) bool {
	if b.Style == nil || b.Kind == layout.KText || b.Kind == layout.KLine {
		return false
	}
	k, ok := b.Style.Get(css.PMaskBorderSource).(css.Keyword)
	return !ok || k != "none"
}

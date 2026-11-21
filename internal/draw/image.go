package draw

import (
	"fmt"
	"math"

	"github.com/esoteric-programmer/weasyprint-go/internal/css"
	"github.com/esoteric-programmer/weasyprint-go/internal/images"
	"github.com/esoteric-programmer/weasyprint-go/internal/layout"
	"github.com/esoteric-programmer/weasyprint-go/internal/pdf"
)

func (cv *canvas) imageFor(url string) *images.Image {
	if cv.r.images == nil {
		return nil
	}
	return cv.r.images.Get(url)
}

func (r *Renderer) imageRef(im *images.Image, interpolate bool) pdf.Ref {
	if ref, ok := r.imgRefs[im]; ok {
		return ref
	}
	d := pdf.Dict{
		"Type":             pdf.Name("XObject"),
		"Subtype":          pdf.Name("Image"),
		"Width":            im.Width,
		"Height":           im.Height,
		"BitsPerComponent": 8,
	}
	if interpolate {
		d["Interpolate"] = true
	}
	cs := pdf.Name("DeviceRGB")
	switch im.Components {
	case 1:
		cs = pdf.Name("DeviceGray")
	case 4:
		cs = pdf.Name("DeviceCMYK")
	}
	d["ColorSpace"] = cs
	var st *pdf.Stream
	if im.JPEG != nil {
		d["Filter"] = pdf.Name("DCTDecode")
		if im.InvertCMYK {
			d["Decode"] = []float64{1, 0, 1, 0, 1, 0, 1, 0}
		}
		st = &pdf.Stream{Dict: d, Data: im.JPEG}
	} else {
		if im.Alpha != nil {
			smask := &pdf.Stream{Dict: pdf.Dict{
				"Type": pdf.Name("XObject"), "Subtype": pdf.Name("Image"),
				"Width": im.Width, "Height": im.Height, "BitsPerComponent": 8,
				"ColorSpace": pdf.Name("DeviceGray"),
			}, Data: im.Alpha, Compress: true}
			d["SMask"] = r.doc.Add(smask)
		}
		st = &pdf.Stream{Dict: d, Data: im.Pixels, Compress: true}
	}
	ref := r.doc.Add(st)
	r.imgRefs[im] = ref
	return ref
}

// paintImage draws an image scaled into the rectangle.
func (cv *canvas) paintImage(im *images.Image, x, y, w, h float64) {
	if w <= 0 || h <= 0 {
		return
	}
	if im.SVG != nil {
		cv.drawSVG(im.SVG, x, y, w, h)
		return
	}
	ref := cv.r.imageRef(im, true)
	name := fmt.Sprintf("Im%d", int(ref))
	cv.xobjects[name] = ref
	cv.c.Save()
	cv.c.Transform(w, 0, 0, -h, x, y+h)
	cv.c.DrawXObject(name)
	cv.c.Restore()
}

// drawReplaced draws the content of a replaced box with object-fit.
func (cv *canvas) drawReplaced(b *layout.Box) {
	cv.marked(b, "Figure", func() { cv.drawReplacedContent(b) })
}

func (cv *canvas) drawReplacedContent(b *layout.Box) {
	if b.Image == nil || b.Style.Kw(css.PVisibility) != "visible" {
		return
	}
	cr := contentRect(b)
	if cr.w <= 0 || cr.h <= 0 {
		return
	}
	iw, ih, ok := b.Image.IntrinsicSize()
	ratio := b.Image.Ratio()
	if !ok || iw <= 0 || ih <= 0 {
		iw, ih = cr.w, cr.h
		if ratio > 0 {
			ih = iw / ratio
		}
	}
	w, h := cr.w, cr.h
	switch b.Style.Kw(css.PObjectFit) {
	case "contain", "cover", "scale-down":
		sx, sy := cr.w/iw, cr.h/ih
		s := math.Min(sx, sy)
		if b.Style.Kw(css.PObjectFit) == "cover" {
			s = math.Max(sx, sy)
		}
		if b.Style.Kw(css.PObjectFit) == "scale-down" && s > 1 {
			s = 1
		}
		w, h = iw*s, ih*s
	case "none":
		w, h = iw, ih
	}
	pos, _ := b.Style.Get(css.PObjectPosition).(css.Position)
	px := pos.X.Resolve(cr.w - w)
	py := pos.Y.Resolve(cr.h - h)
	if pos.XOrigin == "right" {
		px = cr.w - w - px
	}
	if pos.YOrigin == "bottom" {
		py = cr.h - h - py
	}
	cv.c.Save()
	pathRounded(cv.c, cr, radiiFor(b, "content-box"))
	cv.c.Clip()
	cv.paintImage(b.Image, cr.x+px, cr.y+py, w, h)
	cv.c.Restore()
}

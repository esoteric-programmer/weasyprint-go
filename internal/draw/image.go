package draw

import (
	"bytes"
	"fmt"
	"image"
	"image/jpeg"
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
	return cv.r.images.GetOriented(url, cv.orientation)
}

// imageRef returns the reference of the image XObject, written at the end
// of the document (once the resolution it needs is known).
func (r *Renderer) imageRef(im *images.Image, interpolate bool) pdf.Ref {
	if ref, ok := r.imgRefs[im]; ok {
		if interpolate {
			r.imgInterp[im] = true
		}
		return ref
	}
	ref := r.doc.Reserve()
	r.imgRefs[im] = ref
	r.imgOrder = append(r.imgOrder, im)
	r.imgRatio[im] = 0
	if interpolate {
		r.imgInterp[im] = true
	}
	return ref
}

// useImageAt records the size an image is drawn at, for the maximum
// resolution (dpi option).
func (r *Renderer) useImageAt(im *images.Image, w, h float64, ctm [6]float64) {
	ratio := 1.0
	if r.opts.DPI > 0 && w > 0 && h > 0 {
		// Size on the page, in inches (the CTM is in points), so that
		// transforms and SVG scaling count.
		wi := math.Hypot(ctm[0]*w, ctm[1]*w) / 72
		hi := math.Hypot(ctm[2]*h, ctm[3]*h) / 72
		if wi > 0 && hi > 0 {
			dpi := math.Max(float64(im.Width)/wi, float64(im.Height)/hi)
			if dpi > r.opts.DPI {
				ratio = r.opts.DPI / dpi
			}
		}
	}
	if ratio > r.imgRatio[im] {
		r.imgRatio[im] = ratio
	}
}

// writeImages writes the image XObjects, like WeasyPrint's get_x_object:
// downscaled to the maximum resolution, JPEG images re-encoded with the
// JPEG quality, and other images compressed with PNG predictors when
// optimized.
func (r *Renderer) writeImages() {
	for _, im := range r.imgOrder {
		ratio := r.imgRatio[im]
		if ratio <= 0 {
			ratio = 1
		}
		r.doc.Set(r.imgRefs[im], r.imageObject(im, r.imgInterp[im], ratio))
	}
}

func (r *Renderer) imageObject(im *images.Image, interpolate bool, ratio float64) *pdf.Stream {
	width, height := im.Width, im.Height
	pixels, alpha, jpegData := im.Pixels, im.Alpha, im.JPEG
	comps, invert := im.Components, im.InvertCMYK
	quality := r.opts.JPEGQuality
	if ratio < 1 || (jpegData != nil && quality > 0) {
		if jpegData != nil && comps == 4 {
			// Go's JPEG encoder only writes RGB: CMYK images are kept as
			// they are, or scaled and compressed losslessly.
			if ratio < 1 {
				if img, err := jpeg.Decode(bytes.NewReader(jpegData)); err == nil {
					b := img.Bounds()
					w := max(1, int(math.Round(float64(b.Dx())*ratio)))
					h := max(1, int(math.Round(float64(b.Dy())*ratio)))
					if c, ok := images.Scale(img, w, h).(*image.CMYK); ok {
						jpegData, pixels, width, height, invert = nil, c.Pix, w, h, false
					}
				}
			}
		} else if jpegData != nil {
			if q := quality; q > 0 || ratio < 1 {
				if q <= 0 {
					q = 75
				}
				if data, w, h, ok := reencodeJPEG(jpegData, ratio, q); ok {
					jpegData, width, height = data, w, h
				}
			}
		} else if ratio < 1 {
			w := max(1, int(math.Round(float64(width)*ratio)))
			h := max(1, int(math.Round(float64(height)*ratio)))
			if alpha != nil {
				pixels, alpha = images.ResampleAlpha(pixels, im.Components, alpha, width, height, w, h)
			} else {
				pixels = images.Resample(pixels, im.Components, width, height, w, h)
			}
			width, height = w, h
		}
	}
	d := pdf.Dict{
		"Type":             pdf.Name("XObject"),
		"Subtype":          pdf.Name("Image"),
		"Width":            width,
		"Height":           height,
		"BitsPerComponent": 8,
	}
	if interpolate {
		d["Interpolate"] = true
	}
	cs := pdf.Name("DeviceRGB")
	switch comps {
	case 1:
		cs = pdf.Name("DeviceGray")
	case 4:
		cs = pdf.Name("DeviceCMYK")
	}
	d["ColorSpace"] = cs
	if jpegData != nil {
		d["Filter"] = pdf.Name("DCTDecode")
		if invert {
			d["Decode"] = []float64{1, 0, 1, 0, 1, 0, 1, 0}
		}
		return &pdf.Stream{Dict: d, Data: jpegData}
	}
	// PNG predictors make most images much smaller, like the PNG data
	// embedded by WeasyPrint.
	encode := func(dict pdf.Dict, data []byte, comps int) []byte {
		if width*height < 64 {
			return data
		}
		dict["DecodeParms"] = pdf.Dict{"Predictor": 15, "Colors": comps, "BitsPerComponent": 8, "Columns": width}
		return pngPredict(data, comps, width, height)
	}
	if alpha != nil {
		md := pdf.Dict{
			"Type": pdf.Name("XObject"), "Subtype": pdf.Name("Image"),
			"Width": width, "Height": height, "BitsPerComponent": 8,
			"ColorSpace": pdf.Name("DeviceGray"),
		}
		if interpolate {
			md["Interpolate"] = true
		}
		data := encode(md, alpha, 1)
		d["SMask"] = r.doc.Add(&pdf.Stream{Dict: md, Data: data, Compress: true})
	}
	data := encode(d, pixels, comps)
	return &pdf.Stream{Dict: d, Data: data, Compress: true}
}

// reencodeJPEG decodes a JPEG image, scales it and encodes it again.
func reencodeJPEG(data []byte, ratio float64, quality int) ([]byte, int, int, bool) {
	img, err := jpeg.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, 0, 0, false
	}
	b := img.Bounds()
	if ratio < 1 {
		w := max(1, int(math.Round(float64(b.Dx())*ratio)))
		h := max(1, int(math.Round(float64(b.Dy())*ratio)))
		img = images.Scale(img, w, h)
		b = img.Bounds()
	}
	var out bytes.Buffer
	if err := jpeg.Encode(&out, img, &jpeg.Options{Quality: min(max(quality, 1), 100)}); err != nil {
		return nil, 0, 0, false
	}
	return out.Bytes(), b.Dx(), b.Dy(), true
}

// pngPredict applies PNG predictors to the rows of an image, choosing the
// filter giving the smallest sum of absolute differences for each row.
func pngPredict(data []byte, comps, width, height int) []byte {
	stride := comps * width
	out := make([]byte, 0, (stride+1)*height)
	prev := make([]byte, stride)
	cand := make([][]byte, 5)
	for i := range cand {
		cand[i] = make([]byte, stride)
	}
	for y := 0; y < height; y++ {
		row := data[y*stride : (y+1)*stride]
		best, bestSum := 0, -1
		for f := 0; f < 5; f++ {
			c := cand[f]
			sum := 0
			for i := 0; i < stride; i++ {
				var a, b, cc byte
				if i >= comps {
					a = row[i-comps]
					cc = prev[i-comps]
				}
				b = prev[i]
				var v byte
				switch f {
				case 0:
					v = row[i]
				case 1:
					v = row[i] - a
				case 2:
					v = row[i] - b
				case 3:
					v = row[i] - byte((int(a)+int(b))/2)
				case 4:
					v = row[i] - paeth(a, b, cc)
				}
				c[i] = v
				sum += int(int8(v)) * sign(int8(v))
			}
			if bestSum < 0 || sum < bestSum {
				best, bestSum = f, sum
			}
		}
		out = append(out, byte(best))
		out = append(out, cand[best]...)
		prev = row
	}
	return out
}

func sign(v int8) int {
	if v < 0 {
		return -1
	}
	return 1
}

func paeth(a, b, c byte) byte {
	p := int(a) + int(b) - int(c)
	pa, pb, pc := abs(p-int(a)), abs(p-int(b)), abs(p-int(c))
	switch {
	case pa <= pb && pa <= pc:
		return a
	case pb <= pc:
		return b
	}
	return c
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
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
	cv.r.useImageAt(im, w, h, cv.c.CTM())
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

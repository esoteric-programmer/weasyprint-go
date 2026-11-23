package weasyprint

import (
	"bytes"
	"encoding/base64"
	"testing"

	"github.com/esoteric-programmer/weasyprint-go/internal/layout"
)

func TestEPSImage(t *testing.T) {
	eps := "%!PS-Adobe-3.0 EPSF-3.0\n%%BoundingBox: 10 20 110 70\n" +
		"1 0 0 setrgbcolor 10 20 50 25 rectfill\n" +
		"/Helvetica findfont 12 scalefont setfont 0 setgray 20 50 moveto (EPS) show\n" +
		"<< /ShadingType 2 /ColorSpace /DeviceRGB /Coords [60 0 110 0] /Function << /FunctionType 2 /Domain [0 1] /C0 [0 0 1] /C1 [0 1 0] /N 1 >> >> shfill\n"
	src := `<img src="data:application/postscript;base64,` + base64.StdEncoding.EncodeToString([]byte(eps)) + `">`
	doc, pdf := render(t, src, &Options{Uncompressed: true})
	// The intrinsic size is the bounding box, one pixel per point.
	var w, h float64
	for _, p := range doc.pages {
		p.Box.Descendants(func(b *layout.Box) {
			if b.Element != nil && b.Element.Tag == "img" {
				w, h = b.W, b.H
			}
		})
	}
	if w != 100 || h != 50 {
		t.Errorf("image size %vx%v, want 100x50", w, h)
	}
	for _, want := range []string{"/Subtype /Form", "/BBox [10 20 110 70]", "/ShadingType 2", "1 0 0 rg"} {
		if !bytes.Contains(pdf, []byte(want)) {
			t.Errorf("PDF has no %q", want)
		}
	}
}

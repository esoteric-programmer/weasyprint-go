package draw

import (
	"fmt"
	"strings"

	"github.com/esoteric-programmer/weasyprint-go/internal/css"
	"github.com/esoteric-programmer/weasyprint-go/internal/images"
)

// pageBleed returns the bleed of a page (top, right, bottom, left) in px;
// auto is 6pt with crop marks and 0 otherwise.
func pageBleed(s *css.Style) [4]float64 {
	var out [4]float64
	if s == nil {
		return out
	}
	crop := false
	if ms, ok := s.Get(css.PMarks).([]string); ok {
		for _, m := range ms {
			crop = crop || m == "crop"
		}
	}
	for i, id := range []css.PropID{css.PBleedTop, css.PBleedRight, css.PBleedBottom, css.PBleedLeft} {
		if l, ok := s.Get(id).(css.Length); ok {
			out[i] = l.Resolve(0)
		} else if crop {
			out[i] = 8
		}
	}
	return out
}

// drawMarks draws the crop and cross marks of a page in its bleed area,
// with the same drawing as WeasyPrint.
func (cv *canvas) drawMarks(s *css.Style, area rect, bleed [4]float64) {
	ms, _ := s.Get(css.PMarks).([]string)
	if len(ms) == 0 || bleed == [4]float64{} {
		return
	}
	t, r, b, l := bleed[0], bleed[1], bleed[2], bleed[3]
	ht, hr, hb, hl := t/2, r/2, b/2, l/2
	w, h := area.w, area.h
	f := func(v float64) string { return fmt.Sprintf("%g", v) }
	var sb strings.Builder
	fmt.Fprintf(&sb, `<svg height="%s" width="%s" fill="transparent" stroke="black" stroke-width="1" xmlns="http://www.w3.org/2000/svg">`, f(h), f(w))
	for _, m := range ms {
		switch m {
		case "crop":
			fmt.Fprintf(&sb, `<path d="M0,%s h%s"/>`, f(t), f(hl))
			fmt.Fprintf(&sb, `<path d="M0,%s h%s" transform="translate(%s,0) scale(-1,1)"/>`, f(t), f(hr), f(w))
			fmt.Fprintf(&sb, `<path d="M0,%s h%s" transform="translate(%s,%s) scale(-1,-1)"/>`, f(b), f(hr), f(w), f(h))
			fmt.Fprintf(&sb, `<path d="M0,%s h%s" transform="translate(0,%s) scale(1,-1)"/>`, f(b), f(hl), f(h))
			fmt.Fprintf(&sb, `<path d="M%s,0 v%s"/>`, f(l), f(ht))
			fmt.Fprintf(&sb, `<path d="M%s,0 v%s" transform="translate(%s,%s) scale(-1,-1)"/>`, f(r), f(hb), f(w), f(h))
			fmt.Fprintf(&sb, `<path d="M%s,0 v%s" transform="translate(0,%s) scale(1,-1)"/>`, f(l), f(hb), f(h))
			fmt.Fprintf(&sb, `<path d="M%s,0 v%s" transform="translate(%s,0) scale(-1,1)"/>`, f(r), f(ht), f(w))
		case "cross":
			fmt.Fprintf(&sb, `<circle r="%s" transform="scale(0.5) translate(%s,%s) scale(0.5)"/>`, f(ht), f(w), f(ht))
			fmt.Fprintf(&sb, `<path transform="scale(0.5) translate(%s,0)" d="M-%s,%s h%s M0,0 v%s"/>`, f(w), f(ht), f(ht), f(t), f(t))
			fmt.Fprintf(&sb, `<circle r="%s" transform="translate(0,%s) scale(0.5) translate(%s,-%s) scale(0.5)"/>`, f(hb), f(h), f(w), f(hb))
			fmt.Fprintf(&sb, `<path d="M-%s,-%s h%s M0,0 v-%s" transform="translate(0,%s) scale(0.5) translate(%s,0)"/>`, f(hb), f(hb), f(b), f(b), f(h), f(w))
			fmt.Fprintf(&sb, `<circle r="%s" transform="scale(0.5) translate(%s,%s) scale(0.5)"/>`, f(hl), f(hl), f(h))
			fmt.Fprintf(&sb, `<path d="M%s,-%s v%s M0,0 h%s" transform="scale(0.5) translate(0,%s)"/>`, f(hl), f(hl), f(l), f(l), f(h))
			fmt.Fprintf(&sb, `<circle r="%s" transform="translate(%s,0) scale(0.5) translate(-%s,%s) scale(0.5)"/>`, f(hr), f(w), f(hr), f(h))
			fmt.Fprintf(&sb, `<path d="M-%s,-%s v%s M0,0 h-%s" transform="translate(%s,0) scale(0.5) translate(0,%s)"/>`, f(hr), f(hr), f(r), f(r), f(w), f(h))
		}
	}
	sb.WriteString(`</svg>`)
	svg, err := images.ParseSVG([]byte(sb.String()), "")
	if err != nil {
		return
	}
	cv.drawSVG(svg, area.x, area.y, w, h)
}

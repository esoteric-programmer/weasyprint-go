package weasyprint

import (
	"math"
	"testing"

	"github.com/esoteric-programmer/weasyprint-go/internal/layout"
)

// blockWidths returns the widths of the div boxes in document order.
func blockWidths(d *Document) []float64 {
	var ws []float64
	for _, p := range d.pages {
		p.Box.Descendants(func(b *layout.Box) {
			if b.Kind == layout.KBlock && b.Element != nil && b.Element.Tag == "div" {
				ws = append(ws, b.W)
			}
		})
	}
	return ws
}

func TestFontRelativeUnits(t *testing.T) {
	// The expected widths are WeasyPrint's (DejaVu Sans: "x" is 1120
	// units high, "0" 1303 units wide, of 2048).
	doc, _ := render(t, `<style>@page{size:A5;margin:10mm 20mm} body{margin:0;font:18px 'DejaVu Sans'} div{height:1px}</style>
<div style="width:10ex"></div><div style="width:10ch"></div>
<div style="width:10ex;font-variant-caps:small-caps"></div><div style="width:10ch;font-variant-caps:all-small-caps"></div>
<div style="width:10ex;font-size:2ex"></div><div style="width:10lh;line-height:2"></div><div style="width:2lh;font-size:1lh;line-height:2"></div>
<div style="width:50vw"></div><div style="width:50pvw"></div><div style="width:50vi"></div>`, nil)
	want := []float64{
		10 * 0.54688 * 18,                 // ex
		10 * 0.63623 * 18,                 // ch
		10 * 0.58320 * 18,                 // ex of a small "X"
		10 * 0.50898 * 18,                 // ch of a small "0"
		10 * 0.54688 * (2 * 0.54688 * 18), // ex in font-size: 2ex
		10 * 2 * 18,                       // lh
		2 * 2 * (2384.0 / 2048 * 18),      // font-size: 1lh is the parent's
		(559.37 - 2*75.59) / 2,            // vw: the page area
		559.37 / 2,                        // pvw: the page box
		(559.37 - 2*75.59) / 2,            // vi
	}
	got := blockWidths(doc)
	if len(got) != len(want) {
		t.Fatalf("got %d divs", len(got))
	}
	for i, w := range want {
		if math.Abs(got[i]-w) > 0.05 {
			t.Errorf("div %d: width %.2f, want %.2f", i, got[i], w)
		}
	}
}

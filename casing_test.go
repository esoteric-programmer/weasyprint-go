package weasyprint

import (
	"strings"
	"testing"

	"github.com/esoteric-programmer/weasyprint-go/internal/layout"
)

// texts returns the text of the text boxes, per paragraph.
func texts(d *Document) []string {
	var out []string
	for _, p := range d.pages {
		p.Box.Descendants(func(b *layout.Box) {
			if b.Kind == layout.KBlock && b.Element != nil && b.Element.Tag == "p" {
				var sb strings.Builder
				b.Descendants(func(c *layout.Box) {
					if c.Kind == layout.KText {
						sb.WriteString(c.Text)
					}
				})
				out = append(out, sb.String())
			}
		})
	}
	return out
}

func TestTextTransform(t *testing.T) {
	// WeasyPrint's results, but for titlecase in capitalize.
	doc, _ := render(t, `<style>p{font:14px 'DejaVu Sans'}</style>
<p style="text-transform:capitalize">hello-world (abc) l'été ǆem ßa x.y</p>
<p style="text-transform:uppercase">straße ﬁne</p>
<p style="text-transform:lowercase">ΣΟΦΟΣ ΟΔΟΣ.</p>
<p lang=tr style="text-transform:uppercase">istanbul</p>
<p lang=tr style="text-transform:lowercase">ISTANBUL İ</p>
<p lang=el style="text-transform:uppercase">άυλος ἀρετή</p>
<p lang=nl style="text-transform:capitalize">ijsland</p>
<p style="text-transform:full-width">a-b</p>`, nil)
	want := []string{"Hello-world (Abc) L'été ǅem Ssa X.y", "STRASSE FINE", "σοφος οδος.", "İSTANBUL", "ıstanbul i",
		"ΑΫΛΟΣ ΑΡΕΤΗ", "IJsland", "ａ−ｂ"}
	got := texts(doc)
	if len(got) != len(want) {
		t.Fatalf("got %d paragraphs", len(got))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("paragraph %d: %q, want %q", i, got[i], want[i])
		}
	}
}

func TestSmallCapsEmulation(t *testing.T) {
	// DejaVu Sans has no smcp: Pango's emulation, with lowercase letters
	// uppercased at 0.8 times the size, spaces following the previous
	// character, and "ß" as "SS".
	doc, _ := render(t, `<p style="font:20px 'DejaVu Sans';font-variant-caps:small-caps">Ab ßC d</p>`, nil)
	var sizes []float64
	for _, p := range doc.pages {
		p.Box.Descendants(func(b *layout.Box) {
			if b.Kind == layout.KText && b.Shaped != nil {
				for _, g := range b.Shaped.Glyphs {
					sizes = append(sizes, g.Size)
				}
			}
		})
	}
	want := []float64{20, 16, 16, 16, 16, 20, 20, 16}
	if len(sizes) != len(want) {
		t.Fatalf("got %d glyphs: %v", len(sizes), sizes)
	}
	for i := range want {
		if sizes[i] != want[i] {
			t.Errorf("glyph %d: size %v, want %v", i, sizes[i], want[i])
		}
	}
}

func TestCapitalSharpS(t *testing.T) {
	// German uses "ẞ" when the font has it (DejaVu Sans), "SS" otherwise
	// (Anonymous Pro) and in other languages.
	doc, _ := render(t, `<style>p{font:14px 'DejaVu Sans';text-transform:uppercase}</style>
<p lang=de>Straße</p>
<p lang=de-AT style="font-family:'Anonymous Pro'">Straße</p>
<p lang=en>Straße</p>
<p lang=de style="text-transform:none">Straße</p>`, nil)
	want := []string{"STRAẞE", "STRASSE", "STRASSE", "Straße"}
	got := texts(doc)
	if len(got) != len(want) {
		t.Fatalf("got %d paragraphs", len(got))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("paragraph %d: %q, want %q", i, got[i], want[i])
		}
	}
	// Emulated small caps.
	for _, c := range []struct {
		lang   string
		glyphs int
	}{{"de", 4}, {"en", 5}} {
		doc, _ = render(t, `<p lang=`+c.lang+` style="font:20px 'DejaVu Sans';font-variant-caps:small-caps">Maße</p>`, nil)
		n := 0
		for _, p := range doc.pages {
			p.Box.Descendants(func(b *layout.Box) {
				if b.Kind == layout.KText && b.Shaped != nil {
					n += len(b.Shaped.Glyphs)
				}
			})
		}
		if n != c.glyphs {
			t.Errorf("small caps in %s: %d glyphs, want %d", c.lang, n, c.glyphs)
		}
	}
}

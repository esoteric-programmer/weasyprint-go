package weasyprint

import (
	"strings"
	"testing"

	"github.com/esoteric-programmer/weasyprint-go/internal/layout"
)

// lineWidths returns, per paragraph, the widths of its lines and the
// available width.
func lineWidths(d *Document) (lines [][]float64, avail []float64) {
	for _, p := range d.pages {
		p.Box.Descendants(func(b *layout.Box) {
			if b.Kind != layout.KBlock || len(b.Children) == 0 || b.Children[0].Kind != layout.KLine {
				return
			}
			var ws []float64
			for _, l := range b.Children {
				if l.Kind == layout.KLine {
					ws = append(ws, l.W)
				}
			}
			lines = append(lines, ws)
			avail = append(avail, b.W)
		})
	}
	return
}

func TestTextWrapPretty(t *testing.T) {
	txt := strings.Repeat("Die Donaudampfschifffahrtsgesellschaft beantragte beim Bundesverfassungsgericht eine Überprüfung der Rechtschreibreform, weil die Silbentrennung uneinheitlich war. ", 3)
	for _, width := range []string{"40mm", "70mm", "100mm"} {
		for _, align := range []string{"justify", "left"} {
			src := `<style>p{font:11pt serif;hyphens:auto;width:` + width + `;text-align:` + align + `}</style>` +
				`<p lang=de>` + txt + `</p><p lang=de style="text-wrap-style:pretty">` + txt + `</p>`
			doc, _ := render(t, src, nil)
			lines, avail := lineWidths(doc)
			if len(lines) < 2 {
				t.Fatalf("got %d paragraphs", len(lines))
			}
			lines, avail = lines[len(lines)-2:], avail[len(avail)-2:]
			for k, ws := range lines {
				for _, w := range ws {
					if w > avail[k]+0.5 {
						t.Errorf("%s %s paragraph %d: line of %.1f in %.1f", width, align, k, w, avail[k])
					}
				}
			}
			if len(lines[1]) > len(lines[0])+1 {
				t.Errorf("%s %s: pretty uses %d lines, greedy %d", width, align, len(lines[1]), len(lines[0]))
			}
		}
	}
}

func TestTextWrapBalance(t *testing.T) {
	doc, _ := render(t, `<h1 style="width:300px;font:20px serif;text-wrap:balance">A rather long heading that wraps onto a second line</h1>`, nil)
	lines, _ := lineWidths(doc)
	ws := lines[len(lines)-1]
	if len(ws) != 2 {
		t.Fatalf("lines %v", lines)
	}
	if d := ws[0] - ws[1]; d > 40 || d < -40 {
		t.Errorf("unbalanced lines %v", ws)
	}
}

func TestTextWrapMode(t *testing.T) {
	doc, _ := render(t, `<p style="width:100px;text-wrap-mode:nowrap">one two three four five six</p>`, nil)
	lines, _ := lineWidths(doc)
	if ws := lines[len(lines)-1]; len(ws) != 1 {
		t.Fatalf("nowrap: lines %v", lines)
	}
}

func TestHyphenationRules(t *testing.T) {
	src := `<style>p{width:30px;hyphens:auto;font:16px serif}</style><p lang=de>Silbentrennung</p>`
	count := func(opts *Options) int {
		doc, _ := render(t, src, opts)
		lines, _ := lineWidths(doc)
		return len(lines[len(lines)-1])
	}
	if n := count(nil); n < 2 {
		t.Fatalf("built-in: %d lines", n)
	}
	if n := count(&Options{HyphenationRules: []HyphenationRules{{Lang: "de", Exceptions: []byte("Silbentrennung")}}}); n != 1 {
		t.Errorf("exception: %d lines", n)
	}
}

func TestHyphenateWord(t *testing.T) {
	r, err := HyphenateWord("Silbentrennung", "de", nil)
	if err != nil || r.Hyphenated != "Sil-ben-tren-nung" || len(r.Positions) != 3 || r.Positions[0] != 3 ||
		r.Dictionary == "" || r.Left != 2 {
		t.Errorf("de: %+v %v", r, err)
	}
	// Parts between non-letters are hyphenated separately.
	r, _ = HyphenateWord("e-mail-Adresse", "de", nil)
	if len(r.Positions) != 1 || r.Positions[0] != 12 {
		t.Errorf("e-mail-Adresse: %+v", r)
	}
	// User exceptions.
	r, err = HyphenateWord("Weasyprint", "en", &Options{HyphenationRules: []HyphenationRules{{Lang: "en", Exceptions: []byte("weasy-print")}}})
	if err != nil || r.Hyphenated != "Weasy-print" || len(r.Exceptions) != 1 || !strings.HasSuffix(r.Dictionary, "+ user rules") {
		t.Errorf("exception: %+v %v", r, err)
	}
	// No patterns.
	r, _ = HyphenateWord("word", "xx", nil)
	if r.Dictionary != "" || len(r.Positions) != 0 || r.Hyphenated != "word" {
		t.Errorf("xx: %+v", r)
	}
}

func TestHyphenationRuleLayers(t *testing.T) {
	// A program's list, the user's global configuration and the
	// document's configuration, in that order: later lists win.
	opts := &Options{HyphenationRules: []HyphenationRules{
		{Lang: "en", Exceptions: []byte("weasy-print ta-ble")},
		{Lang: "en", Exceptions: []byte("weasyp-rint")},
		{Lang: "en-GB", Exceptions: []byte("weas-yprint")},
	}}
	for _, c := range [][3]string{{"Weasyprint", "en", "Weasyp-rint"}, {"Weasyprint", "en-GB", "Weas-yprint"},
		{"table", "en-GB", "ta-ble"}, {"hyphenation", "en", "hy-phen-ation"}} {
		r, err := HyphenateWord(c[0], c[1], opts)
		if err != nil || r.Hyphenated != c[2] {
			t.Errorf("%s (%s): %+v %v, want %s", c[0], c[1], r, err, c[2])
		}
	}
}

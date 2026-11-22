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

package otl

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/esoteric-programmer/weasyprint-go/internal/fonts"
)

// TestHarfBuzzCompare compares shaping results with HarfBuzz's, produced
// by testdata/hb.py (set OTL_HB_CASES and OTL_HB_REF).
func TestHarfBuzzCompare(t *testing.T) {
	casesPath, refPath := os.Getenv("OTL_HB_CASES"), os.Getenv("OTL_HB_REF")
	if casesPath == "" {
		t.Skip("OTL_HB_CASES not set")
	}
	var cases []struct {
		Font     string
		Index    int
		Text     string
		Features []string
		Lang     string
		Script   string
		RTL      bool
	}
	var ref [][][5]int
	b, _ := os.ReadFile(casesPath)
	json.Unmarshal(b, &cases)
	b, _ = os.ReadFile(refPath)
	json.Unmarshal(b, &ref)
	loaded := map[string]*fonts.Font{}
	scriptTag := map[string]string{"": "latn", "Latn": "latn", "Hebr": "hebr", "Arab": "arab", "Grek": "grek", "Cyrl": "cyrl"}
	bad := 0
	for ci, c := range cases {
		f := loaded[c.Font]
		if f == nil {
			data, err := os.ReadFile(c.Font)
			if err != nil {
				t.Fatal(err)
			}
			f, err = fonts.Parse(data, c.Index)
			if err != nil {
				t.Fatal(err)
			}
			loaded[c.Font] = f
		}
		var feats []Feature
		for _, s := range c.Features {
			tag, v := s, 1
			if i := strings.Index(s, "="); i >= 0 {
				tag = s[:i]
				v, _ = strconv.Atoi(s[i+1:])
			}
			feats = append(feats, Feature{tag, v})
		}
		runes := []rune(c.Text)
		// Script of each case: like the text package, common characters
		// belong to the surrounding script; here one script per case.
		p := NewPlan(f, Options{Scripts: []string{scriptTag[c.Script]}, Language: c.Lang, RTL: c.RTL, Features: feats})
		out := p.Shape(Input{Runes: runes, Size: f.UnitsPerEm / 1024})
		got := make([][5]int, len(out))
		for i, g := range out {
			id := int(g.ID)
			if g.ignorable {
				// HarfBuzz hides default ignorables with the space glyph.
				id = int(f.GlyphIndex(' '))
			}
			got[i] = [5]int{id, g.Cluster, int(g.XAdvance), int(g.XOffset), int(g.YOffset)}
		}
		want := ref[ci]
		if fmt.Sprint(got) != fmt.Sprint(want) {
			bad++
			if bad <= 40 {
				// First difference.
				k := 0
				for k < len(got) && k < len(want) && got[k] == want[k] {
					k++
				}
				ctx := func(v [][5]int) string {
					lo := max(0, k-1)
					hi := min(len(v), k+3)
					return fmt.Sprint(v[lo:hi])
				}
				t.Errorf("case %d %s %q %v: glyph %d: got %s want %s (len %d/%d)", ci, c.Font[strings.LastIndex(c.Font, "/")+1:], c.Text, c.Features, k, ctx(got), ctx(want), len(got), len(want))
			}
		}
	}
	t.Logf("%d/%d cases differ", bad, len(cases))
}

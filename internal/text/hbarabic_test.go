package text

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"github.com/esoteric-programmer/weasyprint-go/internal/fonts"
	"github.com/esoteric-programmer/weasyprint-go/internal/otl"
)

// TestHarfBuzzArabic compares Arabic shaping with HarfBuzz's (set
// OTL_HB_ARAB_CASES and OTL_HB_ARAB_REF, see internal/otl/testdata).
func TestHarfBuzzArabic(t *testing.T) {
	casesPath, refPath := os.Getenv("OTL_HB_ARAB_CASES"), os.Getenv("OTL_HB_ARAB_REF")
	if casesPath == "" {
		t.Skip("OTL_HB_ARAB_CASES not set")
	}
	var cases []struct {
		Font string
		Text string
	}
	var ref [][][5]int
	b, _ := os.ReadFile(casesPath)
	json.Unmarshal(b, &cases)
	b, _ = os.ReadFile(refPath)
	json.Unmarshal(b, &ref)
	bad := 0
	for ci, c := range cases {
		data, _ := os.ReadFile(c.Font)
		f, err := fonts.Parse(data, 0)
		if err != nil {
			t.Fatal(err)
		}
		runes := []rune(c.Text)
		forms := arabicForms(runes)
		masks := make([]uint32, len(runes))
		for k := range masks {
			switch forms[k] {
			case formIsol:
				masks[k] = otl.MaskIsol
			case formFina:
				masks[k] = otl.MaskFina
			case formMedi:
				masks[k] = otl.MaskMedi
			case formInit:
				masks[k] = otl.MaskInit
			}
		}
		p := otl.NewPlan(f, otl.Options{Scripts: []string{"arab"}, RTL: true, Arabic: true})
		out := p.Shape(otl.Input{Runes: runes, Size: f.UnitsPerEm / 1024, Masks: masks})
		got := make([][5]int, len(out))
		for i, g := range out {
			got[i] = [5]int{int(g.ID), g.Cluster, int(g.XAdvance), int(g.XOffset), int(g.YOffset)}
		}
		if fmt.Sprint(got) != fmt.Sprint(ref[ci]) {
			bad++
			t.Errorf("case %d %s %q fallback=%v:\n got %v\nwant %v", ci, c.Font, c.Text, p.ArabicFallback, got, ref[ci])
		}
	}
	t.Logf("%d/%d differ", bad, len(cases))
}

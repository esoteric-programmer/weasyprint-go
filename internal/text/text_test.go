package text

import (
	"testing"
	"time"

	"github.com/esoteric-programmer/weasyprint-go/internal/fonts"
)

func TestBreaks(t *testing.T) {
	r := []rune("Hello world-wide (test), 漢字です. ab­cd")
	b := Breaks(r, BreakOptions{})
	var got []int
	for i, k := range b {
		if k != BreakNone {
			got = append(got, i)
		}
	}
	t.Logf("%v", got)
	if b[6] != BreakAllowed || b[12] != BreakAllowed || b[18] == BreakAllowed {
		t.Errorf("unexpected breaks %v", got)
	}
}

func TestShape(t *testing.T) {
	start := time.Now()
	lib := fonts.NewLibrary()
	s := NewShaper(lib)
	sh := s.Shape("Hello, AV fi World! 漢字", Spec{Families: []string{"DejaVu Serif"}, Weight: 400, Size: 16})
	t.Logf("shape took %v", time.Since(start))
	if len(sh.Glyphs) == 0 || sh.Pos[len(sh.Runes)] <= 0 {
		t.Fatal("no glyphs")
	}
	for _, g := range sh.Glyphs {
		if g.Face == nil {
			t.Errorf("no face for %q", string(sh.Runes[g.Start:g.End]))
			continue
		}
	}
	t.Logf("width %.2f, face %s, last face %s", sh.Pos[len(sh.Runes)], sh.Glyphs[0].Face.Font.Family, sh.Glyphs[len(sh.Glyphs)-1].Face.Font.Family)
	m := s.PrimaryMetrics(Spec{Families: []string{"DejaVu Serif"}, Weight: 400, Size: 16})
	t.Logf("metrics %+v", m)
	b := s.Faces(Spec{Families: []string{"sans-serif"}, Weight: 700, Size: 16})
	b[0].Loaded()
	t.Logf("sans bold: %s %s synth=%v", b[0].Font.Family, b[0].Font.Subfamily, b[0].SynthBold)
	data := sh.Glyphs[0].Face.Font.Subset(map[uint16]bool{sh.Glyphs[0].ID: true})
	if _, err := fonts.Parse(data, 0); err != nil {
		t.Errorf("subset unparsable: %v", err)
	}
	t.Logf("subset size %d (orig %d)", len(data), len(sh.Glyphs[0].Face.Font.Data))
}

func TestBreaksAroundObjects(t *testing.T) {
	// Atomic inlines (U+FFFC) are break opportunities, except after an
	// opening bracket or before a closing one.
	b := Breaks([]rune("a￼￼b(￼)"), BreakOptions{})
	if b[1] != BreakAllowed || b[2] != BreakAllowed || b[3] != BreakAllowed {
		t.Errorf("expected breaks around objects: %v", b)
	}
	if b[5] == BreakAllowed || b[6] == BreakAllowed {
		t.Errorf("unexpected breaks inside brackets: %v", b)
	}
}

func TestArabicForms(t *testing.T) {
	// بيت: initial beh, medial yeh, final teh; ا isolated.
	got := arabicForms([]rune("بيت ا"))
	want := []uint8{formInit, formMedi, formFina, formNone, formIsol}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("forms %v, want %v", got, want)
		}
	}
	lib := fonts.NewLibrary()
	s := NewShaper(lib)
	spec := Spec{Families: []string{"DejaVu Sans"}, Weight: 400, Size: 16}
	sh := s.Shape("بيت", spec)
	iso := s.Shape("ب", spec)
	if len(sh.Glyphs) == 0 || sh.Glyphs[0].Face == nil || sh.Glyphs[0].Face.Font.GlyphIndex('ب') == 0 {
		t.Skip("no Arabic font")
	}
	if sh.Glyphs[0].ID == iso.Glyphs[0].ID {
		t.Error("initial form not substituted")
	}
}

func TestPangoUnits(t *testing.T) {
	if got := pangoUnits(1.0 / 3); got != 341.0/1024 {
		t.Errorf("pangoUnits(1/3) = %v", got)
	}
}

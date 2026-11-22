package hyphen

import (
	"reflect"
	"testing"
)

func TestPositions(t *testing.T) {
	cases := []struct {
		lang, word string
		want       []int
	}{
		{"nl", "lettergrepen", []int{3, 6, 9}},
		{"en-US", "hyphenation", []int{2, 6}},
		{"fr-FR", "constitution", []int{6, 8}},
	}
	for _, c := range cases {
		d := ForLang(c.lang)
		if d == nil {
			t.Fatalf("no dictionary for %s", c.lang)
		}
		if got := d.Positions(c.word, 2, 2); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s %s: got %v, want %v", c.lang, c.word, got, c.want)
		}
	}
	if ForLang("xx") != nil {
		t.Error("unexpected dictionary for xx")
	}
}

func TestTeX(t *testing.T) {
	cases := []struct{ lang, word, want string }{
		{"de", "Silbentrennung", "Sil-ben-tren-nung"},
		{"de-DE", "Urinstinkt", "Ur-instinkt"},
		// Ambiguous compounds are not hyphenated at the ambiguous joint.
		{"de", "Staubecken", "Staubecken"},
		{"de-AT", "beinhalten", "be-inhal-ten"},
		{"de", "Straße", "Stra-ße"},
		{"de-1901", "Zucker", "Zucker"},
		{"de-CH-1901", "Strasse", "Stras-se"},
		{"en", "hyphenation", "hy-phen-ation"},
		// Exceptions of hyph-en-us.
		{"en-US", "present", "present"},
		{"en-US", "Project", "Project"},
		{"en-US", "table", "ta-ble"},
		{"en-GB", "university", "uni-ver-sity"},
		// \righthyphenmin is 3 for English.
		{"en", "nibblingly", "nib-blingly"},
	}
	for _, c := range cases {
		if got := ForLang(c.lang).Hyphenate(c.word); got != c.want {
			t.Errorf("%s %s: got %s, want %s", c.lang, c.word, got, c.want)
		}
	}
	if got := Lookup("de", Pyphen).Hyphenate("Urinstinkt"); got != "Urin-stinkt" {
		t.Errorf("Pyphen de Urinstinkt: got %s", got)
	}
	// Czech has only GPL TeX patterns: Pyphen's are used.
	if d := Lookup("cs", TeX); d == nil || d.MinWord != 5 {
		t.Error("Czech should fall back to Pyphen")
	}
}

func TestDecomposed(t *testing.T) {
	d := ForLang("de")
	if got := d.Hyphenate("Bücherei"); got != "Bü-che-rei" {
		t.Errorf("decomposed: got %q", got)
	}
	if got := d.Hyphenate("Bücherei"); got != "Bü-che-rei" {
		t.Errorf("composed: got %q", got)
	}
}

func TestAllTeXSets(t *testing.T) {
	for tag, set := range texSets {
		set := set
		d := cached("tex/"+set.file+".txt.gz", func(d *Dict) {
			d.Left, d.Right, d.MinWord = set.left, set.right, set.left+set.right
		})
		if d == nil || len(d.patterns) == 0 {
			t.Errorf("%s: no patterns", tag)
		}
	}
	cases := []struct{ lang, word, want string }{
		{"fr", "hyphénation", "hy-phé-na-tion"},
		{"es", "ejemplo", "ejem-plo"},
		{"it", "sillabazione", "sil-la-ba-zio-ne"},
		{"nl", "lettergreep", "let-ter-greep"},
		{"pl", "przykład", "przy-kład"},
		{"ru", "пример", "при-мер"},
		{"sv", "avstavning", "av-stav-ning"},
		{"no", "orddeling", "ord-de-ling"},
		{"el", "παράδειγμα", "πα-ρά-δειγ-μα"},
		{"sr-Latn", "primer", "pri-mer"},
	}
	for _, c := range cases {
		if got := ForLang(c.lang).Hyphenate(c.word); got != c.want {
			t.Errorf("%s %s: got %s, want %s", c.lang, c.word, got, c.want)
		}
	}
}

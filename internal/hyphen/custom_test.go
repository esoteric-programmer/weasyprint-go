package hyphen

import (
	"strings"
	"testing"
)

func TestCustomRules(t *testing.T) {
	tex := []byte(`% test patterns
\patterns{ % two patterns
1ba 1ca
}
\hyphenation{abc-abcab}`)
	plain := []byte("1ba\n1ca % comment\n")
	hunspell := []byte("ISO8859-1\nLEFTHYPHENMIN 1\n1b\xe4\n1ca\n")
	set, err := NewSet([]Rules{
		{Lang: "xx", Patterns: tex},
		{Lang: "xy", Patterns: plain, Left: 1, Right: 1},
		{Lang: "xz", Patterns: hunspell},
		{Lang: "de", Exceptions: []byte("Silbentrennung\nUr-in-stinkt")},
	}, TeX, nil)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct{ lang, word, want string }{
		{"xx", "abababa", "aba-ba-ba"},
		{"xx-YY", "abcabcab", "abc-abcab"}, // exception, more specific tag
		{"xy", "abab", "a-bab"},
		{"xz", "abäcab", "a-bä-cab"},
		{"de", "Silbentrennung", "Silbentrennung"},
		{"de", "Urinstinkt", "Ur-in-stinkt"},
		{"de", "Druckerei", "Dru-cke-rei"}, // built-in patterns kept
		{"en", "hyphenation", "hy-phen-ation"},
	}
	for _, c := range cases {
		if got := set.Lookup(c.lang, TeX).Hyphenate(c.word); got != c.want {
			t.Errorf("%s %s: got %s, want %s", c.lang, c.word, got, c.want)
		}
	}
	// The built-in dictionary is not modified.
	if got := ForLang("de").Hyphenate("Silbentrennung"); got != "Sil-ben-tren-nung" {
		t.Errorf("built-in changed: %s", got)
	}
	// Patterns extend the built-in ones: "Urinstinkt" gets an extra point
	// from a user pattern, other words keep the built-in points, and the
	// built-in exceptions stay.
	add, err := NewSet([]Rules{{Lang: "en", Patterns: []byte("ur1in")}, {Lang: "de", Patterns: []byte("4n1st"), Exceptions: []byte("Ur-ins-tinkt")}}, TeX, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ lang, word, want string }{
		{"de", "Urinstinkt", "Ur-ins-tinkt"}, // user exception wins
		{"de", "Silbentrennung", "Sil-ben-tren-nung"},
		{"en", "present", "present"}, // built-in exception kept
		{"en", "hyphenation", "hy-phen-ation"},
	} {
		if got := add.Lookup(c.lang, TeX).Hyphenate(c.word); got != c.want {
			t.Errorf("extended %s %s: got %s, want %s", c.lang, c.word, got, c.want)
		}
	}
	// A user pattern adds points (odd 9 wins over all built-in values).
	plus, err := NewSet([]Rules{{Lang: "en", Patterns: []byte("e9x")}}, TeX, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := plus.Lookup("en", TeX).Hyphenate("hexagonal"); !strings.HasPrefix(got, "he-x") {
		t.Errorf("added pattern: got %s", got)
	}
	if got := plus.Lookup("en", TeX).Hyphenate("hyphenation"); got != "hy-phen-ation" {
		t.Errorf("built-in patterns lost: got %s", got)
	}
	// Replace drops the built-in patterns.
	rep, err := NewSet([]Rules{{Lang: "de", Patterns: []byte("1ba"), Replace: true}}, TeX, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := rep.Lookup("de", TeX).Hyphenate("Silbentrennung"); got != "Silbentrennung" {
		t.Errorf("replaced: got %s", got)
	}
	if _, err := NewSet([]Rules{{Lang: "xx", Patterns: []byte(`\patterns{1ba`)}}, TeX, nil); err == nil {
		t.Error("unterminated \\patterns accepted")
	}
}

func TestRuleLayers(t *testing.T) {
	// A list built into a program, the user's global configuration and a
	// document's: later layers win, earlier entries they do not redefine
	// stay, and rules for "de" also apply to "de-CH".
	program := Rules{Lang: "de", Exceptions: []byte("Ur-instinkt Wea-sy-print Sil-bentrennung")}
	global := Rules{Lang: "de", Exceptions: []byte("Urin-stinkt Weasy-print"), Left: 3}
	document := Rules{Lang: "de-CH", Exceptions: []byte("Ur-in-stinkt")}
	set, err := NewSet([]Rules{program, global, document}, TeX, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ lang, word, want string }{
		{"de", "Urinstinkt", "Urin-stinkt"},         // global over program
		{"de-CH", "Urinstinkt", "Ur-in-stinkt"},     // document over global
		{"de-CH", "Weasyprint", "Weasy-print"},      // global, kept in the document's layer
		{"de", "Silbentrennung", "Sil-bentrennung"}, // program, kept
		{"de", "Druckerei", "Dru-cke-rei"},          // built-in patterns
		{"de-AT", "Weasyprint", "Weasy-print"},      // "de" layers only
	} {
		d := set.Lookup(c.lang, TeX)
		var b strings.Builder
		runes := []rune(c.word)
		last := 0
		for _, p := range d.Positions(c.word, 1, 1) {
			b.WriteString(string(runes[last:p]) + "-")
			last = p
		}
		b.WriteString(string(runes[last:]))
		if got := b.String(); got != c.want {
			t.Errorf("%s %s: got %s, want %s", c.lang, c.word, got, c.want)
		}
	}
	if d := set.Lookup("de", TeX); d.Left != 3 || d.MinWord != 5 {
		t.Errorf("limits: left %d, min word %d", d.Left, d.MinWord)
	}
	// Replace drops the built-in patterns and the earlier layers.
	rep, err := NewSet([]Rules{global, {Lang: "de", Exceptions: []byte("Ur-instinkt"), Replace: true}}, TeX, nil)
	if err != nil {
		t.Fatal(err)
	}
	d := rep.Lookup("de", TeX)
	if got := d.Hyphenate("Weasyprint"); got != "Weasyprint" {
		t.Errorf("replace kept an earlier layer: %s", got)
	}
	if got := d.Hyphenate("Urinstinkt"); got != "Ur-instinkt" {
		t.Errorf("replace layer: %s", got)
	}
}

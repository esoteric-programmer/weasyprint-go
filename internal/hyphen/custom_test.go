package hyphen

import "testing"

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
	if _, err := NewSet([]Rules{{Lang: "xx", Patterns: []byte(`\patterns{1ba`)}}, TeX, nil); err == nil {
		t.Error("unterminated \\patterns accepted")
	}
}

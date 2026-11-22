package unorm

import "testing"

func TestCasing(t *testing.T) {
	for _, c := range []struct{ in, upper, lower string }{
		{"straße", "STRASSE", "straße"},
		{"ﬁne ŉ", "FINE ʼN", "ﬁne ŉ"},
		{"ΣΟΦΟΣ ΟΔΟΣ. Σ", "ΣΟΦΟΣ ΟΔΟΣ. Σ", "σοφος οδος. σ"},
		{"İ", "İ", "i̇"},
	} {
		if got := ToUpper(c.in); got != c.upper {
			t.Errorf("ToUpper(%q) = %q, want %q", c.in, got, c.upper)
		}
		if got := ToLower(c.in); got != c.lower {
			t.Errorf("ToLower(%q) = %q, want %q", c.in, got, c.lower)
		}
	}
	if got := TitleRune('ǆ'); got != "ǅ" {
		t.Errorf("TitleRune(ǆ) = %q", got)
	}
	if got := TitleRune('ß'); got != "Ss" {
		t.Errorf("TitleRune(ß) = %q", got)
	}
}

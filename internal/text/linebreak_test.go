package text

import "testing"

func TestAnywherePunctuation(t *testing.T) {
	runes := []rune("ab(cd), ef")
	b := Breaks(runes, BreakOptions{Anywhere: true})
	for _, i := range []int{3, 6, 7} { // after "(", before ")" and ","
		if b[i] != BreakNone {
			t.Errorf("break allowed at %d", i)
		}
	}
	if b[4] != BreakAllowed {
		t.Error("no break inside the word")
	}
}

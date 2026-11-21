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

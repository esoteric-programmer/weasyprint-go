package unorm

import (
	"bufio"
	"os"
	"strconv"
	"strings"
	"testing"
)

func TestNFC(t *testing.T) {
	cases := map[string]string{
		"ä": "ä", "Ǻ": "Ǻ", "ệ": "ệ", "ệ": "ệ",
		"각": "각", "ǘx": "ǘx", "ą́": "ą́",
	}
	for in, want := range cases {
		if got, _ := NFC([]rune(in)); string(got) != want {
			t.Errorf("NFC(%q) = %q, want %q", in, string(got), want)
		}
	}
	f, err := os.Open("/tmp/nfc_cases.txt")
	if err != nil {
		t.Skip("no Python cases")
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	n := 0
	for sc.Scan() {
		parts := strings.Split(sc.Text(), "\t")
		in, _ := strconv.Unquote(`"` + parts[0] + `"`)
		want, _ := strconv.Unquote(`"` + parts[1] + `"`)
		if got, _ := NFC([]rune(in)); string(got) != want {
			if n++; n < 10 {
				t.Errorf("NFC(%+q) = %+q, want %+q", in, string(got), want)
			}
		}
	}
}

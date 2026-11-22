package brotli

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// The .br files are compressed by the reference encoder at several
// qualities and window sizes (see testdata/README).
func TestDecode(t *testing.T) {
	files, _ := filepath.Glob("testdata/*.br")
	if len(files) == 0 {
		t.Skip("no test data")
	}
	for _, f := range files {
		comp, _ := os.ReadFile(f)
		want, err := os.ReadFile(f[:len(f)-3])
		if err != nil {
			t.Fatal(err)
		}
		got, err := Decode(comp)
		if err != nil {
			t.Errorf("%s: %v", f, err)
			continue
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s: output differs (%d vs %d bytes)", f, len(got), len(want))
		}
	}
}

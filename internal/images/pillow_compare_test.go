package images

import (
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// TestPillowCompare decodes the files of IMGT_DIR and writes the size and
// the MD5 of their RGBA pixels to IMGT_DIR/go.txt, for comparison with
// Pillow by a script.
func TestPillowCompare(t *testing.T) {
	dir := os.Getenv("IMGT_DIR")
	if dir == "" {
		t.Skip()
	}
	files, _ := filepath.Glob(filepath.Join(dir, "*"))
	sort.Strings(files)
	var out strings.Builder
	for _, f := range files {
		if strings.HasSuffix(f, ".txt") {
			continue
		}
		data, _ := os.ReadFile(f)
		im, err := Decode(data, "", f)
		name := filepath.Base(f)
		if err != nil {
			fmt.Fprintf(&out, "%s ERROR %v\n", name, err)
			continue
		}
		h := md5.New()
		for i := 0; i < im.Width*im.Height; i++ {
			var px [4]byte
			if im.Components == 1 {
				px = [4]byte{im.Pixels[i], im.Pixels[i], im.Pixels[i], 255}
			} else {
				px = [4]byte{im.Pixels[3*i], im.Pixels[3*i+1], im.Pixels[3*i+2], 255}
			}
			if im.Alpha != nil {
				px[3] = im.Alpha[i]
			}
			h.Write(px[:])
		}
		fmt.Fprintf(&out, "%s %dx%d %s\n", name, im.Width, im.Height, hex.EncodeToString(h.Sum(nil)))
	}
	os.WriteFile(filepath.Join(dir, "go.txt"), []byte(out.String()), 0o644)
}

package fonts

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFontConfigExpand(t *testing.T) {
	dir := t.TempDir()
	conf := `<?xml version="1.0"?>
<fontconfig>
  <alias binding="same"><family>Helvetica</family><accept><family>Bar Gothic</family></accept></alias>
  <match target="pattern">
    <test qual="all" name="family" compare="not_eq"><string>sans-serif</string></test>
    <test qual="all" name="family" compare="not_eq"><string>serif</string></test>
    <edit name="family" mode="append_last"><string>sans-serif</string></edit>
  </match>
  <alias><family>sans-serif</family><prefer><family>Foo Sans</family></prefer></alias>
  <dir>fonts</dir>
</fontconfig>`
	path := filepath.Join(dir, "fonts.conf")
	if err := os.WriteFile(path, []byte(conf), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FONTCONFIG_FILE", path)
	fc := LoadFontConfig()
	if fc == nil {
		t.Fatal("config not loaded")
	}
	got := fc.Expand([]string{"Helvetica"})
	want := []FamilyValue{{"Helvetica", true}, {"Bar Gothic", true}, {"Foo Sans", false}, {"sans-serif", false}}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
	if len(fc.Dirs) != 1 || fc.Dirs[0] != filepath.Join(dir, "fonts") {
		t.Errorf("dirs %v", fc.Dirs)
	}
}

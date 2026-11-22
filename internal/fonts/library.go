package fonts

import (
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"unicode"
)

// FaceInfo describes a font face available on the system.
type FaceInfo struct {
	Path       string
	Index      int
	Families   []string // lowercase
	FullName   string   // lowercase
	PostScript string   // lowercase
	Weight     int
	Italic     bool
	Stretch    int
	Variable   bool
	IsType1    bool

	// Location of the cmap table, for coverage checks without loading
	// the whole font.
	cmapOff, cmapLen int
	coverage         []runeRange
	coverageLoaded   bool

	// For @font-face faces.
	font         *Font
	unicodeRange [][2]rune
	userDefined  bool
	// Features are the OpenType features of the @font-face rule.
	Features []FeatureSetting
	// Axes are the variation axes of variable fonts.
	Axes []Axis
}

// FeatureSetting is an OpenType feature setting.
type FeatureSetting struct {
	Tag   string
	Value int
}

// Covers reports whether a user-defined face's unicode-range covers r.
func (fi *FaceInfo) Covers(r rune) bool {
	if len(fi.unicodeRange) == 0 {
		return true
	}
	for _, ur := range fi.unicodeRange {
		if r >= ur[0] && r <= ur[1] {
			return true
		}
	}
	return false
}

// Library is a font library: system fonts plus @font-face fonts.
type Library struct {
	mu       sync.Mutex
	scanned  bool
	faces    []*FaceInfo
	byFamily map[string][]*FaceInfo
	user     map[string][]*FaceInfo
	loaded   map[*FaceInfo]*Font
	fallback map[rune]*FaceInfo
	allCmaps bool
	byNorm   map[string][]*FaceInfo // families without blanks
	fcOnce   sync.Once
	fc       *FontConfig
	// Dirs are the directories scanned for system fonts.
	Dirs []string
	// CustomDirs disables the font directories of the fontconfig
	// configuration.
	CustomDirs bool
	// Warn receives warnings.
	Warn func(string)
}

// NewLibrary returns a library scanning the default font directories.
func NewLibrary() *Library {
	return &Library{
		byFamily: map[string][]*FaceInfo{},
		user:     map[string][]*FaceInfo{},
		loaded:   map[*FaceInfo]*Font{},
		fallback: map[rune]*FaceInfo{},
		Dirs:     DefaultFontDirs(),
	}
}

// DefaultFontDirs returns the platform font directories.
func DefaultFontDirs() []string {
	var dirs []string
	home, _ := os.UserHomeDir()
	if p := os.Getenv("WEASYPRINT_FONT_PATH"); p != "" {
		dirs = append(dirs, filepath.SplitList(p)...)
	}
	switch runtime.GOOS {
	case "darwin":
		dirs = append(dirs, "/System/Library/Fonts", "/Library/Fonts", filepath.Join(home, "Library/Fonts"))
	case "windows":
		windir := os.Getenv("WINDIR")
		if windir == "" {
			windir = `C:\Windows`
		}
		dirs = append(dirs, filepath.Join(windir, "Fonts"))
		if la := os.Getenv("LOCALAPPDATA"); la != "" {
			dirs = append(dirs, filepath.Join(la, `Microsoft\Windows\Fonts`))
		}
	default:
		if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
			dirs = append(dirs, filepath.Join(xdg, "fonts"))
		} else if home != "" {
			dirs = append(dirs, filepath.Join(home, ".local/share/fonts"))
		}
		if home != "" {
			dirs = append(dirs, filepath.Join(home, ".fonts"))
		}
		dirs = append(dirs, "/usr/local/share/fonts", "/usr/share/fonts")
		if xdgDirs := os.Getenv("XDG_DATA_DIRS"); xdgDirs != "" {
			for _, d := range filepath.SplitList(xdgDirs) {
				dirs = append(dirs, filepath.Join(d, "fonts"))
			}
		}
	}
	return dirs
}

func (l *Library) warn(msg string) {
	if l.Warn != nil {
		l.Warn(msg)
	}
}

func (l *Library) scan() {
	if l.scanned {
		return
	}
	l.scanned = true
	dirs := l.Dirs
	if !l.CustomDirs {
		if fc := l.fontConfig(); fc != nil {
			dirs = append(append([]string{}, dirs...), fc.Dirs...)
		}
	}
	l.faces = append(l.faces, cachedScan(dirs, indexFile)...)
	l.byNorm = map[string][]*FaceInfo{}
	for _, fi := range l.faces {
		for _, fam := range fi.Families {
			l.byFamily[fam] = append(l.byFamily[fam], fi)
			k := normFamily(fam)
			l.byNorm[k] = append(l.byNorm[k], fi)
		}
	}
}

// readerAt reads parts of a file lazily.
type fileReader struct {
	f *os.File
}

func (r fileReader) read(off, n int) []byte {
	b := make([]byte, n)
	m, err := r.f.ReadAt(b, int64(off))
	if err != nil && err != io.EOF {
		return nil
	}
	return b[:m]
}

func indexFile(path string) (faces []*FaceInfo) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	r := fileReader{f}
	head := r.read(0, 12)
	if len(head) < 12 {
		return
	}
	if string(head[:4]) == "wOFF" || string(head[:4]) == "wOF2" {
		data, err := os.ReadFile(path)
		if err != nil {
			return
		}
		n := 1
		if string(head[:4]) == "wOF2" && string(head[4:8]) == "ttcf" {
			if sfnt, err := decodeWOFF2(data); err == nil && len(sfnt) >= 12 {
				n = min(int(u32(sfnt, 8)), 64)
			}
		}
		for i := 0; i < n; i++ {
			font, err := Parse(data, i)
			if err != nil {
				return
			}
			if fi := newFaceInfo(path, i, font); fi != nil {
				faces = append(faces, fi)
			}
		}
		return
	}
	offsets := []int{0}
	if string(head[:4]) == "ttcf" {
		n := int(u32(head, 8))
		if n > 64 {
			n = 64
		}
		dir := r.read(12, 4*n)
		offsets = nil
		for i := 0; i < n; i++ {
			offsets = append(offsets, int(u32(dir, 4*i)))
		}
	}
	for idx, off := range offsets {
		hdr := r.read(off, 12)
		if len(hdr) < 12 {
			continue
		}
		tag := string(hdr[:4])
		if tag != "\x00\x01\x00\x00" && tag != "OTTO" && tag != "true" {
			continue
		}
		n := int(u16(hdr, 4))
		dir := r.read(off+12, 16*n)
		font := &Font{tables: map[string][]byte{}, tabOff: map[string]int{}}
		for i := 0; i < n && 16*i+16 <= len(dir); i++ {
			e := 16 * i
			name := string(dir[e : e+4])
			toff := int(u32(dir, e+8))
			tlen := int(u32(dir, e+12))
			switch name {
			case "name", "OS/2", "head", "fvar":
				if tlen > 1<<20 {
					continue
				}
				font.tables[name] = r.read(toff, tlen)
			case "cmap", "glyf", "CFF ":
				font.tables[name] = nil
				if name == "cmap" {
					font.tabOff["cmap"] = toff
					font.tabOff["cmap-len"] = tlen
				}
			}
		}
		_, hasCmap := font.tables["cmap"]
		if !hasCmap {
			continue
		}
		font.Weight = 400
		font.Stretch = 5
		if os2 := font.tables["OS/2"]; len(os2) >= 64 {
			font.Weight = int(u16(os2, 4))
			font.Stretch = int(u16(os2, 6))
			fsSel := u16(os2, 62)
			font.Italic = fsSel&1 != 0 || fsSel&0x200 != 0
		} else if hd := font.tables["head"]; len(hd) >= 46 {
			ms := u16(hd, 44)
			font.Italic = ms&2 != 0
			if ms&1 != 0 {
				font.Weight = 700
			}
		}
		_, font.Variable = font.tables["fvar"]
		font.parseNames()
		if fi := newFaceInfo(path, idx, font); fi != nil {
			fi.cmapOff, fi.cmapLen = font.tabOff["cmap"], font.tabOff["cmap-len"]
			faces = append(faces, fi)
		}
	}
	return faces
}

func newFaceInfo(path string, idx int, font *Font) *FaceInfo {
	fi := &FaceInfo{Path: path, Index: idx, Weight: font.Weight, Italic: font.Italic,
		Stretch: font.Stretch, Variable: font.Variable, Axes: parseAxes(font.tables["fvar"]),
		FullName: strings.ToLower(font.FullName), PostScript: strings.ToLower(font.PostScript)}
	if fi.Weight < 1 || fi.Weight > 1000 {
		fi.Weight = 400
	}
	// Fonts with tiny OS/2 weight classes (1..9) use the old scale.
	if fi.Weight < 10 {
		fi.Weight *= 100
	}
	if fi.Stretch < 1 || fi.Stretch > 9 {
		fi.Stretch = 5
	}
	sub := strings.ToLower(font.Subfamily + " " + font.TypoSubfamily)
	if strings.Contains(sub, "italic") || strings.Contains(sub, "oblique") {
		fi.Italic = true
	}
	for _, fam := range font.Families() {
		fi.Families = append(fi.Families, strings.ToLower(fam))
	}
	if len(fi.Families) == 0 {
		return nil
	}
	if font.cmap != nil || font.Data != nil {
		fi.coverage = rangesOf(font.Cmap())
		fi.coverageLoaded = true
	}
	return fi
}

type runeRange struct{ lo, hi rune }

func rangesOf(cmap map[rune]uint16) []runeRange {
	runes := make([]rune, 0, len(cmap))
	for r, g := range cmap {
		if g != 0 {
			runes = append(runes, r)
		}
	}
	sort.Slice(runes, func(i, j int) bool { return runes[i] < runes[j] })
	var out []runeRange
	for _, r := range runes {
		if n := len(out); n > 0 && out[n-1].hi+1 == r {
			out[n-1].hi = r
		} else {
			out = append(out, runeRange{r, r})
		}
	}
	return out
}

// covers reports whether a face maps r, reading only its cmap table.
func (l *Library) covers(fi *FaceInfo, r rune) bool {
	if fi.font != nil {
		return fi.font.HasRune(r)
	}
	if f, ok := l.loaded[fi]; ok && f != nil {
		return f.HasRune(r)
	}
	if !fi.coverageLoaded {
		fi.coverageLoaded = true
		fi.coverage = loadCoverage(fi)
	}
	cov := fi.coverage
	i := sort.Search(len(cov), func(i int) bool { return cov[i].hi >= r })
	return i < len(cov) && cov[i].lo <= r
}

// AddFontFace registers an @font-face font.
func (l *Library) AddFontFace(family string, font *Font, weight [2]float64, style, stretch string, unicodeRange [][2]rune, features []FeatureSetting) {
	l.mu.Lock()
	defer l.mu.Unlock()
	w := int(weight[0])
	fi := &FaceInfo{Families: []string{strings.ToLower(family)}, Weight: w, Italic: style == "italic" || style == "oblique",
		Stretch: stretchValue(stretch), font: font, unicodeRange: unicodeRange, userDefined: true, Features: features,
		Variable: font.Variable, Axes: parseAxes(font.tables["fvar"])}
	fam := strings.ToLower(family)
	l.user[fam] = append(l.user[fam], fi)
}

// FindLocal finds a system face by full name or PostScript name.
func (l *Library) FindLocal(name string) *Font {
	l.mu.Lock()
	l.scan()
	l.mu.Unlock()
	name = strings.ToLower(name)
	for _, fi := range l.faces {
		if fi.FullName == name || fi.PostScript == name {
			return l.Load(fi)
		}
	}
	return nil
}

func stretchValue(s string) int {
	switch s {
	case "ultra-condensed":
		return 1
	case "extra-condensed":
		return 2
	case "condensed":
		return 3
	case "semi-condensed":
		return 4
	case "semi-expanded":
		return 6
	case "expanded":
		return 7
	case "extra-expanded":
		return 8
	case "ultra-expanded":
		return 9
	}
	return 5
}

// StretchValue converts a font-stretch keyword to the 1-9 scale.
func StretchValue(s string) int { return stretchValue(s) }

// Load loads the full font of a face.
func (l *Library) Load(fi *FaceInfo) *Font {
	if fi.font != nil {
		return fi.font
	}
	if f, ok := l.loaded[fi]; ok {
		return f
	}
	f, err := loadShared(fi.Path, fi.Index)
	if f == nil && err == nil {
		l.loaded[fi] = nil
		return nil
	}
	if err != nil {
		l.warn("Failed to load font " + fi.Path + ": " + err.Error())
		l.loaded[fi] = nil
		return nil
	}
	l.loaded[fi] = f
	return f
}

// Generic family fallbacks.
var genericFamilies = map[string][]string{
	"serif":         {"DejaVu Serif", "Liberation Serif", "Times New Roman", "Noto Serif", "FreeSerif", "Times", "Georgia", "Nimbus Roman", "TeX Gyre Termes", "Tinos", "Cambria"},
	"sans-serif":    {"DejaVu Sans", "Liberation Sans", "Arial", "Helvetica", "Noto Sans", "FreeSans", "Nimbus Sans", "Arimo", "Verdana", "Open Sans", "Roboto", "Segoe UI"},
	"monospace":     {"DejaVu Sans Mono", "Liberation Mono", "Courier New", "Noto Sans Mono", "FreeMono", "Nimbus Mono PS", "Cousine", "Menlo", "Consolas", "Courier"},
	"cursive":       {"Comic Sans MS", "Comic Neue", "URW Chancery L", "Z003", "Apple Chancery"},
	"fantasy":       {"Impact", "Papyrus", "Luminari"},
	"system-ui":     {"DejaVu Sans", "Segoe UI", "Roboto", "Noto Sans", "Liberation Sans", "Arial"},
	"ui-serif":      {"DejaVu Serif", "Liberation Serif", "Times New Roman"},
	"ui-sans-serif": {"DejaVu Sans", "Liberation Sans", "Arial"},
	"ui-monospace":  {"DejaVu Sans Mono", "Liberation Mono", "Courier New"},
	"math":          {"Latin Modern Math", "STIX Two Math", "DejaVu Math TeX Gyre", "DejaVu Serif"},
	"emoji":         {"Noto Emoji", "Symbola", "DejaVu Sans"},
}

// Metric-compatible substitutes for common proprietary fonts.
var familyAliases = map[string][]string{
	"times new roman": {"Liberation Serif", "Tinos", "TeX Gyre Termes", "Nimbus Roman", "FreeSerif"},
	"times":           {"Times New Roman", "Liberation Serif", "TeX Gyre Termes", "Nimbus Roman"},
	"arial":           {"Liberation Sans", "Arimo", "Nimbus Sans", "FreeSans"},
	"helvetica":       {"Arial", "Liberation Sans", "TeX Gyre Heros", "Nimbus Sans", "Arimo"},
	"helvetica neue":  {"Helvetica", "Arial", "Liberation Sans"},
	"courier new":     {"Liberation Mono", "Cousine", "Nimbus Mono PS", "FreeMono"},
	"courier":         {"Courier New", "Liberation Mono", "TeX Gyre Cursor", "Nimbus Mono PS"},
	"georgia":         {"Gelasio"},
	"calibri":         {"Carlito"},
	"cambria":         {"Caladea"},
	"verdana":         {"DejaVu Sans"},
}

// fontConfig returns the system fontconfig rules, or nil.
func (l *Library) fontConfig() *FontConfig {
	l.fcOnce.Do(func() {
		if os.Getenv("WEASYPRINT_GO_FONTCONFIG") != "0" {
			l.fc = LoadFontConfig()
		}
	})
	return l.fc
}

// OrderedFamilies expands a CSS family list with the system's fontconfig
// substitution rules (aliases, generic families, metric-compatible
// substitutes) and returns the families in matching order: strongly bound
// families first, then weakly bound ones. ok is false when there is no
// fontconfig configuration.
func (l *Library) OrderedFamilies(families []string) (out []string, ok bool) {
	fc := l.fontConfig()
	if fc == nil {
		return nil, false
	}
	expanded := fc.Expand(families)
	seen := map[string]bool{}
	for _, strong := range []bool{true, false} {
		for _, f := range expanded {
			if f.Strong != strong {
				continue
			}
			if k := normFamily(f.Name); !seen[k] {
				seen[k] = true
				out = append(out, f.Name)
			}
		}
	}
	return out, true
}

// FallbackInFamilies returns a face of the first family of an ordered
// list covering r (fontconfig font sorting, as used by Pango to find
// fallback fonts), or nil.
func (l *Library) FallbackInFamilies(families []string, r rune, req Request) *Face {
	l.mu.Lock()
	l.scan()
	var found []*FaceInfo
	for _, fam := range families {
		name := strings.ToLower(fam)
		faces, ok := l.user[name]
		if !ok {
			faces = l.byFamily[name]
			if len(faces) == 0 {
				faces = l.byNorm[normFamily(fam)]
			}
		}
		for _, fi := range faces {
			if l.covers(fi, r) {
				found = faces
				break
			}
		}
		if found != nil {
			break
		}
	}
	l.mu.Unlock()
	if found == nil {
		return nil
	}
	// Match the style among the faces covering the rune.
	l.mu.Lock()
	var covering []*FaceInfo
	for _, c := range found {
		if l.covers(c, r) {
			covering = append(covering, c)
		}
	}
	fi := Select(covering, req)
	f := l.Load(fi)
	l.mu.Unlock()
	if f == nil || !f.HasRune(r) {
		return nil
	}
	return l.resolveFaces([]*FaceInfo{fi}, req)
}

// OrderedFamilyValues is like OrderedFamilies but tells which families
// are strongly bound (named in the CSS family list).
func (l *Library) OrderedFamilyValues(families []string) ([]FamilyValue, bool) {
	fc := l.fontConfig()
	if fc == nil {
		return nil, false
	}
	var out []FamilyValue
	seen := map[string]bool{}
	for _, strong := range []bool{true, false} {
		for _, f := range fc.Expand(families) {
			if f.Strong != strong {
				continue
			}
			if k := normFamily(f.Name); !seen[k] {
				seen[k] = true
				out = append(out, f)
			}
		}
	}
	return out, true
}

// ResolveExact returns the selected face of an installed or user-defined
// family, without generic family or alias substitution.
func (l *Library) ResolveExact(family string, req Request) *Face {
	l.mu.Lock()
	name := strings.ToLower(family)
	faces, ok := l.user[name]
	if !ok {
		l.scan()
		faces = l.byFamily[name]
		if len(faces) == 0 {
			faces = l.byNorm[normFamily(family)]
		}
	}
	l.mu.Unlock()
	if len(faces) == 0 {
		return nil
	}
	return l.resolveFaces(faces, req)
}

// IsGeneric reports whether a family name is a generic family.
func IsGeneric(name string) bool {
	_, ok := genericFamilies[strings.ToLower(name)]
	return ok
}

// FacesForFamily returns the faces of a family (user-defined first).
func (l *Library) FacesForFamily(name string) []*FaceInfo {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.facesForFamily(strings.ToLower(name), 0)
}

func (l *Library) facesForFamily(name string, depth int) []*FaceInfo {
	if u, ok := l.user[name]; ok {
		return u
	}
	l.scan()
	if fs, ok := l.byFamily[name]; ok {
		return fs
	}
	if depth > 3 {
		return nil
	}
	if g, ok := genericFamilies[name]; ok {
		for _, alt := range g {
			if fs := l.facesForFamily(strings.ToLower(alt), depth+1); len(fs) > 0 {
				return fs
			}
		}
		if name != "sans-serif" && name != "serif" {
			return l.facesForFamily("sans-serif", depth+1)
		}
		// Anything at all.
		if len(l.faces) > 0 {
			for _, fi := range l.faces {
				if !fi.IsType1 {
					return l.byFamily[fi.Families[0]]
				}
			}
		}
		return nil
	}
	if a, ok := familyAliases[name]; ok {
		for _, alt := range a {
			if fs := l.facesForFamily(strings.ToLower(alt), depth+1); len(fs) > 0 {
				return fs
			}
		}
	}
	return nil
}

// Request describes the desired face.
type Request struct {
	Weight  int
	Italic  bool
	Stretch int
}

// Select applies the CSS font matching algorithm to faces.
func Select(faces []*FaceInfo, req Request) *FaceInfo {
	if len(faces) == 0 {
		return nil
	}
	cands := faces
	// Stretch.
	if req.Stretch == 0 {
		req.Stretch = 5
	}
	cands = filterBest(cands, func(fi *FaceInfo) float64 {
		d := fi.Stretch - req.Stretch
		if lo, hi, ok := fi.AxisRange("wdth"); ok {
			if p := StretchPercent(req.Stretch); p >= lo && p <= hi {
				d = 0
			}
		}
		if d == 0 {
			return 0
		}
		if req.Stretch <= 5 {
			if d < 0 {
				return float64(-d)
			}
			return 100 + float64(d)
		}
		if d > 0 {
			return float64(d)
		}
		return 100 - float64(d)
	})
	// Style.
	cands = filterBest(cands, func(fi *FaceInfo) float64 {
		if fi.Italic == req.Italic {
			return 0
		}
		if req.Italic {
			if _, hi, ok := fi.AxisRange("ital"); ok && hi >= 1 {
				return 0
			}
			if lo, _, ok := fi.AxisRange("slnt"); ok && lo < 0 {
				return 0
			}
		}
		return 1
	})
	// Weight.
	w := req.Weight
	cands = filterBest(cands, func(fi *FaceInfo) float64 {
		fw := fi.Weight
		if lo, hi, ok := fi.AxisRange("wght"); ok && float64(w) >= lo && float64(w) <= hi {
			return 0
		}
		if fw == w {
			return 0
		}
		switch {
		case w >= 400 && w <= 500:
			if fw > w && fw <= 500 {
				return float64(fw - w)
			}
			if fw < w {
				return 1000 + float64(w-fw)
			}
			return 2000 + float64(fw-w)
		case w < 400:
			if fw < w {
				return float64(w - fw)
			}
			return 1000 + float64(fw-w)
		default:
			if fw > w {
				return float64(fw - w)
			}
			return 1000 + float64(w-fw)
		}
	})
	// Prefer static fonts over variable ones, then file name order.
	sort.SliceStable(cands, func(i, j int) bool {
		if cands[i].Variable != cands[j].Variable {
			return !cands[i].Variable
		}
		return cands[i].Path < cands[j].Path
	})
	return cands[0]
}

func filterBest(faces []*FaceInfo, score func(*FaceInfo) float64) []*FaceInfo {
	best := -1.0
	var out []*FaceInfo
	for _, fi := range faces {
		s := score(fi)
		if best < 0 || s < best {
			best = s
			out = []*FaceInfo{fi}
		} else if s == best {
			out = append(out, fi)
		}
	}
	return out
}

// Face is a selected font with synthesis flags.
type Face struct {
	Font        *Font
	Info        *FaceInfo
	SynthBold   bool
	SynthItalic bool
	// Variations are the axis values of an instance of a variable font.
	Variations map[string]float64

	// Lazily resolved faces (see ResolveLazy) load Font on demand.
	lib  *Library
	once sync.Once
}

// ResolveLazy selects the face of an exact family name like ResolveExact,
// but doesn't load its font: like Pango, fonts of a family list are only
// loaded when they are used. Call Loaded before reading Font.
func (l *Library) ResolveLazy(family string, req Request) *Face {
	l.mu.Lock()
	name := strings.ToLower(family)
	faces, ok := l.user[name]
	if !ok {
		l.scan()
		faces = l.byFamily[name]
		if len(faces) == 0 {
			faces = l.byNorm[normFamily(family)]
		}
	}
	l.mu.Unlock()
	if len(faces) == 0 {
		return nil
	}
	return &Face{Info: Select(faces, req), lib: l}
}

// Loaded loads the font of a lazily resolved face and reports whether it
// is available.
func (f *Face) Loaded() bool {
	f.once.Do(func() {
		if f.Font == nil && f.lib != nil {
			f.lib.mu.Lock()
			f.Font = f.lib.Load(f.Info)
			f.lib.mu.Unlock()
			if f.Font != nil && f.Variations != nil {
				f.Font = f.Font.Instance(f.Variations)
			}
		}
	})
	return f.Font != nil
}

// HasRune reports whether the face maps r, without loading a lazily
// resolved font.
func (f *Face) HasRune(r rune) bool {
	if f.lib == nil {
		return f.Font != nil && f.Font.HasRune(r)
	}
	f.lib.mu.Lock()
	defer f.lib.mu.Unlock()
	return f.lib.covers(f.Info, r)
}

// Resolve returns the selected face for a family name, or nil.
func (l *Library) Resolve(family string, req Request) *Face {
	faces := l.FacesForFamily(family)
	if len(faces) == 0 {
		return nil
	}
	return l.resolveFaces(faces, req)
}

func (l *Library) resolveFaces(faces []*FaceInfo, req Request) *Face {
	fi := Select(faces, req)
	l.mu.Lock()
	f := l.Load(fi)
	l.mu.Unlock()
	if f == nil {
		return nil
	}
	face := &Face{Font: f, Info: fi}
	// Like WeasyPrint, which writes the real glyphs of fonts to PDF, bold
	// and italic are not synthesized.
	return face
}

// Fallback finds a face covering rune r.
func (l *Library) Fallback(r rune, req Request) *Face {
	l.mu.Lock()
	defer l.mu.Unlock()
	if fi, ok := l.fallback[r]; ok {
		if fi == nil {
			return nil
		}
		return l.faceFor(fi, req, r)
	}
	l.scan()
	try := func(names []string) *FaceInfo {
		for _, n := range names {
			for _, fi := range l.byFamily[strings.ToLower(n)] {
				if l.covers(fi, r) {
					if f := l.Load(fi); f != nil && f.HasRune(r) {
						return fi
					}
				}
			}
		}
		return nil
	}
	fi := try(fallbackFamilies(r))
	if fi == nil && !unicode.IsSpace(r) && !unicode.IsControl(r) {
		// Last resort: try every font.
		for _, cand := range l.faces {
			if l.covers(cand, r) {
				if f := l.Load(cand); f != nil && f.HasRune(r) {
					fi = cand
					break
				}
			}
		}
	}
	l.fallback[r] = fi
	if fi == nil {
		return nil
	}
	return l.faceFor(fi, req, r)
}

func (l *Library) faceFor(fi *FaceInfo, req Request, r rune) *Face {
	// Pick the best style within the same family.
	if len(fi.Families) > 0 {
		if fs := l.byFamily[fi.Families[0]]; len(fs) > 0 {
			best := Select(fs, req)
			if l.covers(best, r) {
				fi = best
			}
		}
	}
	f := l.Load(fi)
	if f == nil {
		return nil
	}
	face := &Face{Font: f, Info: fi}
	return face
}

func fallbackFamilies(r rune) []string {
	base := []string{"DejaVu Sans", "Noto Sans", "Liberation Sans", "FreeSans", "Arial Unicode MS", "Segoe UI"}
	switch {
	case r >= 0x2190 && r <= 0x2bff:
		return append([]string{"DejaVu Sans", "Noto Sans Symbols", "Noto Sans Symbols 2", "Symbola", "FreeSerif"}, base...)
	case (r >= 0x3000 && r <= 0x9fff) || (r >= 0xac00 && r <= 0xd7af) || (r >= 0xf900 && r <= 0xfaff) || (r >= 0xff00 && r <= 0xffef):
		return []string{"Noto Sans CJK SC", "Noto Sans CJK JP", "Noto Serif CJK SC", "Source Han Sans", "WenQuanYi Micro Hei", "Droid Sans Fallback", "PingFang SC", "Microsoft YaHei", "MS Gothic"}
	case r >= 0x0600 && r <= 0x06ff:
		return append([]string{"Noto Sans Arabic", "Noto Naskh Arabic", "DejaVu Sans"}, base...)
	case r >= 0x0590 && r <= 0x05ff:
		return append([]string{"Noto Sans Hebrew", "DejaVu Sans"}, base...)
	case r >= 0x0900 && r <= 0x097f:
		return append([]string{"Noto Sans Devanagari", "FreeSans"}, base...)
	case r >= 0x1f000:
		return []string{"Noto Emoji", "Symbola", "Noto Sans Symbols 2", "DejaVu Sans"}
	}
	return base
}

// cmapRanges extracts the covered character ranges of a cmap table without
// building a full character map (segments are taken as a whole).
func cmapRanges(cm []byte) []runeRange {
	if len(cm) < 4 {
		return nil
	}
	n := int(u16(cm, 2))
	best, bestOff := -1, -1
	for i := 0; i < n; i++ {
		r := 4 + 8*i
		platform, enc := u16(cm, r), u16(cm, r+2)
		off := int(u32(cm, r+4))
		if off+2 > len(cm) {
			continue
		}
		format := u16(cm, off)
		score := -1
		switch {
		case format == 12 && (platform == 3 && enc == 10 || platform == 0):
			score = 3
		case format == 4 && (platform == 3 || platform == 0):
			score = 2
		}
		if score > best {
			best, bestOff = score, off
		}
	}
	if bestOff < 0 {
		f := &Font{tables: map[string][]byte{"cmap": cm}}
		return rangesOf(f.Cmap())
	}
	t := cm[bestOff:]
	var out []runeRange
	switch u16(t, 0) {
	case 4:
		segX2 := int(u16(t, 6))
		for s := 0; s < segX2/2; s++ {
			end := rune(u16(t, 14+2*s))
			start := rune(u16(t, 16+segX2+2*s))
			if start > end || start == 0xffff {
				continue
			}
			out = append(out, runeRange{start, end})
		}
	case 12:
		ng := int(u32(t, 12))
		for i := 0; i < ng && 16+12*i+12 <= len(t); i++ {
			out = append(out, runeRange{rune(u32(t, 16+12*i)), rune(u32(t, 16+12*i+4))})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].lo < out[j].lo })
	return out
}

// Package hyphen implements automatic hyphenation with Liang's algorithm.
//
// Languages use the TeX hyphenation patterns of hyph-utf8 (for German
// the experimental patterns of dehyph-exptl, which babel loads as
// "ngerman-x-latest"), with TeX's exception lists and minimum fragment
// lengths. Languages without (permissively licensed) TeX patterns use the
// Hunspell/LibreOffice patterns shipped by Pyphen, which WeasyPrint uses
// for "hyphens: auto"; Pyphen can also be chosen for all languages to
// hyphenate exactly like WeasyPrint.
//
// The pattern files keep their own licenses: see dictionaries/README.md
// and tex/README.md.
package hyphen

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"embed"
	"io"
	"sort"
	"strconv"
	"strings"
	"sync"
	"unicode"

	"github.com/esoteric-programmer/weasyprint-go/internal/unorm"
)

//go:embed dictionaries/*.dic.gz tex/*.txt.gz
var dictFS embed.FS

// Source selects the built-in hyphenation patterns.
type Source int

const (
	// TeX uses the TeX patterns (the default).
	TeX Source = iota
	// Pyphen uses the Hunspell patterns of Pyphen, like WeasyPrint.
	Pyphen
)

var (
	langOnce  sync.Once
	languages map[string]string // lowercase name → file
	dictMu    sync.Mutex
	dicts     = map[string]*Dict{}
)

func loadLanguages() {
	languages = map[string]string{}
	entries, _ := dictFS.ReadDir("dictionaries")
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	for _, n := range names {
		if !strings.HasPrefix(n, "hyph_") || !strings.HasSuffix(n, ".dic.gz") {
			continue
		}
		name := strings.ToLower(n[5 : len(n)-7])
		languages[name] = n
		short := strings.SplitN(name, "_", 2)[0]
		if _, ok := languages[short]; !ok {
			languages[short] = n
		}
	}
}

// LanguageFallback returns the Pyphen dictionary key for a language tag
// using truncation inheritance ("de-AT-1996" → "de_at"), or "" if none
// matches.
func LanguageFallback(lang string) string {
	langOnce.Do(loadLanguages)
	parts := splitTag(lang)
	for len(parts) > 0 {
		l := strings.Join(parts, "_")
		if _, ok := languages[l]; ok {
			return l
		}
		parts = parts[:len(parts)-1]
	}
	return ""
}

func splitTag(lang string) []string {
	return strings.Split(strings.ToLower(strings.ReplaceAll(lang, "-", "_")), "_")
}

type texSet struct {
	file        string
	left, right int
}

// texPatterns returns the TeX pattern set of a language tag and its
// minimum fragment lengths (TeX's \lefthyphenmin and \righthyphenmin), or
// "" for languages without TeX patterns. Tags are mapped to pattern sets
// as babel and hyph-utf8 do.
func texPatterns(lang string) (string, int, int) {
	parts := splitTag(lang)
	has := func(sub string) bool {
		for _, p := range parts[1:] {
			if p == sub {
				return true
			}
		}
		return false
	}
	tag := parts[0]
	switch tag {
	case "de":
		switch {
		case has("1901") && (has("ch") || has("li")):
			tag = "de-ch-1901"
		case has("1901"):
			tag = "de-1901"
		default:
			tag = "de-1996"
		}
	case "en":
		// babel's british, australian and newzealand use the British
		// patterns; other varieties the American ones.
		tag = "en-us"
		for _, p := range []string{"gb", "uk", "au", "nz", "ie", "za", "in"} {
			if has(p) {
				tag = "en-gb"
			}
		}
	case "el":
		tag = "el-monoton"
		if has("polyton") {
			tag = "el-polyton"
		}
	case "fi":
		if has("school") {
			tag = "fi-x-school"
		}
	case "la":
		switch {
		case has("classic"):
			tag = "la-x-classic"
		case has("liturgic"):
			tag = "la-x-liturgic"
		}
	case "mn":
		tag = "mn-cyrl"
	case "no":
		tag = "nb"
	case "sh":
		tag = "sh-latn"
		if has("cyrl") {
			tag = "sh-cyrl"
		}
	case "sr":
		// Serbian: babel's serbian (Latin) and serbianc patterns.
		tag = "sh-cyrl"
		if has("latn") {
			tag = "sh-latn"
		}
	case "am", "ti", "gez", "byn", "tig":
		tag = "mul-ethi"
	case "zh":
		if !has("latn") {
			return "", 0, 0
		}
		tag = "zh-latn-pinyin"
	}
	if set, ok := texSets[tag]; ok {
		return set.file, set.left, set.right
	}
	return "", 0, 0
}

// ForLang returns the default hyphenation dictionary for a language tag,
// or nil.
func ForLang(lang string) *Dict {
	return Lookup(lang, TeX)
}

// Lookup returns the hyphenation dictionary for a language tag, using the
// given built-in patterns, or nil.
func Lookup(lang string, src Source) *Dict {
	if src == TeX {
		if file, left, right := texPatterns(lang); file != "" {
			return cached("tex/"+file+".txt.gz", func(d *Dict) {
				d.Left, d.Right, d.MinWord = left, right, left+right
			})
		}
	}
	key := LanguageFallback(lang)
	if key == "" {
		return nil
	}
	return cached("dictionaries/"+languages[key], func(d *Dict) {
		d.Left, d.Right, d.MinWord = 2, 2, 5
	})
}

func cached(file string, init func(*Dict)) *Dict {
	dictMu.Lock()
	defer dictMu.Unlock()
	if d, ok := dicts[file]; ok {
		return d
	}
	d := loadDict(file, strings.HasPrefix(file, "dictionaries/"))
	if d != nil {
		init(d)
	}
	dicts[file] = d
	return d
}

type pattern struct {
	offset int
	values []uint8
}

// Dict holds the hyphenation patterns of one language.
type Dict struct {
	// Left and Right are the default minimum numbers of letters before
	// and after a hyphen, MinWord the default minimum word length (used
	// for "hyphenate-limit-chars: auto").
	Left, Right, MinWord int

	patterns   map[string]pattern
	exceptions map[string][]int
	maxLen     int
	mu         sync.Mutex
	cache      map[string][]int
}

func loadDict(file string, hunspell bool) *Dict {
	d := &Dict{patterns: map[string]pattern{}, cache: map[string][]int{}}
	f, err := dictFS.Open(file)
	if err != nil {
		return nil
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		return nil
	}
	data, err := io.ReadAll(zr)
	if err != nil {
		return nil
	}
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	// The first line of Hunspell dictionaries is their encoding.
	first := hunspell
	exceptions := false
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if first {
			first = false
			continue
		}
		if !hunspell && line == "%exceptions" {
			exceptions = true
			continue
		}
		if line == "" || ignored(line) {
			continue
		}
		if exceptions {
			d.addException(line)
			continue
		}
		d.addPattern(line)
	}
	return d
}

// addPattern adds a Liang pattern ("zzug4s").
func (d *Dict) addPattern(line string) {
	line = parseHex(line)
	alternative := false
	if i := strings.Index(line, "/"); i >= 0 && strings.Contains(line, "=") {
		// Nonstandard hyphenation (e.g. "ck" → "k-k") changes the
		// letters of the word; these points are not used.
		line = line[:i]
		alternative = true
	}
	var tags []rune
	var values []uint8
	pending := uint8(0)
	hasPending := false
	for _, r := range line {
		if r >= '0' && r <= '9' {
			pending = uint8(r - '0')
			hasPending = true
			continue
		}
		if hasPending {
			values = append(values, pending)
		} else {
			values = append(values, 0)
		}
		tags = append(tags, r)
		pending, hasPending = 0, false
	}
	if hasPending {
		values = append(values, pending)
		tags = append(tags, 0)
	}
	if alternative {
		for i, v := range values {
			if v%2 == 1 {
				values[i] = 0
			}
		}
	}
	maxV := uint8(0)
	for _, v := range values {
		if v > maxV {
			maxV = v
		}
	}
	if maxV == 0 {
		return
	}
	start, end := 0, len(values)
	for values[start] == 0 {
		start++
	}
	for values[end-1] == 0 {
		end--
	}
	// The trailing value of a pattern is not attached to a letter.
	key := string(tagsNoZero(tags))
	d.patterns[key] = pattern{start, append([]uint8(nil), values[start:end]...)}
	if n := len([]rune(key)); n > d.maxLen {
		d.maxLen = n
	}

}

// addException adds a word of a TeX exception list ("as-so-ciate"); a
// word without hyphens is never hyphenated.
func (d *Dict) addException(line string) {
	if d.exceptions == nil {
		d.exceptions = map[string][]int{}
	}
	var word []rune
	pts := []int{}
	for _, r := range line {
		if r == '-' {
			pts = append(pts, len(word))
			continue
		}
		word = append(word, unicode.ToLower(r))
	}
	d.exceptions[string(word)] = pts
}

func tagsNoZero(tags []rune) []rune {
	if len(tags) > 0 && tags[len(tags)-1] == 0 {
		return tags[:len(tags)-1]
	}
	return tags
}

func ignored(line string) bool {
	for _, p := range []string{"%", "#", "LEFTHYPHENMIN", "RIGHTHYPHENMIN", "COMPOUNDLEFTHYPHENMIN", "COMPOUNDRIGHTHYPHENMIN", "NOHYPHEN", "NEXTLEVEL"} {
		if strings.HasPrefix(line, p) {
			return true
		}
	}
	return false
}

// parseHex replaces "^^xx" escapes.
func parseHex(s string) string {
	if !strings.Contains(s, "^^") {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if i+3 < len(s) && s[i] == '^' && s[i+1] == '^' {
			if v, err := strconv.ParseUint(s[i+2:i+4], 16, 8); err == nil {
				b.WriteRune(rune(v))
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// points returns the hyphenation points of a word (rune offsets).
func (d *Dict) points(word string) []int {
	word = strings.ToLower(word)
	if p, ok := d.exceptions[word]; ok {
		return p
	}
	d.mu.Lock()
	if p, ok := d.cache[word]; ok {
		d.mu.Unlock()
		return p
	}
	d.mu.Unlock()
	pw := []rune("." + word + ".")
	refs := make([]uint8, len(pw)+1)
	for i := 0; i < len(pw)-1; i++ {
		stop := i + d.maxLen
		if stop > len(pw) {
			stop = len(pw)
		}
		for j := i + 1; j <= stop; j++ {
			p, ok := d.patterns[string(pw[i:j])]
			if !ok {
				continue
			}
			for k, v := range p.values {
				idx := i + p.offset + k
				if idx < len(refs) && v > refs[idx] {
					refs[idx] = v
				}
			}
		}
	}
	var pts []int
	for i, r := range refs {
		if r%2 == 1 {
			pts = append(pts, i-1)
		}
	}
	d.mu.Lock()
	d.cache[word] = pts
	d.mu.Unlock()
	return pts
}

// Positions returns the rune offsets inside word where it can be
// hyphenated, keeping at least left characters before and right
// characters after. Words with combining marks are hyphenated in their
// composed form, the limits counting characters with their marks.
func (d *Dict) Positions(word string, left, right int) []int {
	runes := []rune(word)
	var src []int
	for _, r := range runes {
		if r >= 0x300 && (unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Mc, r) || unicode.Is(unicode.Me, r)) {
			var nfc []rune
			nfc, src = unorm.NFC(runes)
			word = string(nfc)
			break
		}
	}
	n := len([]rune(word))
	left, right = max(left, 1), max(right, 1)
	var out []int
	for _, p := range d.points(word) {
		if p >= left && p <= n-right {
			if src != nil {
				// Break before the composed character p, after the
				// marks of the previous one.
				p = src[p]
			}
			out = append(out, p)
		}
	}
	return out
}

// Hyphenate returns word with hyphens at its hyphenation points, using
// the dictionary's default limits (for tests and debugging).
func (d *Dict) Hyphenate(word string) string {
	runes := []rune(word)
	if len(runes) < d.MinWord {
		return word
	}
	var b strings.Builder
	last := 0
	for _, p := range d.Positions(word, d.Left, d.Right) {
		b.WriteString(string(runes[last:p]))
		b.WriteByte('-')
		last = p
	}
	b.WriteString(string(runes[last:]))
	return b.String()
}

// IsWordRune reports whether r is part of a hyphenatable word.
func IsWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Mc, r)
}

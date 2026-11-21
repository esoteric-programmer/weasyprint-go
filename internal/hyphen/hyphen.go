// Package hyphen implements automatic hyphenation with Liang's algorithm
// and the Hunspell/LibreOffice hyphenation patterns shipped by Pyphen,
// which WeasyPrint uses for "hyphens: auto".
//
// The dictionaries in the dictionaries directory come from Pyphen,
// converted to UTF-8; each keeps its own license (see the README files).
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
)

//go:embed dictionaries/*.dic.gz
var dictFS embed.FS

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

// LanguageFallback returns the dictionary key for a language tag using
// truncation inheritance ("de-AT-1996" → "de_at"), or "" if none matches.
func LanguageFallback(lang string) string {
	langOnce.Do(loadLanguages)
	parts := strings.Split(strings.ToLower(strings.ReplaceAll(lang, "-", "_")), "_")
	for len(parts) > 0 {
		l := strings.Join(parts, "_")
		if _, ok := languages[l]; ok {
			return l
		}
		parts = parts[:len(parts)-1]
	}
	return ""
}

// ForLang returns the hyphenation dictionary for a language tag, or nil.
func ForLang(lang string) *Dict {
	key := LanguageFallback(lang)
	if key == "" {
		return nil
	}
	file := languages[key]
	dictMu.Lock()
	defer dictMu.Unlock()
	if d, ok := dicts[file]; ok {
		return d
	}
	d := loadDict(file)
	dicts[file] = d
	return d
}

type pattern struct {
	offset int
	values []uint8
}

// Dict holds the hyphenation patterns of one language.
type Dict struct {
	patterns map[string]pattern
	maxLen   int
	mu       sync.Mutex
	cache    map[string][]int
}

func loadDict(file string) *Dict {
	d := &Dict{patterns: map[string]pattern{}, cache: map[string][]int{}}
	f, err := dictFS.Open("dictionaries/" + file)
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
	first := true
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if first {
			first = false
			continue
		}
		if line == "" || ignored(line) {
			continue
		}
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
			continue
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
	return d
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
// hyphenated, keeping at least left runes before and right runes after.
func (d *Dict) Positions(word string, left, right int) []int {
	n := len([]rune(word))
	var out []int
	for _, p := range d.points(word) {
		if p >= left && p <= n-right {
			out = append(out, p)
		}
	}
	return out
}

// IsWordRune reports whether r is part of a hyphenatable word.
func IsWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Mc, r)
}

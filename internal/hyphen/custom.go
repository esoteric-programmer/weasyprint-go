package hyphen

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"unicode/utf8"
)

// User-defined hyphenation rules.

// Rules are user-defined hyphenation rules for a language.
type Rules struct {
	// Lang is the BCP 47 language tag the rules apply to ("de",
	// "de-CH-1901", "en-GB"…); they also apply to more specific tags
	// ("de" rules to "de-CH").
	Lang string
	// Patterns are added to the built-in patterns of the language; a
	// pattern for the same letters as a built-in one replaces it.
	// Accepted formats: TeX files with \patterns{…} (and
	// \hyphenation{…}, added to the exceptions), Hunspell/LibreOffice .dic
	// files (first line is the encoding), and plain pattern lists
	// (hyph-utf8 .pat.txt).
	Patterns []byte
	// Exceptions are words with their hyphenation points ("as-so-ciate";
	// a word without hyphens is never hyphenated), one per line or
	// separated by spaces, or a TeX \hyphenation{…} list. They are added
	// to the built-in exceptions and those of earlier rules; a word in
	// several lists uses the points of the last one.
	Exceptions []byte
	// Replace drops the built-in patterns and exceptions and those of
	// earlier rules for the language, instead of extending them.
	Replace bool
	// Left and Right, if positive, are the minimum numbers of characters
	// before and after a hyphen for hyphenate-limit-chars: auto
	// (\lefthyphenmin and \righthyphenmin).
	Left, Right int
}

// Set holds the user-defined rules of a render, in front of the built-in
// patterns. The zero value and nil use the built-in patterns only.
//
// Rules are layers applied in order: the built-in patterns, then each
// rule whose language is the looked-up tag or a prefix of it ("de" for
// "de-CH"), in the order given. A later layer wins for the same pattern
// letters, the same exception word and the limits; entries of earlier
// layers that it does not redefine stay. A rule with Replace starts over:
// the built-in patterns and the earlier rules are dropped.
type Set struct {
	rules []parsedRule
	mu    sync.Mutex
	cache map[string]*Dict // by tag and source
}

type parsedRule struct {
	tag         string
	patterns    *Dict // user patterns (and \hyphenation exceptions), or nil
	exceptions  *Dict // exception list, or nil
	replace     bool
	left, right int
}

// NewSet builds a set of rules; parse problems are reported to warn.
func NewSet(rules []Rules, src Source, warn func(string)) (*Set, error) {
	s := &Set{cache: map[string]*Dict{}}
	for _, r := range rules {
		tag := normTag(r.Lang)
		if tag == "" {
			return nil, errors.New("hyphenation rules without language")
		}
		pr := parsedRule{tag: tag, replace: r.Replace, left: r.Left, right: r.Right}
		if len(r.Patterns) > 0 {
			ud, err := ParsePatterns(r.Patterns, warn)
			if err != nil {
				return nil, fmt.Errorf("hyphenation patterns for %s: %w", r.Lang, err)
			}
			pr.patterns = ud
		}
		if len(r.Exceptions) > 0 {
			ed := newDict()
			if err := ed.AddExceptions(r.Exceptions); err != nil {
				return nil, fmt.Errorf("hyphenation exceptions for %s: %w", r.Lang, err)
			}
			pr.exceptions = ed
		}
		s.rules = append(s.rules, pr)
	}
	return s, nil
}

func normTag(lang string) string {
	return strings.Trim(strings.ToLower(strings.ReplaceAll(lang, "_", "-")), "-")
}

// Lookup returns the dictionary of a language tag: the built-in patterns
// with the layers of user rules that apply to the tag.
func (s *Set) Lookup(lang string, src Source) *Dict {
	if s == nil || len(s.rules) == 0 {
		return Lookup(lang, src)
	}
	tag := normTag(lang)
	var layers []*parsedRule
	for i := range s.rules {
		r := &s.rules[i]
		if r.tag == tag || strings.HasPrefix(tag, r.tag+"-") {
			if r.replace {
				layers = layers[:0]
			}
			layers = append(layers, r)
		}
	}
	if len(layers) == 0 {
		return Lookup(lang, src)
	}
	key := fmt.Sprintf("%s\x00%d", tag, src)
	s.mu.Lock()
	defer s.mu.Unlock()
	if d, ok := s.cache[key]; ok {
		return d
	}
	var d *Dict
	if base := Lookup(lang, src); base != nil && !layers[0].replace {
		// Copied: the built-in dictionaries are shared.
		d = base.clone()
		d.Name = base.Name + " + user rules"
	} else {
		d = newDict()
		d.Left, d.Right, d.MinWord = 2, 2, 4
		d.Name = "user rules"
	}
	for _, r := range layers {
		if r.patterns != nil {
			d.merge(r.patterns)
		}
		if r.exceptions != nil {
			d.merge(r.exceptions)
		}
		if r.left > 0 {
			d.Left = r.left
		}
		if r.right > 0 {
			d.Right = r.right
		}
		if r.left > 0 || r.right > 0 {
			d.MinWord = d.Left + d.Right
		}
	}
	s.cache[key] = d
	return d
}

func newDict() *Dict {
	return &Dict{patterns: map[string]pattern{}, cache: map[string][]int{}}
}

// clone returns a copy of d (patterns and exceptions copied on write).
func (d *Dict) clone() *Dict {
	return &Dict{Left: d.Left, Right: d.Right, MinWord: d.MinWord, Name: d.Name, patterns: d.patterns,
		exceptions: d.exceptions, maxLen: d.maxLen, cache: map[string][]int{}, shared: true}
}

// own copies the shared maps of a clone before it is modified.
func (d *Dict) own() {
	if !d.shared {
		return
	}
	p := make(map[string]pattern, len(d.patterns))
	for k, v := range d.patterns {
		p[k] = v
	}
	d.patterns = p
	d.exceptions = copyExceptions(d.exceptions)
	d.shared = false
}

// merge adds the patterns, exceptions and minimums of u to d; u's entries
// win.
func (d *Dict) merge(u *Dict) {
	d.own()
	for k, v := range u.patterns {
		d.patterns[k] = v
	}
	if u.maxLen > d.maxLen {
		d.maxLen = u.maxLen
	}
	if d.exceptions == nil {
		d.exceptions = map[string][]int{}
	}
	for k, v := range u.exceptions {
		d.exceptions[k] = v
	}
	if u.Left > 0 {
		d.Left = u.Left
	}
	if u.Right > 0 {
		d.Right = u.Right
	}
	if u.Left > 0 || u.Right > 0 {
		d.MinWord = d.Left + d.Right
	}
	d.cache = map[string][]int{}
}

func copyExceptions(m map[string][]int) map[string][]int {
	out := make(map[string][]int, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// ParsePatterns parses hyphenation patterns (TeX, Hunspell or plain).
// Left and Right are only set by Hunspell LEFTHYPHENMIN/RIGHTHYPHENMIN.
func ParsePatterns(data []byte, warn func(string)) (*Dict, error) {
	d := newDict()
	text, hunspell, err := decodeRules(data)
	if err != nil {
		return nil, err
	}
	if strings.Contains(text, `\patterns`) {
		pats, ok := texGroup(text, `\patterns`)
		if !ok {
			return nil, errors.New(`unterminated \patterns{`)
		}
		for _, p := range strings.Fields(pats) {
			d.addPattern(p)
		}
		if exc, ok := texGroup(text, `\hyphenation`); ok {
			for _, w := range strings.Fields(exc) {
				d.addException(w)
			}
		}
	} else {
		lines := strings.Split(text, "\n")
		if hunspell {
			lines = lines[1:]
		}
		for n, line := range lines {
			line = strings.TrimSpace(stripComment(line))
			if line == "" {
				continue
			}
			if hunspell {
				if v, ok := hunspellMin(line, "LEFTHYPHENMIN"); ok {
					d.Left = v
					continue
				}
				if v, ok := hunspellMin(line, "RIGHTHYPHENMIN"); ok {
					d.Right = v
					continue
				}
				if ignored(line) {
					continue
				}
			}
			for _, p := range strings.Fields(line) {
				if !validPattern(p) {
					if warn != nil {
						warn(fmt.Sprintf("Ignored invalid hyphenation pattern %q on line %d", p, n+1))
					}
					continue
				}
				d.addPattern(p)
			}
		}
	}
	if len(d.patterns) == 0 && len(d.exceptions) == 0 {
		return nil, errors.New("no patterns found")
	}
	return d, nil
}

// AddExceptions adds hyphenation exceptions to the dictionary.
func (d *Dict) AddExceptions(data []byte) error {
	text, _, err := decodeRules(data)
	if err != nil {
		return err
	}
	if g, ok := texGroup(text, `\hyphenation`); ok {
		text = g
	}
	d.own()
	if d.exceptions == nil {
		d.exceptions = map[string][]int{}
	}
	for _, line := range strings.Split(text, "\n") {
		for _, w := range strings.Fields(stripComment(line)) {
			d.addException(w)
		}
	}
	d.mu.Lock()
	d.cache = map[string][]int{}
	d.mu.Unlock()
	return nil
}

func hunspellMin(line, key string) (int, bool) {
	if !strings.HasPrefix(line, key) {
		return 0, false
	}
	var v int
	if _, err := fmt.Sscan(strings.TrimSpace(line[len(key):]), &v); err != nil || v < 1 {
		return 0, false
	}
	return v, true
}

func validPattern(p string) bool {
	letters := 0
	for _, r := range p {
		switch {
		case r >= '0' && r <= '9', r == '.':
		case r == '/' || r == '=' || r == ',':
			return true // nonstandard Hunspell pattern
		default:
			letters++
		}
	}
	return letters > 0
}

// stripComment removes a TeX/Hunspell comment ("%" or "#" to the end of
// the line).
func stripComment(line string) string {
	for i, r := range line {
		if (r == '%' || r == '#') && (i == 0 || line[i-1] != '\\') {
			return line[:i]
		}
	}
	return line
}

// texGroup returns the content of "\cmd{…}" with comments removed.
func texGroup(text, cmd string) (string, bool) {
	i := strings.Index(text, cmd)
	if i < 0 {
		return "", false
	}
	rest := text[i+len(cmd):]
	j := strings.Index(rest, "{")
	if j < 0 {
		return "", false
	}
	rest = rest[j+1:]
	var b strings.Builder
	for _, line := range strings.SplitAfter(rest, "\n") {
		line = stripComment(line)
		if k := strings.Index(line, "}"); k >= 0 {
			b.WriteString(line[:k])
			return b.String(), true
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return "", false
}

// decodeRules returns the text of a rules file (UTF-8, or the single-byte
// encoding named on the first line of Hunspell files) and whether it is a
// Hunspell file.
func decodeRules(data []byte) (string, bool, error) {
	if len(data) >= 3 && data[0] == 0xef && data[1] == 0xbb && data[2] == 0xbf {
		data = data[3:]
	}
	first := string(data)
	if i := strings.IndexByte(first, '\n'); i >= 0 {
		first = first[:i]
	}
	first = strings.TrimSpace(first)
	enc := strings.ToLower(first)
	hunspell := false
	switch {
	case enc == "utf-8" || enc == "utf8":
		hunspell = true
	case strings.HasPrefix(enc, "iso8859-") || strings.HasPrefix(enc, "iso-8859-") ||
		strings.HasPrefix(enc, "microsoft-cp") || strings.HasPrefix(enc, "cp12") ||
		strings.HasPrefix(enc, "koi8") || enc == "tis-620":
		hunspell = true
		name := enc
		name = strings.Replace(name, "iso-8859-", "iso8859-", 1)
		name = strings.Replace(name, "microsoft-", "", 1)
		if name == "iso8859-1" {
			var b strings.Builder
			for _, c := range data {
				b.WriteRune(rune(c))
			}
			return strings.ReplaceAll(b.String(), "\r", ""), true, nil
		}
		table, ok := charsets[name]
		if !ok {
			return "", false, fmt.Errorf("unsupported encoding %q", first)
		}
		var b strings.Builder
		for _, c := range data {
			if c < 0x80 {
				b.WriteByte(c)
			} else {
				b.WriteRune(table[c-0x80])
			}
		}
		return strings.ReplaceAll(b.String(), "\r", ""), true, nil
	}
	if !utf8.Valid(data) {
		return "", false, errors.New("not UTF-8")
	}
	return strings.ReplaceAll(string(data), "\r", ""), hunspell, nil
}

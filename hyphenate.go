package weasyprint

import (
	"strings"

	"github.com/esoteric-programmer/weasyprint-go/internal/hyphen"
)

// WordHyphenation tells how a word is hyphenated by automatic hyphenation
// (see HyphenateWord).
type WordHyphenation struct {
	// Positions are the character (rune) offsets in the word where a
	// hyphen may be inserted: the word can be broken before these
	// characters.
	Positions []int
	// Hyphenated is the word with a hyphen at each position.
	Hyphenated string
	// Dictionary names the patterns used for the language ("TeX
	// hyph-de-1996", "Pyphen hyph_de_DE.dic", "... + user rules"), or ""
	// when the language has none.
	Dictionary string
	// MinWord, Left and Right are the limits of "hyphenate-limit-chars:
	// auto": words shorter than MinWord characters are not hyphenated, and
	// at least Left characters come before a hyphen and Right after it.
	MinWord, Left, Right int
	// Exceptions lists the words (lowercase) whose points come from the
	// exception list instead of the patterns.
	Exceptions []string
}

// HyphenateWord returns the hyphenation positions of a word in a
// language (a BCP 47 tag such as "de", "en-US"), for debugging. It uses
// the patterns that "hyphens: auto" uses with the hyphenation options of
// opts (Hyphenation and HyphenationRules; nil for the defaults), with the
// limits of "hyphenate-limit-chars: auto". Like in text, the parts of the
// word between non-letters ("e-mail", "Donau-Dampfschiff") are hyphenated
// separately, and offsets count characters of the whole word.
func HyphenateWord(word, lang string, opts *Options) (*WordHyphenation, error) {
	if opts == nil {
		opts = &Options{}
	}
	warn := opts.Warn
	if warn == nil {
		warn = func(string) {}
	}
	set, err := hyphenationRules(opts.HyphenationRules, opts.Hyphenation, warn)
	if err != nil {
		return nil, err
	}
	res := &WordHyphenation{Hyphenated: word}
	dict := set.Lookup(lang, hyphen.Source(opts.Hyphenation))
	if dict == nil {
		return res, nil
	}
	res.Dictionary = dict.Name
	res.MinWord, res.Left, res.Right = dict.MinWord, dict.Left, dict.Right
	runes := []rune(word)
	for i := 0; i < len(runes); {
		if !hyphen.IsWordRune(runes[i]) {
			i++
			continue
		}
		j := i
		for j < len(runes) && hyphen.IsWordRune(runes[j]) {
			j++
		}
		part := string(runes[i:j])
		if j-i >= dict.MinWord {
			if dict.IsException(part) {
				res.Exceptions = append(res.Exceptions, strings.ToLower(part))
			}
			for _, p := range dict.Positions(part, dict.Left, dict.Right) {
				if p > 0 && p < j-i {
					res.Positions = append(res.Positions, i+p)
				}
			}
		}
		i = j
	}
	var b strings.Builder
	last := 0
	for _, p := range res.Positions {
		b.WriteString(string(runes[last:p]))
		b.WriteByte('-')
		last = p
	}
	b.WriteString(string(runes[last:]))
	res.Hyphenated = b.String()
	return res, nil
}

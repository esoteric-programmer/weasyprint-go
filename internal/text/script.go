package text

import (
	"strings"
	"unicode"

	"github.com/esoteric-programmer/weasyprint-go/internal/fonts"
)

// Pango itemizes text by script and asks fontconfig for fonts sorted for
// the language of each run: with generic (weakly bound) families, fonts
// supporting the language come first. This file reproduces that ordering.

type script struct {
	table  *unicode.RangeTable
	name   string
	sample rune // a character any font supporting the script covers
}

var scripts = []script{
	{unicode.Latin, "Latin", 'a'},
	{unicode.Arabic, "Arabic", 0x0627},
	{unicode.Hebrew, "Hebrew", 0x05d0},
	{unicode.Han, "Han", 0x6c34},
	{unicode.Hiragana, "Hiragana", 0x3042},
	{unicode.Katakana, "Katakana", 0x30a2},
	{unicode.Hangul, "Hangul", 0xac00},
	{unicode.Cyrillic, "Cyrillic", 0x0416},
	{unicode.Greek, "Greek", 0x03b1},
	{unicode.Thai, "Thai", 0x0e01},
	{unicode.Devanagari, "Devanagari", 0x0915},
	{unicode.Armenian, "Armenian", 0x0531},
	{unicode.Georgian, "Georgian", 0x10d0},
	{unicode.Bengali, "Bengali", 0x0995},
	{unicode.Tamil, "Tamil", 0x0b95},
	{unicode.Ethiopic, "Ethiopic", 0x1200},
}

// scriptOf returns the index of a rune's script in scripts, or -1 for
// common and inherited characters (and unknown scripts).
func scriptOf(r rune) int {
	if r < 0x80 {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
			return 0
		}
		return -1
	}
	for i, s := range scripts {
		if unicode.Is(s.table, r) {
			return i
		}
	}
	return -1
}

// langScript returns the script of a language tag (-1 if unknown).
func langScript(lang string) int {
	l := strings.ToLower(lang)
	if i := strings.IndexAny(l, "-_"); i >= 0 {
		l = l[:i]
	}
	switch l {
	case "ar", "fa", "ur", "ps", "ug", "ku", "sd":
		return 1
	case "he", "iw", "yi":
		return 2
	case "zh":
		return 3
	case "ja":
		return 3
	case "ko":
		return 6
	case "ru", "uk", "be", "bg", "sr", "mk", "kk", "ky", "mn", "tg":
		return 7
	case "el":
		return 8
	case "th":
		return 9
	case "hi", "mr", "ne", "sa":
		return 10
	case "hy":
		return 11
	case "ka":
		return 12
	case "bn", "as":
		return 13
	case "ta":
		return 14
	case "am", "ti":
		return 15
	}
	return 0
}

// runScripts gives each rune the script of its run: common and inherited
// characters take the script of the preceding text, or of the following
// text at the start (as Pango's script iterator does); text without any
// script takes the script of the language.
func runScripts(runes []rune, lang string) []int {
	out := make([]int, len(runes))
	cur := -1
	for i, r := range runes {
		if s := scriptOf(r); s >= 0 {
			cur = s
		}
		out[i] = cur
	}
	// Leading common characters.
	next := -1
	for i := len(runes) - 1; i >= 0; i-- {
		if s := scriptOf(runes[i]); s >= 0 {
			next = s
		}
		if out[i] >= 0 {
			break
		}
		out[i] = next
	}
	def := langScript(lang)
	for i := range out {
		if out[i] < 0 {
			out[i] = def
		}
	}
	return out
}

// sortForScript orders faces as fontconfig sorts them for a run: strongly
// bound families first, then weakly bound families supporting the script
// (language), then the others.
func sortForScript(faces []*fonts.Face, strong []bool, sc int) []*fonts.Face {
	if sc < 0 || sc >= len(scripts) {
		return faces
	}
	sample := scripts[sc].sample
	out := make([]*fonts.Face, 0, len(faces))
	for i, f := range faces {
		if strong[i] {
			out = append(out, f)
		}
	}
	for i, f := range faces {
		if !strong[i] && f.Font.HasRune(sample) {
			out = append(out, f)
		}
	}
	for i, f := range faces {
		if !strong[i] && !f.Font.HasRune(sample) {
			out = append(out, f)
		}
	}
	return out
}

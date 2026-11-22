package unorm

import (
	"strings"
	"unicode"
)

// UpperRune returns the full uppercase mapping of r (ß → SS).
func UpperRune(r rune) string {
	if s, ok := specialUpper[r]; ok {
		return s
	}
	return string(unicode.ToUpper(r))
}

// TitleRune returns the full titlecase mapping of r (ß → Ss).
func TitleRune(r rune) string {
	if s, ok := specialTitle[r]; ok {
		return s
	}
	return string(unicode.ToTitle(r))
}

// ToUpper maps s to uppercase with the full case mappings, like Python's
// str.upper.
func ToUpper(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if m, ok := specialUpper[r]; ok {
			b.WriteString(m)
		} else {
			b.WriteRune(unicode.ToUpper(r))
		}
	}
	return b.String()
}

// ToLower maps s to lowercase with the full case mappings and the
// Final_Sigma rule, like Python's str.lower.
func ToLower(s string) string {
	rs := []rune(s)
	var b strings.Builder
	b.Grow(len(s))
	for i, r := range rs {
		switch {
		case r == 0x130: // İ
			b.WriteString("i̇")
		case r == 0x3a3 && finalSigma(rs, i):
			b.WriteRune(0x3c2)
		default:
			b.WriteRune(unicode.ToLower(r))
		}
	}
	return b.String()
}

// finalSigma reports whether the capital sigma at i ends a word: it
// follows a cased letter and no cased letter follows it (case-ignorable
// characters skipped both ways).
func finalSigma(rs []rune, i int) bool {
	before := false
	for j := i - 1; j >= 0; j-- {
		if caseIgnorable(rs[j]) {
			continue
		}
		before = cased(rs[j])
		break
	}
	if !before {
		return false
	}
	for j := i + 1; j < len(rs); j++ {
		if caseIgnorable(rs[j]) {
			continue
		}
		return !cased(rs[j])
	}
	return true
}

func cased(r rune) bool {
	return unicode.IsUpper(r) || unicode.IsLower(r) || unicode.IsTitle(r) ||
		unicode.Is(unicode.Other_Lowercase, r) || unicode.Is(unicode.Other_Uppercase, r)
}

func caseIgnorable(r rune) bool {
	switch r {
	case '\'', '.', ':', '^', '`', 0xad, 0xb7, 0x2018, 0x2019, 0x2024, 0x2027, 0xfe13, 0xfe52, 0xfe55, 0xff07, 0xff0e, 0xff1a:
		return true
	}
	return unicode.In(r, unicode.Mn, unicode.Me, unicode.Cf, unicode.Lm, unicode.Sk)
}

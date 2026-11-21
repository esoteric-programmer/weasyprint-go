package text

import (
	"unicode"

	"github.com/esoteric-programmer/weasyprint-go/internal/bidi"
	"github.com/esoteric-programmer/weasyprint-go/internal/fonts"
)

// Arabic joining forms.
const (
	formNone = iota
	formIsol
	formFina
	formInit
	formMedi
)

func isArabic(r rune) bool {
	return (r >= 0x0600 && r <= 0x06ff) || (r >= 0x0750 && r <= 0x077f) || (r >= 0x08a0 && r <= 0x08ff)
}

func hasArabic(runes []rune) bool {
	for _, r := range runes {
		if isArabic(r) {
			return true
		}
	}
	return false
}

// joiningType returns the joining type of a rune: 'D' dual, 'R' right,
// 'C' join causing, 'T' transparent or 'U' non-joining.
func joiningType(r rune) byte {
	if t, ok := bidi.JoiningTypes[r]; ok {
		return t
	}
	if r == 0x200c {
		return 'U'
	}
	if unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Me, r) || unicode.Is(unicode.Cf, r) {
		return 'T'
	}
	return 'U'
}

// arabicForms computes the contextual form of each rune (cursive joining
// of Arabic letters, as in HarfBuzz's Arabic shaper).
func arabicForms(runes []rune) []uint8 {
	n := len(runes)
	types := make([]byte, n)
	for i, r := range runes {
		types[i] = joiningType(r)
	}
	forms := make([]uint8, n)
	for i := 0; i < n; i++ {
		t := types[i]
		if t != 'D' && t != 'R' {
			continue
		}
		prev, next := byte('U'), byte('U')
		for j := i - 1; j >= 0; j-- {
			if types[j] != 'T' {
				prev = types[j]
				break
			}
		}
		for j := i + 1; j < n; j++ {
			if types[j] != 'T' {
				next = types[j]
				break
			}
		}
		joinPrev := prev == 'D' || prev == 'C'
		joinNext := t == 'D' && (next == 'D' || next == 'R' || next == 'C')
		switch {
		case joinPrev && joinNext:
			forms[i] = formMedi
		case joinPrev:
			forms[i] = formFina
		case joinNext:
			forms[i] = formInit
		default:
			forms[i] = formIsol
		}
	}
	return forms
}

// arabicGlyph returns the glyph of a letter in a contextual form, using
// the font's GSUB features or, without them, the Unicode presentation
// forms.
func arabicGlyph(f *fonts.Font, r rune, form uint8, gid uint16) uint16 {
	if form == formNone || gid == 0 {
		return gid
	}
	sub := f.Arabic()
	if g, ok := sub.Forms[form-1][gid]; ok {
		return g
	}
	if len(sub.Forms[1]) > 0 || len(sub.Forms[2]) > 0 {
		return gid // the font shapes with GSUB; no special form
	}
	if pf, ok := bidi.PresentationForms[r]; ok {
		if cp := pf[form-1]; cp != 0 {
			if g := f.GlyphIndex(cp); g != 0 {
				return g
			}
		}
	}
	return gid
}

// lamAlef returns the presentation-form ligature of lam followed by an
// alef, for fonts without GSUB ligatures.
func lamAlef(f *fonts.Font, lamForm uint8, alef rune) uint16 {
	k := 0
	if lamForm == formFina || lamForm == formMedi {
		k = 1
	}
	if cp, ok := bidi.LamAlef[[2]rune{rune(k), alef}]; ok {
		return f.GlyphIndex(cp)
	}
	return 0
}

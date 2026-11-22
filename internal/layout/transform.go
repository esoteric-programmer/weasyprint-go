package layout

import (
	"strings"
	"unicode"

	"github.com/esoteric-programmer/weasyprint-go/internal/css"
	"github.com/esoteric-programmer/weasyprint-go/internal/unorm"
)

// langCode returns the primary language subtag of the style's lang, in
// lower case.
func langCode(s *css.Style) string {
	l, ok := s.Get(css.PLang).(css.Str)
	if !ok {
		return ""
	}
	code, _, _ := strings.Cut(string(l), "-")
	return strings.ToLower(code)
}

// transformText applies text-transform t to s, with the full Unicode case
// mappings and WeasyPrint's language tailorings (Greek, Turkish,
// Azerbaijani, Lithuanian, Dutch).
func transformText(s, t, lang string) string {
	switch t {
	case "uppercase":
		return upperLang(s, lang)
	case "lowercase":
		switch lang {
		case "tr", "az":
			s = strings.NewReplacer("I", "ı", "İ", "i").Replace(s)
		case "lt":
			s = strings.NewReplacer("Ì", "i̇̀", "Í", "i̇́", "Ĩ", "i̇̃").Replace(s)
		}
		return unorm.ToLower(s)
	case "capitalize":
		// The first letter or digit after a space separator.
		rs := []rune(s)
		var b strings.Builder
		found := false
		for i := 0; i < len(rs); i++ {
			r := rs[i]
			switch {
			case !found && (unicode.IsLetter(r) || unicode.IsNumber(r)):
				found = true
				switch {
				case lang == "nl" && r == 'i' && i+1 < len(rs) && rs[i+1] == 'j':
					b.WriteString("IJ")
					i++
				case (lang == "tr" || lang == "az") && r == 'i':
					b.WriteRune('İ')
				default:
					// Titlecase, unlike WeasyPrint which uppercases
					// ("ǆ" → "ǅ", "ß" → "Ss").
					b.WriteString(unorm.TitleRune(r))
				}
				continue
			case unicode.In(r, unicode.Zs, unicode.Zl, unicode.Zp):
				found = false
			}
			b.WriteRune(r)
		}
		return b.String()
	case "full-width":
		rs := []rune(s)
		for i, r := range rs {
			switch {
			case r == ' ':
				rs[i] = 0x3000
			case r == '-':
				rs[i] = 0x2212
			case r > 0x20 && r < 0x7f:
				rs[i] = r + 0xfee0
			}
		}
		return string(rs)
	}
	return s
}

// greekDiphthongs are uppercased with a diaeresis keeping them apart.
var greekDiphthongs = strings.NewReplacer("άι", "ΑΪ", "άυ", "ΑΫ", "όι", "ΟΪ", "όυ", "ΟΫ", "έυ", "ΗΫ")

// upperLang uppercases s with the tailoring of language lang.
func upperLang(s, lang string) string {
	switch lang {
	case "el":
		// Greek capitals drop the accents and breathings.
		s = greekDiphthongs.Replace(s)
		var d []rune
		for _, r := range s {
			d = unorm.FullDecompose(d, r)
		}
		out := d[:0]
		for _, r := range d {
			switch r {
			case 0x313, 0x314, 0x301, 0x300, 0x306, 0x342, 0x304, 0x345:
				continue
			}
			out = append(out, r)
		}
		c, _ := unorm.NFC(out)
		s = string(c)
	case "tr", "az":
		s = strings.ReplaceAll(s, "i", "İ")
	}
	return unorm.ToUpper(s)
}

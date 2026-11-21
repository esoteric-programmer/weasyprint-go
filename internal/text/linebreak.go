package text

import "unicode"

// Break is the kind of line break opportunity before a rune.
type Break uint8

const (
	BreakNone Break = iota
	BreakAllowed
	BreakMandatory
	BreakHyphen // after a soft hyphen: a hyphen is shown if broken here
)

type lbClass uint8

const (
	clAL lbClass = iota // alphabetic / other
	clSP                // space
	clBA                // break after (hyphens, tab)
	clBB                // break before
	clB2                // em dash: break before and after
	clHY                // hyphen-minus
	clID                // ideographic
	clCL                // closing punctuation
	clOP                // opening punctuation
	clEX                // exclamation / interrogation
	clIS                // infix separator , . : ;
	clGL                // glue (no break)
	clZW                // zero width space
	clNU                // numeric
	clCM                // combining
	clQU                // quotation
	clNS                // non-starter (small kana, etc.)
	clBK                // mandatory break
	clSH                // soft hyphen
	clPR                // prefix numeric
	clPO                // postfix numeric
	clWJ                // word joiner
)

func classify(r rune) lbClass {
	switch r {
	case ' ':
		return clSP
	case '\n', '\r', 0x0b, 0x0c, 0x85, 0x2028, 0x2029:
		return clBK
	case '\t', 0x1680, 0x2000, 0x2001, 0x2002, 0x2003, 0x2004, 0x2005, 0x2006,
		0x2008, 0x2009, 0x200a, 0x205f, 0x2010, 0x2012, 0x2013, 0x05be, 0x0f0b, 0x1361, 0x17d8, 0x17da, '|':
		return clBA
	case 0x00ad:
		return clSH
	case '-':
		return clHY
	case 0x2014:
		return clB2
	case 0x200b:
		return clZW
	case 0x00a0, 0x202f, 0x2007, 0x180e, 0x034f, 0x2011, 0x0f0c:
		return clGL
	case 0x2060, 0xfeff:
		return clWJ
	case ')', ']', '}', 0x3001, 0x3002, 0xff0c, 0xff0e, 0x300d, 0x300f, 0x3011, 0x3015, 0x3017, 0x3019, 0x301b, 0xff09, 0xff3d, 0xff5d, 0x3009, 0x300b, 0xfe50, 0xfe52:
		return clCL
	case '(', '[', '{', 0x300c, 0x300e, 0x3010, 0x3014, 0x3016, 0x3018, 0x301a, 0xff08, 0xff3b, 0xff5b, 0x3008, 0x300a, 0x00a1, 0x00bf:
		return clOP
	case '!', '?', 0xff01, 0xff1f:
		return clEX
	case ',', '.', ':', ';', 0x037e, 0x0589, 0x060c, 0x060d, 0x07f8, 0x2044, 0xfe10, 0xfe13, 0xfe14:
		return clIS
	case '"', '\'', 0x00ab, 0x00bb, 0x2018, 0x2019, 0x201b, 0x201c, 0x201d, 0x201f, 0x2039, 0x203a:
		return clQU
	case '$', '+', 0x00a3, 0x00a5, 0x20ac, 0x2116, '\\':
		return clPR
	case '%', 0x00a2, 0x00b0, 0x2030, 0x2032, 0x2033, 0x2103, 0x2109:
		return clPO
	case 0x3005, 0x303b, 0x309d, 0x309e, 0x30fd, 0x30fe, 0x30fc, 0x3041, 0x3043, 0x3045, 0x3047, 0x3049, 0x3063, 0x3083, 0x3085, 0x3087, 0x308e, 0x30a1, 0x30a3, 0x30a5, 0x30a7, 0x30a9, 0x30c3, 0x30e3, 0x30e5, 0x30e7, 0x30ee, 0x30f5, 0x30f6, 0x30fb, 0xff1a, 0xff1b, 0x30a0, 0x301c:
		return clNS
	}
	switch {
	case r >= '0' && r <= '9':
		return clNU
	case unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Me, r) || (r >= 0x200c && r <= 0x200f):
		return clCM
	case (r >= 0x2e80 && r <= 0x2fff) || (r >= 0x3040 && r <= 0x30ff) || (r >= 0x3100 && r <= 0x31ff) ||
		(r >= 0x3200 && r <= 0x4dbf) || (r >= 0x4e00 && r <= 0x9fff) || (r >= 0xa000 && r <= 0xa4cf) ||
		(r >= 0xac00 && r <= 0xd7af) || (r >= 0xf900 && r <= 0xfaff) || (r >= 0xfe30 && r <= 0xfe4f) ||
		(r >= 0xff00 && r <= 0xffef) || (r >= 0x20000 && r <= 0x3fffd) || (r >= 0x1f300 && r <= 0x1faff) || r == 0x3000:
		return clID
	}
	return clAL
}

// BreakOptions controls line breaking.
type BreakOptions struct {
	BreakAll bool // word-break: break-all
	KeepAll  bool // word-break: keep-all
	Anywhere bool // line-break: anywhere / overflow-wrap: anywhere
}

// Breaks computes break opportunities. The result has len(runes)+1
// entries; entry i is the opportunity before rune i (entry 0 is never a
// break opportunity, the last entry is always the end of text).
func Breaks(runes []rune, opt BreakOptions) []Break {
	n := len(runes)
	out := make([]Break, n+1)
	if n == 0 {
		return out
	}
	cls := make([]lbClass, n)
	for i, r := range runes {
		cls[i] = classify(r)
		if opt.KeepAll && cls[i] == clID {
			cls[i] = clAL
		}
	}
	// Combining marks take the class of their base.
	for i := 1; i < n; i++ {
		if cls[i] == clCM && cls[i-1] != clSP && cls[i-1] != clBK && cls[i-1] != clZW {
			cls[i] = cls[i-1]
		}
	}
	for i := 1; i < n; i++ {
		before := cls[i-1]
		after := cls[i]
		// Find the class before any spaces.
		j := i - 1
		for j >= 0 && cls[j] == clSP {
			j--
		}
		var beforeSP lbClass = clAL
		if j >= 0 {
			beforeSP = cls[j]
		}
		b := BreakNone
		switch {
		case before == clBK:
			b = BreakMandatory
		case after == clBK || after == clSP || after == clZW:
			b = BreakNone
		case before == clZW:
			b = BreakAllowed
		case after == clCM:
			b = BreakNone
		case before == clWJ || after == clWJ || before == clGL || after == clGL:
			b = BreakNone
		case after == clCL || after == clEX || after == clIS:
			b = BreakNone
		case before == clSP:
			// Break after spaces unless the preceding non-space forbids it.
			if beforeSP == clOP {
				b = BreakNone
			} else {
				b = BreakAllowed
			}
		case before == clOP:
			b = BreakNone
		case before == clQU || after == clQU:
			b = BreakNone
		case after == clNS:
			b = BreakNone
		case before == clSH:
			b = BreakHyphen
		case before == clB2 && after == clB2:
			b = BreakNone
		case after == clBA || after == clHY:
			b = BreakNone
		case after == clBB:
			b = BreakAllowed
		case before == clHY:
			if after == clNU || i < 2 || cls[i-2] == clSP {
				b = BreakNone
			} else {
				b = BreakAllowed
			}
		case before == clBA:
			b = BreakAllowed
		case before == clB2 || after == clB2:
			b = BreakAllowed
		case before == clPR && (after == clNU || after == clID || after == clAL):
			b = BreakNone
		case after == clPO && before == clNU:
			b = BreakNone
		case before == clID || after == clID:
			b = BreakAllowed
		case opt.BreakAll && (before == clAL || before == clNU) && (after == clAL || after == clNU):
			b = BreakAllowed
		default:
			b = BreakNone
		}
		if b == BreakNone && opt.Anywhere && before != clSP && after != clSP && after != clCM {
			b = BreakAllowed
		}
		out[i] = b
	}
	out[n] = BreakAllowed
	return out
}

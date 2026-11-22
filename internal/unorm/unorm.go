// Package unorm provides the Unicode canonical decomposition, composition
// and combining class data used for shaping and hyphenation (a subset of
// the Unicode normalization algorithm, UAX #15).
package unorm

import "sort"

const (
	sBase  = 0xAC00
	lBase  = 0x1100
	vBase  = 0x1161
	tBase  = 0x11A7
	lCount = 19
	vCount = 21
	tCount = 28
	nCount = vCount * tCount
	sCount = lCount * nCount
)

// CombiningClass returns the canonical combining class of r.
func CombiningClass(r rune) uint8 {
	i := sort.Search(len(combiningClasses), func(i int) bool { return combiningClasses[i].hi >= r })
	if i < len(combiningClasses) && combiningClasses[i].lo <= r {
		return combiningClasses[i].class
	}
	return 0
}

// Decompose returns the one-level canonical decomposition of r: a and b
// (b is 0 for singleton decompositions), or ok false.
func Decompose(r rune) (a, b rune, ok bool) {
	if r >= sBase && r < sBase+sCount {
		s := r - sBase
		if t := s % tCount; t != 0 {
			return r - t, tBase + t, true
		}
		return lBase + s/nCount, vBase + (s%nCount)/tCount, true
	}
	i := sort.Search(len(decompositions), func(i int) bool { return decompositions[i][0] >= r })
	if i < len(decompositions) && decompositions[i][0] == r {
		return decompositions[i][1], decompositions[i][2], true
	}
	return 0, 0, false
}

// Compose returns the primary composite of a and b, or ok false.
func Compose(a, b rune) (rune, bool) {
	if a >= lBase && a < lBase+lCount && b >= vBase && b < vBase+vCount {
		return sBase + ((a-lBase)*vCount+b-vBase)*tCount, true
	}
	if a >= sBase && a < sBase+sCount && (a-sBase)%tCount == 0 && b > tBase && b < tBase+tCount {
		return a + b - tBase, true
	}
	i := sort.Search(len(compositions), func(i int) bool {
		c := compositions[i]
		return c[0] > a || (c[0] == a && c[1] >= b)
	})
	if i < len(compositions) && compositions[i][0] == a && compositions[i][1] == b {
		return compositions[i][2], true
	}
	return 0, false
}

// FullDecompose appends the full canonical decomposition of r to dst.
func FullDecompose(dst []rune, r rune) []rune {
	a, b, ok := Decompose(r)
	if !ok {
		return append(dst, r)
	}
	dst = FullDecompose(dst, a)
	if b != 0 {
		dst = FullDecompose(dst, b)
	}
	return dst
}

// NFC returns the canonical composition of runes, and for each output
// rune the index of the input rune it starts at.
func NFC(runes []rune) ([]rune, []int) {
	var d []rune
	var src []int
	for i, r := range runes {
		n := len(d)
		d = FullDecompose(d, r)
		for len(src) < len(d) {
			src = append(src, i)
		}
		// Canonical ordering of the marks just added.
		for j := n; j < len(d); j++ {
			for k := j; k > 0; k-- {
				c := CombiningClass(d[k])
				if c == 0 || CombiningClass(d[k-1]) <= c {
					break
				}
				d[k], d[k-1] = d[k-1], d[k]
				src[k], src[k-1] = src[k-1], src[k]
			}
		}
	}
	if len(d) == 0 {
		return d, src
	}
	out := d[:0:0]
	outSrc := src[:0:0]
	starter := -1
	lastClass := -1
	for i, r := range d {
		c := int(CombiningClass(r))
		if starter >= 0 && lastClass < c {
			if comp, ok := Compose(out[starter], r); ok {
				out[starter] = comp
				if src[i] < outSrc[starter] {
					outSrc[starter] = src[i]
				}
				continue
			}
		}
		if c == 0 {
			starter = len(out)
			lastClass = -1
		} else {
			lastClass = c
		}
		out = append(out, r)
		outSrc = append(outSrc, src[i])
	}
	return out, outSrc
}

// Package bidi implements the Unicode Bidirectional Algorithm (UAX #9),
// as used by Pango (FriBidi) when WeasyPrint lays out text boxes.
package bidi

import "sort"

// Class is a bidirectional character type.
type Class uint8

// Bidi classes.
const (
	L Class = iota
	R
	AL
	EN
	ES
	ET
	AN
	CS
	NSM
	BN
	B
	S
	WS
	ON
	LRE
	LRO
	RLE
	RLO
	PDF
	LRI
	RLI
	FSI
	PDI
)

// ClassOf returns the bidi class of a rune.
func ClassOf(r rune) Class {
	i := sort.Search(len(classRanges), func(i int) bool { return classRanges[i].hi >= r })
	if i < len(classRanges) && classRanges[i].lo <= r {
		return classRanges[i].c
	}
	return L
}

// Mirror returns the mirrored character of r, if any.
func Mirror(r rune) (rune, bool) {
	m, ok := mirrors[r]
	return m, ok
}

// HasRTL reports whether text contains right-to-left characters or
// explicit formatting characters, i.e. whether reordering may be needed.
func HasRTL(runes []rune) bool {
	for _, r := range runes {
		if r < 0x0590 {
			continue
		}
		switch ClassOf(r) {
		case R, AL, AN, RLE, RLO, RLI, FSI, LRE, LRO, LRI:
			return true
		}
	}
	return false
}

const maxDepth = 125

// Levels returns the embedding level of each rune of a paragraph whose
// base level is 0 (left-to-right) or 1 (right-to-left).
func Levels(runes []rune, base int) []uint8 {
	n := len(runes)
	orig := make([]Class, n)
	for i, r := range runes {
		orig[i] = ClassOf(r)
	}
	types := append([]Class(nil), orig...)
	levels := make([]uint8, n)

	// X1–X8: explicit levels and directions.
	type status struct {
		level    uint8
		override Class // ON (none), L or R
		isolate  bool
	}
	stack := []status{{uint8(base), ON, false}}
	overflowIsolates, overflowEmbeddings, validIsolates := 0, 0, 0
	matchingPDI := matchPDI(orig)
	for i := 0; i < n; i++ {
		t := orig[i]
		top := stack[len(stack)-1]
		switch t {
		case RLE, LRE, RLO, LRO, RLI, LRI, FSI:
			isIsolate := t == RLI || t == LRI || t == FSI
			if isIsolate {
				levels[i] = top.level
				if top.override != ON {
					types[i] = top.override
				}
			}
			rtl := t == RLE || t == RLO || t == RLI
			if t == FSI {
				end := matchingPDI[i]
				if end < 0 {
					end = n
				}
				rtl = firstStrong(orig[i+1:end]) == 1
			}
			var nl uint8
			if rtl {
				nl = (top.level + 1) | 1
			} else {
				nl = (top.level + 2) &^ 1
			}
			if nl <= maxDepth && overflowIsolates == 0 && overflowEmbeddings == 0 {
				ov := ON
				if t == RLO {
					ov = R
				} else if t == LRO {
					ov = L
				}
				if isIsolate {
					validIsolates++
				}
				stack = append(stack, status{nl, ov, isIsolate})
			} else if isIsolate {
				overflowIsolates++
			} else if overflowIsolates == 0 {
				overflowEmbeddings++
			}
			if !isIsolate {
				levels[i] = top.level
				types[i] = BN
			}
		case PDI:
			if overflowIsolates > 0 {
				overflowIsolates--
			} else if validIsolates > 0 {
				overflowEmbeddings = 0
				for !stack[len(stack)-1].isolate {
					stack = stack[:len(stack)-1]
				}
				stack = stack[:len(stack)-1]
				validIsolates--
			}
			top = stack[len(stack)-1]
			levels[i] = top.level
			if top.override != ON {
				types[i] = top.override
			}
		case PDF:
			if overflowIsolates == 0 {
				if overflowEmbeddings > 0 {
					overflowEmbeddings--
				} else if !top.isolate && len(stack) >= 2 {
					stack = stack[:len(stack)-1]
				}
			}
			levels[i] = top.level
			types[i] = BN
		case B:
			levels[i] = uint8(base)
		case BN:
			levels[i] = top.level
		default:
			levels[i] = top.level
			if top.override != ON {
				types[i] = top.override
			}
		}
	}

	// X9: removed characters are skipped through the isolating run
	// sequences. X10: resolve each isolating run sequence.
	for _, seq := range isolatingRunSequences(orig, types, levels, matchingPDI, base) {
		resolveSequence(seq, runes, orig, types, levels)
	}

	// L1: separators and trailing whitespace go back to the base level.
	trailing := true
	for i := n - 1; i >= 0; i-- {
		switch orig[i] {
		case B, S:
			levels[i] = uint8(base)
			trailing = true
		case WS, FSI, LRI, RLI, PDI, BN, LRE, RLE, LRO, RLO, PDF:
			if trailing {
				levels[i] = uint8(base)
			}
		default:
			trailing = false
		}
	}
	// Removed characters take the level of the previous character.
	for i := 0; i < n; i++ {
		switch orig[i] {
		case LRE, RLE, LRO, RLO, PDF, BN:
			if i > 0 {
				levels[i] = levels[i-1]
			} else {
				levels[i] = uint8(base)
			}
		}
	}
	return levels
}

// firstStrong returns 0 for L, 1 for R/AL, -1 if none (P2–P3), skipping
// isolates.
func firstStrong(t []Class) int {
	depth := 0
	for _, c := range t {
		switch c {
		case LRI, RLI, FSI:
			depth++
		case PDI:
			if depth > 0 {
				depth--
			}
		case L:
			if depth == 0 {
				return 0
			}
		case R, AL:
			if depth == 0 {
				return 1
			}
		}
	}
	return -1
}

// BaseLevel returns the paragraph level from the first strong character,
// or def if there is none.
func BaseLevel(runes []rune, def int) int {
	t := make([]Class, len(runes))
	for i, r := range runes {
		t[i] = ClassOf(r)
	}
	if l := firstStrong(t); l >= 0 {
		return l
	}
	return def
}

func matchPDI(t []Class) []int {
	m := make([]int, len(t))
	for i := range m {
		m[i] = -1
	}
	var open []int
	for i, c := range t {
		switch c {
		case LRI, RLI, FSI:
			open = append(open, i)
		case PDI:
			if len(open) > 0 {
				m[open[len(open)-1]] = i
				open = open[:len(open)-1]
			}
		case B:
			open = open[:0]
		}
	}
	return m
}

func removed(c Class) bool {
	switch c {
	case LRE, RLE, LRO, RLO, PDF, BN:
		return true
	}
	return false
}

type sequence struct {
	idx      []int // indexes of the characters (removed ones excluded)
	level    uint8
	sos, eos Class
}

func isolatingRunSequences(orig, types []Class, levels []uint8, matching []int, base int) []sequence {
	n := len(orig)
	// Level runs, ignoring removed characters.
	var runs [][]int
	var cur []int
	curLevel := -1
	for i := 0; i < n; i++ {
		if removed(orig[i]) {
			continue
		}
		if int(levels[i]) != curLevel && cur != nil {
			runs = append(runs, cur)
			cur = nil
		}
		curLevel = int(levels[i])
		cur = append(cur, i)
	}
	if cur != nil {
		runs = append(runs, cur)
	}
	runOf := map[int]int{}
	for ri, r := range runs {
		runOf[r[0]] = ri
	}
	var seqs []sequence
	used := make([]bool, len(runs))
	for ri := range runs {
		if used[ri] {
			continue
		}
		first := runs[ri][0]
		if orig[first] == PDI && isMatchedPDI(first, matching) {
			continue // continues an earlier sequence
		}
		var idx []int
		r := ri
		for {
			used[r] = true
			idx = append(idx, runs[r]...)
			last := runs[r][len(runs[r])-1]
			c := orig[last]
			if (c == LRI || c == RLI || c == FSI) && matching[last] >= 0 {
				nr, ok := runOf[matching[last]]
				if ok {
					r = nr
					continue
				}
			}
			break
		}
		seqs = append(seqs, sequenceBounds(idx, orig, levels, matching, base))
	}
	return seqs
}

func isMatchedPDI(i int, matching []int) bool {
	for _, m := range matching {
		if m == i {
			return true
		}
	}
	return false
}

func sequenceBounds(idx []int, orig []Class, levels []uint8, matching []int, base int) sequence {
	n := len(orig)
	level := levels[idx[0]]
	// sos: higher of this level and the level before the sequence.
	prev := base
	for i := idx[0] - 1; i >= 0; i-- {
		if !removed(orig[i]) {
			prev = int(levels[i])
			break
		}
	}
	next := base
	last := idx[len(idx)-1]
	c := orig[last]
	if !((c == LRI || c == RLI || c == FSI) && matching[last] < 0) && !(c == LRI || c == RLI || c == FSI) {
		for i := last + 1; i < n; i++ {
			if !removed(orig[i]) {
				next = int(levels[i])
				break
			}
		}
	}
	dir := func(l int) Class {
		if l%2 == 1 {
			return R
		}
		return L
	}
	return sequence{idx: idx, level: level, sos: dir(max(int(level), prev)), eos: dir(max(int(level), next))}
}

func resolveSequence(seq sequence, runes []rune, orig, types []Class, levels []uint8) {
	idx := seq.idx
	n := len(idx)
	t := make([]Class, n)
	for i, k := range idx {
		t[i] = types[k]
	}
	e := L
	if seq.level%2 == 1 {
		e = R
	}
	// W1: NSM.
	prev := seq.sos
	for i := range t {
		if t[i] == NSM {
			if prev == LRI || prev == RLI || prev == FSI || prev == PDI {
				t[i] = ON
			} else {
				t[i] = prev
			}
		}
		prev = t[i]
	}
	// W2: EN after AL becomes AN.
	last := seq.sos
	for i := range t {
		switch t[i] {
		case L, R, AL:
			last = t[i]
		case EN:
			if last == AL {
				t[i] = AN
			}
		}
	}
	// W3: AL → R.
	for i := range t {
		if t[i] == AL {
			t[i] = R
		}
	}
	// W4: single separators between numbers.
	for i := 1; i+1 < n; i++ {
		if t[i] == ES && t[i-1] == EN && t[i+1] == EN {
			t[i] = EN
		} else if t[i] == CS && t[i-1] == EN && t[i+1] == EN {
			t[i] = EN
		} else if t[i] == CS && t[i-1] == AN && t[i+1] == AN {
			t[i] = AN
		}
	}
	// W5: ET adjacent to EN.
	for i := 0; i < n; i++ {
		if t[i] != ET {
			continue
		}
		j := i
		for j < n && t[j] == ET {
			j++
		}
		if (i > 0 && t[i-1] == EN) || (j < n && t[j] == EN) {
			for k := i; k < j; k++ {
				t[k] = EN
			}
		}
		i = j - 1
	}
	// W6: remaining separators and terminators become ON.
	for i := range t {
		switch t[i] {
		case ES, ET, CS:
			t[i] = ON
		}
	}
	// W7: EN after L (or sos L) becomes L.
	last = seq.sos
	for i := range t {
		switch t[i] {
		case L, R:
			last = t[i]
		case EN:
			if last == L {
				t[i] = L
			}
		}
	}
	// N0: bracket pairs.
	strongOf := func(c Class) Class {
		switch c {
		case L:
			return L
		case R, AN, EN:
			return R
		}
		return ON
	}
	for _, p := range bracketPairs(idx, runes, orig, t) {
		o, c := p[0], p[1]
		found := ON
		for k := o + 1; k < c; k++ {
			if s := strongOf(t[k]); s != ON {
				if s == e {
					found = e
					break
				}
				found = s
			}
		}
		if found == ON {
			continue
		}
		if found != e {
			// Opposite direction inside: check the context before.
			ctx := seq.sos
			for k := o - 1; k >= 0; k-- {
				if s := strongOf(t[k]); s != ON {
					ctx = s
					break
				}
			}
			if ctx != found {
				found = e
			}
		}
		t[o], t[c] = found, found
		// NSMs following the brackets take their direction.
		for k := o + 1; k < n && orig[idx[k]] == NSM; k++ {
			t[k] = found
		}
		for k := c + 1; k < n && orig[idx[k]] == NSM; k++ {
			t[k] = found
		}
	}
	// N1–N2: neutrals.
	isNeutral := func(c Class) bool {
		switch c {
		case B, S, WS, ON, FSI, LRI, RLI, PDI:
			return true
		}
		return false
	}
	for i := 0; i < n; i++ {
		if !isNeutral(t[i]) {
			continue
		}
		j := i
		for j < n && isNeutral(t[j]) {
			j++
		}
		before := seq.sos
		if i > 0 {
			before = strongOf(t[i-1])
		}
		after := seq.eos
		if j < n {
			after = strongOf(t[j])
		}
		dir := e
		if before == after && before != ON {
			dir = before
		}
		for k := i; k < j; k++ {
			t[k] = dir
		}
		i = j - 1
	}
	// I1–I2: implicit levels.
	for i, k := range idx {
		l := levels[k]
		switch {
		case l%2 == 0 && t[i] == R:
			levels[k] = l + 1
		case l%2 == 0 && (t[i] == AN || t[i] == EN):
			levels[k] = l + 2
		case l%2 == 1 && (t[i] == L || t[i] == EN || t[i] == AN):
			levels[k] = l + 1
		}
	}
}

// bracketPairs identifies bracket pairs (BD16) in a sequence, as
// positions in the sequence.
func bracketPairs(idx []int, runes []rune, orig, t []Class) [][2]int {
	type open struct {
		closer rune
		pos    int
	}
	var stack []open
	var pairs [][2]int
	for i, k := range idx {
		if t[i] != ON {
			continue
		}
		r := runes[k]
		if cl, ok := openBrackets[r]; ok {
			if len(stack) == 63 {
				break
			}
			stack = append(stack, open{cl, i})
			continue
		}
		// Closing bracket: find the matching opener.
		for s := len(stack) - 1; s >= 0; s-- {
			if stack[s].closer == r || (r == 0x232A && stack[s].closer == 0x3009) || (r == 0x3009 && stack[s].closer == 0x232A) {
				pairs = append(pairs, [2]int{stack[s].pos, i})
				stack = stack[:s]
				break
			}
		}
	}
	sort.Slice(pairs, func(a, b int) bool { return pairs[a][0] < pairs[b][0] })
	return pairs
}

// Reorder returns the visual order of the characters of a line with the
// given levels (rule L2): out[visual] = logical index.
func Reorder(levels []uint8) []int {
	n := len(levels)
	order := make([]int, n)
	for i := range order {
		order[i] = i
	}
	if n == 0 {
		return order
	}
	var hi, lo uint8 = 0, 255
	for _, l := range levels {
		if l > hi {
			hi = l
		}
		if l%2 == 1 && l < lo {
			lo = l
		}
	}
	if lo == 255 {
		return order
	}
	lv := append([]uint8(nil), levels...)
	for l := hi; l >= lo && l > 0; l-- {
		for i := 0; i < n; i++ {
			if lv[i] < l {
				continue
			}
			j := i
			for j < n && lv[j] >= l {
				j++
			}
			for a, b := i, j-1; a < b; a, b = a+1, b-1 {
				order[a], order[b] = order[b], order[a]
				lv[a], lv[b] = lv[b], lv[a]
			}
			i = j
		}
	}
	return order
}

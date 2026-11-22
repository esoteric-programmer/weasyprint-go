package otl

// GSUB lookup application.

// applyLookupAll applies a lookup to the whole buffer.
func (s *shaper) applyLookupAll(t *layoutTable, li int, mask uint32, value int) {
	l := t.lookup(li)
	if l == nil || l.typ == 0 {
		return
	}
	m := &matcher{s: s, flag: l.flag, filterSet: l.filterSet, mask: mask, ignoreZWNJ: t.gpos}
	if !t.gpos && l.typ == 8 {
		for i := len(s.buf) - 1; i >= 0; i-- {
			g := &s.buf[i]
			if (mask != 0 && g.mask&mask == 0) || m.ignoredByFlag(g) {
				continue
			}
			for _, so := range l.subtables {
				if s.reverseChain(t, m, so, i) {
					break
				}
			}
		}
		return
	}
	for i := 0; i < len(s.buf); {
		g := &s.buf[i]
		if (mask != 0 && g.mask&mask == 0) || m.ignoredByFlag(g) {
			i++
			continue
		}
		next, ok := s.applyAt(t, l, m, i, value)
		if !ok {
			i++
			continue
		}
		if next <= i {
			next = i + 1
		}
		i = next
	}
}

// applyAt applies the first matching subtable of a lookup at position i,
// returning the position to continue at.
func (s *shaper) applyAt(t *layoutTable, l *lookup, m *matcher, i, value int) (int, bool) {
	s.depth++
	defer func() { s.depth-- }()
	if s.depth > 16 {
		return 0, false
	}
	for _, so := range l.subtables {
		var next int
		var ok bool
		if t.gpos {
			next, ok = s.applyGPOS(t, l, m, so, i)
		} else {
			next, ok = s.applyGSUB(t, l.typ, m, so, i, value)
		}
		if ok {
			return next, true
		}
	}
	return 0, false
}

func (s *shaper) applyGSUB(t *layoutTable, typ uint16, m *matcher, so, i, value int) (int, bool) {
	d := t.d
	g := s.buf[i].ID
	switch typ {
	case 1: // single
		ci := d.coverage(so+int(d.u16(so+2)), g)
		if ci < 0 {
			return 0, false
		}
		switch d.u16(so) {
		case 1:
			s.setGlyph(i, g+d.u16(so+4), 0)
		case 2:
			if ci >= int(d.u16(so+4)) {
				return 0, false
			}
			s.setGlyph(i, d.u16(so+6+2*ci), 0)
		default:
			return 0, false
		}
		return i + 1, true
	case 2: // multiple
		ci := d.coverage(so+int(d.u16(so+2)), g)
		if ci < 0 || ci >= int(d.u16(so+4)) {
			return 0, false
		}
		seq := so + int(d.u16(so+6+2*ci))
		n := int(d.u16(seq))
		return s.multiple(i, d, seq+2, n), true
	case 3: // alternate
		ci := d.coverage(so+int(d.u16(so+2)), g)
		if ci < 0 || ci >= int(d.u16(so+4)) {
			return 0, false
		}
		set := so + int(d.u16(so+6+2*ci))
		n := int(d.u16(set))
		if value < 1 || value > n {
			return 0, false
		}
		s.setGlyph(i, d.u16(set+2*value), 0)
		return i + 1, true
	case 4: // ligature
		return s.ligature(d, m, so, i)
	case 5:
		return s.context(t, m, so, i)
	case 6:
		return s.chainContext(t, m, so, i)
	}
	return 0, false
}

// multiple replaces buf[i] by n glyphs read at off.
func (s *shaper) multiple(i int, d data, off, n int) int {
	if n == 1 {
		s.setGlyph(i, d.u16(off), 0)
		return i + 1
	}
	orig := s.buf[i]
	if n == 0 {
		// Deletion: the cluster goes to a neighbor.
		if i+1 < len(s.buf) {
			s.mergeClusters(i, i+2)
		} else if i > 0 {
			s.mergeClusters(i-1, i+1)
		}
		s.buf = append(s.buf[:i], s.buf[i+1:]...)
		return i
	}
	ng := make([]Glyph, n)
	for k := 0; k < n; k++ {
		ng[k] = orig
		ng[k].ID = d.u16(off + 2*k)
		if k > 0 {
			ng[k].cp = 0
		}
		ng[k].multiplied = true
		ng[k].ligID = 0
		ng[k].ligComp = uint8(k)
		klass := orig.props
		if klass&propLig != 0 {
			klass = propBase
		}
		s.setProps(&ng[k], klass)
	}
	rest := append([]Glyph(nil), s.buf[i+1:]...)
	s.buf = append(append(s.buf[:i], ng...), rest...)
	return i + n
}

func (s *shaper) ligature(d data, m *matcher, so, i int) (int, bool) {
	ci := d.coverage(so+int(d.u16(so+2)), s.buf[i].ID)
	if ci < 0 || ci >= int(d.u16(so+4)) {
		return 0, false
	}
	set := so + int(d.u16(so+6+2*ci))
	n := int(d.u16(set))
	for k := 0; k < n; k++ {
		lo := set + int(d.u16(set+2+2*k))
		lig := d.u16(lo)
		count := int(d.u16(lo + 2))
		pos, ok := m.matchInput(i, count, func(g uint16, k int) bool { return g == d.u16(lo+4+2*(k-1)) })
		if !ok {
			continue
		}
		s.ligate(pos, lig)
		return i + 1, true
	}
	return 0, false
}

// ligate replaces the glyphs at pos by a ligature glyph.
func (s *shaper) ligate(pos []int, lig uint16) {
	first, last := pos[0], pos[len(pos)-1]
	isMarkLig := true
	for _, p := range pos {
		if s.buf[p].props&propMark == 0 {
			isMarkLig = false
			break
		}
	}
	var ligID uint8
	klass := uint8(propLig)
	if isMarkLig {
		klass = propMark
	} else {
		s.nextLigID++
		if s.nextLigID == 0 {
			s.nextLigID = 1
		}
		ligID = s.nextLigID
	}
	s.mergeClusters(first, last+1)
	isComponent := map[int]bool{}
	for _, p := range pos {
		isComponent[p] = true
	}
	numComps := func(g *Glyph) int {
		if g.props&propLig != 0 && g.ligID != 0 && g.ligComps > 0 {
			return int(g.ligComps)
		}
		return 1
	}
	// Marks between components belong to the component before them.
	soFar, lastNum := 0, 0
	lastLig := uint8(0)
	for j := first; j <= last; j++ {
		g := &s.buf[j]
		if isComponent[j] {
			lastLig = g.ligID
			lastNum = numComps(g)
			soFar += lastNum
			continue
		}
		if !isMarkLig && g.props&propMark != 0 {
			comp := int(g.ligComp)
			if comp == 0 {
				comp = lastNum
			}
			g.ligID = ligID
			g.ligComp = uint8(soFar - lastNum + min(comp, lastNum))
		}
	}
	// Marks after the ligature that belonged to its last component.
	if !isMarkLig && lastLig != 0 {
		for j := last + 1; j < len(s.buf) && s.buf[j].ligID == lastLig; j++ {
			comp := int(s.buf[j].ligComp)
			if comp == 0 {
				break
			}
			s.buf[j].ligID = ligID
			s.buf[j].ligComp = uint8(soFar - lastNum + min(comp, lastNum))
		}
	}
	g := &s.buf[first]
	g.ID = lig
	g.cp = 0
	g.ligID = ligID
	g.ligComp = 0
	g.ligComps = uint8(min(soFar, 15))
	g.multiplied = false
	s.setProps(g, klass)
	// Remove the other components.
	out := s.buf[:first+1]
	for j := first + 1; j < len(s.buf); j++ {
		if j <= last && isComponent[j] {
			continue
		}
		out = append(out, s.buf[j])
	}
	s.buf = out
}

// applyNested applies the lookups of a context match.
func (s *shaper) applyNested(t *layoutTable, pos []int, records data, roff, n int) int {
	end := pos[len(pos)-1] + 1
	for r := 0; r < n; r++ {
		seq := int(records.u16(roff + 4*r))
		li := int(records.u16(roff + 4*r + 2))
		if seq >= len(pos) {
			continue
		}
		p := pos[seq]
		if p >= len(s.buf) {
			continue
		}
		l := t.lookup(li)
		if l == nil || l.typ == 0 {
			continue
		}
		m := &matcher{s: s, flag: l.flag, filterSet: l.filterSet, ignoreZWNJ: t.gpos}
		if m.ignoredByFlag(&s.buf[p]) {
			continue
		}
		before := len(s.buf)
		if _, ok := s.applyAt(t, l, m, p, 1); !ok {
			continue
		}
		delta := len(s.buf) - before
		if delta != 0 {
			end += delta
			for k := seq + 1; k < len(pos); k++ {
				pos[k] += delta
			}
		}
	}
	return end
}

// context applies a (GSUB type 5 / GPOS type 7) context subtable.
func (s *shaper) context(t *layoutTable, m *matcher, so, i int) (int, bool) {
	d := t.d
	g := s.buf[i].ID
	cm := &matcher{s: s, flag: m.flag, filterSet: m.filterSet, ignoreZWNJ: true}
	switch d.u16(so) {
	case 1:
		ci := d.coverage(so+int(d.u16(so+2)), g)
		if ci < 0 || ci >= int(d.u16(so+4)) {
			return 0, false
		}
		set := so + int(d.u16(so+6+2*ci))
		n := int(d.u16(set))
		for k := 0; k < n; k++ {
			r := set + int(d.u16(set+2+2*k))
			count := int(d.u16(r))
			nl := int(d.u16(r + 2))
			pos, ok := cm.matchInput(i, count, func(g uint16, k int) bool { return g == d.u16(r+4+2*(k-1)) })
			if ok {
				return s.applyNested(t, pos, d, r+4+2*(count-1), nl), true
			}
		}
	case 2:
		if d.coverage(so+int(d.u16(so+2)), g) < 0 {
			return 0, false
		}
		cd := so + int(d.u16(so+4))
		c := d.class(cd, g)
		if c >= int(d.u16(so+6)) {
			return 0, false
		}
		so2 := d.u16(so + 8 + 2*c)
		if so2 == 0 {
			return 0, false
		}
		set := so + int(so2)
		n := int(d.u16(set))
		for k := 0; k < n; k++ {
			r := set + int(d.u16(set+2+2*k))
			count := int(d.u16(r))
			nl := int(d.u16(r + 2))
			pos, ok := cm.matchInput(i, count, func(g uint16, k int) bool { return d.class(cd, g) == int(d.u16(r+4+2*(k-1))) })
			if ok {
				return s.applyNested(t, pos, d, r+4+2*(count-1), nl), true
			}
		}
	case 3:
		count := int(d.u16(so + 2))
		nl := int(d.u16(so + 4))
		if count == 0 || d.coverage(so+int(d.u16(so+6)), g) < 0 {
			return 0, false
		}
		pos, ok := cm.matchInput(i, count, func(g uint16, k int) bool { return d.coverage(so+int(d.u16(so+6+2*k)), g) >= 0 })
		if ok {
			return s.applyNested(t, pos, d, so+6+2*count, nl), true
		}
	}
	return 0, false
}

// chainContext applies a (GSUB type 6 / GPOS type 8) chained context
// subtable.
func (s *shaper) chainContext(t *layoutTable, m *matcher, so, i int) (int, bool) {
	d := t.d
	g := s.buf[i].ID
	cm := &matcher{s: s, flag: m.flag, filterSet: m.filterSet, ignoreZWNJ: true}
	switch d.u16(so) {
	case 1:
		ci := d.coverage(so+int(d.u16(so+2)), g)
		if ci < 0 || ci >= int(d.u16(so+4)) {
			return 0, false
		}
		set := so + int(d.u16(so+6+2*ci))
		n := int(d.u16(set))
		for k := 0; k < n; k++ {
			r := set + int(d.u16(set+2+2*k))
			nb := int(d.u16(r))
			bt := r + 2
			ip := bt + 2*nb
			ni := int(d.u16(ip))
			la := ip + 2 + 2*(ni-1)
			nla := int(d.u16(la))
			lr := la + 2 + 2*nla
			nl := int(d.u16(lr))
			pos, ok := cm.matchInput(i, ni, func(g uint16, k int) bool { return g == d.u16(ip+2+2*(k-1)) })
			if !ok {
				continue
			}
			if !cm.matchBacktrack(i, nb, func(g uint16, k int) bool { return g == d.u16(bt+2*k) }) {
				continue
			}
			if !cm.matchLookahead(pos[len(pos)-1], nla, func(g uint16, k int) bool { return g == d.u16(la+2+2*k) }) {
				continue
			}
			return s.applyNested(t, pos, d, lr+2, nl), true
		}
	case 2:
		if d.coverage(so+int(d.u16(so+2)), g) < 0 {
			return 0, false
		}
		bcd := so + int(d.u16(so+4))
		icd := so + int(d.u16(so+6))
		lcd := so + int(d.u16(so+8))
		c := d.class(icd, g)
		if c >= int(d.u16(so+10)) {
			return 0, false
		}
		so2 := d.u16(so + 12 + 2*c)
		if so2 == 0 {
			return 0, false
		}
		set := so + int(so2)
		n := int(d.u16(set))
		for k := 0; k < n; k++ {
			r := set + int(d.u16(set+2+2*k))
			nb := int(d.u16(r))
			bt := r + 2
			ip := bt + 2*nb
			ni := int(d.u16(ip))
			la := ip + 2 + 2*(ni-1)
			nla := int(d.u16(la))
			lr := la + 2 + 2*nla
			nl := int(d.u16(lr))
			pos, ok := cm.matchInput(i, ni, func(g uint16, k int) bool { return d.class(icd, g) == int(d.u16(ip+2+2*(k-1))) })
			if !ok {
				continue
			}
			if !cm.matchBacktrack(i, nb, func(g uint16, k int) bool { return d.class(bcd, g) == int(d.u16(bt+2*k)) }) {
				continue
			}
			if !cm.matchLookahead(pos[len(pos)-1], nla, func(g uint16, k int) bool { return d.class(lcd, g) == int(d.u16(la+2+2*k)) }) {
				continue
			}
			return s.applyNested(t, pos, d, lr+2, nl), true
		}
	case 3:
		nb := int(d.u16(so + 2))
		bt := so + 4
		ip := bt + 2*nb
		ni := int(d.u16(ip))
		la := ip + 2 + 2*ni
		nla := int(d.u16(la))
		lr := la + 2 + 2*nla
		nl := int(d.u16(lr))
		if ni == 0 || d.coverage(so+int(d.u16(ip+2)), g) < 0 {
			return 0, false
		}
		pos, ok := cm.matchInput(i, ni, func(g uint16, k int) bool { return d.coverage(so+int(d.u16(ip+2+2*k)), g) >= 0 })
		if !ok {
			return 0, false
		}
		if !cm.matchBacktrack(i, nb, func(g uint16, k int) bool { return d.coverage(so+int(d.u16(bt+2*k)), g) >= 0 }) {
			return 0, false
		}
		if !cm.matchLookahead(pos[len(pos)-1], nla, func(g uint16, k int) bool { return d.coverage(so+int(d.u16(la+2+2*k)), g) >= 0 }) {
			return 0, false
		}
		return s.applyNested(t, pos, d, lr+2, nl), true
	}
	return 0, false
}

// reverseChain applies a reverse chaining single substitution at i.
func (s *shaper) reverseChain(t *layoutTable, m *matcher, so, i int) bool {
	d := t.d
	ci := d.coverage(so+int(d.u16(so+2)), s.buf[i].ID)
	if ci < 0 {
		return false
	}
	nb := int(d.u16(so + 4))
	bt := so + 6
	la := bt + 2*nb
	nla := int(d.u16(la))
	sub := la + 2 + 2*nla
	cm := &matcher{s: s, flag: m.flag, filterSet: m.filterSet, ignoreZWNJ: true}
	if !cm.matchBacktrack(i, nb, func(g uint16, k int) bool { return d.coverage(so+int(d.u16(bt+2*k)), g) >= 0 }) {
		return false
	}
	if !cm.matchLookahead(i, nla, func(g uint16, k int) bool { return d.coverage(so+int(d.u16(la+2+2*k)), g) >= 0 }) {
		return false
	}
	if ci >= int(d.u16(sub)) {
		return false
	}
	s.setGlyph(i, d.u16(sub+2+2*ci), 0)
	return true
}

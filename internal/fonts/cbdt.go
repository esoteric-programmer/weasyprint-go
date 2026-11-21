package fonts

// Color bitmap glyphs (CBLC/CBDT tables with PNG images, as in Noto Color
// Emoji), drawn as images like WeasyPrint does with HarfBuzz.

// HasColorBitmaps reports whether the font has PNG color bitmaps.
func (f *Font) HasColorBitmaps() bool {
	return len(f.tables["CBLC"]) >= 8 && len(f.tables["CBDT"]) >= 4
}

// GlyphPNG returns the PNG image of a glyph in the largest bitmap strike,
// or nil.
func (f *Font) GlyphPNG(gid uint16) []byte {
	if !f.HasColorBitmaps() {
		return nil
	}
	defer func() { recover() }()
	cblc, cbdt := f.tables["CBLC"], f.tables["CBDT"]
	numSizes := int(u32(cblc, 4))
	best, bestPPEM := -1, -1
	for i := 0; i < numSizes; i++ {
		r := 8 + 48*i
		start, end := u16(cblc, r+40), u16(cblc, r+42)
		ppem := int(cblc[r+44])
		if gid >= start && gid <= end && ppem > bestPPEM {
			best, bestPPEM = r, ppem
		}
	}
	if best < 0 {
		return nil
	}
	arrayOff := int(u32(cblc, best))
	nSub := int(u32(cblc, best+8))
	for i := 0; i < nSub; i++ {
		e := arrayOff + 8*i
		first, last := u16(cblc, e), u16(cblc, e+2)
		if gid < first || gid > last {
			continue
		}
		sub := arrayOff + int(u32(cblc, e+4))
		indexFormat := u16(cblc, sub)
		imageFormat := u16(cblc, sub+2)
		dataOff := int(u32(cblc, sub+4))
		k := int(gid - first)
		var off, length int
		switch indexFormat {
		case 1:
			a, b := int(u32(cblc, sub+8+4*k)), int(u32(cblc, sub+12+4*k))
			off, length = dataOff+a, b-a
		case 2:
			size := int(u32(cblc, sub+8))
			off, length = dataOff+k*size, size
		case 3:
			a, b := int(u16(cblc, sub+8+2*k)), int(u16(cblc, sub+10+2*k))
			off, length = dataOff+a, b-a
		case 4:
			n := int(u32(cblc, sub+8))
			for j := 0; j < n; j++ {
				p := sub + 12 + 4*j
				if u16(cblc, p) == gid {
					a, b := int(u16(cblc, p+2)), int(u16(cblc, p+6))
					off, length = dataOff+a, b-a
				}
			}
		case 5:
			size := int(u32(cblc, sub+8))
			n := int(u32(cblc, sub+20))
			for j := 0; j < n; j++ {
				if u16(cblc, sub+24+2*j) == gid {
					off, length = dataOff+j*size, size
				}
			}
		default:
			return nil
		}
		if length <= 0 || off+length > len(cbdt) {
			return nil
		}
		d := cbdt[off : off+length]
		var hdr int
		switch imageFormat {
		case 17:
			hdr = 5
		case 18:
			hdr = 8
		case 19:
			hdr = 0
		default:
			return nil
		}
		if len(d) < hdr+4 {
			return nil
		}
		n := int(u32(d, hdr))
		if hdr+4+n > len(d) {
			return nil
		}
		return d[hdr+4 : hdr+4+n]
	}
	return nil
}

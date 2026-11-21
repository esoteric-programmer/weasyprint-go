// Package draw paints laid-out pages into a PDF document.
package draw

import (
	"crypto/md5"
	"fmt"
	"sort"
	"strings"
	"unicode/utf16"

	"github.com/esoteric-programmer/weasyprint-go/internal/fonts"
	"github.com/esoteric-programmer/weasyprint-go/internal/pdf"
)

type fontEntry struct {
	font   *fonts.Font
	ref    pdf.Ref
	name   string
	used   map[uint16]string // glyph → text
	cidMap map[uint16]uint16
	// full embeds the whole font (fonts used by form fields).
	full bool
}

type fontManager struct {
	doc     *pdf.Document
	entries map[*fonts.Font]*fontEntry
	order   []*fontEntry
}

func newFontManager(doc *pdf.Document) *fontManager {
	return &fontManager{doc: doc, entries: map[*fonts.Font]*fontEntry{}}
}

func (fm *fontManager) get(f *fonts.Font) *fontEntry {
	if e, ok := fm.entries[f]; ok {
		return e
	}
	e := &fontEntry{font: f, ref: fm.doc.Reserve(), name: fmt.Sprintf("F%d", len(fm.order)+1), used: map[uint16]string{}}
	if f.IsCFF {
		e.cidMap = f.CFFCIDs()
	}
	fm.entries[f] = e
	fm.order = append(fm.order, e)
	return e
}

// code returns the 2-byte code for a glyph and records its text.
func (e *fontEntry) code(gid uint16, text string) []byte {
	if _, ok := e.used[gid]; !ok || e.used[gid] == "" {
		e.used[gid] = text
	}
	cid := gid
	if e.cidMap != nil {
		if c, ok := e.cidMap[gid]; ok {
			cid = c
		}
	}
	return []byte{byte(cid >> 8), byte(cid)}
}

func (e *fontEntry) cid(gid uint16) uint16 {
	if e.cidMap != nil {
		if c, ok := e.cidMap[gid]; ok {
			return c
		}
	}
	return gid
}

func sanitizeName(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r > 32 && r < 127 && !strings.ContainsRune("[](){}<>/%#", r) {
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return "Font"
	}
	return b.String()
}

// fontOptions control font embedding.
type fontOptions struct {
	compress, full, hinting bool
	// cidSet adds the CIDSet of fonts (PDF 1.4 and earlier, like
	// WeasyPrint).
	cidSet bool
}

func (fm *fontManager) finish(o fontOptions) {
	for _, e := range fm.order {
		fm.writeFont(e, o)
	}
}

func (fm *fontManager) writeFont(e *fontEntry, o fontOptions) {
	compress := o.compress
	f := e.font
	gids := make([]int, 0, len(e.used))
	glyphSet := map[uint16]bool{}
	for g := range e.used {
		gids = append(gids, int(g))
		glyphSet[g] = true
	}
	sort.Ints(gids)
	h := md5.New()
	for _, g := range gids {
		fmt.Fprintf(h, "%d,", g)
	}
	h.Write([]byte(f.PostScript))
	sum := h.Sum(nil)
	tag := make([]byte, 6)
	for i := range tag {
		tag[i] = 'A' + sum[i]%26
	}
	baseName := string(tag) + "+" + sanitizeName(f.PostScript)
	k := 1000 / f.UnitsPerEm

	var data []byte
	if o.full || e.full {
		data = f.Standalone()
	} else {
		data = f.Subset(glyphSet, o.hinting)
	}
	var fileKey string
	var fileStream *pdf.Stream
	if f.IsCFF {
		fileKey = "FontFile3"
		fileStream = &pdf.Stream{Dict: pdf.Dict{"Subtype": pdf.Name("OpenType")}, Data: data, Compress: compress}
	} else {
		fileKey = "FontFile2"
		fileStream = &pdf.Stream{Dict: pdf.Dict{"Length1": len(data)}, Data: data, Compress: compress}
	}
	fileRef := fm.doc.Add(fileStream)
	flags := 4
	if f.FixedPitch {
		flags |= 1
	}
	if f.Italic || f.ItalicAngle != 0 {
		flags |= 64
	}
	fd := pdf.Dict{
		"Type":        pdf.Name("FontDescriptor"),
		"FontName":    pdf.Name(baseName),
		"Flags":       flags,
		"FontBBox":    []float64{f.BBox[0] * k, f.BBox[1] * k, f.BBox[2] * k, f.BBox[3] * k},
		"ItalicAngle": f.ItalicAngle,
		"Ascent":      f.Ascent * k,
		"Descent":     -f.Descent * k,
		"CapHeight":   f.CapHeight * k,
		"StemV":       80,
		fileKey:       fileRef,
	}
	if o.cidSet && len(gids) > 0 {
		maxCID := 0
		for _, g := range gids {
			if c := int(e.cid(uint16(g))); c > maxCID {
				maxCID = c
			}
		}
		bits := make([]byte, maxCID/8+1)
		for _, g := range gids {
			c := e.cid(uint16(g))
			bits[c/8] |= 0x80 >> (c % 8)
		}
		fd["CIDSet"] = fm.doc.Add(&pdf.Stream{Dict: pdf.Dict{}, Data: bits, Compress: compress})
	}
	fdRef := fm.doc.Add(fd)

	// Widths.
	var w pdf.Array
	type cidW struct {
		cid uint16
		w   float64
	}
	var cws []cidW
	for _, g := range gids {
		cws = append(cws, cidW{e.cid(uint16(g)), f.Advance(uint16(g)) * k})
	}
	sort.Slice(cws, func(i, j int) bool { return cws[i].cid < cws[j].cid })
	for i := 0; i < len(cws); {
		start := i
		var ws pdf.Array
		for i < len(cws) && (i == start || cws[i].cid == cws[i-1].cid+1) {
			ws = append(ws, float64(int(cws[i].w*100+0.5))/100)
			i++
		}
		w = append(w, int(cws[start].cid), ws)
	}
	subtype := "CIDFontType2"
	if f.IsCFF {
		subtype = "CIDFontType0"
	}
	cid := pdf.Dict{
		"Type":     pdf.Name("Font"),
		"Subtype":  pdf.Name(subtype),
		"BaseFont": pdf.Name(baseName),
		"CIDSystemInfo": pdf.Dict{
			"Registry": pdf.String("Adobe"), "Ordering": pdf.String("Identity"), "Supplement": 0,
		},
		"FontDescriptor": fdRef,
		"W":              w,
		"DW":             0,
	}
	if !f.IsCFF {
		cid["CIDToGIDMap"] = pdf.Name("Identity")
	}
	cidRef := fm.doc.Add(cid)

	// ToUnicode.
	var cmap strings.Builder
	cmap.WriteString("/CIDInit /ProcSet findresource begin\n12 dict begin\nbegincmap\n")
	cmap.WriteString("/CIDSystemInfo << /Registry (Adobe) /Ordering (UCS) /Supplement 0 >> def\n")
	cmap.WriteString("/CMapName /Adobe-Identity-UCS def\n/CMapType 2 def\n")
	cmap.WriteString("1 begincodespacerange\n<0000> <FFFF>\nendcodespacerange\n")
	var entries []string
	for _, g := range gids {
		text := e.used[uint16(g)]
		if text == "" {
			continue
		}
		var hex strings.Builder
		for _, u := range utf16.Encode([]rune(text)) {
			fmt.Fprintf(&hex, "%04X", u)
		}
		entries = append(entries, fmt.Sprintf("<%04X> <%s>", e.cid(uint16(g)), hex.String()))
	}
	for i := 0; i < len(entries); i += 100 {
		j := i + 100
		if j > len(entries) {
			j = len(entries)
		}
		fmt.Fprintf(&cmap, "%d beginbfchar\n%s\nendbfchar\n", j-i, strings.Join(entries[i:j], "\n"))
	}
	cmap.WriteString("endcmap\nCMapName currentdict /CMap defineresource pop\nend\nend\n")
	tuRef := fm.doc.Add(&pdf.Stream{Dict: pdf.Dict{}, Data: []byte(cmap.String()), Compress: compress})

	fm.doc.Set(e.ref, pdf.Dict{
		"Type":            pdf.Name("Font"),
		"Subtype":         pdf.Name("Type0"),
		"BaseFont":        pdf.Name(baseName),
		"Encoding":        pdf.Name("Identity-H"),
		"DescendantFonts": pdf.Array{cidRef},
		"ToUnicode":       tuRef,
	})
}

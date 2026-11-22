package draw

import (
	"fmt"
	"io"
	"math"
	"sort"
	"strings"

	"github.com/esoteric-programmer/weasyprint-go/internal/css"
	"github.com/esoteric-programmer/weasyprint-go/internal/images"
	"github.com/esoteric-programmer/weasyprint-go/internal/layout"
	"github.com/esoteric-programmer/weasyprint-go/internal/pdf"
	"github.com/esoteric-programmer/weasyprint-go/internal/text"
)

// Metadata is document metadata.
type Metadata struct {
	Title       string
	Authors     []string
	Description string
	Keywords    []string
	Generator   string
	Created     string
	Modified    string
	Lang        string
	// Custom are the other <meta> names and contents, in document order.
	Custom [][2]string
}

// Options controls PDF output.
type Options struct {
	Compress bool
	Metadata Metadata
	// Producer is written in the document information dictionary.
	Producer string
	// Version is the PDF version ("1.7" by default).
	Version string
	// Identifier is the file identifier; AutoIdentifier uses a hash of
	// the file instead.
	Identifier     []byte
	AutoIdentifier bool
	// Variant is a PDF variant name (see Variants), or "".
	Variant string
	// Tags enables tagged PDF.
	Tags bool
	// Forms includes PDF form fields for form controls.
	Forms bool
	// CustomMetadata writes custom <meta> elements in the information
	// dictionary.
	CustomMetadata bool
	// XMPMetadata are XML fragments added to the XMP metadata.
	XMPMetadata [][]byte
	// OutputIntent is "srgb", "device-cmyk" or the name of a profile of
	// ColorProfiles, or "".
	OutputIntent  string
	ColorProfiles map[string]*ColorProfile
	// Attachments are embedded in the PDF.
	Attachments []Attachment
	// FetchAttachment loads files of <a rel=attachment> links.
	FetchAttachment func(url string) (Attachment, bool)
	// Warn receives warnings.
	Warn func(string)
	// Image options: lossless optimization, JPEG re-encoding quality
	// (0 to keep images) and maximum resolution (0 to keep it).
	OptimizeImages bool
	JPEGQuality    int
	DPI            float64
	// Font options: embed whole fonts, keep hinting.
	FullFonts bool
	Hinting   bool
	// Zoom scales the pages (1 by default).
	Zoom float64
}

// scale returns the number of PDF points per CSS pixel.
func (r *Renderer) scale() float64 {
	if r.opts.Zoom > 0 {
		return 0.75 * r.opts.Zoom
	}
	return 0.75
}

type anchorPos struct {
	page int
	x, y float64
}

type bookmark struct {
	level int
	label string
	page  int
	x, y  float64
	open  bool
}

type pendingLink struct {
	rect [4]float64 // in PDF points
	link *css.LinkValue
	page int
	box  *layout.Box
}

// Renderer writes pages into a PDF document.
type Renderer struct {
	doc         *pdf.Document
	fm          *fontManager
	shaper      *text.Shaper
	imgRefs     map[*images.Image]pdf.Ref
	imgOrder    []*images.Image
	imgRatio    map[*images.Image]float64
	imgInterp   map[*images.Image]bool
	colorGlyphs map[colorGlyphKey]*images.Image
	pageRefs    []pdf.Ref
	anchors     map[string]anchorPos
	links       []pendingLink
	marks       []bookmark
	opts        Options
	nobj        int
	images      *images.Cache
	propagated  map[*layout.Box]bool
	written     []writtenAttachment
	tags        *tagState
	// colorSpaceRefs are the color spaces used in the document.
	colorSpaceRefs map[string]pdf.Ref
	forms          *formState
}

// Write renders pages to w as a PDF file.
func Write(w io.Writer, pages []*layout.Page, shaper *text.Shaper, imgs *images.Cache, opts Options) error {
	r := &Renderer{doc: pdf.NewDocument(), shaper: shaper, imgRefs: map[*images.Image]pdf.Ref{},
		imgRatio: map[*images.Image]float64{}, imgInterp: map[*images.Image]bool{}, colorGlyphs: map[colorGlyphKey]*images.Image{},
		anchors: map[string]anchorPos{}, opts: opts, images: imgs, propagated: map[*layout.Box]bool{}}
	r.fm = newFontManager(r.doc)
	if opts.Tags {
		r.tags = newTagState(len(pages))
	}
	pagesRef := r.doc.Reserve()
	for range pages {
		r.pageRefs = append(r.pageRefs, r.doc.Reserve())
	}
	for i, p := range pages {
		r.collectNavigation(i, p)
	}
	var annots [][]pdf.Ref
	for i, p := range pages {
		annots = append(annots, nil)
		r.drawPage(i, p, pagesRef)
	}
	_ = annots
	// Links.
	pageAnnots := make([]pdf.Array, len(pages))
	annotFiles := map[string]pdf.Ref{}
	for _, l := range r.links {
		a := pdf.Dict{
			"Type":    pdf.Name("Annot"),
			"Subtype": pdf.Name("Link"),
			"Rect":    []float64{l.rect[0], l.rect[1], l.rect[2], l.rect[3]},
			"Border":  pdf.Array{0, 0, 0},
		}
		if !l.link.Internal && isAttachmentLink(l.box) {
			// <a rel=attachment>: embed the file in an annotation.
			spec, ok := annotFiles[l.link.Target]
			if !ok && opts.FetchAttachment != nil {
				if att, ok := opts.FetchAttachment(l.link.Target); ok {
					att.Name = l.box.Element.Get("download")
					spec = r.writeAttachment(att).ref
				}
				annotFiles[l.link.Target] = spec
			}
			if spec == 0 {
				continue
			}
			rc := []float64{l.rect[0], l.rect[1], l.rect[2], l.rect[3]}
			ap := r.doc.Add(&pdf.Stream{Dict: pdf.Dict{"Type": pdf.Name("XObject"), "Subtype": pdf.Name("Form"), "BBox": rc}, Compress: opts.Compress})
			a = pdf.Dict{"Type": pdf.Name("Annot"), "Rect": rc, "Subtype": pdf.Name("FileAttachment"),
				"T": pdf.String(""), "FS": spec, "AP": pdf.Dict{"N": ap}, "AS": pdf.Name("N")}
		} else if l.link.Internal {
			if _, ok := r.anchors[l.link.Target]; !ok {
				continue
			}
			a["Dest"] = pdf.Name(l.link.Target)
		} else {
			a["A"] = pdf.Dict{"Type": pdf.Name("Action"), "S": pdf.Name("URI"), "URI": pdf.String(l.link.Target)}
		}
		ref := r.doc.Add(a)
		pageAnnots[l.page] = append(pageAnnots[l.page], ref)
		if r.tags != nil && a["Subtype"] == pdf.Name("Link") {
			r.tags.linkAnnots[l.page][l.box] = append(r.tags.linkAnnots[l.page][l.box], ref)
		}
	}
	if opts.Forms {
		for i, p := range pages {
			r.addForms(i, p, nil, &pageAnnots[i])
		}
	}
	for i, ref := range r.pageRefs {
		d := r.doc.Get(ref).(pdf.Dict)
		if len(pageAnnots[i]) > 0 {
			d["Annots"] = pageAnnots[i]
		}
	}
	r.writeImages()
	r.fm.finish(fontOptions{compress: opts.Compress, full: opts.FullFonts, hinting: opts.Hinting,
		cidSet: opts.Version != "" && !pdf.VersionAtLeast(opts.Version, "1.5")})
	kids := make(pdf.Array, len(r.pageRefs))
	for i, ref := range r.pageRefs {
		kids[i] = ref
	}
	r.doc.Set(pagesRef, pdf.Dict{"Type": pdf.Name("Pages"), "Kids": kids, "Count": len(r.pageRefs)})
	catalog := pdf.Dict{"Type": pdf.Name("Catalog"), "Pages": pagesRef}
	// Named destinations.
	if len(r.anchors) > 0 && len(pages) > 0 {
		names := make([]string, 0, len(r.anchors))
		for n := range r.anchors {
			names = append(names, n)
		}
		sort.Strings(names)
		dests := pdf.Dict{}
		for _, n := range names {
			a := r.anchors[n]
			dests[n] = r.dest(a.page, a.x, a.y, pages)
		}
		catalog["Dests"] = r.doc.Add(dests)
	}
	if outlines := r.outlines(pages); outlines != 0 {
		catalog["Outlines"] = outlines
		catalog["PageMode"] = pdf.Name("UseOutlines")
	}
	if opts.Metadata.Lang != "" {
		catalog["Lang"] = pdf.String(opts.Metadata.Lang)
	}
	info := r.infoDict()
	// Embedded files.
	var embedded pdf.Array
	for _, a := range opts.Attachments {
		w := r.writeAttachment(a)
		embedded = append(embedded, r.doc.Get(w.ref).(pdf.Dict)["F"], w.ref)
	}
	if len(embedded) > 0 {
		catalog["Names"] = pdf.Dict{"EmbeddedFiles": r.doc.Add(pdf.Dict{"Names": embedded})}
	}
	if r.forms != nil {
		r.forms.acro["Fields"] = r.forms.fields
		catalog["AcroForm"] = r.forms.acro
	}
	if r.tags != nil {
		r.addTags(catalog, pages)
	}
	r.addOutputIntent(catalog)
	if v, ok := Variants[opts.Variant]; ok {
		var annots []pdf.Ref
		for _, a := range pageAnnots {
			for _, ref := range a {
				annots = append(annots, ref.(pdf.Ref))
			}
		}
		r.applyVariant(v, catalog, info, annots)
	}
	rootRef := r.doc.Add(catalog)
	var infoRef pdf.Ref
	if len(info) > 0 {
		infoRef = r.doc.Add(info)
	}
	r.doc.Version = opts.Version
	return r.doc.Write(w, rootRef, infoRef, pdf.WriteOptions{
		Identifier: opts.Identifier, AutoID: opts.AutoIdentifier, Compress: opts.Compress})
}

// addOutputIntent adds the output intent and its ICC profile, like
// WeasyPrint.
func (r *Renderer) addOutputIntent(catalog pdf.Dict) {
	profiles := r.opts.ColorProfiles
	intent := r.opts.OutputIntent
	var p *ColorProfile
	switch {
	case intent == "srgb":
		p = SRGBProfile()
	case profiles[intent] != nil:
		p = profiles[intent]
	case profiles["device-cmyk"] != nil:
		p = profiles["device-cmyk"]
	}
	if p == nil {
		return
	}
	profile := r.doc.Add(&pdf.Stream{Dict: pdf.Dict{"N": p.Components}, Data: p.Data, Compress: r.opts.Compress})
	subtype := "GTS_PDFX"
	if strings.Contains(r.opts.Variant, "pdf/a") {
		subtype = "GTS_PDFA1"
	}
	catalog["OutputIntents"] = pdf.Array{pdf.Dict{
		"Type": pdf.Name("OutputIntent"), "S": pdf.Name(subtype),
		"OutputConditionIdentifier": pdf.String(p.Name), "Info": pdf.String(p.Name),
		"DestOutputProfile": profile,
	}}
}

func (r *Renderer) infoDict() pdf.Dict {
	m := r.opts.Metadata
	d := pdf.Dict{"Producer": pdf.String(r.producer())}
	if m.Title != "" {
		d["Title"] = pdf.String(m.Title)
	}
	if len(m.Authors) > 0 {
		d["Author"] = pdf.String(strings.Join(m.Authors, ", "))
	}
	if m.Description != "" {
		d["Subject"] = pdf.String(m.Description)
	}
	if len(m.Keywords) > 0 {
		d["Keywords"] = pdf.String(strings.Join(m.Keywords, ", "))
	}
	if m.Generator != "" {
		d["Creator"] = pdf.String(m.Generator)
	}
	if date, ok := w3cDateToPDF(m.Created); ok {
		d["CreationDate"] = pdf.String(date)
	}
	if date, ok := w3cDateToPDF(m.Modified); ok {
		d["ModDate"] = pdf.String(date)
	}
	if r.opts.CustomMetadata {
		for _, kv := range m.Custom {
			key := strings.Map(func(c rune) rune {
				if c < 0x80 && (c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9') {
					return c
				}
				return -1
			}, kv[0])
			if key != "" {
				d[key] = pdf.String(kv[1])
			}
		}
	}
	return d
}

func (r *Renderer) dest(page int, x, y float64, pages []*layout.Page) pdf.Array {
	h := pages[page].Height
	k := r.scale()
	return pdf.Array{r.pageRefs[page], pdf.Name("XYZ"), x * k, (h - y) * k, 0}
}

// ---------------------------------------------------------------- navigation

func (r *Renderer) collectNavigation(pi int, p *layout.Page) {
	var walk func(b *layout.Box)
	walk = func(b *layout.Box) {
		if b.Anchor != "" && !b.NoStartDecor {
			if _, ok := r.anchors[b.Anchor]; !ok {
				r.anchors[b.Anchor] = anchorPos{pi, b.BorderBoxX(), b.BorderBoxY()}
			}
		}
		if b.BookmarkLevel > 0 && !b.NoStartDecor && b.BookmarkLabel != "" {
			r.marks = append(r.marks, bookmark{b.BookmarkLevel, b.BookmarkLabel, pi, b.BorderBoxX(), b.BorderBoxY(), b.BookmarkOpen})
		}
		for _, ch := range b.Children {
			walk(ch)
		}
	}
	for _, ch := range p.Box.Children {
		walk(ch)
	}
}

func (r *Renderer) outlines(pages []*layout.Page) pdf.Ref {
	if len(r.marks) == 0 {
		return 0
	}
	type node struct {
		b        bookmark
		ref      pdf.Ref
		children []*node
		parent   *node
	}
	root := &node{b: bookmark{level: 0}}
	stack := []*node{root}
	for _, m := range r.marks {
		n := &node{b: m, ref: r.doc.Reserve()}
		for len(stack) > 1 && stack[len(stack)-1].b.level >= m.level {
			stack = stack[:len(stack)-1]
		}
		parent := stack[len(stack)-1]
		n.parent = parent
		parent.children = append(parent.children, n)
		stack = append(stack, n)
	}
	root.ref = r.doc.Reserve()
	var count func(n *node) int
	count = func(n *node) int {
		c := 0
		for _, ch := range n.children {
			c++
			if ch.b.open {
				c += count(ch)
			}
		}
		return c
	}
	var write func(n *node)
	write = func(n *node) {
		for i, ch := range n.children {
			d := pdf.Dict{
				"Title":  pdf.String(ch.b.label),
				"Parent": n.ref,
				"Dest":   r.dest(ch.b.page, ch.b.x, ch.b.y, pages),
			}
			if i > 0 {
				d["Prev"] = n.children[i-1].ref
			}
			if i < len(n.children)-1 {
				d["Next"] = n.children[i+1].ref
			}
			if len(ch.children) > 0 {
				d["First"] = ch.children[0].ref
				d["Last"] = ch.children[len(ch.children)-1].ref
				c := count(ch)
				if !ch.b.open {
					c = -len(ch.children)
				}
				d["Count"] = c
			}
			r.doc.Set(ch.ref, d)
			write(ch)
		}
	}
	write(root)
	r.doc.Set(root.ref, pdf.Dict{
		"Type":  pdf.Name("Outlines"),
		"First": root.children[0].ref,
		"Last":  root.children[len(root.children)-1].ref,
		"Count": count(root),
	})
	return root.ref
}

// ---------------------------------------------------------------- canvas

type canvas struct {
	// ellipsisRight is the limit of text ending with an ellipsis, or 0.
	ellipsisRight float64
	// colorSpaces are the color spaces used by the page.
	colorSpaces pdf.Dict
	// orientation of the background images being drawn.
	orientation images.Orientation
	// lineX is the start of the line box being painted, the origin of the
	// patterns of text decorations.
	lineX    float64
	r        *Renderer
	c        *pdf.Content
	stack    []*pdf.Content
	fonts    pdf.Dict
	xobjects pdf.Dict
	states   pdf.Dict
	shadings pdf.Dict
	patterns pdf.Dict
	stateIDs map[string]string
	page     int
	height   float64
}

func (cv *canvas) alphaState(fill, stroke float64) string {
	key := fmt.Sprintf("%.4f/%.4f", fill, stroke)
	if n, ok := cv.stateIDs[key]; ok {
		return n
	}
	name := fmt.Sprintf("GS%d", len(cv.stateIDs)+1)
	cv.stateIDs[key] = name
	cv.states[name] = pdf.Dict{"Type": pdf.Name("ExtGState"), "ca": fill, "CA": stroke}
	return name
}

func (cv *canvas) setFill(col css.Color) {
	if !cv.setSpecialColor(col, false) {
		cv.c.SetFillRGB(col.R, col.G, col.B)
	}
	if col.A < 1 {
		cv.c.SetState(cv.alphaState(col.A, 1))
	}
}

func (cv *canvas) setStroke(col css.Color) {
	if !cv.setSpecialColor(col, true) {
		cv.c.SetStrokeRGB(col.R, col.G, col.B)
	}
	if col.A < 1 {
		cv.c.SetState(cv.alphaState(1, col.A))
	}
}

// setSpecialColor sets colors in other color spaces than sRGB, like
// WeasyPrint: CIE Lab (lab(), lch(), oklab(), oklch(), XYZ), DeviceCMYK
// and ICC profiles of @color-profile rules.
func (cv *canvas) setSpecialColor(col css.Color, stroke bool) bool {
	n := func(v float64) string { return pdf.FormatNumber(v) }
	op := "sc"
	csOp := "cs"
	if stroke {
		op, csOp = "SC", "CS"
	}
	switch {
	case col.Space == "":
		return false
	case col.Space == "device-cmyk":
		k := "k"
		if stroke {
			k = "K"
		}
		cv.c.Op(fmt.Sprintf("%s %s %s %s %s", n(col.C[0]), n(col.C[1]), n(col.C[2]), n(col.C[3]), k))
		return true
	case col.Space == "lab-d50" || col.Space == "lab-d65":
		cv.useColorSpace(col.Space)
		cv.c.Op(fmt.Sprintf("/%s %s %s %s %s %s", col.Space, csOp, n(col.C[0]), n(col.C[1]), n(col.C[2]), op))
		return true
	default:
		p := cv.r.opts.ColorProfiles[col.Space]
		if p == nil {
			return false
		}
		cv.useColorSpace(col.Space)
		parts := []string{"/" + pdfName(col.Space), csOp}
		for i := 0; i < p.Components && i < 4; i++ {
			parts = append(parts, n(col.C[i]))
		}
		cv.c.Op(strings.Join(append(parts, op), " "))
		return true
	}
}

// pdfName escapes a name for content streams.
func pdfName(s string) string {
	var b strings.Builder
	for _, c := range []byte(s) {
		if c < '!' || c > '~' || strings.IndexByte("#()<>[]{}/%", c) >= 0 {
			fmt.Fprintf(&b, "#%02X", c)
		} else {
			b.WriteByte(c)
		}
	}
	return b.String()
}

// useColorSpace adds a color space to the resources of the page.
func (cv *canvas) useColorSpace(name string) {
	if cv.colorSpaces == nil {
		cv.colorSpaces = pdf.Dict{}
	}
	if _, ok := cv.colorSpaces[name]; ok {
		return
	}
	r := cv.r
	if r.colorSpaceRefs == nil {
		r.colorSpaceRefs = map[string]pdf.Ref{}
	}
	ref, ok := r.colorSpaceRefs[name]
	if !ok {
		var cs pdf.Array
		switch name {
		case "lab-d50":
			cs = pdf.Array{pdf.Name("Lab"), pdf.Dict{"WhitePoint": []float64{0.3457 / 0.3585, 1, (1 - 0.3457 - 0.3585) / 0.3585}, "Range": []float64{-125, 125, -125, 125}}}
		case "lab-d65":
			cs = pdf.Array{pdf.Name("Lab"), pdf.Dict{"WhitePoint": []float64{0.3127 / 0.3290, 1, (1 - 0.3127 - 0.3290) / 0.3290}, "Range": []float64{-125, 125, -125, 125}}}
		default:
			p := r.opts.ColorProfiles[name]
			cs = pdf.Array{pdf.Name("ICCBased"), r.doc.Add(&pdf.Stream{Dict: pdf.Dict{"N": p.Components}, Data: p.Data, Compress: r.opts.Compress})}
		}
		ref = r.doc.Add(cs)
		r.colorSpaceRefs[name] = ref
	}
	cv.colorSpaces[name] = ref
}

func (cv *canvas) pushGroup() {
	cv.stack = append(cv.stack, cv.c)
	cv.c = pdf.NewContent(cv.r.opts.Compress)
}

func (cv *canvas) popGroup(bbox [4]float64) string {
	group := cv.c
	cv.c = cv.stack[len(cv.stack)-1]
	cv.stack = cv.stack[:len(cv.stack)-1]
	group.Dict["Type"] = pdf.Name("XObject")
	group.Dict["Subtype"] = pdf.Name("Form")
	group.Dict["BBox"] = []float64{bbox[0], bbox[1], bbox[2], bbox[3]}
	group.Dict["Group"] = pdf.Dict{"Type": pdf.Name("Group"), "S": pdf.Name("Transparency"), "CS": pdf.Name("DeviceRGB")}
	group.Dict["Resources"] = cv.resources()
	ref := cv.r.doc.Add(group)
	name := fmt.Sprintf("X%d", cv.r.nextID())
	cv.xobjects[name] = ref
	return name
}

func (r *Renderer) nextID() int {
	r.nobj++
	return r.nobj
}

func (cv *canvas) resources() pdf.Dict {
	res := pdf.Dict{"ProcSet": pdf.Array{pdf.Name("PDF"), pdf.Name("Text"), pdf.Name("ImageB"), pdf.Name("ImageC")}}
	if len(cv.fonts) > 0 {
		res["Font"] = cv.fonts
	}
	if len(cv.xobjects) > 0 {
		res["XObject"] = cv.xobjects
	}
	if len(cv.states) > 0 {
		res["ExtGState"] = cv.states
	}
	if len(cv.shadings) > 0 {
		res["Shading"] = cv.shadings
	}
	if len(cv.patterns) > 0 {
		res["Pattern"] = cv.patterns
	}
	if len(cv.colorSpaces) > 0 {
		res["ColorSpace"] = cv.colorSpaces
	}
	return res
}

func (r *Renderer) drawPage(pi int, p *layout.Page, parent pdf.Ref) {
	cv := &canvas{r: r, c: pdf.NewContent(r.opts.Compress), fonts: pdf.Dict{}, xobjects: pdf.Dict{},
		states: pdf.Dict{}, shadings: pdf.Dict{}, patterns: pdf.Dict{}, stateIDs: map[string]string{},
		page: pi, height: p.Height}
	k := r.scale()
	wPt, hPt := p.Width*k, p.Height*k
	cv.c.Transform(k, 0, 0, -k, 0, hPt)
	page := p.Box
	// Page background (painted over the bleed area) with crop and cross
	// marks, then the root (canvas) background over the page area.
	pageArea := borderRect(page)
	bleed := pageBleed(page.Style)
	bleedArea := rect{-bleed[3], -bleed[0], p.Width + bleed[1] + bleed[3], p.Height + bleed[0] + bleed[2]}
	cv.artifact(func() {
		cv.drawBackgroundStyle(page.Style, page, &bleedArea, nil)
		cv.drawMarks(page.Style, bleedArea, bleed)
		if root, rootStyle := r.canvasBackgroundSource(page); rootStyle != nil {
			cv.drawBackgroundStyle(rootStyle, root, &pageArea, page)
		}
		cv.drawBorders(page)
	})
	cv.paintLayers(page, true)
	contentRef := r.doc.Add(cv.c)
	pd := pdf.Dict{
		"Type":      pdf.Name("Page"),
		"Parent":    parent,
		"MediaBox":  []float64{-bleed[3] * k, -bleed[2] * k, wPt + bleed[1]*k, hPt + bleed[0]*k},
		"Contents":  contentRef,
		"Resources": cv.resources(),
		"TrimBox":   []float64{0, 0, wPt, hPt},
		// Like WeasyPrint, the PDF bleed box is at most 10pt from the trim box.
		"BleedBox": []float64{-math.Min(10, bleed[3]*k), -math.Min(10, bleed[2]*k), wPt + math.Min(10, bleed[1]*k), hPt + math.Min(10, bleed[0]*k)},
	}
	if r.tags != nil {
		pd["Tabs"] = pdf.Name("S")
		pd["StructParents"] = pi
	}
	r.doc.Set(r.pageRefs[pi], pd)
}

// canvasBackgroundSource returns the box whose background is propagated to
// the canvas (the root element, or the body if the root has none), and marks
// it so its own background is not painted again.
func (r *Renderer) canvasBackgroundSource(page *layout.Box) (*layout.Box, *css.Style) {
	var root *layout.Box
	for _, ch := range page.Children {
		if ch.Kind != layout.KMargin {
			root = ch
			break
		}
	}
	if root == nil || root.Style == nil {
		return nil, nil
	}
	if hasBackground(root.Style) {
		r.propagated[root.Original()] = true
		return root, root.Style
	}
	if root.Element != nil && root.Element.Tag == "html" {
		for _, ch := range root.Children {
			if ch.Element != nil && ch.Element.Tag == "body" && !ch.Anonymous && hasBackground(ch.Style) {
				r.propagated[ch.Original()] = true
				return root, ch.Style
			}
		}
	}
	return nil, nil
}

func hasBackground(s *css.Style) bool {
	if !s.Color(css.PBackgroundColor).IsTransparent() {
		return true
	}
	if imgs, ok := s.Get(css.PBackgroundImage).([]css.Value); ok {
		for _, im := range imgs {
			if k, ok := im.(css.Keyword); !ok || k != "none" {
				return true
			}
		}
	}
	return false
}

// ---------------------------------------------------------------- painting order

func createsStackingContext(b *layout.Box) bool {
	if b.Style == nil || b.Kind == layout.KText || b.Kind == layout.KLine {
		return false
	}
	if b.Style.Opacity() < 1 || b.Style.Transforms() != nil || hasMaskBorder(b) {
		return true
	}
	pos := b.Style.Kw(css.PPosition)
	return pos != "static" && pos != "" && b.Style.Kw(css.PZIndex) != "auto"
}

func isPositioned(b *layout.Box) bool {
	if b.Style == nil || b.Kind == layout.KText || b.Kind == layout.KLine {
		return false
	}
	pos := b.Style.Kw(css.PPosition)
	return pos != "static" && pos != ""
}

func isUnit(b *layout.Box) bool {
	return isPositioned(b) || createsStackingContext(b) || b.IsFloated() || b.IsAtomicInline()
}

func zIndex(b *layout.Box) int {
	if b.Style.Kw(css.PZIndex) == "auto" {
		return 0
	}
	return int(b.Style.Num(css.PZIndex))
}

func (cv *canvas) paintUnit(b *layout.Box) {
	if b.Style == nil {
		return
	}
	op := b.Style.Opacity()
	if op <= 0 {
		return
	}
	tr := transformMatrix(b)
	// clip of absolutely positioned boxes, like WeasyPrint (before
	// opacity and transforms).
	if r, ok := b.Style.Get(css.PClip).(css.ClipRect); ok {
		if pos := b.Style.Kw(css.PPosition); pos == "absolute" || pos == "fixed" {
			bx, by := b.BorderBoxX(), b.BorderBoxY()
			bw, bh := b.BorderWidth(), b.BorderHeight()
			top, right, bottom, left := 0.0, bw, bh, 0.0
			if r[0] != nil {
				top = r[0].V
			}
			if r[1] != nil {
				right = r[1].V
			}
			if r[2] != nil {
				bottom = r[2].V
			}
			if r[3] != nil {
				left = r[3].V
			}
			cv.c.Save()
			defer cv.c.Restore()
			cv.c.Rect(bx+left, by+top, right-left, bottom-top)
			cv.c.Clip()
		}
	}
	grouped := op < 1
	if grouped {
		cv.pushGroup()
	}
	if tr != nil {
		cv.c.Save()
		cv.c.Transform(tr[0], tr[1], tr[2], tr[3], tr[4], tr[5])
	}
	if mask := cv.maskBorderState(b); mask != "" {
		cv.c.Save()
		cv.c.SetState(mask)
		cv.paintLayers(b, false)
		cv.c.Restore()
	} else {
		cv.paintLayers(b, false)
	}
	if tr != nil {
		cv.c.Restore()
	}
	if grouped {
		name := cv.popGroup([4]float64{-1e5, -1e5, 1e5, 1e5})
		cv.c.Save()
		cv.c.SetState(cv.alphaState(op, op))
		cv.c.DrawXObject(name)
		cv.c.Restore()
	}
}

func (cv *canvas) paintLayers(root *layout.Box, isPage bool) {
	if !isPage {
		cv.backgroundAndBorder(root)
	}
	var neg, zero, pos []*layout.Box
	cv.collectUnits(root, true, &neg, &zero, &pos)
	sort.SliceStable(neg, func(i, j int) bool { return zIndex(neg[i]) < zIndex(neg[j]) })
	sort.SliceStable(pos, func(i, j int) bool { return zIndex(pos[i]) < zIndex(pos[j]) })
	for _, b := range neg {
		cv.paintUnit(b)
	}
	cv.withClip(root, isPage, func() {
		cv.paintBlocks(root, true)
		cv.paintFloats(root, true)
		cv.paintInlines(root, true, nil)
	})
	for _, b := range zero {
		cv.paintUnit(b)
	}
	for _, b := range pos {
		cv.paintUnit(b)
	}
	cv.paintOutlines(root, true)
}

// collectUnits collects positioned descendants and stacking contexts.
func (cv *canvas) collectUnits(b *layout.Box, isRoot bool, neg, zero, pos *[]*layout.Box) {
	if !isRoot {
		if isPositioned(b) || createsStackingContext(b) {
			z := zIndex(b)
			switch {
			case z < 0:
				*neg = append(*neg, b)
			case z > 0:
				*pos = append(*pos, b)
			default:
				*zero = append(*zero, b)
			}
			return
		}
		if b.IsFloated() || b.IsAtomicInline() {
			return
		}
	}
	for _, ch := range b.Children {
		cv.collectUnits(ch, false, neg, zero, pos)
	}
	if b.Marker != nil {
		cv.collectUnits(b.Marker, false, neg, zero, pos)
	}
}

func clips(b *layout.Box) bool {
	return b.Style != nil && b.Kind != layout.KInline && b.Kind != layout.KText && b.Kind != layout.KLine &&
		b.Style.Kw(css.POverflow) == "hidden" && b.Kind != layout.KPage
}

func (cv *canvas) withClip(b *layout.Box, skip bool, f func()) {
	if skip || !clips(b) {
		f()
		return
	}
	cv.c.Save()
	pr := paddingRect(b)
	radii := innerRadii(b, outerRadii(b))
	pathRounded(cv.c, pr, radii)
	cv.c.Clip()
	f()
	cv.c.Restore()
}

func isBlockLike(b *layout.Box) bool {
	switch b.Kind {
	case layout.KBlock, layout.KTableWrapper, layout.KTable, layout.KRowGroup, layout.KRow,
		layout.KCell, layout.KCaption, layout.KFlex, layout.KMargin, layout.KColGroup, layout.KGrid:
		return true
	case layout.KReplaced:
		return !b.IsInline
	}
	return false
}

func (cv *canvas) paintBlocks(b *layout.Box, isRoot bool) {
	if !isRoot {
		if isUnit(b) || !isBlockLike(b) {
			return
		}
		cv.backgroundAndBorder(b)
	}
	if b.Kind == layout.KTable && b.Collapse != nil {
		cv.withClip(b, isRoot, func() {
			for _, ch := range b.Children {
				cv.paintBlocks(ch, false)
			}
		})
		cv.artifact(func() { cv.drawCollapsedBorders(b) })
		return
	}
	cv.withClip(b, isRoot, func() {
		for _, ch := range b.Children {
			cv.paintBlocks(ch, false)
		}
	})
	if len(b.Children) > 1 && b.Children[0].IsColumn {
		cv.artifact(func() { cv.drawColumnRules(b) })
	}
}

func (cv *canvas) paintFloats(b *layout.Box, isRoot bool) {
	if !isRoot {
		if b.IsFloated() && !isPositioned(b) && !createsStackingContext(b) {
			cv.paintUnit(b)
			return
		}
		if isUnit(b) {
			return
		}
	}
	cv.withClip(b, isRoot, func() {
		for _, ch := range b.Children {
			cv.paintFloats(ch, false)
		}
	})
}

type decoration struct {
	line      string
	color     css.Color
	style     string
	thickness css.Value
	src       *css.Style
}

func decorationsOf(s *css.Style) []decoration {
	var out []decoration
	for _, l := range s.TextDecorations() {
		out = append(out, decoration{l, s.Color(css.PTextDecorationColor), s.Kw(css.PTextDecorationStyle), s.Get(css.PTextDecorationThickness), s})
	}
	return out
}

func (cv *canvas) paintInlines(b *layout.Box, isRoot bool, decos []decoration) {
	if !isRoot && isUnit(b) {
		if b.IsAtomicInline() && !isPositioned(b) && !createsStackingContext(b) && !b.IsFloated() {
			cv.paintUnit(b)
		}
		return
	}
	switch b.Kind {
	case layout.KText:
		cv.marked(b, "Span", func() { cv.drawText(b, decos) })
		return
	case layout.KLeader:
		cv.marked(b, "Span", func() { cv.drawLeader(b) })
		return
	case layout.KInline:
		cv.backgroundAndBorder(b)
		decos = append(append([]decoration{}, decos...), decorationsOf(b.Style)...)
	case layout.KReplaced:
		if !b.IsInline {
			cv.drawReplaced(b)
		}
		return
	case layout.KLine:
		savedX := cv.lineX
		cv.lineX = b.X
		defer func() { cv.lineX = savedX }()
		if b.EllipsisRight != 0 {
			saved := cv.ellipsisRight
			cv.ellipsisRight = b.EllipsisRight
			defer func() { cv.ellipsisRight = saved }()
		}
	default:
		if b.Style != nil && b.Kind != layout.KPage {
			decos = append(append([]decoration{}, decos...), decorationsOf(b.Style)...)
		}
	}
	if b.Marker != nil {
		cv.paintUnitMarker(b.Marker)
	}
	cv.withClip(b, isRoot || b.Kind == layout.KInline, func() {
		for _, ch := range b.Children {
			cv.paintInlines(ch, false, decos)
		}
	})
}

func (cv *canvas) paintUnitMarker(m *layout.Box) {
	cv.paintBlocks(m, true)
	cv.paintInlines(m, true, nil)
}

func (cv *canvas) paintOutlines(b *layout.Box, isRoot bool) {
	if !isRoot && isUnit(b) {
		return
	}
	if b.Style != nil && b.Kind != layout.KText && b.Kind != layout.KLine {
		cv.artifact(func() { cv.drawOutline(b) })
	}
	for _, ch := range b.Children {
		cv.paintOutlines(ch, false)
	}
}

func (cv *canvas) backgroundAndBorder(b *layout.Box) {
	if b.Style == nil || b.Kind == layout.KLine || b.Kind == layout.KText {
		return
	}
	if b.Style.Kw(css.PVisibility) != "visible" {
		return
	}
	if b.Kind == layout.KCell && b.Collapse == nil && b.CellEmpty && b.Style.Kw(css.PEmptyCells) == "hide" {
		return
	}
	if b.Kind == layout.KRow || b.Kind == layout.KRowGroup || b.Kind == layout.KColGroup {
		cv.artifact(func() { cv.drawBackground(b) })
		return
	}
	cv.artifact(func() {
		cv.drawBoxShadows(b, false)
		if !cv.r.propagated[b.Original()] {
			cv.drawBackground(b)
		}
		cv.drawBoxShadows(b, true)
		if !((b.Kind == layout.KCell || b.Kind == layout.KTable) && b.Collapse != nil) {
			cv.drawBorders(b)
		}
	})
	if b.Kind == layout.KCell && b.Collapse != nil {
		return
	}
	if b.Kind == layout.KTable && b.Collapse != nil {
		return
	}
	if b.Kind == layout.KReplaced && b.IsInline {
		cv.drawReplaced(b)
	}
	// Links.
	if b.Link != nil {
		cv.addLink(b)
	}
}

func (cv *canvas) addLink(b *layout.Box) {
	x0, y0 := b.BorderBoxX(), b.BorderBoxY()
	x1, y1 := x0+b.BorderWidth(), y0+b.BorderHeight()
	if x1 <= x0 || y1 <= y0 {
		return
	}
	cv.r.links = append(cv.r.links, pendingLink{
		rect: [4]float64{x0 * cv.r.scale(), (cv.height - y1) * cv.r.scale(), x1 * cv.r.scale(), (cv.height - y0) * cv.r.scale()},
		link: b.Link, page: cv.page, box: b,
	})
}

// ---------------------------------------------------------------- transforms

func transformMatrix(b *layout.Box) []float64 {
	ts := b.Style.Transforms()
	if ts == nil {
		return nil
	}
	bw, bh := b.BorderWidth(), b.BorderHeight()
	ox, oy := b.BorderBoxX(), b.BorderBoxY()
	if o, ok := b.Style.Get(css.PTransformOrigin).(css.Position); ok {
		px := o.X.Resolve(bw)
		py := o.Y.Resolve(bh)
		if o.XOrigin == "right" {
			px = bw - px
		}
		if o.YOrigin == "bottom" {
			py = bh - py
		}
		ox += px
		oy += py
	}
	m := []float64{1, 0, 0, 1, ox, oy}
	mul := func(a, b []float64) []float64 {
		return []float64{
			b[0]*a[0] + b[1]*a[2], b[0]*a[1] + b[1]*a[3],
			b[2]*a[0] + b[3]*a[2], b[2]*a[1] + b[3]*a[3],
			b[4]*a[0] + b[5]*a[2] + a[4], b[4]*a[1] + b[5]*a[3] + a[5],
		}
	}
	num := func(v css.Value, base float64) float64 {
		switch x := v.(type) {
		case css.Number:
			return float64(x)
		case css.Length:
			return x.Resolve(base)
		}
		return 0
	}
	for _, t := range ts {
		var tm []float64
		a := t.Args
		switch t.Name {
		case "translate":
			tx := num(a[0], bw)
			ty := 0.0
			if len(a) > 1 {
				ty = num(a[1], bh)
			}
			tm = []float64{1, 0, 0, 1, tx, ty}
		case "translatex":
			tm = []float64{1, 0, 0, 1, num(a[0], bw), 0}
		case "translatey":
			tm = []float64{1, 0, 0, 1, 0, num(a[0], bh)}
		case "scale":
			sx := num(a[0], 1)
			sy := sx
			if len(a) > 1 {
				sy = num(a[1], 1)
			}
			tm = []float64{sx, 0, 0, sy, 0, 0}
		case "scalex":
			tm = []float64{num(a[0], 1), 0, 0, 1, 0, 0}
		case "scaley":
			tm = []float64{1, 0, 0, num(a[0], 1), 0, 0}
		case "rotate":
			r := num(a[0], 0)
			tm = []float64{math.Cos(r), math.Sin(r), -math.Sin(r), math.Cos(r), 0, 0}
		case "skewx":
			tm = []float64{1, 0, math.Tan(num(a[0], 0)), 1, 0, 0}
		case "skewy":
			tm = []float64{1, math.Tan(num(a[0], 0)), 0, 1, 0, 0}
		case "skew":
			ay := 0.0
			if len(a) > 1 {
				ay = num(a[1], 0)
			}
			tm = []float64{1, math.Tan(ay), math.Tan(num(a[0], 0)), 1, 0, 0}
		case "matrix":
			if len(a) == 6 {
				tm = []float64{num(a[0], 1), num(a[1], 1), num(a[2], 1), num(a[3], 1), num(a[4], 1), num(a[5], 1)}
			}
		}
		if tm != nil {
			m = mul(m, tm)
		}
	}
	m = mul(m, []float64{1, 0, 0, 1, -ox, -oy})
	return m
}

// isAttachmentLink reports whether b is an <a> element with the
// attachment link type.
func isAttachmentLink(b *layout.Box) bool {
	if b == nil || b.Element == nil || !strings.EqualFold(b.Element.Tag, "a") {
		return false
	}
	for _, t := range strings.Fields(strings.ToLower(b.Element.Get("rel"))) {
		if t == "attachment" {
			return true
		}
	}
	return false
}

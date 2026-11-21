package draw

import (
	"fmt"
	"strings"

	"github.com/esoteric-programmer/weasyprint-go/internal/css"
	"github.com/esoteric-programmer/weasyprint-go/internal/layout"
	"github.com/esoteric-programmer/weasyprint-go/internal/pdf"
)

// Tagged PDF, ported from WeasyPrint's pdf/tags.py: text and images are
// drawn in marked content with MCIDs, decorations as artifacts, and the
// structure tree is built from the box tree of each page.

type tagMark struct {
	tag  string
	mcid int
}

type tagState struct {
	// marks of the boxes drawn on each page, and the next MCID.
	marks []map[*layout.Box][]tagMark
	next  []int
	// annotations of link boxes, by page.
	linkAnnots []map[*layout.Box][]pdf.Ref
}

func newTagState(pages int) *tagState {
	t := &tagState{next: make([]int, pages)}
	for i := 0; i < pages; i++ {
		t.marks = append(t.marks, map[*layout.Box][]tagMark{})
		t.linkAnnots = append(t.linkAnnots, map[*layout.Box][]pdf.Ref{})
	}
	return t
}

// marked draws f in a marked-content sequence tagged for box b.
func (cv *canvas) marked(b *layout.Box, tag string, f func()) {
	t := cv.r.tags
	if t == nil {
		f()
		return
	}
	mcid := t.next[cv.page]
	t.next[cv.page]++
	t.marks[cv.page][b] = append(t.marks[cv.page][b], tagMark{tag, mcid})
	cv.c.Op(fmt.Sprintf("/%s <</MCID %d>> BDC", tag, mcid))
	f()
	cv.c.Op("EMC")
}

// artifact draws f as an artifact (decorations not part of the content).
func (cv *canvas) artifact(f func()) {
	if cv.r.tags == nil {
		f()
		return
	}
	cv.c.Op("/Artifact BMC")
	f()
	cv.c.Op("EMC")
}

// pdfTag returns the structure type of an HTML element (WeasyPrint's
// _get_pdf_tag).
func pdfTag(tag string) string {
	switch {
	case tag == "":
		return "NonStruct"
	case tag == "div":
		return "Div"
	case tag == "a" || strings.HasPrefix(tag, "a::"):
		return "Link"
	case tag == "span":
		return "Span"
	case tag == "main":
		return "Part"
	case tag == "article":
		return "Art"
	case tag == "section":
		return "Sect"
	case tag == "blockquote":
		return "BlockQuote"
	case tag == "p":
		return "P"
	case len(tag) == 2 && tag[0] == 'h' && tag[1] >= '1' && tag[1] <= '6':
		return strings.ToUpper(tag)
	case tag == "dl" || tag == "ul" || tag == "ol":
		return "L"
	case tag == "li" || tag == "dt" || tag == "dd":
		return "LI"
	case tag == "li::marker":
		return "Lbl"
	case tag == "table":
		return "Table"
	case tag == "tr" || tag == "th" || tag == "td":
		return strings.ToUpper(tag)
	case tag == "thead" || tag == "tbody" || tag == "tfoot":
		return strings.ToUpper(tag[:2]) + tag[2:]
	case tag == "img" || tag == "svg":
		return "Figure"
	case tag == "caption" || tag == "figcaption":
		return "Caption"
	}
	return "NonStruct"
}

func elementTag(b *layout.Box) string {
	if b.Element == nil {
		return ""
	}
	t := strings.ToLower(b.Element.Tag)
	if b.Pseudo != "" {
		t += "::" + b.Pseudo
	}
	return t
}

type structElem struct {
	ref  pdf.Ref
	dict pdf.Dict
}

type tagBuilder struct {
	r        *Renderer
	page     int
	pageRef  pdf.Ref
	marks    map[*layout.Box][]tagMark
	nums     map[int]pdf.Ref
	annots   *[]annotStruct
	cells    map[*layout.Box]pdf.Dict
	consumed map[*layout.Box]bool
}

type annotStruct struct {
	elem  pdf.Ref
	annot pdf.Ref
}

func (tb *tagBuilder) newElem(tag string, parent pdf.Ref) *structElem {
	d := pdf.Dict{"Type": pdf.Name("StructElem"), "S": pdf.Name(tag), "K": pdf.Array{}, "Pg": tb.pageRef, "P": parent}
	return &structElem{tb.r.doc.Add(d), d}
}

func (e *structElem) add(v any) { e.dict["K"] = append(e.dict["K"].(pdf.Array), v) }

// popMark returns the next marked content of box b.
func (tb *tagBuilder) popMark(b *layout.Box) (tagMark, bool) {
	m := tb.marks[b]
	if len(m) == 0 {
		return tagMark{}, false
	}
	tb.marks[b] = m[1:]
	if len(tb.marks[b]) == 0 {
		delete(tb.marks, b)
	}
	return m[0], true
}

// build returns the structure elements for box b (WeasyPrint's
// _build_box_tree).
func (tb *tagBuilder) build(b *layout.Box, parent *structElem) []*structElem {
	etag := elementTag(b)
	tag := pdfTag(etag)
	switch {
	case etag == "html" || etag == "body" || b.Kind == layout.KPage:
		if b.Kind != layout.KLine {
			var out []*structElem
			for _, ch := range tb.children(b) {
				out = append(out, tb.build(ch, parent)...)
			}
			return out
		}
	case b.Kind == layout.KMargin:
		// Consume the marked content of margin boxes without linking it.
		for _, ch := range b.Children {
			tb.build(ch, parent)
		}
		return nil
	case tag == "TD" || tag == "TH":
		if s := parent.dict["S"]; s == pdf.Name("TD") || s == pdf.Name("TH") {
			tag = "NonStruct"
		}
	case tag == "LI":
		s := parent.dict["S"]
		noBullet := etag == "li" && (!strings.Contains(b.Style.Display(), "list-item") || b.Style.Kw(css.PListStyleType) == "none")
		switch {
		case s == pdf.Name("LI"):
			tag = "LBody"
		case s == pdf.Name("LBody"):
			tag = "NonStruct"
		case etag == "dt" || etag == "dd" || noBullet:
			li := tb.newElem("LI", parent.ref)
			for _, e := range tb.buildAs(b, li, "LBody") {
				li.add(e.ref)
			}
			return []*structElem{li}
		}
	}
	return tb.buildAs(b, parent, tag)
}

func (tb *tagBuilder) children(b *layout.Box) []*layout.Box {
	var out []*layout.Box
	if b.Marker != nil {
		out = append(out, b.Marker)
	}
	return append(out, b.Children...)
}

func (tb *tagBuilder) buildAs(b *layout.Box, parent *structElem, tag string) []*structElem {
	elem := tb.newElem(tag, parent.ref)
	box := b
	switch tag {
	case "Figure":
		x1, y1 := b.ContentX(), b.ContentY()
		elem.dict["A"] = pdf.Dict{"O": pdf.Name("Layout"), "BBox": []float64{x1, y1, x1 + b.W, y1 + b.H}}
		if b.Element != nil && strings.EqualFold(b.Element.Tag, "svg") {
			if t := b.Element.Find("title"); t != nil && strings.TrimSpace(t.TextContent()) != "" {
				elem.dict["Alt"] = pdf.String(t.TextContent())
			}
		} else if b.Element != nil && b.Element.Get("alt") != "" {
			elem.dict["Alt"] = pdf.String(b.Element.Get("alt"))
		} else if b.Element != nil {
			src := b.Element.Get("src")
			if src == "" {
				src = "unknown"
			}
			tb.r.warn(fmt.Sprintf("Image %q has no required alt description", src))
		}
	case "Table":
		if b.Kind == layout.KTableWrapper {
			for _, ch := range b.Children {
				if ch.Kind == layout.KTable {
					t := ch.CopyWith(nil)
					for _, c := range b.Children {
						if c == ch {
							t.Children = append(t.Children, ch.Children...)
						} else {
							t.Children = append(t.Children, c)
						}
					}
					box = t
					break
				}
			}
		}
	case "TH", "TD":
		a := pdf.Dict{"O": pdf.Name("Table")}
		if b.Colspan > 1 {
			a["ColSpan"] = b.Colspan
		}
		if b.Rowspan > 1 {
			a["RowSpan"] = b.Rowspan
		}
		elem.dict["A"] = a
		tb.cells[b.Original()] = elem.dict
	}
	// Link annotations.
	for _, annot := range tb.r.tags.linkAnnots[tb.page][b] {
		objr := tb.r.doc.Add(pdf.Dict{"Type": pdf.Name("OBJR"), "Obj": annot, "Pg": tb.pageRef})
		*tb.annots = append(*tb.annots, annotStruct{elem.ref, annot})
		elem.add(objr)
	}
	// Widget annotations of form fields.
	if r := tb.r; r.forms != nil {
		if annot, ok := r.forms.widgetsFor[b]; ok {
			objr := r.doc.Add(pdf.Dict{"Type": pdf.Name("OBJR"), "Obj": annot, "Pg": tb.pageRef})
			form := tb.newElem("Form", elem.ref)
			form.add(objr)
			*tb.annots = append(*tb.annots, annotStruct{form.ref, annot})
			elem.add(form.ref)
		}
	}
	// Children.
	var lbody *structElem
	for _, child := range tb.children(box) {
		kids := []*layout.Box{child}
		target := elem
		if child.Kind == layout.KLine {
			kids = child.Children
			if tag == "LI" {
				// Inline content of list items is in an anonymous block
				// in WeasyPrint, tagged as the list item body.
				if lbody == nil {
					lbody = tb.newElem("LBody", elem.ref)
					elem.add(lbody.ref)
				}
				target = lbody
			}
		}
		for _, ch := range kids {
			if ch.Kind == layout.KText || ch.Kind == layout.KLeader {
				for {
					m, ok := tb.popMark(ch)
					if !ok {
						break
					}
					if tag == "Link" {
						target.add(m.mcid)
						tb.nums[m.mcid] = target.ref
					} else {
						kid := tb.newElem(m.tag, target.ref)
						kid.add(m.mcid)
						target.add(kid.ref)
						tb.nums[m.mcid] = kid.ref
					}
				}
				continue
			}
			p := target
			if t := elementTag(ch); (t == "ul" || t == "ol") && elem.dict["S"] == pdf.Name("LI") {
				// Nested lists belong to the parent list in PDF.
				p = parent
			}
			// Children are inserted before the grandchildren added to the
			// same element during the recursion (nested lists).
			at := len(p.dict["K"].(pdf.Array))
			var refs pdf.Array
			for _, e := range tb.build(ch, p) {
				refs = append(refs, e.ref)
			}
			k := p.dict["K"].(pdf.Array)
			p.dict["K"] = append(append(append(pdf.Array{}, k[:at]...), refs...), k[at:]...)
		}
	}
	if b.Kind == layout.KReplaced {
		for {
			m, ok := tb.popMark(b)
			if !ok {
				break
			}
			elem.add(m.mcid)
			tb.nums[m.mcid] = elem.ref
		}
	}
	if tag == "Table" {
		tb.tableHeaders(box, elem)
	}
	return []*structElem{elem}
}

// tableHeaders links data cells to their header cells.
func (tb *tagBuilder) tableHeaders(table *layout.Box, elem *structElem) {
	var rows []*layout.Box
	var collect func(b *layout.Box)
	collect = func(b *layout.Box) {
		for _, ch := range b.Children {
			if ch.Kind == layout.KRow {
				rows = append(rows, ch)
			} else if ch.Kind == layout.KRowGroup {
				collect(ch)
			}
		}
	}
	collect(table)
	colHeaders := map[int][]any{}
	rowHeaders := map[int][]any{}
	for i, row := range rows {
		for _, cell := range row.Children {
			if elementTag(cell) != "th" {
				continue
			}
			d := tb.cells[cell.Original()]
			if d == nil {
				continue
			}
			key := pdf.String(fmt.Sprintf("%d-%d-%d", elem.ref, i, cell.GridX))
			d["ID"] = key
			if cell.Element.Get("scope") == "row" {
				rowHeaders[i] = append(rowHeaders[i], key)
			} else {
				colHeaders[cell.GridX] = append(colHeaders[cell.GridX], key)
			}
		}
	}
	for i, row := range rows {
		for _, cell := range row.Children {
			if elementTag(cell) != "td" {
				continue
			}
			d := tb.cells[cell.Original()]
			if d == nil {
				continue
			}
			headers := pdf.Array{}
			for s := 0; s < max(1, cell.Rowspan); s++ {
				headers = append(headers, rowHeaders[i+s]...)
			}
			for s := 0; s < max(1, cell.Colspan); s++ {
				headers = append(headers, colHeaders[cell.GridX+s]...)
			}
			d["A"].(pdf.Dict)["Headers"] = headers
		}
	}
}

// addTags adds the structure tree to the catalog.
func (r *Renderer) addTags(catalog pdf.Dict, pages []*layout.Page) {
	parentTree := pdf.Dict{}
	parentTreeRef := r.doc.Add(parentTree)
	rootDict := pdf.Dict{"Type": pdf.Name("StructTreeRoot"), "ParentTree": parentTreeRef}
	rootRef := r.doc.Add(rootDict)
	document := &structElem{dict: pdf.Dict{"Type": pdf.Name("StructElem"), "S": pdf.Name("Document"), "K": pdf.Array{}, "P": rootRef}}
	document.ref = r.doc.Add(document.dict)
	rootDict["K"] = pdf.Array{document.ref}
	catalog["StructTreeRoot"] = rootRef
	if pdf.VersionAtLeast(r.opts.Version, "2.0") {
		ns := r.doc.Add(pdf.Dict{"Type": pdf.Name("Namespace"), "NS": pdf.String("http://iso.org/pdf2/ssn")})
		rootDict["Namespaces"] = pdf.Array{ns}
		document.dict["NS"] = ns
	}
	nums := pdf.Array{}
	var annots []annotStruct
	for i, p := range pages {
		tb := &tagBuilder{r: r, page: i, pageRef: r.pageRefs[i], marks: r.tags.marks[i],
			nums: map[int]pdf.Ref{}, annots: &annots, cells: map[*layout.Box]pdf.Dict{}}
		for _, e := range tb.build(p.Box, document) {
			document.add(e.ref)
		}
		// Marked content of boxes not found in the tree (should not
		// happen) is attached to the document.
		for b := range tb.marks {
			for {
				m, ok := tb.popMark(b)
				if !ok {
					break
				}
				kid := tb.newElem(m.tag, document.ref)
				kid.add(m.mcid)
				document.add(kid.ref)
				tb.nums[m.mcid] = kid.ref
			}
		}
		refs := make(pdf.Array, r.tags.next[i])
		for k := range refs {
			if ref, ok := tb.nums[k]; ok {
				refs[k] = ref
			} else {
				refs[k] = document.ref
			}
		}
		nums = append(nums, i, refs)
	}
	for i, a := range annots {
		n := len(pages) + i
		nums = append(nums, n, a.elem)
		if d, ok := r.doc.Get(a.annot).(pdf.Dict); ok {
			d["StructParent"] = n
		}
	}
	parentTree["Nums"] = nums
	catalog["ViewerPreferences"] = pdf.Dict{"DisplayDocTitle": true}
	catalog["MarkInfo"] = pdf.Dict{"Marked": true}
	if _, ok := catalog["Lang"]; !ok {
		r.warn(`Missing required "lang" attribute at the root of the document`)
		catalog["Lang"] = pdf.String("")
	}
}

func (r *Renderer) warn(s string) {
	if r.opts.Warn != nil {
		r.opts.Warn(s)
	}
}

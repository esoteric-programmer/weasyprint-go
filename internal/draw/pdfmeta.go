package draw

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/esoteric-programmer/weasyprint-go/internal/pdf"
)

// Variant describes a PDF variant (PDF/A, PDF/UA, PDF/X), like the
// VARIANTS of WeasyPrint.
type Variant struct {
	// Family is "a", "ua" or "x".
	Family string
	// Part is the version of the standard (1 for PDF/A-1...).
	Part int
	// Conformance is the conformance level of PDF/A ("A", "B", "U", "E",
	// "F") or the PDF/X variant ("a:2003", ":2003", "", "g").
	Conformance string
	// Default options of the variant.
	Version      string
	Identifier   bool
	OutputIntent string
	Tags         bool
}

// Variants are the supported PDF variants, by name.
var Variants = map[string]Variant{
	"pdf/a-1b": {Family: "a", Part: 1, Conformance: "B", Version: "1.4", Identifier: true, OutputIntent: "srgb"},
	"pdf/a-2b": {Family: "a", Part: 2, Conformance: "B", Version: "1.7", Identifier: true, OutputIntent: "srgb"},
	"pdf/a-3b": {Family: "a", Part: 3, Conformance: "B", Version: "1.7", Identifier: true, OutputIntent: "srgb"},
	"pdf/a-2u": {Family: "a", Part: 2, Conformance: "U", Version: "1.7", Identifier: true, OutputIntent: "srgb"},
	"pdf/a-3u": {Family: "a", Part: 3, Conformance: "U", Version: "1.7", Identifier: true, OutputIntent: "srgb"},
	"pdf/a-4u": {Family: "a", Part: 4, Conformance: "U", Version: "2.0", Identifier: true, OutputIntent: "srgb"},
	"pdf/a-1a": {Family: "a", Part: 1, Conformance: "A", Version: "1.4", Identifier: true, OutputIntent: "srgb", Tags: true},
	"pdf/a-2a": {Family: "a", Part: 2, Conformance: "A", Version: "1.7", Identifier: true, OutputIntent: "srgb", Tags: true},
	"pdf/a-3a": {Family: "a", Part: 3, Conformance: "A", Version: "1.7", Identifier: true, OutputIntent: "srgb", Tags: true},
	"pdf/a-4e": {Family: "a", Part: 4, Conformance: "E", Version: "2.0", Identifier: true, OutputIntent: "srgb"},
	"pdf/a-4f": {Family: "a", Part: 4, Conformance: "F", Version: "2.0", Identifier: true, OutputIntent: "srgb"},
	"pdf/ua-1": {Family: "ua", Part: 1, Version: "1.7", Tags: true},
	"pdf/ua-2": {Family: "ua", Part: 2, Version: "2.0", Tags: true},
	"pdf/x-1a": {Family: "x", Part: 1, Conformance: "a:2003", Version: "1.4", Identifier: true, OutputIntent: "device-cmyk"},
	"pdf/x-3":  {Family: "x", Part: 3, Conformance: ":2003", Version: "1.4", Identifier: true, OutputIntent: "device-cmyk"},
	"pdf/x-4":  {Family: "x", Part: 4, Conformance: "", Version: "1.6", Identifier: true, OutputIntent: "device-cmyk"},
	"pdf/x-5g": {Family: "x", Part: 5, Conformance: "g", Version: "1.6", Identifier: true, OutputIntent: "device-cmyk"},
}

// VariantNames returns the names of the supported PDF variants, sorted
// like WeasyPrint's.
func VariantNames() []string {
	return []string{"pdf/a-1b", "pdf/a-2b", "pdf/a-3b", "pdf/a-2u", "pdf/a-3u", "pdf/a-4u",
		"pdf/a-1a", "pdf/a-2a", "pdf/a-3a", "pdf/a-4e", "pdf/a-4f", "pdf/ua-1", "pdf/ua-2",
		"pdf/x-1a", "pdf/x-3", "pdf/x-4", "pdf/x-5g"}
}

var w3cDate = regexp.MustCompile(`^[ \t\n\f\r]*(\d\d\d\d)(?:-(0\d|1[012])(?:-([012]\d|3[01])(?:T([01]\d|2[0-3]):([0-5]\d)(?::([0-5]\d)(?:\.\d+)?)?(?:Z|([+-](?:[01]\d|2[0-3])):([0-5]\d)))?)?)?[ \t\n\f\r]*$`)

// ValidW3CDate reports whether s is a date in one of the W3C profiles of
// ISO 8601.
func ValidW3CDate(s string) bool { return w3cDate.MatchString(s) }

// w3cDateToPDF converts a W3C date to a PDF date like WeasyPrint.
func w3cDateToPDF(s string) (string, bool) {
	m := w3cDate.FindStringSubmatch(s)
	if m == nil {
		return "", false
	}
	year, month, day, hour, minute, second, tzHour, tzMinute := m[1], m[2], m[3], m[4], m[5], m[6], m[7], m[8]
	date := ""
	found := hour != ""
	for _, part := range []struct {
		v   string
		one bool
	}{{second, false}, {minute, false}, {hour, false}, {day, true}, {month, true}, {year, false}} {
		switch {
		case part.v != "":
			found = true
			date = part.v + date
		case found:
			if part.one {
				date = "01" + date
			} else {
				date = "00" + date
			}
		}
	}
	if hour != "" {
		if tzHour != "" {
			h, _ := strconv.Atoi(tzHour)
			mm, _ := strconv.Atoi(tzMinute)
			date += fmt.Sprintf("%+03d'%02d", h, mm)
		} else {
			date += "Z"
		}
	}
	return "D:" + date, true
}

func xmlEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;").Replace(s)
}

var xmpNamespaces = map[string]string{
	"rdf":     "http://www.w3.org/1999/02/22-rdf-syntax-ns#",
	"dc":      "http://purl.org/dc/elements/1.1/",
	"xmp":     "http://ns.adobe.com/xap/1.0/",
	"xmpMM":   "http://ns.adobe.com/xap/1.0/mm/",
	"pdf":     "http://ns.adobe.com/pdf/1.3/",
	"pdfaid":  "http://www.aiim.org/pdfa/ns/id/",
	"pdfuaid": "http://www.aiim.org/pdfua/ns/id/",
	"pdfxid":  "http://www.npes.org/pdfx/ns/id/",
	"pdfx":    "http://ns.adobe.com/pdfx/1.3/",
}

// rdfMetadata generates the RDF metadata like WeasyPrint's
// generate_rdf_metadata (serialized like Python's ElementTree).
func (r *Renderer) rdfMetadata(family string, part int, conformance string) string {
	m := r.opts.Metadata
	used := map[string]bool{"rdf": true}
	var body strings.Builder
	type attr struct{ k, v string }
	desc := func(attrs []attr, children string) {
		body.WriteString(`<rdf:Description rdf:about=""`)
		for _, a := range attrs {
			used[strings.Split(a.k, ":")[0]] = true
			fmt.Fprintf(&body, ` %s="%s"`, a.k, xmlEscape(a.v))
		}
		if children == "" {
			body.WriteString(" />")
			return
		}
		body.WriteString(">" + children + "</rdf:Description>")
	}
	elem := func(name, text string) string {
		used[strings.Split(name, ":")[0]] = true
		return "<" + name + ">" + xmlEscape(text) + "</" + name + ">"
	}
	if part != 0 {
		ns := "pdf" + family + "id"
		var attrs []attr
		if !(family == "x" && part >= 4) {
			attrs = append(attrs, attr{ns + ":part", strconv.Itoa(part)})
		}
		children := ""
		if conformance != "" {
			if family == "x" {
				for _, k := range []string{"pdfxid:GTS_PDFXVersion", "pdfx:GTS_PDFXVersion", "pdfx:GTS_PDFXConformance"} {
					children += elem(k, conformance)
				}
				children += elem("pdf:Trapped", "False")
				if part >= 4 {
					children += elem("xmp:MetadataDate", m.Modified)
					children += elem("xmpMM:DocumentID", "xmp.did:"+r.uuid())
					children += elem("xmpMM:RenditionClass", "proof:pdf")
					children += elem("xmpMM:VersionID", "1")
				}
			} else {
				attrs = append(attrs, attr{ns + ":conformance", conformance})
				if family == "a" && part == 4 {
					children += elem("pdfaid:rev", "2020")
				} else if family == "ua" && part == 2 {
					children += elem("pdfuaid:rev", "2024")
				}
			}
		}
		desc(attrs, children)
	}
	desc([]attr{{"pdf:Producer", r.producer()}}, "")
	used["pdf"] = true
	alt := func(text string) string {
		return `<rdf:Alt><rdf:li xml:lang="x-default">` + xmlEscape(text) + `</rdf:li></rdf:Alt>`
	}
	if m.Title != "" {
		used["dc"] = true
		desc(nil, "<dc:title>"+alt(m.Title)+"</dc:title>")
	}
	if len(m.Authors) > 0 {
		used["dc"] = true
		seq := "<rdf:Seq>"
		for _, a := range m.Authors {
			seq += "<rdf:li>" + xmlEscape(a) + "</rdf:li>"
		}
		desc(nil, "<dc:creator>"+seq+"</rdf:Seq></dc:creator>")
	}
	if m.Description != "" {
		used["dc"] = true
		desc(nil, "<dc:description>"+alt(m.Description)+"</dc:description>")
	}
	if len(m.Keywords) > 0 {
		desc(nil, elem("pdf:Keywords", strings.Join(m.Keywords, ", ")))
	}
	if m.Generator != "" {
		desc(nil, elem("xmp:CreatorTool", m.Generator))
	}
	if m.Created != "" {
		desc(nil, elem("xmp:CreateDate", m.Created))
	}
	if m.Modified != "" {
		desc(nil, elem("xmp:ModifyDate", m.Modified))
	}
	// Namespace declarations sorted by prefix, like ElementTree.
	var prefixes []string
	for p := range used {
		prefixes = append(prefixes, p)
	}
	sortStrings(prefixes)
	var out strings.Builder
	out.WriteString("<rdf:RDF")
	for _, p := range prefixes {
		fmt.Fprintf(&out, ` xmlns:%s="%s"`, p, xmpNamespaces[p])
	}
	out.WriteString(">" + body.String() + "</rdf:RDF>")
	return out.String()
}

// addMetadataStream adds the XMP metadata stream to the catalog, like
// WeasyPrint's include_in_pdf.
func (r *Renderer) addMetadataStream(catalog pdf.Dict, family string, part int, conformance string, compress bool) {
	content := "<?xpacket begin=\"\" id=\"W5M0MpCehiHzreSzNTczkc9d\"?>\n<x:xmpmeta xmlns:x=\"adobe:ns:meta/\">\n" +
		r.rdfMetadata(family, part, conformance)
	for _, x := range r.opts.XMPMetadata {
		content += "\n" + string(x)
	}
	content += "\n</x:xmpmeta>\n<?xpacket end=\"r\"?>"
	catalog["Metadata"] = r.doc.Add(&pdf.Stream{
		Dict: pdf.Dict{"Type": pdf.Name("Metadata"), "Subtype": pdf.Name("XML")},
		Data: []byte(content), Compress: compress,
	})
}

func (r *Renderer) uuid() string {
	b := make([]byte, 16)
	t := time.Now().UnixNano()
	for i := range b {
		b[i] = byte(t >> (i * 4))
		t = t*6364136223846793005 + 1442695040888963407
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

func (r *Renderer) producer() string {
	if r.opts.Producer != "" {
		return r.opts.Producer
	}
	return "WeasyPrint-Go"
}

// pdfNow returns the current date in PDF and W3C formats.
func pdfNow() (string, string) {
	now := time.Now()
	_, off := now.Zone()
	sign := '+'
	if off < 0 {
		sign = '-'
		off = -off
	}
	iso := now.Format("2006-01-02T15:04:05") + fmt.Sprintf("%c%02d:%02d", sign, off/3600, off%3600/60)
	p := now.Format("D:20060102150405") + fmt.Sprintf("%c%02d'%02d'", sign, off/3600, off%3600/60)
	return p, iso
}

// applyVariant sets what a PDF variant needs, like WeasyPrint's pdfa,
// pdfua and pdfx functions.
func (r *Renderer) applyVariant(v Variant, catalog, info pdf.Dict, annots []pdf.Ref) {
	compress := r.opts.Compress
	switch v.Family {
	case "a":
		if v.Part <= 2 {
			r.removeAttachmentFiles(v.Part, catalog)
		} else {
			r.addAssociatedFiles(catalog)
		}
		// Print annotations.
		for _, a := range annots {
			if d, ok := r.doc.Get(a).(pdf.Dict); ok {
				d["F"] = 4
			}
		}
		if v.Part == 1 {
			compress = false
		}
		r.addMetadataStream(catalog, "a", v.Part, v.Conformance, compress)
		if v.Part >= 4 {
			for k := range info {
				delete(info, k)
			}
		}
	case "ua":
		conformance := ""
		if v.Part >= 2 {
			conformance = fmt.Sprintf("PDF/UA-%d", v.Part)
		}
		r.addMetadataStream(catalog, "ua", v.Part, conformance, compress)
	case "x":
		conformance := fmt.Sprintf("PDF/X-%d%s", v.Part, v.Conformance)
		if v.Part < 4 {
			info["GTS_PDFXVersion"] = pdf.String(conformance)
			info["GTS_PDFXConformance"] = pdf.String(conformance)
		}
		info["Trapped"] = pdf.Name("False")
		now, nowISO := pdfNow()
		if r.opts.Metadata.Modified == "" {
			r.opts.Metadata.Modified = nowISO
			info["ModDate"] = pdf.String(now)
		}
		if r.opts.Metadata.Created == "" {
			r.opts.Metadata.Created = nowISO
			info["CreationDate"] = pdf.String(now)
		}
		if v.Part >= 4 {
			compress = false
		}
		r.addMetadataStream(catalog, "x", v.Part, conformance, compress)
	}
}

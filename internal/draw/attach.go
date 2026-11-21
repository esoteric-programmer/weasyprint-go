package draw

import (
	"crypto/md5"
	"encoding/hex"
	"mime"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/esoteric-programmer/weasyprint-go/internal/pdf"
)

// Attachment is a file embedded in the PDF.
type Attachment struct {
	Data []byte
	// URL is where the data comes from, used for the file name and type.
	URL      string
	MimeType string
	// Name is the file name in the PDF (taken from the URL if empty).
	Name        string
	Description string
	// Relationship is the relationship with the document for PDF/A-3
	// ("Source", "Data", "Alternative", "Supplement"... default
	// "Unspecified").
	Relationship      string
	Created, Modified time.Time
}

type writtenAttachment struct {
	ref          pdf.Ref // file specification
	stream       pdf.Ref // embedded file stream
	md5          string
	mimeType     string
	relationship string
}

// writeAttachment embeds a file like WeasyPrint's write_pdf_attachment and
// returns its file specification.
func (r *Renderer) writeAttachment(a Attachment) writtenAttachment {
	sum := md5.Sum(a.Data)
	hash := hex.EncodeToString(sum[:])
	name := a.Name
	if name == "" {
		if u, err := url.Parse(a.URL); err == nil && a.URL != "" && u.Scheme != "data" && u.Path != "" {
			name = path.Base(u.Path)
		}
	}
	if name == "" || name == "/" || name == "." {
		name = "attachment.bin"
	}
	mt := a.MimeType
	if i := strings.IndexByte(mt, ';'); i >= 0 {
		mt = strings.TrimSpace(mt[:i])
	}
	if mt == "" {
		if ext := path.Ext(name); ext != "" {
			mt = mime.TypeByExtension(ext)
			if i := strings.IndexByte(mt, ';'); i >= 0 {
				mt = mt[:i]
			}
		}
	}
	if mt == "" {
		mt = "application/octet-stream"
	}
	now := time.Now()
	created, modified := a.Created, a.Modified
	if created.IsZero() {
		created = now
	}
	if modified.IsZero() {
		modified = now
	}
	stream := r.doc.Add(&pdf.Stream{
		Dict: pdf.Dict{
			"Type":    pdf.Name("EmbeddedFile"),
			"Subtype": pdf.Name(mt),
			"Params": pdf.Dict{
				"CheckSum":     pdf.Raw("<" + hash + ">"),
				"Size":         len(a.Data),
				"CreationDate": pdf.String(created.UTC().Format("D:20060102150405Z")),
				"ModDate":      pdf.String(modified.UTC().Format("D:20060102150405Z")),
			},
		},
		Data: a.Data, Compress: r.opts.Compress,
	})
	ascii := strings.Map(func(c rune) rune {
		if c > 0x7f {
			return -1
		}
		return c
	}, name)
	spec := r.doc.Add(pdf.Dict{
		"Type": pdf.Name("Filespec"),
		"F":    pdf.String(ascii),
		"UF":   pdf.String(name),
		"EF":   pdf.Dict{"F": stream},
		"Desc": pdf.String(a.Description),
	})
	rel := a.Relationship
	if rel == "" {
		rel = "Unspecified"
	}
	w := writtenAttachment{ref: spec, stream: stream, md5: hash, mimeType: mt, relationship: rel}
	r.written = append(r.written, w)
	return w
}

// removeAttachmentFiles removes the embedded files that PDF/A-1 (all) and
// PDF/A-2 (those that are not PDF files) forbid.
func (r *Renderer) removeAttachmentFiles(part int, catalog pdf.Dict) {
	if part == 1 {
		if names, ok := catalog["Names"].(pdf.Dict); ok {
			delete(names, "EmbeddedFiles")
			if len(names) == 0 {
				delete(catalog, "Names")
			}
		}
	}
	for _, w := range r.written {
		if part == 1 || w.mimeType != "application/pdf" {
			if d, ok := r.doc.Get(w.ref).(pdf.Dict); ok {
				delete(d, "EF")
			}
		}
	}
}

// addAssociatedFiles lists the attachments as associated files with their
// relationship, for PDF/A-3 and later.
func (r *Renderer) addAssociatedFiles(catalog pdf.Dict) {
	var af pdf.Array
	for _, w := range r.written {
		if d, ok := r.doc.Get(w.ref).(pdf.Dict); ok {
			d["AFRelationship"] = pdf.Name(w.relationship)
		}
		af = append(af, w.ref)
	}
	if len(af) > 0 {
		catalog["AF"] = af
	}
}

package weasyprint

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/esoteric-programmer/weasyprint-go/internal/css"
	"github.com/esoteric-programmer/weasyprint-go/internal/draw"
	"github.com/esoteric-programmer/weasyprint-go/internal/fetch"
)

// PDFOptions control the PDF file, like the PDF options of WeasyPrint's
// write_pdf.
type PDFOptions struct {
	// Variant is a PDF variant: "pdf/a-1b", "pdf/a-2b", "pdf/a-3b",
	// "pdf/a-2u", "pdf/a-3u", "pdf/a-4u", "pdf/a-1a", "pdf/a-2a",
	// "pdf/a-3a", "pdf/a-4e", "pdf/a-4f", "pdf/ua-1", "pdf/ua-2",
	// "pdf/x-1a", "pdf/x-3", "pdf/x-4" or "pdf/x-5g". It sets the
	// default version, identifier, output intent and tags.
	Variant string
	// Version is the PDF version ("1.7" by default).
	Version string
	// Identifier is the PDF file identifier (none by default, a hash of
	// the file for PDF variants).
	Identifier string
	// Tags makes a tagged PDF, for accessibility.
	Tags bool
	// Forms includes PDF form fields for form controls (and for elements
	// with appearance: auto).
	Forms bool
	// CustomMetadata stores custom <meta> elements in the PDF metadata.
	CustomMetadata bool
	// XMPMetadata are URLs or paths of XML files added to the XMP metadata.
	XMPMetadata []string
	// OutputIntent is "srgb", "device-cmyk" or the name of an
	// @color-profile rule.
	OutputIntent string
	// Attachments are files embedded in the PDF.
	Attachments []Attachment
	// OptimizeImages is accepted for compatibility: images other than
	// JPEG are always compressed with PNG predictors, and JPEG images are
	// kept as they are (unless JPEGQuality or DPI is set).
	OptimizeImages bool
	// JPEGQuality (1 to 95) re-encodes images as JPEG with this quality;
	// 0 keeps them.
	JPEGQuality int
	// DPI is the maximum resolution of embedded images; 0 keeps them.
	DPI float64
	// FullFonts embeds whole font files instead of subsets.
	FullFonts bool
	// Hinting keeps the hinting instructions of embedded fonts.
	Hinting bool
	// Zoom scales the pages in the PDF (1 by default). Like in
	// WeasyPrint, physical CSS units are then "wrong".
	Zoom float64
}

// Attachment is a file embedded in the PDF.
type Attachment struct {
	// URL is a URL or path of the file, or Data its content.
	URL  string
	Data []byte
	// Name is the file name (from the URL by default).
	Name string
	// Description of the file.
	Description string
	// Relationship with the document, for PDF/A-3 and later: "Source",
	// "Data", "Alternative", "Supplement", "EncryptedPayload",
	// "FormData", "Schema" or "Unspecified" (default).
	Relationship string
	// Created and Modified dates (now by default, or the dates of the
	// file).
	Created, Modified time.Time
}

// FetcherOptions configure the default URL fetcher.
type FetcherOptions struct {
	// Timeout of HTTP requests (10 seconds by default).
	Timeout time.Duration
	// AllowedProtocols are the URL schemes that may be fetched ("file",
	// "http", "https", "data"...); all when empty.
	AllowedProtocols []string
	// NoHTTPRedirects disables following HTTP redirects.
	NoHTTPRedirects bool
	// FailOnHTTPErrors makes rendering fail on any HTTP error.
	FailOnHTTPErrors bool
	// CacheDir stores fetched HTTP resources, reused by later renderings.
	CacheDir string
}

// ErrHTTP is returned when rendering fails because of an HTTP error with
// FailOnHTTPErrors.
var ErrHTTP = errors.New("weasyprint: HTTP error")

// NewURLFetcher returns the default URL fetcher with options. With
// FailOnHTTPErrors, the first HTTP error is reported by Err.
func NewURLFetcher(opts FetcherOptions) (URLFetcher, func() error) {
	var first error
	f := fetch.New(fetch.Options{
		Timeout: opts.Timeout, AllowedProtocols: opts.AllowedProtocols,
		NoRedirects: opts.NoHTTPRedirects, FailOnErrors: opts.FailOnHTTPErrors,
		CacheDir: opts.CacheDir,
		OnFatal: func(err error) {
			if first == nil {
				first = fmt.Errorf("%w: %v", ErrHTTP, err)
			}
		},
	})
	fetcher := func(url string) ([]byte, string, string, error) {
		r, err := f(url)
		if err != nil {
			return nil, "", "", err
		}
		return r.Data, r.MimeType, r.RedirectURL, nil
	}
	return fetcher, func() error { return first }
}

// PDFVariants returns the names of the supported PDF variants.
func PDFVariants() []string { return draw.VariantNames() }

// pdfDrawOptions converts the options for the PDF writer.
func (d *Document) pdfDrawOptions(o *PDFOptions) (draw.Options, error) {
	m := d.Metadata
	do := draw.Options{
		Compress: d.compress,
		Producer: "WeasyPrint-Go " + Version,
		Metadata: draw.Metadata{
			Title: m.Title, Authors: m.Authors, Description: m.Description, Keywords: m.Keywords,
			Generator: m.Generator, Created: m.Created, Modified: m.Modified, Lang: m.Lang,
			Custom: m.Custom,
		},
		Version:        o.Version,
		Tags:           o.Tags,
		Forms:          o.Forms,
		CustomMetadata: o.CustomMetadata,
		OutputIntent:   o.OutputIntent,
		ColorProfiles:  d.colorProfiles,
		Variant:        o.Variant,
		Warn:           d.warn,
		OptimizeImages: o.OptimizeImages,
		JPEGQuality:    o.JPEGQuality,
		DPI:            o.DPI,
		FullFonts:      o.FullFonts,
		Hinting:        o.Hinting,
		Zoom:           o.Zoom,
	}
	if o.Identifier != "" {
		do.Identifier = []byte(o.Identifier)
	}
	if o.Variant != "" {
		v, ok := draw.Variants[strings.ToLower(o.Variant)]
		if !ok {
			names := draw.VariantNames()
			sort.Strings(names)
			return do, fmt.Errorf("weasyprint: unknown PDF variant %q (known: %s)", o.Variant, strings.Join(names, ", "))
		}
		do.Variant = strings.ToLower(o.Variant)
		if do.Version == "" {
			do.Version = v.Version
		}
		if do.Identifier == nil && v.Identifier {
			do.AutoIdentifier = true
		}
		if do.OutputIntent == "" {
			do.OutputIntent = v.OutputIntent
		}
		if v.Tags {
			do.Tags = true
		}
	}
	fetcher := d.fetcher
	for _, u := range o.XMPMetadata {
		res, err := fetcher(fetch.EnsureURL(u))
		if err != nil {
			return do, fmt.Errorf("weasyprint: XMP metadata %s: %w", u, err)
		}
		do.XMPMetadata = append(do.XMPMetadata, res.Data)
	}
	// Attachments of <link rel=attachment> elements, then options.
	all := append(append([]Attachment{}, m.Attachments...), o.Attachments...)
	for _, a := range all {
		da, err := d.loadAttachment(a)
		if err != nil {
			d.warn("Failed to load attachment: " + err.Error())
			continue
		}
		do.Attachments = append(do.Attachments, da)
	}
	do.FetchAttachment = func(url string) (draw.Attachment, bool) {
		da, err := d.loadAttachment(Attachment{URL: url})
		if err != nil {
			d.warn("Failed to load attachment: " + err.Error())
			return da, false
		}
		return da, true
	}
	return do, nil
}

func (d *Document) loadAttachment(a Attachment) (draw.Attachment, error) {
	da := draw.Attachment{Data: a.Data, Name: a.Name, Description: a.Description,
		Relationship: a.Relationship, Created: a.Created, Modified: a.Modified}
	if a.Data == nil {
		url := fetch.EnsureURL(a.URL)
		res, err := d.fetcher(url)
		if err != nil {
			return da, err
		}
		da.Data, da.MimeType, da.URL = res.Data, res.MimeType, res.RedirectURL
		// Dates of local files, like WeasyPrint.
		if !strings.Contains(a.URL, "://") && !strings.HasPrefix(a.URL, "data:") {
			if st, err := os.Stat(a.URL); err == nil {
				if da.Created.IsZero() {
					da.Created = st.ModTime()
				}
				if da.Modified.IsZero() {
					da.Modified = st.ModTime()
				}
			}
		}
	}
	return da, nil
}

// loadColorProfiles loads the ICC profiles of @color-profile rules.
func loadColorProfiles(rules []*css.ColorProfileRule, f fetch.Fetcher, warn func(string)) map[string]*draw.ColorProfile {
	out := map[string]*draw.ColorProfile{}
	for _, r := range rules {
		res, err := f(r.URL)
		if err != nil {
			warn("Failed to load color profile " + r.Name + ": " + err.Error())
			continue
		}
		p, err := draw.ParseICC(res.Data)
		if err != nil {
			warn("Invalid profile file for profile named " + r.Name + ", the whole @color-profile rule was ignored")
			continue
		}
		out[r.Name] = p
	}
	return out
}

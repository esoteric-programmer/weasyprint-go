// Package weasyprint converts HTML and CSS documents to PDF.
//
// It is a Go reimplementation of WeasyPrint (https://weasyprint.org/), a
// visual rendering engine for HTML and CSS that can export to PDF. It
// supports CSS paged media (page sizes, margins, headers and footers,
// counters, named pages, page breaks), floats, tables, flexbox, positioned
// elements, web fonts, images, links, bookmarks and metadata.
//
// Typical usage:
//
//	doc, err := weasyprint.HTMLFromFile("report.html")
//	if err != nil { ... }
//	err = doc.WritePDFFile("report.pdf", nil)
package weasyprint

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/esoteric-programmer/weasyprint-go/internal/css"
	"github.com/esoteric-programmer/weasyprint-go/internal/draw"
	"github.com/esoteric-programmer/weasyprint-go/internal/fetch"
	"github.com/esoteric-programmer/weasyprint-go/internal/fonts"
	"github.com/esoteric-programmer/weasyprint-go/internal/html"
	"github.com/esoteric-programmer/weasyprint-go/internal/hyphen"
	"github.com/esoteric-programmer/weasyprint-go/internal/images"
	"github.com/esoteric-programmer/weasyprint-go/internal/layout"
	"github.com/esoteric-programmer/weasyprint-go/internal/text"
)

// Version is the version of WeasyPrint-Go.
const Version = "0.1.0"

// URLFetcher fetches resources. It receives an absolute URL and returns
// the resource data, its MIME type (may be empty) and its final URL.
type URLFetcher func(url string) (data []byte, mimeType string, redirectedURL string, err error)

// DefaultURLFetcher fetches file:, data:, http: and https: URLs.
func DefaultURLFetcher(url string) ([]byte, string, string, error) {
	r, err := fetch.Default(url)
	if err != nil {
		return nil, "", "", err
	}
	return r.Data, r.MimeType, r.RedirectURL, nil
}

func (f URLFetcher) internal() fetch.Fetcher {
	if f == nil {
		return fetch.Default
	}
	return func(u string) (*fetch.Result, error) {
		data, mt, red, err := f(u)
		if err != nil {
			return nil, err
		}
		if red == "" {
			red = u
		}
		return &fetch.Result{Data: data, MimeType: mt, RedirectURL: red}, nil
	}
}

// HTML is a parsed HTML document.
type HTML struct {
	root    *html.Node
	BaseURL string
	// MediaType is the media type used for @media rules (default "print").
	MediaType string
	// URLFetcher fetches stylesheets, images and fonts (default fetches
	// local files, data: URLs and HTTP(S)).
	URLFetcher URLFetcher
}

// HTMLFromString parses an HTML string. baseURL is used to resolve relative
// URLs (it may be a path or a URL, or empty).
func HTMLFromString(source, baseURL string) *HTML {
	return newHTML(source, fetch.EnsureURL(baseURL))
}

// HTMLFromBytes parses HTML bytes, decoding them with the given encoding
// (or sniffing it if empty).
func HTMLFromBytes(data []byte, encoding, baseURL string) *HTML {
	return newHTML(html.DecodeBytes(data, encoding), fetch.EnsureURL(baseURL))
}

// HTMLFromReader parses HTML read from r.
func HTMLFromReader(r io.Reader, encoding, baseURL string) (*HTML, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	return HTMLFromBytes(data, encoding, baseURL), nil
}

// HTMLFromFile parses an HTML file.
func HTMLFromFile(path string) (*HTML, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return HTMLFromBytes(data, "", fetch.PathToURL(path)), nil
}

// HTMLFromURL fetches and parses an HTML document.
func HTMLFromURL(url string, fetcher URLFetcher) (*HTML, error) {
	url = fetch.EnsureURL(url)
	res, err := fetcher.internal()(url)
	if err != nil {
		return nil, err
	}
	h := newHTML(html.DecodeBytes(res.Data, res.Encoding), res.RedirectURL)
	h.URLFetcher = fetcher
	return h, nil
}

func newHTML(source, baseURL string) *HTML {
	h := &HTML{root: html.Parse(source), BaseURL: baseURL, MediaType: "print"}
	// <base href>.
	if b := h.root.Find("base"); b != nil {
		if href, ok := b.Attr("href"); ok && strings.TrimSpace(href) != "" {
			h.BaseURL = fetch.Resolve(baseURL, href)
		}
	}
	return h
}

// CSS is a user stylesheet.
type CSS struct {
	source  string
	baseURL string
}

// CSSFromString returns a stylesheet from a string.
func CSSFromString(source, baseURL string) *CSS {
	return &CSS{source: source, baseURL: fetch.EnsureURL(baseURL)}
}

// CSSFromFile reads a stylesheet file.
func CSSFromFile(path string) (*CSS, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return &CSS{source: html.DecodeBytes(data, ""), baseURL: fetch.PathToURL(path)}, nil
}

// CSSFromURL fetches a stylesheet.
func CSSFromURL(url string, fetcher URLFetcher) (*CSS, error) {
	url = fetch.EnsureURL(url)
	res, err := fetcher.internal()(url)
	if err != nil {
		return nil, err
	}
	return &CSS{source: html.DecodeBytes(res.Data, res.Encoding), baseURL: res.RedirectURL}, nil
}

// Options controls rendering.
type Options struct {
	// Stylesheets are additional stylesheets applied after the document's.
	Stylesheets []*CSS
	// PresentationalHints enables HTML presentational hints (align,
	// bgcolor, width attributes...).
	PresentationalHints bool
	// Uncompressed disables PDF stream compression.
	Uncompressed bool
	// Warn receives warnings (unsupported CSS, missing resources...).
	Warn func(string)
	// FontDirs replaces the default system font directories if not nil.
	FontDirs []string
	// DefaultPageSize is the page size in CSS px of pages whose size the
	// document's CSS doesn't set (default A4). See PageSize.
	DefaultPageSize [2]float64
	// Hyphenation selects the built-in hyphenation patterns used by
	// "hyphens: auto".
	Hyphenation Hyphenation
	// HyphenationRules are user-defined hyphenation patterns and
	// exceptions, which replace or extend the built-in ones of their
	// languages.
	HyphenationRules []HyphenationRules
	// PDFOptions are used when writing the document as PDF.
	PDFOptions
}

// Hyphenation selects hyphenation patterns.
type Hyphenation int

const (
	// HyphenationTeX uses TeX's hyphenation patterns, exceptions and
	// minimum fragment lengths (hyph-utf8; for German the experimental
	// patterns of dehyph-exptl, babel's "ngerman-x-latest"), and Pyphen's
	// for languages without them.
	HyphenationTeX Hyphenation = iota
	// HyphenationPyphen uses the Hunspell patterns of Pyphen, like
	// WeasyPrint.
	HyphenationPyphen
)

// HyphenationRules are user-defined hyphenation rules for a language,
// like LaTeX's \patterns and \hyphenation.
type HyphenationRules struct {
	// Lang is the BCP 47 language tag the rules apply to ("de",
	// "de-CH-1901", "en-GB"…); they also apply to more specific tags.
	Lang string
	// Patterns (or the file PatternsFile) replace the built-in patterns
	// of the language: a TeX file with \patterns{…} (and optionally
	// \hyphenation{…}), a Hunspell/LibreOffice .dic file, or a plain list
	// of patterns such as hyph-utf8's .pat.txt files.
	Patterns     []byte
	PatternsFile string
	// Exceptions (or the file ExceptionsFile) are words with their
	// hyphenation points ("as-so-ciate"; a word without hyphens is never
	// hyphenated), added to the patterns' exceptions.
	Exceptions     []byte
	ExceptionsFile string
	// Left and Right, if positive, set the minimum numbers of characters
	// before and after a hyphen used for hyphenate-limit-chars: auto.
	Left, Right int
}

// hyphenationRules loads user-defined hyphenation rules.
func hyphenationRules(hrs []HyphenationRules, src Hyphenation, warn func(string)) (*hyphen.Set, error) {
	if len(hrs) == 0 {
		return nil, nil
	}
	var rules []hyphen.Rules
	for _, hr := range hrs {
		r := hyphen.Rules{Lang: hr.Lang, Patterns: hr.Patterns, Exceptions: hr.Exceptions, Left: hr.Left, Right: hr.Right}
		if hr.PatternsFile != "" {
			data, err := os.ReadFile(hr.PatternsFile)
			if err != nil {
				return nil, err
			}
			r.Patterns = data
		}
		if hr.ExceptionsFile != "" {
			data, err := os.ReadFile(hr.ExceptionsFile)
			if err != nil {
				return nil, err
			}
			r.Exceptions = data
		}
		rules = append(rules, r)
	}
	return hyphen.NewSet(rules, hyphen.Source(src), warn)
}

// PageSize parses a CSS page size such as "A4", "A5 landscape", "letter"
// or "210mm 297mm" (the values of the @page size descriptor) into CSS px,
// for Options.DefaultPageSize.
func PageSize(value string) ([2]float64, error) {
	size, ok := css.ParsePageSize(value)
	if !ok {
		return size, fmt.Errorf("invalid page size %q", value)
	}
	return size, nil
}

// Document is a rendered document.
type Document struct {
	Pages    []*Page
	Metadata Metadata

	pages         []*layout.Page
	shaper        *text.Shaper
	images        *images.Cache
	compress      bool
	fetcher       fetch.Fetcher
	warn          func(string)
	colorProfiles map[string]*draw.ColorProfile
	pdfOptions    PDFOptions
}

// Page is a rendered page.
type Page struct {
	// Width and Height are in CSS pixels (1/96 inch).
	Width, Height float64
}

// Metadata is document metadata extracted from the HTML head.
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
	// Attachments are the files of <link rel=attachment> elements.
	Attachments []Attachment
}

// metadata extracts the metadata like WeasyPrint's get_html_metadata.
func (h *HTML) metadata(warn func(string)) Metadata {
	m := Metadata{}
	seen := map[string]bool{}
	var title, description, generator, created, modified bool
	h.root.Walk(func(n *html.Node) bool {
		if n.Type != html.ElementNode {
			return true
		}
		switch n.Tag {
		case "title":
			if !title {
				title = true
				m.Title = strings.Join(strings.Fields(n.TextContent()), " ")
			}
		case "meta":
			name := strings.ToLower(n.Get("name"))
			content := n.Get("content")
			switch name {
			case "keywords":
				for _, k := range strings.Split(content, ",") {
					k = strings.Trim(k, " \t\n\f\r")
					dup := false
					for _, e := range m.Keywords {
						dup = dup || e == k
					}
					if !dup {
						m.Keywords = append(m.Keywords, k)
					}
				}
			case "author":
				m.Authors = append(m.Authors, content)
			case "description":
				if !description {
					description, m.Description = true, content
				}
			case "generator":
				if !generator {
					generator, m.Generator = true, content
				}
			case "dcterms.created", "dcterms.modified":
				if !draw.ValidW3CDate(content) {
					warn(fmt.Sprintf("Invalid date in <meta name=%q> %q", name, content))
					break
				}
				if name == "dcterms.created" && !created {
					created, m.Created = true, content
				} else if name == "dcterms.modified" && !modified {
					modified, m.Modified = true, content
				}
			case "":
			default:
				if !seen[name] {
					seen[name] = true
					m.Custom = append(m.Custom, [2]string{name, content})
				}
			}
		case "link":
			for _, t := range strings.Fields(strings.ToLower(n.Get("rel"))) {
				if t == "attachment" {
					if href := n.Get("href"); href != "" {
						m.Attachments = append(m.Attachments, Attachment{
							URL: fetch.Resolve(h.BaseURL, href), Description: n.Get("title")})
					} else {
						warn(`Missing href in <link rel="attachment">`)
					}
					break
				}
			}
		}
		return true
	})
	if r := h.root.Root(); r != nil {
		m.Lang = r.Get("lang")
	}
	return m
}

func (h *HTML) documentStylesheets(env *css.Env, warn func(string)) []*css.Stylesheet {
	var sheets []*css.Stylesheet
	f := env.Fetcher
	h.root.Walk(func(n *html.Node) bool {
		if n.Type != html.ElementNode {
			return true
		}
		switch n.Tag {
		case "style":
			if media, ok := n.Attr("media"); ok && !css.EvaluateMediaQuery(css.Tokenize(media), env) {
				return false
			}
			if t := strings.ToLower(n.Get("type")); t != "" && t != "text/css" {
				return false
			}
			sheets = append(sheets, css.ParseStylesheet(n.TextContent(), h.BaseURL, css.OriginAuthor, env))
			return false
		case "link":
			rels := strings.Fields(strings.ToLower(n.Get("rel")))
			isSheet := false
			for _, r := range rels {
				if r == "stylesheet" {
					isSheet = true
				}
				if r == "alternate" {
					isSheet = false
					break
				}
			}
			href := strings.TrimSpace(n.Get("href"))
			if !isSheet || href == "" {
				return false
			}
			if media, ok := n.Attr("media"); ok && !css.EvaluateMediaQuery(css.Tokenize(media), env) {
				return false
			}
			u := fetch.Resolve(h.BaseURL, href)
			res, err := f(u)
			if err != nil {
				warn("Failed to load stylesheet at " + u + ": " + err.Error())
				return false
			}
			base := res.RedirectURL
			if base == "" {
				base = u
			}
			sheets = append(sheets, css.ParseStylesheet(html.DecodeBytes(res.Data, res.Encoding), base, css.OriginAuthor, env))
			return false
		case "svg", "template":
			return false
		}
		return true
	})
	return sheets
}

// Render lays out the document into pages.
func (h *HTML) Render(opts *Options) (doc *Document, err error) {
	defer func() {
		if r := recover(); r != nil {
			if os.Getenv("WEASYPRINT_GO_DEBUG") != "" {
				panic(r)
			}
			doc, err = nil, fmt.Errorf("weasyprint: internal error: %v", r)
		}
	}()
	if opts == nil {
		opts = &Options{}
	}
	warn := opts.Warn
	if warn == nil {
		warn = func(string) {}
	}
	fetcher := h.URLFetcher.internal()
	media := h.MediaType
	if media == "" {
		media = "print"
	}
	env := &css.Env{Fetcher: fetcher, MediaType: media, Warn: warn}
	ua := css.ParseStylesheet(css.UserAgentCSS(), "", css.OriginUA, env)
	sheets := []*css.Stylesheet{ua}
	if opts.Forms {
		sheets = append(sheets, css.ParseStylesheet(css.FormsCSS(), "", css.OriginUA, env))
	}
	if opts.PresentationalHints {
		ph := css.ParseStylesheet(css.PresentationalHintsCSS(), "", css.OriginAuthor, env)
		for _, r := range ph.Rules {
			for _, s := range r.Selectors {
				s.Specificity = css.Specificity{}
			}
			r.Order = -1000000 + r.Order
		}
		sheets = append(sheets, ph)
	}
	sheets = append(sheets, h.documentStylesheets(env, warn)...)
	for _, s := range opts.Stylesheets {
		sheets = append(sheets, css.ParseStylesheet(s.source, s.baseURL, css.OriginAuthor, env))
	}

	lib := fonts.NewLibrary()
	lib.Warn = warn
	if opts.FontDirs != nil {
		lib.Dirs = opts.FontDirs
		lib.CustomDirs = true
	}
	resolver := css.NewResolver(h.root, sheets, h.BaseURL)
	if opts.PresentationalHints {
		resolver.Hints = presentationalHints
	}
	loadFontFaces(lib, resolver.FontFaces, fetcher, warn)
	shaper := text.NewShaper(lib)
	imgs := images.NewCache(fetcher)
	imgs.Warn = warn
	defaultSize := layout.A4
	if opts.DefaultPageSize[0] > 0 && opts.DefaultPageSize[1] > 0 {
		defaultSize = opts.DefaultPageSize
	}

	resolver.Viewport = defaultSize
	resolver.ResolveAll()
	hyphens, err := hyphenationRules(opts.HyphenationRules, opts.Hyphenation, warn)
	if err != nil {
		return nil, err
	}
	builder := layout.NewBuilder(resolver, imgs, h.BaseURL)
	builder.Warn = warn
	root := builder.Build(h.root)
	ctx := layout.NewContext(shaper, resolver, imgs, builder)
	ctx.Warn = warn
	ctx.DefaultPageSize = defaultSize
	ctx.Hyphenation = hyphen.Source(opts.Hyphenation)
	ctx.Hyphens = hyphens
	// Viewport units are relative to the first page size.
	if w, hh := ctx.FirstPageSize(root); w != resolver.Viewport[0] || hh != resolver.Viewport[1] {
		resolver.Viewport = [2]float64{w, hh}
		resolver.ResolveAll()
		root = builder.Build(h.root)
		ctx = layout.NewContext(shaper, resolver, imgs, builder)
		ctx.Warn = warn
		ctx.DefaultPageSize = defaultSize
		ctx.Hyphenation = hyphen.Source(opts.Hyphenation)
		ctx.Hyphens = hyphens
	}
	pages := ctx.LayoutDocument(root)
	if builder.UsesTargets {
		// Second pass with the page numbers of anchors.
		builder.TargetValues = targetValues(pages, builder)
		root = builder.Build(h.root)
		ctx = layout.NewContext(shaper, resolver, imgs, builder)
		ctx.Warn = warn
		ctx.DefaultPageSize = defaultSize
		ctx.Hyphenation = hyphen.Source(opts.Hyphenation)
		ctx.Hyphens = hyphens
		pages = ctx.LayoutDocument(root)
	}
	if os.Getenv("WEASYPRINT_GO_DEBUG") != "" {
		for i, p := range pages {
			fmt.Fprintf(os.Stderr, "=== page %d (%.1fx%.1f)\n", i+1, p.Width, p.Height)
			layout.Dump(os.Stderr, p.Box, 0)
		}
	}
	doc = &Document{Metadata: h.metadata(warn), pages: pages, shaper: shaper, images: imgs, compress: !opts.Uncompressed,
		fetcher: fetcher, warn: warn, pdfOptions: opts.PDFOptions,
		colorProfiles: loadColorProfiles(resolver.ColorProfiles, fetcher, warn)}
	for _, p := range pages {
		doc.Pages = append(doc.Pages, &Page{Width: p.Width, Height: p.Height})
	}
	return doc, nil
}

func targetValues(pages []*layout.Page, b *layout.Builder) map[string]map[string][]int {
	out := map[string]map[string][]int{}
	for anchor, counters := range b.AnchorCounters {
		out[anchor] = counters
	}
	for _, p := range pages {
		var walk func(x *layout.Box)
		walk = func(x *layout.Box) {
			if x.Anchor != "" && !x.NoStartDecor {
				m := out[x.Anchor]
				if m == nil {
					m = map[string][]int{}
					out[x.Anchor] = m
				}
				if _, done := m["\x00page"]; !done {
					m["\x00page"] = []int{1}
					for name, v := range p.Counters {
						m[name] = append([]int(nil), v...)
					}
				}
			}
			for _, ch := range x.Children {
				walk(ch)
			}
		}
		for _, ch := range p.Box.Children {
			walk(ch)
		}
	}
	return out
}

func loadFontFaces(lib *fonts.Library, faces []*css.FontFaceRule, f fetch.Fetcher, warn func(string)) {
	for _, ff := range faces {
		var font *fonts.Font
		for _, src := range ff.Sources {
			if src.Local != "" {
				if fnt := lib.FindLocal(src.Local); fnt != nil {
					font = fnt
					break
				}
				continue
			}
			res, err := f(src.URL)
			if err != nil {
				warn("Failed to load font at " + src.URL + ": " + err.Error())
				continue
			}
			fnt, err := fonts.Parse(res.Data, 0)
			if err != nil {
				warn("Failed to load font at " + src.URL + ": " + err.Error())
				continue
			}
			font = fnt
			break
		}
		if font == nil {
			warn(fmt.Sprintf("Font-face %q cannot be loaded", ff.Family))
			continue
		}
		var feats []fonts.FeatureSetting
		for _, f := range ff.Features {
			feats = append(feats, fonts.FeatureSetting{Tag: f.Tag, Value: f.Value})
		}
		lib.AddFontFace(ff.Family, font, ff.Weight, ff.Style, ff.Stretch, ff.UnicodeRange, feats)
	}
}

// WritePDF writes the document as PDF.
func (d *Document) WritePDF(w io.Writer) (err error) {
	defer func() {
		if r := recover(); r != nil {
			if os.Getenv("WEASYPRINT_GO_DEBUG") != "" {
				panic(r)
			}
			err = fmt.Errorf("weasyprint: internal error while drawing: %v", r)
		}
	}()
	return d.WritePDFWithOptions(w, d.pdfOptions)
}

// WritePDFWithOptions writes the document as PDF with the given options
// instead of those given to Render.
func (d *Document) WritePDFWithOptions(w io.Writer, o PDFOptions) (err error) {
	defer func() {
		if r := recover(); r != nil {
			if os.Getenv("WEASYPRINT_GO_DEBUG") != "" {
				panic(r)
			}
			err = fmt.Errorf("weasyprint: internal error while drawing: %v", r)
		}
	}()
	do, err := d.pdfDrawOptions(&o)
	if err != nil {
		return err
	}
	return draw.Write(w, d.pages, d.shaper, d.images, do)
}

// PDF returns the document as PDF bytes.
func (d *Document) PDF() ([]byte, error) {
	var b bytes.Buffer
	err := d.WritePDF(&b)
	return b.Bytes(), err
}

// WritePDF renders the document and writes it as PDF.
func (h *HTML) WritePDF(w io.Writer, opts *Options) error {
	doc, err := h.Render(opts)
	if err != nil {
		return err
	}
	return doc.WritePDF(w)
}

// WritePDFFile renders the document to a PDF file.
func (h *HTML) WritePDFFile(path string, opts *Options) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := h.WritePDF(f, opts); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// PDF renders the document and returns PDF bytes.
func (h *HTML) PDF(opts *Options) ([]byte, error) {
	var b bytes.Buffer
	err := h.WritePDF(&b, opts)
	return b.Bytes(), err
}

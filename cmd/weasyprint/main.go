// Command weasyprint converts HTML documents to PDF.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"runtime"
	"runtime/pprof"
	"strings"
	"time"

	weasyprint "github.com/esoteric-programmer/weasyprint-go"
)

type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }

// cli holds the flag set and the names of the flags taking a value, used
// to allow flags after the positional arguments.
type cli struct {
	fs     *flag.FlagSet
	valued map[string]bool
}

func (c *cli) str(short, long, def, usage string) *string {
	p := new(string)
	*p = def
	c.fs.StringVar(p, long, def, usage)
	c.valued[long] = true
	if short != "" {
		c.fs.StringVar(p, short, def, usage)
		c.valued[short] = true
	}
	return p
}

func (c *cli) boolean(short, long, usage string) *bool {
	p := new(bool)
	c.fs.BoolVar(p, long, false, usage)
	if short != "" {
		c.fs.BoolVar(p, short, false, usage)
	}
	return p
}

func (c *cli) multi(short, long, usage string) *multiFlag {
	p := new(multiFlag)
	c.fs.Var(p, long, usage)
	c.valued[long] = true
	if short != "" {
		c.fs.Var(p, short, usage)
		c.valued[short] = true
	}
	return p
}

func main() {
	c := &cli{fs: flag.NewFlagSet("weasyprint", flag.ExitOnError), valued: map[string]bool{}}
	fs := c.fs
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: weasyprint [options] <input> <output>\n\n")
		fmt.Fprintf(os.Stderr, "Render HTML (file path, URL or \"-\" for stdin) to PDF (path or \"-\" for stdout).\n\nOptions:\n")
		fs.PrintDefaults()
	}
	info := c.boolean("i", "info", "print system information and exit")
	version := c.boolean("", "version", "print the version number and exit")
	// Rendering.
	stylesheets := c.multi("s", "stylesheet", "URL or path of a user CSS stylesheet (may be repeated)")
	attachments := c.multi("a", "attachment", "URL or path of a file to attach to the PDF (may be repeated)")
	relationships := c.multi("", "attachment-relationship", "relationship of the attachment with the same position\n(Source, Data, Alternative, Supplement, Unspecified...; may be repeated)")
	pdfIdentifier := c.str("", "pdf-identifier", "", "PDF file identifier")
	pdfVariant := c.str("", "pdf-variant", "", "PDF variant to generate: "+strings.Join(weasyprint.PDFVariants(), ", "))
	pdfVersion := c.str("", "pdf-version", "", "PDF version number")
	pdfForms := c.boolean("", "pdf-forms", "include PDF forms")
	pdfTags := c.boolean("", "pdf-tags", "tag PDF for accessibility")
	uncompressed := c.boolean("", "uncompressed-pdf", "do not compress PDF content, mainly for debugging")
	xmpMetadata := c.multi("", "xmp-metadata", "URL or path of an XML file to include in the XMP metadata (may be repeated)")
	customMetadata := c.boolean("", "custom-metadata", "include custom HTML meta tags in PDF metadata")
	outputIntent := c.str("", "output-intent", "", "srgb, device-cmyk or the name of an @color-profile rule used as output intent")
	hints := c.boolean("p", "presentational-hints", "follow HTML presentational hints")
	optimizeImages := c.boolean("", "optimize-images", "optimize size of embedded images with no quality loss")
	jpegQuality := c.str("j", "jpeg-quality", "", "JPEG quality between 0 (worst) to 95 (best)")
	dpi := c.str("D", "dpi", "", "set maximum resolution of images embedded in the PDF")
	fullFonts := c.boolean("", "full-fonts", "embed unmodified font files when possible")
	hinting := c.boolean("", "hinting", "keep hinting information in embedded fonts")
	cache := c.str("c", "cache-folder", "", "store remote resources in a folder to reuse them in later renderings")
	hyphenation := c.str("", "hyphenation", "tex", "built-in hyphenation patterns: tex (LaTeX's, the default)\nor pyphen (the Hunspell patterns used by WeasyPrint)")
	hyphPatterns := c.multi("", "hyphenation-patterns", "LANG=FILE: hyphenation patterns added to the built-in ones of a language\n(TeX \\patterns file, Hunspell .dic or pattern list; may be repeated:\nlater files win)")
	hyphReplace := c.multi("", "hyphenation-replace", "LANG: user patterns replace the built-in patterns and exceptions\nof a language instead of extending them (may be repeated)")
	hyphExceptions := c.multi("", "hyphenation-exceptions", "LANG=FILE: hyphenation exceptions (\"as-so-ciate\") added for a language\n(may be repeated: later files win)")
	hyphMins := c.multi("", "hyphenation-min", "LANG=LEFT,RIGHT: minimum characters before and after a hyphen\nfor a language (may be repeated)")
	pageSize := c.str("", "page-size", "", "page size of all pages, overriding the document's CSS\n(\"A4\", \"A5 landscape\", \"letter\", \"210mm 297mm\"...)")
	// Input.
	encoding := c.str("e", "encoding", "", "character encoding of the input")
	media := c.str("m", "media-type", "print", "media type to use for @media")
	baseURL := c.str("u", "base-url", "", "base for relative URLs in the HTML input")
	timeout := c.str("t", "timeout", "", "set timeout in seconds for HTTP requests")
	allowed := c.str("", "allowed-protocols", "", "only authorize comma-separated list of protocols for fetching URLs")
	noRedirects := c.boolean("", "no-http-redirects", "do not follow HTTP redirects")
	failHTTP := c.boolean("", "fail-on-http-errors", "abort document rendering on any HTTP error")
	// Logging.
	verbose := c.boolean("v", "verbose", "show warnings and information messages")
	debug := c.boolean("d", "debug", "show debugging messages")
	quiet := c.boolean("q", "quiet", "hide logging messages")

	// Reorder so that flags may follow positional arguments.
	var flags, positional []string
	args := os.Args[1:]
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "-" || !strings.HasPrefix(a, "-") {
			positional = append(positional, a)
			continue
		}
		if a == "--" {
			positional = append(positional, args[i+1:]...)
			break
		}
		flags = append(flags, a)
		name := strings.TrimLeft(a, "-")
		if !strings.Contains(name, "=") && c.valued[name] && i+1 < len(args) {
			flags = append(flags, args[i+1])
			i++
		}
	}
	fs.Parse(flags)
	if *version {
		fmt.Println("WeasyPrint-Go version", weasyprint.Version)
		return
	}
	if *info {
		fmt.Println("System:", runtime.GOOS, runtime.GOARCH)
		fmt.Println("Go version:", runtime.Version())
		fmt.Println("WeasyPrint-Go version:", weasyprint.Version)
		fmt.Println("PDF variants:", strings.Join(weasyprint.PDFVariants(), ", "))
		return
	}
	positional = append(positional, fs.Args()...)
	if len(positional) != 2 {
		fs.Usage()
		os.Exit(2)
	}
	input, output := positional[0], positional[1]
	if prof := os.Getenv("WEASYPRINT_GO_CPUPROFILE"); prof != "" {
		if f, err := os.Create(prof); err == nil {
			pprof.StartCPUProfile(f)
			defer pprof.StopCPUProfile()
		}
	}
	fail := func(code int, err any) {
		fmt.Fprintln(os.Stderr, "weasyprint:", err)
		pprof.StopCPUProfile()
		os.Exit(code)
	}

	// URL fetcher.
	fo := weasyprint.FetcherOptions{NoHTTPRedirects: *noRedirects, FailOnHTTPErrors: *failHTTP, CacheDir: *cache}
	if *timeout != "" {
		var secs float64
		if _, err := fmt.Sscan(*timeout, &secs); err != nil {
			fail(2, "invalid timeout "+*timeout)
		}
		fo.Timeout = time.Duration(secs * float64(time.Second))
	}
	if *allowed != "" {
		for _, p := range strings.Split(*allowed, ",") {
			if p = strings.TrimSpace(strings.ToLower(p)); p != "" {
				fo.AllowedProtocols = append(fo.AllowedProtocols, p)
			}
		}
	}
	fetcher, httpErr := weasyprint.NewURLFetcher(fo)

	var doc *weasyprint.HTML
	var err error
	switch {
	case input == "-":
		doc, err = weasyprint.HTMLFromReader(os.Stdin, *encoding, *baseURL)
	case strings.Contains(input, "://"):
		doc, err = weasyprint.HTMLFromURL(input, fetcher)
	default:
		var data []byte
		data, err = os.ReadFile(input)
		if err == nil {
			base := *baseURL
			if base == "" {
				base = input
			}
			doc = weasyprint.HTMLFromBytes(data, *encoding, base)
		}
	}
	if err != nil {
		fail(1, err)
	}
	if *baseURL != "" && input != "-" && !strings.Contains(input, "://") {
		doc.BaseURL = *baseURL
	}
	doc.MediaType = *media
	doc.URLFetcher = fetcher
	opts := &weasyprint.Options{PresentationalHints: *hints, Uncompressed: *uncompressed}
	for _, s := range *stylesheets {
		var sheet *weasyprint.CSS
		if strings.Contains(s, "://") {
			sheet, err = weasyprint.CSSFromURL(s, fetcher)
		} else {
			sheet, err = weasyprint.CSSFromFile(s)
		}
		if err != nil {
			fail(1, err)
		}
		opts.Stylesheets = append(opts.Stylesheets, sheet)
	}
	if *pageSize != "" {
		if _, err := weasyprint.PageSize(*pageSize); err != nil {
			fail(2, err)
		}
		// Like a user stylesheet setting the size of all pages.
		opts.Stylesheets = append(opts.Stylesheets,
			weasyprint.CSSFromString("@page { size: "+*pageSize+" !important }", ""))
	}
	switch strings.ToLower(*hyphenation) {
	case "tex":
	case "pyphen":
		opts.Hyphenation = weasyprint.HyphenationPyphen
	default:
		fail(2, fmt.Sprintf("invalid --hyphenation %q (choose from tex, pyphen)", *hyphenation))
	}
	// Each file is a layer of its own, in the order given (patterns, then
	// exceptions); --hyphenation-replace and --hyphenation-min go to the
	// first layer of the language.
	rule := func(lang string) *weasyprint.HyphenationRules {
		for i := range opts.HyphenationRules {
			if strings.EqualFold(opts.HyphenationRules[i].Lang, lang) {
				return &opts.HyphenationRules[i]
			}
		}
		opts.HyphenationRules = append(opts.HyphenationRules, weasyprint.HyphenationRules{Lang: lang})
		return &opts.HyphenationRules[len(opts.HyphenationRules)-1]
	}
	layer := func(lang string) *weasyprint.HyphenationRules {
		first := rule(lang)
		if first.PatternsFile == "" && first.ExceptionsFile == "" {
			return first
		}
		opts.HyphenationRules = append(opts.HyphenationRules, weasyprint.HyphenationRules{Lang: lang})
		return &opts.HyphenationRules[len(opts.HyphenationRules)-1]
	}
	langValue := func(flag, v string) (string, string) {
		i := strings.Index(v, "=")
		if i <= 0 {
			fail(2, fmt.Sprintf("invalid --%s %q (expected LANG=VALUE)", flag, v))
		}
		return v[:i], v[i+1:]
	}
	for _, lang := range *hyphReplace {
		rule(lang).Replace = true
	}
	for _, v := range *hyphPatterns {
		lang, file := langValue("hyphenation-patterns", v)
		layer(lang).PatternsFile = file
	}
	for _, v := range *hyphExceptions {
		lang, file := langValue("hyphenation-exceptions", v)
		layer(lang).ExceptionsFile = file
	}
	for _, v := range *hyphMins {
		lang, mins := langValue("hyphenation-min", v)
		var l, r int
		if _, err := fmt.Sscanf(mins, "%d,%d", &l, &r); err != nil || l < 1 || r < 1 {
			fail(2, fmt.Sprintf("invalid --hyphenation-min %q (expected LANG=LEFT,RIGHT)", v))
		}
		hr := rule(lang)
		hr.Left, hr.Right = l, r
	}
	// PDF options.
	po := &opts.PDFOptions
	po.Variant, po.Version, po.Identifier = *pdfVariant, *pdfVersion, *pdfIdentifier
	po.Forms, po.Tags, po.CustomMetadata = *pdfForms, *pdfTags, *customMetadata
	po.XMPMetadata, po.OutputIntent = *xmpMetadata, *outputIntent
	po.OptimizeImages, po.FullFonts, po.Hinting = *optimizeImages, *fullFonts, *hinting
	if *jpegQuality != "" {
		if _, err := fmt.Sscan(*jpegQuality, &po.JPEGQuality); err != nil {
			fail(2, "invalid JPEG quality "+*jpegQuality)
		}
	}
	if *dpi != "" {
		if _, err := fmt.Sscan(*dpi, &po.DPI); err != nil {
			fail(2, "invalid DPI "+*dpi)
		}
	}
	for i, a := range *attachments {
		att := weasyprint.Attachment{URL: a}
		if i < len(*relationships) {
			att.Relationship = (*relationships)[i]
		}
		po.Attachments = append(po.Attachments, att)
	}
	if po.Variant != "" {
		known := false
		for _, v := range weasyprint.PDFVariants() {
			known = known || v == strings.ToLower(po.Variant)
		}
		if !known {
			fail(2, fmt.Sprintf("invalid --pdf-variant %q (choose from %s)", po.Variant, strings.Join(weasyprint.PDFVariants(), ", ")))
		}
	}
	switch {
	case *quiet:
	case *verbose || *debug:
		opts.Warn = func(msg string) { fmt.Fprintln(os.Stderr, "WARNING:", msg) }
	default:
		seen := map[string]bool{}
		opts.Warn = func(msg string) {
			if (strings.HasPrefix(msg, "Failed") || strings.HasPrefix(msg, "Missing required") ||
				strings.Contains(msg, "no required alt")) && !seen[msg] {
				seen[msg] = true
				fmt.Fprintln(os.Stderr, "ERROR:", msg)
			}
		}
	}
	start := time.Now()
	rendered, err := doc.Render(opts)
	if err == nil {
		err = httpErr()
	}
	if err != nil {
		fail(1, err)
	}
	if *debug {
		fmt.Fprintf(os.Stderr, "DEBUG: rendered %d pages in %v\n", len(rendered.Pages), time.Since(start))
	}
	var w io.Writer
	if output == "-" {
		w = os.Stdout
	} else {
		f, err := os.Create(output)
		if err != nil {
			fail(1, err)
		}
		defer f.Close()
		w = f
	}
	if err := rendered.WritePDF(w); err != nil {
		fail(1, err)
	}
	if err := httpErr(); err != nil {
		fail(1, err)
	}
	if *debug {
		fmt.Fprintf(os.Stderr, "DEBUG: done in %v\n", time.Since(start))
	}
}

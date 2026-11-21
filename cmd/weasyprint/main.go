// Command weasyprint converts HTML documents to PDF.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"runtime/pprof"
	"strings"

	weasyprint "github.com/esoteric-programmer/weasyprint-go"
)

type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }

func main() {
	var stylesheets multiFlag
	fs := flag.NewFlagSet("weasyprint", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: weasyprint [options] <input> <output>\n\n")
		fmt.Fprintf(os.Stderr, "Render HTML (file path, URL or \"-\" for stdin) to PDF (path or \"-\" for stdout).\n\nOptions:\n")
		fs.PrintDefaults()
	}
	encoding := fs.String("e", "", "character encoding of the input")
	fs.StringVar(encoding, "encoding", "", "character encoding of the input")
	fs.Var(&stylesheets, "s", "URL or path of a user CSS stylesheet (may be repeated)")
	fs.Var(&stylesheets, "stylesheet", "URL or path of a user CSS stylesheet (may be repeated)")
	media := fs.String("m", "print", "media type to use for @media")
	fs.StringVar(media, "media-type", "print", "media type to use for @media")
	baseURL := fs.String("u", "", "base for relative URLs in the HTML input")
	fs.StringVar(baseURL, "base-url", "", "base for relative URLs in the HTML input")
	hints := fs.Bool("p", false, "follow HTML presentational hints")
	fs.BoolVar(hints, "presentational-hints", false, "follow HTML presentational hints")
	pageSize := fs.String("page-size", "", "page size of all pages, overriding the document's CSS\n(\"A4\", \"A5 landscape\", \"letter\", \"210mm 297mm\"...)")
	uncompressed := fs.Bool("uncompressed-pdf", false, "do not compress PDF content streams")
	verbose := fs.Bool("v", false, "show warnings and information messages")
	fs.BoolVar(verbose, "verbose", false, "show warnings and information messages")
	quiet := fs.Bool("q", false, "hide logging messages")
	fs.BoolVar(quiet, "quiet", false, "hide logging messages")
	version := fs.Bool("version", false, "print the version number and exit")
	// Reorder so that flags may follow positional arguments.
	var flags, positional []string
	args := os.Args[1:]
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "-" || !strings.HasPrefix(a, "-") {
			positional = append(positional, a)
			continue
		}
		flags = append(flags, a)
		name := strings.TrimLeft(a, "-")
		if !strings.Contains(name, "=") {
			switch name {
			case "e", "encoding", "s", "stylesheet", "m", "media-type", "u", "base-url", "page-size":
				if i+1 < len(args) {
					flags = append(flags, args[i+1])
					i++
				}
			}
		}
	}
	fs.Parse(flags)
	if *version {
		fmt.Println("WeasyPrint-Go version", weasyprint.Version)
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

	var doc *weasyprint.HTML
	var err error
	switch {
	case input == "-":
		doc, err = weasyprint.HTMLFromReader(os.Stdin, *encoding, *baseURL)
	case strings.Contains(input, "://"):
		doc, err = weasyprint.HTMLFromURL(input, nil)
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
		fmt.Fprintln(os.Stderr, "weasyprint:", err)
		os.Exit(1)
	}
	if *baseURL != "" && input != "-" && !strings.Contains(input, "://") {
		doc.BaseURL = *baseURL
	}
	doc.MediaType = *media
	opts := &weasyprint.Options{PresentationalHints: *hints, Uncompressed: *uncompressed}
	for _, s := range stylesheets {
		var sheet *weasyprint.CSS
		if strings.Contains(s, "://") {
			sheet, err = weasyprint.CSSFromURL(s, nil)
		} else {
			sheet, err = weasyprint.CSSFromFile(s)
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, "weasyprint:", err)
			os.Exit(1)
		}
		opts.Stylesheets = append(opts.Stylesheets, sheet)
	}
	if *pageSize != "" {
		if _, err := weasyprint.PageSize(*pageSize); err != nil {
			fmt.Fprintln(os.Stderr, "weasyprint:", err)
			os.Exit(2)
		}
		// Like a user stylesheet setting the size of all pages.
		opts.Stylesheets = append(opts.Stylesheets,
			weasyprint.CSSFromString("@page { size: "+*pageSize+" !important }", ""))
	}
	if *verbose && !*quiet {
		opts.Warn = func(msg string) { fmt.Fprintln(os.Stderr, "WARNING:", msg) }
	} else if !*quiet {
		seen := map[string]bool{}
		opts.Warn = func(msg string) {
			if strings.HasPrefix(msg, "Failed") && !seen[msg] {
				seen[msg] = true
				fmt.Fprintln(os.Stderr, "ERROR:", msg)
			}
		}
	}
	var w io.Writer
	if output == "-" {
		w = os.Stdout
	} else {
		f, err := os.Create(output)
		if err != nil {
			fmt.Fprintln(os.Stderr, "weasyprint:", err)
			os.Exit(1)
		}
		defer f.Close()
		w = f
	}
	if err := doc.WritePDF(w, opts); err != nil {
		fmt.Fprintln(os.Stderr, "weasyprint:", err)
		os.Exit(1)
	}
}

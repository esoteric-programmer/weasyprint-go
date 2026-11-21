# WeasyPrint-Go

A Go reimplementation of [WeasyPrint](https://weasyprint.org/): a visual
rendering engine for HTML and CSS that turns web documents into
print-quality PDF files.

* **Pure Go, zero dependencies.** Only the standard library is used: HTML
  parsing, the CSS engine, OpenType font parsing and subsetting, text
  layout, image decoding and PDF generation are all implemented here.
  `go build` works anywhere without cgo, Pango, Cairo or Python.
* **Same algorithms as WeasyPrint.** Box generation, margin collapsing,
  line strut / `vertical-align` handling, table and page layout follow
  WeasyPrint's implementation, and the default user-agent stylesheet is
  WeasyPrint's own. Output is validated by rendering a corpus of
  documents with both engines and comparing word positions and pixels.
* **Fast.** A 79-page book renders in about 0.3 s.

## Install

```sh
go install github.com/esoteric-programmer/weasyprint-go/cmd/weasyprint@latest
```

## Command line

```sh
weasyprint input.html output.pdf
weasyprint https://example.com/ page.pdf
weasyprint -s extra.css -p report.html report.pdf
weasyprint --page-size "A5 landscape" flyer.html flyer.pdf
cat doc.html | weasyprint -u ./assets/ - - > doc.pdf
```

Options (mirroring WeasyPrint's CLI):

| Option | Meaning |
| --- | --- |
| `-s`, `--stylesheet FILE` | Apply a user stylesheet (repeatable) |
| `-m`, `--media-type TYPE` | Media type for `@media` (default `print`) |
| `-u`, `--base-url URL` | Base for relative URLs |
| `-e`, `--encoding ENC` | Input character encoding |
| `-p`, `--presentational-hints` | Follow HTML presentational hints (`align`, `bgcolor`, `width`…) |
| `--page-size SIZE` | Page size of all pages, overriding the document's CSS: a CSS `size` value such as `A4`, `A5 landscape`, `letter`, `210mm 297mm` (not in WeasyPrint, where `-s` with `@page { size: … }` does the same) |
| `--uncompressed-pdf` | Don't compress PDF streams |
| `-v` / `-q` | Verbose warnings / quiet |
| `--version` | Print the version |

## Library

```go
import weasyprint "github.com/esoteric-programmer/weasyprint-go"

doc, err := weasyprint.HTMLFromFile("invoice.html")
if err != nil {
	log.Fatal(err)
}
err = doc.WritePDFFile("invoice.pdf", &weasyprint.Options{
	Stylesheets: []*weasyprint.CSS{
		weasyprint.CSSFromString(`@page { size: Letter; margin: 2cm }`, ""),
	},
})
```

Other entry points: `HTMLFromString(source, baseURL)`, `HTMLFromBytes`,
`HTMLFromReader`, `HTMLFromURL`, `CSSFromFile`, `CSSFromURL`.
`Options.DefaultPageSize` sets the size of pages whose size the document
doesn't set (A4 otherwise); `weasyprint.PageSize("A5 landscape")` converts
a CSS page size for it.
`HTML.Render(opts)` returns a `*Document` with page sizes and metadata,
which can then be written with `Document.WritePDF(w)`. A custom
`URLFetcher` can be set on `HTML` to control how stylesheets, images and
fonts are loaded.

## Paper size

The page size comes from CSS, as in WeasyPrint: `@page { size: A4 }`
(the default), `@page { size: letter landscape }`, `@page { size: 210mm
297mm }`, or different sizes for `:first`, `:left`/`:right` and named
pages. The named sizes are WeasyPrint's: A0–A10, B0–B10, C0–C10,
JIS-B0–JIS-B10, letter, legal and ledger, each optionally `portrait` or
`landscape`. Without touching the document, use `--page-size` (or a
stylesheet with `-s`), or `Options.DefaultPageSize` in the library.

## Performance

Measured on 39 test documents (1 to 79 pages, text, tables, columns,
flex, footnotes, SVG, many scripts and fonts) against WeasyPrint 70.0 on
the same machine:

| | WeasyPrint | WeasyPrint-Go | Speed-up (median) |
| --- | --- | --- | --- |
| CLI, one process per document | 47.3 s | 4.5 s | 11× |
| Library, warm process | 12.4 s | 1.7 s | 6× |
| 79-page book (CLI) | 2.9 s | 0.44 s | 6.5× |
| Peak memory, 79-page book | 87 MB | 74 MB | |

The system font scan, the fontconfig configuration and parsed fonts are
cached per process (and refreshed when font directories change), so
rendering many documents from one program only pays for them once.
Rendering is safe from several goroutines.

## Supported features

**HTML**: tolerant HTML5-style parsing (implied tags, tables, entities,
encodings), `<base>`, `<style>` and `<link rel=stylesheet>` with media
queries, `<img>`, `<object>`, `<embed>`, inline `<svg>`, metadata from
`<title>`/`<meta>` (title, author, description, keywords, generator,
dates, language), presentational hints (opt-in).

**CSS syntax and cascade**: full CSS Syntax 3 tokenizer, selectors level 4
(combinators, attribute selectors with `i`/`s`, structural pseudo-classes,
`:nth-child(An+B of S)`, `:not()`, `:is()`, `:where()`, `:has()`,
`:lang()`), `::before`, `::after`, `::marker`, `!important`, origins and
specificity, custom properties and `var()`, `calc()`/`min()`/`max()`/
`clamp()`, `@media`, `@import`, `@supports`, `@layer` (flattened),
`@font-face`, `@counter-style`, `@page`.

**Values**: all absolute and font-relative units, viewport units, colors
(named, hex, `rgb()`, `hsl()`, `hwb()`, `currentColor`, system colors),
linear/radial/repeating gradients, `attr()`, counters and
`counter()`/`counters()` with all predefined counter styles.

**Layout**
* Block layout with margin collapsing, `box-sizing`, min/max sizes,
  `overflow` clipping.
* Inline layout: line breaking (UAX #14 style), bidirectional text
  (UAX #9, `direction`, `dir`), Arabic shaping (joining forms, lam-alef),
  `white-space`,
  `text-align` including `justify`, `text-indent`, `letter-spacing`,
  `word-spacing`, `text-transform`, soft hyphens, automatic hyphenation
  (`hyphens: auto` with the `lang` attribute, `hyphenate-character`,
  `hyphenate-limit-chars`, `hyphenate-limit-zone`; dictionaries for about
  50 languages are built in), `overflow-wrap`,
  `word-break`, `tab-size`, all `vertical-align` values, inline blocks,
  kerning, standard ligatures, small caps (`smcp`/`c2sc` or emulated).
* Floats and `clear`; relative, absolute and fixed positioning
  (fixed boxes repeat on every page); `z-index` stacking.
* Lists with inside and outside markers.
* Tables: automatic and fixed layout, `colspan`/`rowspan`, separated and
  collapsed borders, captions, `border-spacing`, repeated
  `thead`/`tfoot` on every page.
* Flexbox (direction, wrap, grow/shrink/basis, alignment, gaps, order).
* Grid (template rows/columns with `fr`, `minmax()`, `repeat()`,
  `auto-fill`, named areas, line placement and spans, auto-placement,
  gaps, alignment).
* Multi-column layout with balancing and column rules.
* `transform` (2D) and `opacity`.

**Paged media**: `@page` with `size` (named sizes, orientation),
margins, `:first`/`:left`/`:right`/`:blank`/`:nth()` and named pages
(`page` property), the 16 page-margin boxes, `counter(page)` and
`counter(pages)`, `break-before/after/inside` (incl. `left`/`right`/
`recto`/`verso` with blank pages), `orphans`/`widows`, `string-set` and
`string()`, running elements (`position: running()` / `element()`),
`leader()`, `target-counter()`, `target-counters()`, `target-text()`,
footnotes (`float: footnote`, `::footnote-call`, `::footnote-marker`,
the `footnote` counter, the `@footnote` area, `footnote-display`,
`footnote-policy`).

**PDF output**: embedded and subset fonts (TrueType and CFF, including
CID-keyed CJK fonts) with `ToUnicode` maps for text extraction, JPEG
pass-through, PNG/GIF with alpha, internal and external links,
named destinations, bookmarks (`bookmark-level`, `bookmark-label`,
`bookmark-state`), document information dictionary.

**Images**: PNG, JPEG, GIF and SVG (shapes, paths, text, gradients,
`use`/`symbol`, nested `svg`, clip paths, transforms, `<style>`), with
`object-fit`/`object-position` and `image-resolution`.

**Fonts**: system fonts are discovered in the usual directories of Linux,
macOS and Windows (plus `WEASYPRINT_FONT_PATH`) and matched with the CSS
font matching algorithm. Like Pango, family lists are expanded with the
system's fontconfig rules (aliases, generic families, metric-compatible
substitutes, `<dir>` font directories), and per-character fallback
follows the same order, by script and language; elsewhere a built-in
table is used. Color emoji fonts (CBDT/PNG, like Noto Color Emoji) are
drawn as images. As in WeasyPrint, bold and italic are not synthesized.
`@font-face` supports TTF, OTF, TTC and WOFF sources and `local()`.

## Differences from WeasyPrint and limitations

* Only Arabic gets complex shaping; Indic and other scripts needing
  HarfBuzz's script shapers (reordering, mark positioning) are shown
  with their default glyphs. Latin, Greek, Cyrillic, CJK, Hebrew and
  Arabic work well.
* OpenType layout covers kerning, standard and required ligatures,
  Arabic joining forms and small caps, not other features (e.g.
  contextual alternates).
* Nonstandard hyphenation patterns that change letters (such as the
  old German "ck" → "k-k") are not used.
* WOFF2 web fonts are skipped (they require Brotli); variable fonts use
  their default instance.
* Not implemented yet: `::first-line` and
  `::first-letter`, PDF forms, attachments, PDF/A and PDF/UA variants,
  bleed and crop marks.

The output was compared with WeasyPrint 70 page by page. The remaining
differences come from WeasyPrint bugs that are intentionally not
reproduced:

* Glyph widths are written rounded and the correction is truncated, so
  glyphs drift from their laid-out positions (up to 0.5/1000 em each).
* Tab stops are 2.4% too wide (`tab-size: 100px` gives 102.4px).
* `groove`/`ridge`/`inset`/`outset` colors are nearly black (RGB values
  between 0 and 1 are written as percentages).
* Footnotes that fit are often moved to the next page (a floating-point
  comparison without tolerance in the footnote area).
* Leaving a named page for the unnamed one does not break the page (the
  empty page name is taken as "no change").
* Margins of multi-column containers do not collapse with their
  siblings, unlike other block formatting contexts.
* Column balancing overshoots the minimal height, and columns on later
  pages keep top margins at column breaks.
* Overlapping text with many floats, row-spanning cells keeping padding
  from rows moved to the next page, short flex items moved to the last
  page of a fragmented flex line, footnotes called from absolutely
  positioned boxes being lost.
* Outside list markers are placed at the top of the item instead of on
  the baseline of its first line, and flex items are painted in reverse
  order in some cases.
* Files without a charset are decoded as Windows-1252 even when they are
  valid UTF-8, and inline SVG text does not inherit fonts from the
  document.

## Architecture

| Package | Role | WeasyPrint equivalent |
| --- | --- | --- |
| `weasyprint` (root) | Public API, stylesheet collection, presentational hints | `__init__.py`, `html.py` |
| `cmd/weasyprint` | Command line tool | `__main__.py` |
| `internal/html` | HTML tokenizer and tree builder | html5lib |
| `internal/css` | Tokenizer, parser, selectors, cascade, computed values, counter styles, UA stylesheet | `css/`, tinycss2, cssselect2 |
| `internal/fonts` | OpenType parsing, system font library and matching, TrueType/CFF subsetting | fontconfig, fontTools |
| `internal/text` | Shaping (cmap, kerning, ligatures, fallback) and line break opportunities | Pango, HarfBuzz |
| `internal/hyphen` | Hyphenation patterns (Liang's algorithm) | Pyphen |
| `internal/bidi` | Unicode bidirectional algorithm and Arabic joining data | FriBidi |
| `internal/layout` | Box generation and anonymous boxes, block/inline/float/table/flex/grid/column/absolute/page layout | `formatting_structure/`, `layout/` |
| `internal/draw` | Painting order, backgrounds, borders, text, images, SVG, links and bookmarks | `draw/`, `svg/`, `pdf/` |
| `internal/pdf` | Low-level PDF objects and content streams | pydyf |
| `internal/images` | Image decoding and SVG parsing | `images.py`, Pillow |
| `internal/fetch` | URL resolution and fetching (`file:`, `data:`, `http(s):`) | `urls.py` |

## Development

```sh
go test ./...
WEASYPRINT_GO_DEBUG=1 weasyprint doc.html out.pdf   # dump the laid-out box tree
WEASYPRINT_GO_CPUPROFILE=cpu.prof weasyprint doc.html out.pdf
```

## License

BSD 3-Clause, like WeasyPrint. The user agent and presentational hints
stylesheets in `internal/css/ua` come from WeasyPrint (see
`internal/css/ua/LICENSE.weasyprint`), and the layout algorithms follow
WeasyPrint's implementation. The hyphenation dictionaries in
`internal/hyphen/dictionaries` come from Pyphen (LibreOffice/Hunspell
patterns) and keep their own licenses, given in the README files next to
them (mostly LGPL/MPL/GPL tri-licenses).

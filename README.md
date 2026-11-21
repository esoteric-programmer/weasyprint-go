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
`HTML.Render(opts)` returns a `*Document` with page sizes and metadata,
which can then be written with `Document.WritePDF(w)`. A custom
`URLFetcher` can be set on `HTML` to control how stylesheets, images and
fonts are loaded.

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
* Inline layout: line breaking (UAX #14 style), `white-space`,
  `text-align` including `justify`, `text-indent`, `letter-spacing`,
  `word-spacing`, `text-transform`, soft hyphens, `overflow-wrap`,
  `word-break`, `tab-size`, all `vertical-align` values, inline blocks,
  kerning and standard ligatures.
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
`leader()`, `target-counter()`, `target-counters()`, `target-text()`.

**PDF output**: embedded and subset fonts (TrueType and CFF, including
CID-keyed CJK fonts) with `ToUnicode` maps for text extraction, JPEG
pass-through, PNG/GIF with alpha, internal and external links,
named destinations, bookmarks (`bookmark-level`, `bookmark-label`,
`bookmark-state`), document information dictionary.

**Images**: PNG, JPEG, GIF and SVG (shapes, paths, text, gradients,
`use`/`symbol`, nested `svg`, clip paths, transforms, `<style>`), with
`object-fit`/`object-position` and `image-resolution`.

**Fonts**: system fonts are discovered in the usual directories of Linux,
macOS and Windows (plus `WEASYPRINT_FONT_PATH`), matched with the CSS
font matching algorithm, with per-character fallback, metric-compatible
substitutes (Arial → Liberation Sans…) and synthetic bold/italic.
`@font-face` supports TTF, OTF, TTC and WOFF sources and `local()`.

## Differences from WeasyPrint and limitations

* No complex text shaping: Arabic contextual forms, Indic scripts and
  bidirectional reordering are not implemented (Pango/HarfBuzz do this in
  WeasyPrint). Latin, Greek, Cyrillic and CJK text work well.
* No automatic hyphenation (`hyphens: auto`); soft hyphens work.
* WOFF2 web fonts are skipped (they require Brotli); variable fonts use
  their default instance.
* Not implemented yet: footnotes (`float: footnote`), `::first-line` and
  `::first-letter`, PDF forms, attachments, PDF/A and PDF/UA variants,
  bleed and crop marks.
* Table rows, flex and grid containers are not split across pages.
* A few WeasyPrint quirks are intentionally not reproduced, e.g. the
  near-black colors of `groove`/`ridge`/`inset`/`outset` borders and
  files without a charset being decoded as Windows-1252 even when they
  are valid UTF-8.

## Architecture

| Package | Role | WeasyPrint equivalent |
| --- | --- | --- |
| `weasyprint` (root) | Public API, stylesheet collection, presentational hints | `__init__.py`, `html.py` |
| `cmd/weasyprint` | Command line tool | `__main__.py` |
| `internal/html` | HTML tokenizer and tree builder | html5lib |
| `internal/css` | Tokenizer, parser, selectors, cascade, computed values, counter styles, UA stylesheet | `css/`, tinycss2, cssselect2 |
| `internal/fonts` | OpenType parsing, system font library and matching, TrueType/CFF subsetting | fontconfig, fontTools |
| `internal/text` | Shaping (cmap, kerning, ligatures, fallback) and line break opportunities | Pango, HarfBuzz |
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
WeasyPrint's implementation.

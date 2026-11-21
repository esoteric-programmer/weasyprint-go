package weasyprint

import (
	"bytes"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/esoteric-programmer/weasyprint-go/internal/layout"
)

func render(t *testing.T, src string, opts *Options) (*Document, []byte) {
	t.Helper()
	doc, err := HTMLFromString(src, "").Render(opts)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	pdf, err := doc.PDF()
	if err != nil {
		t.Fatalf("pdf: %v", err)
	}
	if !bytes.HasPrefix(pdf, []byte("%PDF-")) || !bytes.HasSuffix(bytes.TrimSpace(pdf), []byte("%%EOF")) {
		t.Fatalf("output is not a PDF file")
	}
	return doc, pdf
}

func TestSimpleDocument(t *testing.T) {
	doc, pdf := render(t, `<title>Test title</title><meta name=author content="Jane Doe"><h1>Hello</h1><p>World</p>`, nil)
	if len(doc.Pages) != 1 {
		t.Fatalf("got %d pages, want 1", len(doc.Pages))
	}
	if doc.Metadata.Title != "Test title" || len(doc.Metadata.Authors) != 1 {
		t.Errorf("metadata %+v", doc.Metadata)
	}
	// Default A4 page.
	if p := doc.Pages[0]; int(p.Width+0.5) != 794 || int(p.Height+0.5) != 1123 {
		t.Errorf("page size %vx%v", p.Width, p.Height)
	}
	if !bytes.Contains(pdf, []byte("/Title (Test title)")) {
		t.Errorf("missing title in info dictionary")
	}
	if !bytes.Contains(pdf, []byte("/MediaBox [0 0 595.2756 841.8898]")) {
		t.Errorf("unexpected media box")
	}
}

func TestPageSizeAndBreaks(t *testing.T) {
	doc, _ := render(t, `<style>@page { size: 100mm 50mm; margin: 5mm } p { break-after: page }</style>
		<p>one</p><p>two</p><p>three</p>`, nil)
	if len(doc.Pages) != 3 {
		t.Fatalf("got %d pages, want 3", len(doc.Pages))
	}
	w := doc.Pages[0].Width * 25.4 / 96
	h := doc.Pages[0].Height * 25.4 / 96
	if w < 99.9 || w > 100.1 || h < 49.9 || h > 50.1 {
		t.Errorf("page size %.2fx%.2f mm", w, h)
	}
}

func TestLeftRightBlankPages(t *testing.T) {
	doc, _ := render(t, `<p>a</p><h1 style="break-before: right">b</h1><h1 style="break-before: right">c</h1>`, nil)
	// a on page 1 (right), b needs a right page: blank page 2, b on 3,
	// c on page 5 after blank page 4.
	if len(doc.Pages) != 5 {
		t.Fatalf("got %d pages, want 5", len(doc.Pages))
	}
	if !doc.pages[1].Blank || !doc.pages[3].Blank || doc.pages[2].Blank {
		t.Errorf("blank pages misplaced")
	}
}

func TestOverflowPagination(t *testing.T) {
	var b strings.Builder
	b.WriteString(`<style>@page { size: A6 } p { margin: 0; line-height: 20px; font-size: 10px }</style>`)
	for i := 0; i < 100; i++ {
		b.WriteString("<p>Line " + strconv.Itoa(i) + "</p>")
	}
	doc, _ := render(t, b.String(), nil)
	// A6 is 148mm high (559px), minus 150px of margins: 409px → 20 lines.
	if len(doc.Pages) != 5 {
		t.Errorf("got %d pages, want 5", len(doc.Pages))
	}
}

func TestTallRowsFlexGridSplitAcrossPages(t *testing.T) {
	var lines strings.Builder
	for i := 0; i < 100; i++ {
		lines.WriteString("<p>Line " + strconv.Itoa(i) + "</p>")
	}
	style := `<style>@page { size: A6 } p { margin: 0; line-height: 20px; font-size: 10px }</style>`
	cases := map[string]string{
		"table row":   `<table><tr><td>` + lines.String() + `</td><td>side</td></tr></table>`,
		"row flex":    `<div style="display:flex"><div>` + lines.String() + `</div><div>side</div></div>`,
		"column flex": `<div style="display:flex;flex-direction:column">` + lines.String() + `</div>`,
		"grid":        `<div style="display:grid;grid-template-columns:1fr 50px"><div>` + lines.String() + `</div><div>side</div></div>`,
	}
	for name, body := range cases {
		doc, _ := render(t, style+body, nil)
		if len(doc.Pages) < 5 {
			t.Errorf("%s: got %d pages, want at least 5", name, len(doc.Pages))
		}
	}
}

func TestAutomaticHyphenation(t *testing.T) {
	src := `<html lang="en"><style>p { width: 60px; font-size: 12px; hyphens: auto }</style>
<p>Internationalization representatives</p>`
	doc, _ := render(t, src, nil)
	hyphenated := 0
	doc.pages[0].Box.Descendants(func(b *layout.Box) {
		if b.Hyphen != "" {
			hyphenated++
		}
	})
	if hyphenated == 0 {
		t.Error("no word was hyphenated")
	}
	// Without a language, or with hyphens: manual, words are not split.
	for _, src := range []string{
		`<style>p { width: 60px; font-size: 12px; hyphens: auto }</style><p>Internationalization representatives</p>`,
		`<html lang="en"><style>p { width: 60px; font-size: 12px }</style><p>Internationalization representatives</p>`,
	} {
		doc, _ := render(t, src, nil)
		doc.pages[0].Box.Descendants(func(b *layout.Box) {
			if b.Hyphen != "" {
				t.Errorf("unexpected hyphenation in %q", src)
			}
		})
	}
}

func TestFootnotes(t *testing.T) {
	var b strings.Builder
	b.WriteString(`<style>@page { size: A6 } p { margin: 0; line-height: 20px; font-size: 10px } .fn { float: footnote }</style>`)
	for i := 0; i < 40; i++ {
		b.WriteString("<p>Line " + strconv.Itoa(i))
		if i%10 == 0 {
			b.WriteString(`<span class="fn">Note for line ` + strconv.Itoa(i) + `.</span>`)
		}
		b.WriteString("</p>")
	}
	doc, _ := render(t, b.String(), nil)
	notes := 0
	for _, p := range doc.pages {
		var area *layout.Box
		for _, ch := range p.Box.Children {
			if strings.Contains(ch.AllText(), "Note for line") {
				area = ch
			}
		}
		if area == nil {
			continue
		}
		content := p.Box.Children[0]
		if bottom := content.Y + content.MarginHeight(); bottom > area.Y+1e-6 {
			t.Errorf("page content (bottom %v) overlaps the footnote area (top %v)", bottom, area.Y)
		}
		for _, line := range regexp.MustCompile(`(\d+)\. Note for line (\d+)`).FindAllStringSubmatch(area.AllText(), -1) {
			notes++
			n, _ := strconv.Atoi(line[1])
			l, _ := strconv.Atoi(line[2])
			if n != l/10+1 {
				t.Errorf("footnote %d is for line %d", n, l)
			}
			// The call is on the same page.
			if !strings.Contains(content.AllText(), "Line "+line[2]+line[1]) {
				t.Errorf("call of footnote %d not on its page", n)
			}
		}
	}
	if notes != 4 {
		t.Errorf("got %d footnotes, want 4", notes)
	}
}

func TestLinksAndBookmarks(t *testing.T) {
	_, pdf := render(t, `<h1 id="top">Title</h1><h2>Sub</h2><p><a href="#top">internal</a> <a href="https://example.com/">external</a></p>`, nil)
	for _, want := range []string{"/Subtype /Link", "/URI (https://example.com/)", "/Dest /top", "/Outlines", "/Title (Sub)"} {
		if !bytes.Contains(pdf, []byte(want)) {
			t.Errorf("missing %q", want)
		}
	}
}

func TestMarginBoxesAndCounters(t *testing.T) {
	doc, _ := render(t, `<style>@page { @bottom-center { content: "Page " counter(page) " of " counter(pages) } }
		div { break-before: page }</style><div>1</div><div>2</div>`, nil)
	if len(doc.Pages) != 2 {
		t.Fatalf("got %d pages", len(doc.Pages))
	}
	found := 0
	for _, p := range doc.pages {
		for _, ch := range p.Box.Children {
			if ch.Kind == layout.KMargin && strings.Contains(ch.AllText(), "of 2") {
				found++
			}
		}
	}
	if found != 2 {
		t.Errorf("found %d margin boxes with page counts, want 2", found)
	}
}

func TestUserStylesheetAndFonts(t *testing.T) {
	css := CSSFromString(`body { font-family: monospace; color: rgb(255 0 0) }`, "")
	_, pdf := render(t, `<p>styled</p>`, &Options{Stylesheets: []*CSS{css}, Uncompressed: true})
	if !regexp.MustCompile(`1 0 0 rg`).Match(pdf) {
		t.Errorf("red text color not found")
	}
	if !bytes.Contains(pdf, []byte("/FontFile")) && !bytes.Contains(pdf, []byte("/Type0")) {
		t.Skip("no fonts available on this system")
	}
}

func TestTablesAndFlexDoNotPanic(t *testing.T) {
	render(t, `<table border=1><thead><tr><th>a<th>b</thead><tbody><tr><td rowspan=2>1<td>2<tr><td colspan=2>3</table>
		<div style="display:flex;gap:4px"><div style="flex:1">x</div><div>y</div></div>
		<ul><li>item<li>item</ul><div style="float:left;width:50px">f</div><p style="position:relative;left:3px">p</p>
		<svg width=20 height=20><circle cx=10 cy=10 r=8 fill=red /></svg>`, &Options{PresentationalHints: true})
}

func TestImagesDataURL(t *testing.T) {
	// 1x1 red PNG.
	png := "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAIAAACQd1PeAAAADElEQVR4nGP4z8AAAAMBAQDJ/pLvAAAAAElFTkSuQmCC"
	_, pdf := render(t, `<img src="data:image/png;base64,`+png+`" style="width:10px">`, nil)
	if !bytes.Contains(pdf, []byte("/Subtype /Image")) {
		t.Errorf("image not embedded")
	}
}

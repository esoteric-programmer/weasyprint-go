package css

import (
	"testing"

	"github.com/esoteric-programmer/weasyprint-go/internal/html"
)

func resolve(t *testing.T, doc string, author string) (*Resolver, *html.Node) {
	var warnings []string
	env := &Env{Warn: func(s string) { warnings = append(warnings, s) }}
	ua := ParseStylesheet(UserAgentCSS(), "", OriginUA, env)
	for _, w := range warnings {
		t.Logf("UA warning: %s", w)
	}
	sheets := []*Stylesheet{ua}
	if author != "" {
		sheets = append(sheets, ParseStylesheet(author, "", OriginAuthor, env))
	}
	d := html.Parse(doc)
	r := NewResolver(d, sheets, "")
	r.ResolveAll()
	return r, d
}

func TestCascade(t *testing.T) {
	r, d := resolve(t, `<html><body><h1 id=t class="a b">Hi</h1><p style="color: red; margin: 1em 2em">x <a href="#t">l</a></p><ul><li>a</li></ul></body></html>`,
		`:root { --main: 10px } h1.a { color: #00ff00; font-size: 3em } p { padding: var(--main) calc(1em + 10%) } li::marker { color: blue } a { text-decoration: none }`)
	h1 := d.Find("h1")
	s := r.Style(h1)
	if s.Display() != "block" {
		t.Errorf("h1 display %q", s.Display())
	}
	if fs := s.FontSize(); fs != 48 {
		t.Errorf("h1 font-size %v", fs)
	}
	if c := s.Color(PColor); c != (Color{0, 1, 0, 1}) {
		t.Errorf("h1 color %v", c)
	}
	if m := s.Px(PMarginTop, 0); m < 32.1 || m > 32.2 {
		t.Errorf("h1 margin-top %v", m)
	}
	if s.Anchor() != "t" {
		t.Errorf("anchor %q", s.Anchor())
	}
	p := r.Style(d.Find("p"))
	if c := p.Color(PColor); c != (Color{1, 0, 0, 1}) {
		t.Errorf("p color %v", c)
	}
	if v := p.Px(PMarginLeft, 0); v != 32 {
		t.Errorf("p margin-left %v", v)
	}
	if v := p.Px(PPaddingTop, 0); v != 10 {
		t.Errorf("p padding-top %v", v)
	}
	if v := p.Px(PPaddingLeft, 100); v != 26 {
		t.Errorf("p padding-left %v", v)
	}
	a := r.Style(d.Find("a"))
	if l := a.Link(); l == nil || !l.Internal || l.Target != "t" {
		t.Errorf("link %v", l)
	}
	if a.TextDecorations() != nil {
		t.Errorf("decorations %v", a.TextDecorations())
	}
	li := d.Find("li")
	if r.Style(li).Display() != "list-item" {
		t.Errorf("li display")
	}
	if m := r.PseudoStyle(li, "marker"); m == nil || m.Color(PColor) != (Color{0, 0, 1, 1}) {
		t.Errorf("marker style")
	}
	if got := r.CounterStyles.Marker(3, Keyword("lower-roman")); got != "iii. " {
		t.Errorf("roman %q", got)
	}
	if got := r.CounterStyles.Marker(1, Keyword("disc")); got != "• " {
		t.Errorf("disc %q", got)
	}
	if got := r.CounterStyles.Marker(28, Keyword("upper-alpha")); got != "AB. " {
		t.Errorf("alpha %q", got)
	}
	ps := r.PageStyle(PageType{Side: "right", First: true})
	if v := ps.Px(PMarginTop, 0); v != 75 {
		t.Errorf("page margin %v", v)
	}
}

func TestSelectors(t *testing.T) {
	d := html.Parse(`<div id=x><p class=a>1</p><p>2</p><span lang=en-US>3</span><p>4</p></div>`)
	ps := d.FindAll("p")
	cases := []struct {
		sel  string
		node *html.Node
		want bool
	}{
		{"div > p.a", ps[0], true},
		{"p + p", ps[1], true},
		{"p ~ p", ps[2], true},
		{"p:first-child", ps[0], true},
		{"p:nth-of-type(2)", ps[1], true},
		{"p:nth-child(2n+1)", ps[2], false},
		{"p:last-child", ps[2], true},
		{"p:not(.a)", ps[0], false},
		{":is(span, p.a)", ps[0], true},
		{"div:has(> span)", d.Find("div"), true},
		{"span:lang(en)", d.Find("span"), true},
		{"#x p", ps[2], true},
		{"[class^=a]", ps[0], true},
	}
	for _, c := range cases {
		sels := ParseSelectorList(Tokenize(c.sel))
		if sels == nil {
			t.Errorf("parse %q failed", c.sel)
			continue
		}
		if got := sels[0].Match(c.node); got != c.want {
			t.Errorf("%q: got %v", c.sel, got)
		}
	}
}

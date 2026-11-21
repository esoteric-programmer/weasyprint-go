package html

import (
	"strings"
	"testing"
)

func dump(n *Node, depth int, b *strings.Builder) {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		b.WriteString(strings.Repeat("  ", depth))
		switch c.Type {
		case ElementNode:
			b.WriteString("<" + c.Tag)
			for _, a := range c.Attrs {
				b.WriteString(" " + a.Name + "=" + a.Value)
			}
			b.WriteString(">\n")
			dump(c, depth+1, b)
		case TextNode:
			b.WriteString("\"" + c.Data + "\"\n")
		}
	}
}

func TestParse(t *testing.T) {
	cases := map[string]string{
		"<p>a<p>b": "<html>\n  <head>\n  <body>\n    <p>\n      \"a\"\n    <p>\n      \"b\"\n",
		"<title>T &amp; U</title><ul><li>1<li>2</ul>":    "<html>\n  <head>\n    <title>\n      \"T & U\"\n  <body>\n    <ul>\n      <li>\n        \"1\"\n      <li>\n        \"2\"\n",
		"<table><tr><td>x<td>y</table>":                  "<html>\n  <head>\n  <body>\n    <table>\n      <tbody>\n        <tr>\n          <td>\n            \"x\"\n          <td>\n            \"y\"\n",
		"<div class=a id='b'>x<br>y &copy; &#x41;</div>": "<html>\n  <head>\n  <body>\n    <div class=a id=b>\n      \"x\"\n      <br>\n      \"y © A\"\n",
		"<style>p > a { }</style><p>x":                   "<html>\n  <head>\n    <style>\n      \"p > a { }\"\n  <body>\n    <p>\n      \"x\"\n",
	}
	for in, want := range cases {
		var b strings.Builder
		dump(Parse(in), 0, &b)
		if b.String() != want {
			t.Errorf("Parse(%q):\n%s\nwant:\n%s", in, b.String(), want)
		}
	}
}

func TestQuirksTableInParagraph(t *testing.T) {
	find := func(doc *Node) *Node {
		var walk func(n *Node) *Node
		walk = func(n *Node) *Node {
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				if c.Type == ElementNode && c.Tag == "table" {
					return c
				}
				if r := walk(c); r != nil {
					return r
				}
			}
			return nil
		}
		return walk(doc)
	}
	// Without a DOCTYPE (quirks mode), tables do not close paragraphs.
	if tbl := find(Parse("<p>a<table><tr><td>x</table>")); tbl == nil || tbl.Parent == nil || tbl.Parent.Tag != "p" {
		t.Error("quirks mode: table should be inside the paragraph")
	}
	if tbl := find(Parse("<!DOCTYPE html><p>a<table><tr><td>x</table>")); tbl == nil || tbl.Parent == nil || tbl.Parent.Tag != "body" {
		t.Error("standards mode: table should close the paragraph")
	}
}

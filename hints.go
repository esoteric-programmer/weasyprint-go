package weasyprint

import (
	"strconv"
	"strings"

	"github.com/esoteric-programmer/weasyprint-go/internal/css"
	"github.com/esoteric-programmer/weasyprint-go/internal/html"
)

// htmlLength converts an HTML dimension attribute to CSS.
func htmlLength(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}
	if strings.HasSuffix(v, "%") {
		if _, err := strconv.ParseFloat(v[:len(v)-1], 64); err == nil {
			return v
		}
		return ""
	}
	if _, err := strconv.ParseFloat(v, 64); err == nil {
		return v + "px"
	}
	// Leading digits.
	i := 0
	for i < len(v) && (v[i] >= '0' && v[i] <= '9' || v[i] == '.') {
		i++
	}
	if i > 0 {
		return v[:i] + "px"
	}
	return ""
}

func legacyColor(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}
	if len(v) == 6 || len(v) == 3 {
		if _, err := strconv.ParseUint(v, 16, 32); err == nil {
			return "#" + v
		}
	}
	return v
}

var fontSizes = map[string]string{
	"1": "x-small", "2": "small", "3": "medium", "4": "large", "5": "x-large", "6": "xx-large", "7": "xxx-large",
}

// presentationalHints returns declarations for HTML presentational
// attributes that cannot be expressed in the hints stylesheet.
func presentationalHints(e *html.Node) []css.Decl {
	var b strings.Builder
	add := func(prop, val string) {
		if val != "" {
			b.WriteString(prop + ":" + val + ";")
		}
	}
	switch e.Tag {
	case "img", "embed", "object", "iframe", "video", "canvas":
		add("width", htmlLength(e.Get("width")))
		add("height", htmlLength(e.Get("height")))
		if v := htmlLength(e.Get("hspace")); v != "" {
			add("margin-left", v)
			add("margin-right", v)
		}
		if v := htmlLength(e.Get("vspace")); v != "" {
			add("margin-top", v)
			add("margin-bottom", v)
		}
		if bd := htmlLength(e.Get("border")); bd != "" && e.Tag == "img" {
			add("border", bd+" solid")
		}
	case "table":
		add("width", htmlLength(e.Get("width")))
		add("height", htmlLength(e.Get("height")))
		add("background-color", legacyColor(e.Get("bgcolor")))
		if v := htmlLength(e.Get("cellspacing")); v != "" {
			add("border-spacing", v)
		}
		if v, ok := e.Attr("border"); ok {
			w := htmlLength(v)
			if v == "" {
				w = "1px"
			}
			if w != "" && w != "0px" {
				add("border-width", w)
			}
		}
		if v := e.Get("background"); v != "" {
			add("background-image", "url(\""+strings.ReplaceAll(v, "\"", "")+"\")")
		}
	case "td", "th":
		add("width", htmlLength(e.Get("width")))
		add("height", htmlLength(e.Get("height")))
		add("background-color", legacyColor(e.Get("bgcolor")))
		if t := tableOf(e); t != nil {
			if v := htmlLength(t.Get("cellpadding")); v != "" {
				add("padding", v)
			}
		}
		switch strings.ToLower(e.Get("align")) {
		case "left", "right", "center", "justify":
			add("text-align", strings.ToLower(e.Get("align")))
		}
	case "tr", "thead", "tbody", "tfoot":
		add("background-color", legacyColor(e.Get("bgcolor")))
		add("height", htmlLength(e.Get("height")))
		switch strings.ToLower(e.Get("align")) {
		case "left", "right", "center", "justify":
			add("text-align", strings.ToLower(e.Get("align")))
		}
	case "col", "colgroup":
		add("width", htmlLength(e.Get("width")))
	case "body":
		add("background-color", legacyColor(e.Get("bgcolor")))
		add("color", legacyColor(e.Get("text")))
		if v := e.Get("background"); v != "" {
			add("background-image", "url(\""+strings.ReplaceAll(v, "\"", "")+"\")")
		}
	case "font":
		add("color", legacyColor(e.Get("color")))
		if f := e.Get("face"); f != "" {
			add("font-family", f)
		}
		if s := strings.TrimSpace(e.Get("size")); s != "" {
			if strings.HasPrefix(s, "+") || strings.HasPrefix(s, "-") {
				n, err := strconv.Atoi(s)
				if err == nil {
					n += 3
					if n < 1 {
						n = 1
					}
					if n > 7 {
						n = 7
					}
					s = strconv.Itoa(n)
				}
			}
			add("font-size", fontSizes[s])
		}
	case "div", "p", "h1", "h2", "h3", "h4", "h5", "h6", "caption":
		switch strings.ToLower(e.Get("align")) {
		case "left", "right", "center", "justify":
			add("text-align", strings.ToLower(e.Get("align")))
		}
	case "hr":
		add("width", htmlLength(e.Get("width")))
		if s := htmlLength(e.Get("size")); s != "" {
			add("height", s)
		}
		if c := legacyColor(e.Get("color")); c != "" {
			add("border-color", c)
			add("background-color", c)
		}
	case "ul", "ol", "li":
	}
	if b.Len() == 0 {
		return nil
	}
	return css.DeclsFromString(b.String(), "")
}

func tableOf(e *html.Node) *html.Node {
	for p := e.Parent; p != nil; p = p.Parent {
		if p.Tag == "table" {
			return p
		}
	}
	return nil
}

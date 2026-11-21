package html

import (
	"strconv"
	"strings"
	"unicode/utf8"
)

var voidElements = map[string]bool{
	"area": true, "base": true, "basefont": true, "bgsound": true, "br": true,
	"col": true, "embed": true, "frame": true, "hr": true, "img": true,
	"input": true, "keygen": true, "link": true, "meta": true, "param": true,
	"source": true, "track": true, "wbr": true, "image": true,
}

var rawTextElements = map[string]bool{
	"script": true, "style": true, "xmp": true, "iframe": true,
	"noembed": true, "noframes": true, "plaintext": true,
}

var rcdataElements = map[string]bool{"textarea": true, "title": true}

var headElements = map[string]bool{
	"base": true, "basefont": true, "bgsound": true, "link": true, "meta": true,
	"noscript": true, "script": true, "style": true, "template": true, "title": true,
}

// Elements whose start tag closes an open <p>.
var closesP = map[string]bool{
	"address": true, "article": true, "aside": true, "blockquote": true,
	"center": true, "details": true, "dialog": true, "dir": true, "div": true,
	"dl": true, "fieldset": true, "figcaption": true, "figure": true,
	"footer": true, "form": true, "h1": true, "h2": true, "h3": true, "h4": true,
	"h5": true, "h6": true, "header": true, "hgroup": true, "hr": true,
	"main": true, "menu": true, "nav": true, "ol": true, "p": true,
	"pre": true, "listing": true, "section": true, "summary": true,
	"table": true, "ul": true, "li": true, "dd": true, "dt": true,
	"plaintext": true, "xmp": true,
}

// Elements that act as scope boundaries when looking for an open element.
var scopeBoundaries = map[string]bool{
	"applet": true, "caption": true, "html": true, "table": true, "td": true,
	"th": true, "marquee": true, "object": true, "template": true,
	"svg": false, "math": false,
}

type tokenType int

const (
	tText tokenType = iota
	tStart
	tEnd
	tComment
	tDoctype
)

type token struct {
	typ         tokenType
	data        string
	attrs       []Attr
	selfClosing bool
}

// Parse parses an HTML document.
func Parse(src string) *Node {
	p := &parser{src: src}
	p.doc = &Node{Type: DocumentNode}
	p.run()
	p.finish()
	return p.doc
}

type parser struct {
	src        string
	pos        int
	doc        *Node
	html       *Node
	head       *Node
	body       *Node
	stack      []*Node
	inBody     bool
	foreign    int // depth of svg/math
	pendingRaw *rawPending
	doctype    *string // DOCTYPE token data, if any
}

// quirksPublicPrefixes are DOCTYPE public identifiers that trigger quirks
// mode (from the HTML standard).
var quirksPublicPrefixes = []string{
	"+//silmaril//dtd html pro v0r11 19970101//", "-//as//dtd html 3.0 aswedit + extensions//",
	"-//advasoft ltd//dtd html 3.0 aswedit + extensions//", "-//ietf//dtd html 2.0",
	"-//ietf//dtd html 3", "-//ietf//dtd html level", "-//ietf//dtd html strict",
	"-//ietf//dtd html//", "-//metrius//dtd metrius presentational//",
	"-//microsoft//dtd internet explorer", "-//netscape comm. corp.//dtd",
	"-//o'reilly and associates//dtd html", "-//softquad", "-//spyglass//dtd html 2.0",
	"-//sq//dtd html 2.0", "-//sun microsystems corp.//dtd hotjava",
	"-//w3c//dtd html 3", "-//w3c//dtd html 4.0 frameset//", "-//w3c//dtd html 4.0 transitional//",
	"-//w3c//dtd html experimental", "-//w3c//dtd w3 html//", "-//w3o//dtd w3 html",
	"-//webtechs//dtd mozilla html", "-/w3c/dtd html 4.0 transitional/en", "html",
}

// quirks reports whether the document is in quirks mode (no DOCTYPE, or a
// legacy one).
func (p *parser) quirks() bool {
	if p.doctype == nil {
		return true
	}
	d := strings.ToLower(strings.TrimSpace(*p.doctype))
	fields := strings.Fields(d)
	if len(fields) == 0 || fields[0] != "html" {
		return true
	}
	rest := strings.TrimSpace(d[len(fields[0]):])
	if !strings.HasPrefix(rest, "public") {
		return false
	}
	ids := quotedStrings(rest)
	if len(ids) == 0 {
		return false
	}
	pub := ids[0]
	for _, q := range quirksPublicPrefixes {
		if pub == q || (q != "html" && strings.HasPrefix(pub, q)) {
			return true
		}
	}
	if len(ids) < 2 && (strings.HasPrefix(pub, "-//w3c//dtd html 4.01 frameset//") || strings.HasPrefix(pub, "-//w3c//dtd html 4.01 transitional//")) {
		return true
	}
	return false
}

func quotedStrings(s string) []string {
	var out []string
	for {
		i := strings.IndexAny(s, "\"'")
		if i < 0 {
			return out
		}
		q := s[i]
		j := strings.IndexByte(s[i+1:], q)
		if j < 0 {
			return out
		}
		out = append(out, s[i+1:i+1+j])
		s = s[i+2+j:]
	}
}

func (p *parser) current() *Node {
	if len(p.stack) == 0 {
		return p.doc
	}
	return p.stack[len(p.stack)-1]
}

// ---------------------------------------------------------------- tokenizer

func (p *parser) next() (token, bool) {
	if p.pos >= len(p.src) {
		return token{}, false
	}
	s := p.src
	if s[p.pos] == '<' && p.pos+1 < len(s) {
		c := s[p.pos+1]
		switch {
		case c == '!':
			if strings.HasPrefix(s[p.pos:], "<!--") {
				end := strings.Index(s[p.pos+4:], "-->")
				var data string
				if end < 0 {
					data = s[p.pos+4:]
					p.pos = len(s)
				} else {
					data = s[p.pos+4 : p.pos+4+end]
					p.pos += 4 + end + 3
				}
				return token{typ: tComment, data: data}, true
			}
			if strings.HasPrefix(s[p.pos:], "<![CDATA[") {
				end := strings.Index(s[p.pos+9:], "]]>")
				var data string
				if end < 0 {
					data = s[p.pos+9:]
					p.pos = len(s)
				} else {
					data = s[p.pos+9 : p.pos+9+end]
					p.pos += 9 + end + 3
				}
				return token{typ: tText, data: data}, true
			}
			end := strings.IndexByte(s[p.pos:], '>')
			var data string
			if end < 0 {
				data = s[p.pos+2:]
				p.pos = len(s)
			} else {
				data = s[p.pos+2 : p.pos+end]
				p.pos += end + 1
			}
			if strings.HasPrefix(strings.ToLower(data), "doctype") {
				return token{typ: tDoctype, data: strings.TrimSpace(data[7:])}, true
			}
			return token{typ: tComment, data: data}, true
		case c == '?':
			end := strings.IndexByte(s[p.pos:], '>')
			var data string
			if end < 0 {
				data = s[p.pos+1:]
				p.pos = len(s)
			} else {
				data = s[p.pos+1 : p.pos+end]
				p.pos += end + 1
			}
			return token{typ: tComment, data: data}, true
		case c == '/':
			if p.pos+2 < len(s) && isASCIIAlpha(s[p.pos+2]) {
				return p.readTag(true), true
			}
			if p.pos+2 < len(s) && s[p.pos+2] == '>' {
				p.pos += 3
				return p.next()
			}
			// Bogus comment.
			end := strings.IndexByte(s[p.pos:], '>')
			if end < 0 {
				p.pos = len(s)
			} else {
				p.pos += end + 1
			}
			return p.next()
		case isASCIIAlpha(c):
			return p.readTag(false), true
		}
	}
	// Text up to next '<'.
	start := p.pos
	p.pos++
	for p.pos < len(s) {
		if s[p.pos] == '<' && p.pos+1 < len(s) {
			c := s[p.pos+1]
			if isASCIIAlpha(c) || c == '/' || c == '!' || c == '?' {
				break
			}
		}
		p.pos++
	}
	return token{typ: tText, data: DecodeEntities(s[start:p.pos], false)}, true
}

func isASCIIAlpha(c byte) bool { return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') }

func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f'
}

func (p *parser) readTag(end bool) token {
	s := p.src
	i := p.pos + 1
	if end {
		i++
	}
	start := i
	for i < len(s) && !isSpace(s[i]) && s[i] != '/' && s[i] != '>' {
		i++
	}
	name := s[start:i]
	if p.foreign == 0 {
		name = strings.ToLower(name)
	}
	tok := token{typ: tStart, data: name}
	if end {
		tok.typ = tEnd
		tok.data = strings.ToLower(name)
	}
	// Attributes.
	for i < len(s) {
		for i < len(s) && (isSpace(s[i]) || (s[i] == '/' && i+1 < len(s) && s[i+1] != '>')) {
			i++
		}
		if i >= len(s) {
			break
		}
		if s[i] == '>' {
			i++
			break
		}
		if s[i] == '/' && i+1 < len(s) && s[i+1] == '>' {
			tok.selfClosing = true
			i += 2
			break
		}
		ns := i
		i++
		for i < len(s) && !isSpace(s[i]) && s[i] != '/' && s[i] != '>' && s[i] != '=' {
			i++
		}
		aname := s[ns:i]
		if p.foreign == 0 || !strings.Contains(aname, ":") {
			aname = strings.ToLower(aname)
		}
		for i < len(s) && isSpace(s[i]) {
			i++
		}
		value := ""
		if i < len(s) && s[i] == '=' {
			i++
			for i < len(s) && isSpace(s[i]) {
				i++
			}
			if i < len(s) && (s[i] == '"' || s[i] == '\'') {
				q := s[i]
				i++
				vs := i
				for i < len(s) && s[i] != q {
					i++
				}
				value = s[vs:i]
				if i < len(s) {
					i++
				}
			} else {
				vs := i
				for i < len(s) && !isSpace(s[i]) && s[i] != '>' {
					i++
				}
				value = s[vs:i]
			}
			value = DecodeEntities(value, true)
		}
		if !end {
			dup := false
			for _, a := range tok.attrs {
				if a.Name == aname {
					dup = true
					break
				}
			}
			if !dup {
				tok.attrs = append(tok.attrs, Attr{aname, value})
			}
		}
	}
	p.pos = i
	// Raw text content.
	if !end && !tok.selfClosing && p.foreign == 0 && (rawTextElements[tok.data] || rcdataElements[tok.data]) {
		closing := "</" + tok.data
		lower := strings.ToLower(s[p.pos:])
		idx := strings.Index(lower, closing)
		if tok.data == "plaintext" {
			idx = -1
		}
		var text string
		if idx < 0 {
			text = s[p.pos:]
			p.pos = len(s)
		} else {
			text = s[p.pos : p.pos+idx]
			p.pos += idx
			// Skip the end tag.
			e := strings.IndexByte(s[p.pos:], '>')
			if e < 0 {
				p.pos = len(s)
			} else {
				p.pos += e + 1
			}
		}
		if rcdataElements[tok.data] {
			text = DecodeEntities(text, false)
		}
		p.pendingRaw = &rawPending{tag: tok.data, text: text}
	}
	return tok
}

type rawPending struct {
	tag, text string
}

// DecodeEntities decodes character references in s.
func DecodeEntities(s string, inAttr bool) string {
	if strings.IndexByte(s, '&') < 0 {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); {
		c := s[i]
		if c != '&' {
			b.WriteByte(c)
			i++
			continue
		}
		rest := s[i+1:]
		if len(rest) > 0 && rest[0] == '#' {
			j := 1
			hex := false
			if j < len(rest) && (rest[j] == 'x' || rest[j] == 'X') {
				hex = true
				j++
			}
			start := j
			for j < len(rest) && ((rest[j] >= '0' && rest[j] <= '9') || (hex && strings.IndexByte("abcdefABCDEF", rest[j]) >= 0)) {
				j++
			}
			if j == start {
				b.WriteByte('&')
				i++
				continue
			}
			base := 10
			if hex {
				base = 16
			}
			n, err := strconv.ParseInt(rest[start:j], base, 32)
			if err != nil || n == 0 || n > 0x10ffff || (n >= 0xd800 && n <= 0xdfff) {
				n = 0xfffd
			}
			if n >= 0x80 && n <= 0x9f {
				if r, ok := win1252[int(n)]; ok {
					n = int64(r)
				}
			}
			b.WriteRune(rune(n))
			if j < len(rest) && rest[j] == ';' {
				j++
			}
			i += 1 + j
			continue
		}
		// Named reference: find the longest match.
		j := 0
		for j < len(rest) && j < 40 && (isASCIIAlpha(rest[j]) || (rest[j] >= '0' && rest[j] <= '9')) {
			j++
		}
		matched := false
		if j < len(rest) && rest[j] == ';' {
			if v, ok := entities[rest[:j+1]]; ok {
				b.WriteString(v)
				i += j + 2
				matched = true
			}
		}
		if !matched {
			for k := j; k > 0; k-- {
				if v, ok := entities[rest[:k]]; ok {
					if inAttr && k < len(rest) && (rest[k] == '=' || isASCIIAlpha(rest[k]) || (rest[k] >= '0' && rest[k] <= '9')) {
						break
					}
					b.WriteString(v)
					i += k + 1
					matched = true
					break
				}
			}
		}
		if !matched {
			b.WriteByte('&')
			i++
		}
	}
	return b.String()
}

var win1252 = map[int]rune{
	0x80: 0x20AC, 0x82: 0x201A, 0x83: 0x0192, 0x84: 0x201E, 0x85: 0x2026,
	0x86: 0x2020, 0x87: 0x2021, 0x88: 0x02C6, 0x89: 0x2030, 0x8A: 0x0160,
	0x8B: 0x2039, 0x8C: 0x0152, 0x8E: 0x017D, 0x91: 0x2018, 0x92: 0x2019,
	0x93: 0x201C, 0x94: 0x201D, 0x95: 0x2022, 0x96: 0x2013, 0x97: 0x2014,
	0x98: 0x02DC, 0x99: 0x2122, 0x9A: 0x0161, 0x9B: 0x203A, 0x9C: 0x0153,
	0x9E: 0x017E, 0x9F: 0x0178,
}

// -------------------------------------------------------------- tree builder

func (p *parser) push(n *Node) {
	p.current().AppendChild(n)
	p.stack = append(p.stack, n)
}

func (p *parser) pop() *Node {
	n := p.stack[len(p.stack)-1]
	p.stack = p.stack[:len(p.stack)-1]
	if n.Tag == "svg" || n.Tag == "math" {
		if p.foreign > 0 {
			p.foreign--
		}
	}
	return n
}

func (p *parser) ensureHTML() {
	if p.html == nil {
		p.html = &Node{Type: ElementNode, Tag: "html"}
		p.doc.AppendChild(p.html)
		p.stack = []*Node{p.html}
	}
}

func (p *parser) ensureHead() {
	p.ensureHTML()
	if p.head == nil {
		p.head = &Node{Type: ElementNode, Tag: "head"}
		p.html.InsertBefore(p.head, p.html.FirstChild)
		if p.html.FirstChild != p.head {
			// Keep head first among elements.
		}
	}
}

func (p *parser) ensureBody() {
	p.ensureHead()
	if p.body == nil {
		p.body = &Node{Type: ElementNode, Tag: "body"}
		p.html.AppendChild(p.body)
	}
	if !p.inBody {
		p.inBody = true
		p.stack = []*Node{p.html, p.body}
	}
}

// inScope returns the index in the stack of the nearest open element with
// one of the given tags, stopping at scope boundaries.
func (p *parser) inScope(tags ...string) int {
	for i := len(p.stack) - 1; i >= 0; i-- {
		t := p.stack[i].Tag
		for _, tag := range tags {
			if t == tag {
				return i
			}
		}
		if scopeBoundaries[t] {
			return -1
		}
	}
	return -1
}

func (p *parser) inListScope(tags ...string) int {
	for i := len(p.stack) - 1; i >= 0; i-- {
		t := p.stack[i].Tag
		for _, tag := range tags {
			if t == tag {
				return i
			}
		}
		if scopeBoundaries[t] || t == "ol" || t == "ul" {
			return -1
		}
	}
	return -1
}

func (p *parser) popTo(i int) {
	for len(p.stack) > i {
		p.pop()
	}
}

func (p *parser) closeP() {
	if i := p.inScope("p"); i >= 0 {
		// Only close if no "button" in between.
		p.popTo(i)
	}
}

func (p *parser) insertText(text string) {
	cur := p.current()
	if cur.LastChild != nil && cur.LastChild.Type == TextNode {
		cur.LastChild.Data += text
		return
	}
	cur.AppendChild(&Node{Type: TextNode, Data: text})
}

func (p *parser) run() {
	for {
		tok, ok := p.next()
		if !ok {
			break
		}
		p.process(tok)
		if p.pendingRaw != nil {
			raw := p.pendingRaw
			p.pendingRaw = nil
			if raw.text != "" {
				p.insertText(raw.text)
			}
			if len(p.stack) > 0 && p.current().Tag == raw.tag {
				p.pop()
			}
		}
	}
}

func (p *parser) process(tok token) {
	switch tok.typ {
	case tDoctype:
		if p.doctype == nil {
			d := tok.data
			p.doctype = &d
		}
		if p.html == nil {
			p.doc.AppendChild(&Node{Type: DoctypeNode, Data: tok.data})
		}
	case tComment:
		if p.html == nil {
			p.doc.AppendChild(&Node{Type: CommentNode, Data: tok.data})
		} else {
			p.current().AppendChild(&Node{Type: CommentNode, Data: tok.data})
		}
	case tText:
		p.text(tok.data)
	case tStart:
		p.start(tok)
	case tEnd:
		p.end(tok)
	}
}

func isAllSpace(s string) bool {
	for i := 0; i < len(s); i++ {
		if !isSpace(s[i]) {
			return false
		}
	}
	return true
}

func (p *parser) text(data string) {
	if !p.inBody {
		if isAllSpace(data) {
			if p.html != nil && p.head != nil && len(p.stack) > 0 && p.current() != p.html {
				p.insertText(data)
			}
			return
		}
		// Leading whitespace is dropped before the body starts.
		if p.html == nil || p.current() == p.html || p.current() == p.head {
			data = strings.TrimLeft(data, " \t\n\r\f")
			p.ensureBody()
		}
	}
	cur := p.current()
	// Drop leading newline right after <pre>, <listing> and <textarea>.
	if (cur.Tag == "pre" || cur.Tag == "listing" || cur.Tag == "textarea") && cur.FirstChild == nil && strings.HasPrefix(data, "\n") {
		data = data[1:]
		if data == "" {
			return
		}
	}
	// Text directly in table structures: foster-parent before the table.
	if p.foreign == 0 && (cur.Tag == "table" || cur.Tag == "tbody" || cur.Tag == "thead" || cur.Tag == "tfoot" || cur.Tag == "tr") {
		if isAllSpace(data) {
			p.insertText(data)
			return
		}
		table := cur
		for table != nil && table.Tag != "table" {
			table = table.Parent
		}
		if table != nil && table.Parent != nil {
			prev := table.PrevSibling
			if prev != nil && prev.Type == TextNode {
				prev.Data += data
			} else {
				table.Parent.InsertBefore(&Node{Type: TextNode, Data: data}, table)
			}
			return
		}
	}
	p.insertText(data)
}

func (p *parser) newElement(tok token) *Node {
	return &Node{Type: ElementNode, Tag: tok.data, Attrs: tok.attrs}
}

func (p *parser) start(tok token) {
	tag := tok.data
	if p.foreign > 0 {
		n := p.newElement(tok)
		p.push(n)
		if tok.selfClosing {
			p.pop()
		}
		return
	}
	switch tag {
	case "html":
		p.ensureHTML()
		for _, a := range tok.attrs {
			if _, ok := p.html.Attr(a.Name); !ok {
				p.html.Attrs = append(p.html.Attrs, a)
			}
		}
		return
	case "head":
		if p.head == nil && !p.inBody {
			p.ensureHTML()
			p.head = p.newElement(tok)
			p.html.AppendChild(p.head)
			p.stack = []*Node{p.html, p.head}
		}
		return
	case "body":
		if p.body == nil {
			p.ensureHead()
			p.body = p.newElement(tok)
			p.html.AppendChild(p.body)
			p.inBody = true
			p.stack = []*Node{p.html, p.body}
		} else {
			for _, a := range tok.attrs {
				if _, ok := p.body.Attr(a.Name); !ok {
					p.body.Attrs = append(p.body.Attrs, a)
				}
			}
		}
		return
	}
	if !p.inBody {
		if headElements[tag] {
			p.ensureHead()
			if len(p.stack) < 2 || p.stack[len(p.stack)-1] != p.head {
				p.stack = []*Node{p.html, p.head}
			}
			n := p.newElement(tok)
			p.push(n)
			if voidElements[tag] || tok.selfClosing {
				p.pop()
			}
			return
		}
		p.ensureBody()
	}

	switch {
	case closesP[tag]:
		switch tag {
		case "li":
			if i := p.inListScope("li"); i >= 0 {
				p.popTo(i)
			}
		case "dd", "dt":
			if i := p.inScope("dd", "dt"); i >= 0 && p.inScope("dl") < i {
				p.popTo(i)
			}
		}
		// In quirks mode, tables do not close paragraphs.
		if !(tag == "table" && p.quirks()) {
			p.closeP()
		}
		if strings.HasPrefix(tag, "h") && len(tag) == 2 && tag[1] >= '1' && tag[1] <= '6' {
			cur := p.current().Tag
			if len(cur) == 2 && cur[0] == 'h' && cur[1] >= '1' && cur[1] <= '6' {
				p.pop()
			}
		}
	case tag == "option":
		if p.current().Tag == "option" {
			p.pop()
		}
	case tag == "optgroup":
		if p.current().Tag == "option" {
			p.pop()
		}
		if p.current().Tag == "optgroup" {
			p.pop()
		}
	case tag == "tr":
		if i := p.inScope("tr"); i >= 0 {
			p.popTo(i)
		}
		p.ensureTableSection()
	case tag == "td" || tag == "th":
		if i := p.inScope("td", "th"); i >= 0 {
			p.popTo(i)
		}
		cur := p.current().Tag
		if cur == "table" || cur == "tbody" || cur == "thead" || cur == "tfoot" {
			p.ensureTableSection()
			p.push(&Node{Type: ElementNode, Tag: "tr"})
		}
	case tag == "thead" || tag == "tbody" || tag == "tfoot" || tag == "caption" || tag == "colgroup":
		if i := p.inScope("table"); i >= 0 {
			p.popTo(i + 1)
		}
	case tag == "col":
		if i := p.inScope("table"); i >= 0 {
			if p.current().Tag != "colgroup" {
				p.popTo(i + 1)
			}
		}
	case tag == "a":
		if i := p.inScope("a"); i >= 0 {
			p.popTo(i)
		}
	case tag == "nobr":
		if i := p.inScope("nobr"); i >= 0 {
			p.popTo(i)
		}
	case tag == "button":
		if i := p.inScope("button"); i >= 0 {
			p.popTo(i)
		}
	case tag == "svg" || tag == "math":
		p.foreign++
		n := p.newElement(tok)
		p.push(n)
		if tok.selfClosing {
			p.pop()
		}
		return
	case tag == "image":
		tok.data = "img"
		tag = "img"
	}
	if headElements[tag] && tag != "noscript" {
		// Allowed in body too.
	}
	n := p.newElement(tok)
	p.push(n)
	if voidElements[tag] {
		p.pop()
	}
}

// ensureTableSection makes sure a row is inserted into a row group.
func (p *parser) ensureTableSection() {
	cur := p.current().Tag
	if cur == "table" {
		p.push(&Node{Type: ElementNode, Tag: "tbody"})
	} else if cur != "tbody" && cur != "thead" && cur != "tfoot" {
		if i := p.inScope("tbody", "thead", "tfoot"); i >= 0 {
			p.popTo(i + 1)
		} else if i := p.inScope("table"); i >= 0 {
			p.popTo(i + 1)
			p.push(&Node{Type: ElementNode, Tag: "tbody"})
		}
	}
}

func (p *parser) end(tok token) {
	tag := tok.data
	if p.foreign > 0 {
		for i := len(p.stack) - 1; i >= 0; i-- {
			if strings.EqualFold(p.stack[i].Tag, tag) {
				p.popTo(i)
				return
			}
			if p.stack[i].Tag == "svg" || p.stack[i].Tag == "math" {
				break
			}
		}
		return
	}
	switch tag {
	case "html", "head":
		if tag == "head" && !p.inBody && len(p.stack) > 0 && p.current() == p.head {
			p.pop()
		}
		return
	case "body":
		return
	case "p":
		if p.inScope("p") < 0 {
			p.ensureBody()
			p.push(&Node{Type: ElementNode, Tag: "p"})
		}
		p.closeP()
		return
	case "br":
		p.ensureBody()
		p.push(&Node{Type: ElementNode, Tag: "br"})
		p.pop()
		return
	case "li":
		if i := p.inListScope("li"); i >= 0 {
			p.popTo(i)
		}
		return
	}
	if voidElements[tag] {
		return
	}
	// Generic: pop to the matching element if it is open in scope.
	for i := len(p.stack) - 1; i >= 0; i-- {
		t := p.stack[i].Tag
		if t == tag {
			p.popTo(i)
			return
		}
		if scopeBoundaries[t] && !(tag == "table" || tag == "td" || tag == "th" || tag == "caption") {
			return
		}
		if (t == "table") && tag != "table" {
			return
		}
	}
}

func (p *parser) finish() {
	p.ensureBody()
	// Number elements in document order.
	i := 0
	p.doc.Walk(func(n *Node) bool {
		n.Index = i
		i++
		return true
	})
}

// DecodeBytes converts raw document bytes into a string, using the given
// encoding hint or sniffing for a BOM or <meta charset>.
func DecodeBytes(data []byte, encoding string) string {
	if len(data) >= 3 && data[0] == 0xef && data[1] == 0xbb && data[2] == 0xbf {
		return string(data[3:])
	}
	if len(data) >= 2 && ((data[0] == 0xfe && data[1] == 0xff) || (data[0] == 0xff && data[1] == 0xfe)) {
		return decodeUTF16(data[2:], data[0] == 0xfe)
	}
	enc := strings.ToLower(encoding)
	if enc == "" {
		enc = sniffCharset(data)
	}
	switch enc {
	case "iso-8859-1", "latin1", "latin-1", "windows-1252", "cp1252", "us-ascii", "ascii":
		var b strings.Builder
		for _, c := range data {
			if c >= 0x80 && c <= 0x9f && enc != "iso-8859-1" {
				if r, ok := win1252[int(c)]; ok {
					b.WriteRune(r)
					continue
				}
			}
			b.WriteRune(rune(c))
		}
		return b.String()
	case "utf-16", "utf-16be":
		return decodeUTF16(data, true)
	case "utf-16le":
		return decodeUTF16(data, false)
	}
	if !utf8.Valid(data) {
		// Fall back to windows-1252 for invalid UTF-8.
		return DecodeBytes(data, "windows-1252")
	}
	return string(data)
}

func decodeUTF16(data []byte, be bool) string {
	var b strings.Builder
	for i := 0; i+1 < len(data); i += 2 {
		var u uint16
		if be {
			u = uint16(data[i])<<8 | uint16(data[i+1])
		} else {
			u = uint16(data[i+1])<<8 | uint16(data[i])
		}
		if u >= 0xd800 && u < 0xdc00 && i+3 < len(data) {
			var u2 uint16
			if be {
				u2 = uint16(data[i+2])<<8 | uint16(data[i+3])
			} else {
				u2 = uint16(data[i+3])<<8 | uint16(data[i+2])
			}
			b.WriteRune(rune(0x10000 + (int(u)-0xd800)<<10 + (int(u2) - 0xdc00)))
			i += 2
			continue
		}
		b.WriteRune(rune(u))
	}
	return b.String()
}

func sniffCharset(data []byte) string {
	n := len(data)
	if n > 2048 {
		n = 2048
	}
	head := strings.ToLower(string(data[:n]))
	idx := strings.Index(head, "charset")
	for idx >= 0 {
		rest := head[idx+7:]
		rest = strings.TrimLeft(rest, " \t\n")
		if strings.HasPrefix(rest, "=") {
			rest = strings.TrimLeft(rest[1:], " \t\n\"'")
			end := strings.IndexAny(rest, "\"'; >/\t\n")
			if end > 0 {
				return rest[:end]
			}
		}
		next := strings.Index(head[idx+7:], "charset")
		if next < 0 {
			break
		}
		idx += 7 + next
	}
	return ""
}

// Package css implements CSS parsing, selector matching, the cascade and
// computed values, mirroring WeasyPrint's css package (and tinycss2 /
// cssselect2, the libraries it relies on).
package css

import (
	"strconv"
	"strings"
	"unicode/utf8"
)

// TokenType is the type of a CSS token or component value.
type TokenType int

const (
	TIdent TokenType = iota
	TAtKeyword
	THash
	TString
	TURL
	TDelim
	TNumber
	TPercentage
	TDimension
	TWhitespace
	TColon
	TSemicolon
	TComma
	TCDO
	TCDC
	TFunction   // Value is the lowercase name, Children the arguments
	TParenBlock // ( ... )
	TSquareBlock
	TCurlyBlock
	TBadString
	TBadURL
	TError
)

// Token is a CSS component value.
type Token struct {
	Type     TokenType
	Value    string // ident/at-keyword/hash/string/url/delim/function name (functions lowercase), unit (lowercase)
	Raw      string // original-case name for idents and functions
	Num      float64
	IsInt    bool
	IsID     bool // for hash tokens
	Children []Token
}

// Is returns whether t is an ident with the given (lowercase) value.
func (t Token) IsIdent(v string) bool { return t.Type == TIdent && t.Value == v }

// IsDelim returns whether t is the given delimiter.
func (t Token) IsDelim(d string) bool { return t.Type == TDelim && t.Value == d }

// String serializes the token back to CSS.
func (t Token) String() string {
	switch t.Type {
	case TIdent:
		return t.Raw
	case TAtKeyword:
		return "@" + t.Value
	case THash:
		return "#" + t.Value
	case TString:
		return strconv.Quote(t.Value)
	case TURL:
		return "url(" + t.Value + ")"
	case TDelim:
		return t.Value
	case TNumber:
		return formatNum(t.Num)
	case TPercentage:
		return formatNum(t.Num) + "%"
	case TDimension:
		return formatNum(t.Num) + t.Value
	case TWhitespace:
		return " "
	case TColon:
		return ":"
	case TSemicolon:
		return ";"
	case TComma:
		return ","
	case TCDO:
		return "<!--"
	case TCDC:
		return "-->"
	case TFunction:
		return t.Raw + "(" + Serialize(t.Children) + ")"
	case TParenBlock:
		return "(" + Serialize(t.Children) + ")"
	case TSquareBlock:
		return "[" + Serialize(t.Children) + "]"
	case TCurlyBlock:
		return "{" + Serialize(t.Children) + "}"
	}
	return ""
}

func formatNum(f float64) string {
	return strconv.FormatFloat(f, 'f', -1, 64)
}

// Serialize serializes a list of tokens.
func Serialize(ts []Token) string {
	var b strings.Builder
	for _, t := range ts {
		b.WriteString(t.String())
	}
	return b.String()
}

type tokenizer struct {
	s   string
	pos int
}

// Tokenize parses CSS source into a list of component values.
func Tokenize(src string) []Token {
	src = strings.ReplaceAll(src, "\r\n", "\n")
	src = strings.ReplaceAll(src, "\r", "\n")
	src = strings.ReplaceAll(src, "\f", "\n")
	src = strings.ReplaceAll(src, "\x00", "�")
	t := &tokenizer{s: src}
	toks, _ := t.parseList(0)
	return toks
}

func (t *tokenizer) peek(off int) byte {
	if t.pos+off < len(t.s) {
		return t.s[t.pos+off]
	}
	return 0
}

// parseList reads component values until the given closing character
// (0 for end of input).
func (t *tokenizer) parseList(closing byte) ([]Token, bool) {
	var out []Token
	for t.pos < len(t.s) {
		c := t.s[t.pos]
		if closing != 0 && c == closing {
			t.pos++
			return out, true
		}
		tok, ok := t.next()
		if ok {
			out = append(out, tok)
		}
	}
	return out, false
}

func isNameStart(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '_' || c >= 0x80
}

func isNameChar(c byte) bool {
	return isNameStart(c) || (c >= '0' && c <= '9') || c == '-'
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func isHex(c byte) bool {
	return isDigit(c) || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}

func (t *tokenizer) validEscape(off int) bool {
	return t.peek(off) == '\\' && t.peek(off+1) != '\n' && t.pos+off+1 < len(t.s)
}

func (t *tokenizer) startsIdent(off int) bool {
	c := t.peek(off)
	if c == '-' {
		c2 := t.peek(off + 1)
		return isNameStart(c2) || c2 == '-' || t.validEscape(off+1)
	}
	if isNameStart(c) {
		return true
	}
	return t.validEscape(off)
}

func (t *tokenizer) startsNumber() bool {
	c := t.peek(0)
	if isDigit(c) {
		return true
	}
	if c == '.' {
		return isDigit(t.peek(1))
	}
	if c == '+' || c == '-' {
		c2 := t.peek(1)
		return isDigit(c2) || (c2 == '.' && isDigit(t.peek(2)))
	}
	return false
}

func (t *tokenizer) readEscape() rune {
	// t.pos is just after the backslash.
	if t.pos >= len(t.s) {
		return 0xfffd
	}
	if isHex(t.s[t.pos]) {
		start := t.pos
		for t.pos < len(t.s) && t.pos-start < 6 && isHex(t.s[t.pos]) {
			t.pos++
		}
		n, _ := strconv.ParseInt(t.s[start:t.pos], 16, 32)
		if t.pos < len(t.s) && (t.s[t.pos] == ' ' || t.s[t.pos] == '\n' || t.s[t.pos] == '\t') {
			t.pos++
		}
		if n == 0 || n > 0x10ffff || (n >= 0xd800 && n <= 0xdfff) {
			return 0xfffd
		}
		return rune(n)
	}
	r, size := utf8.DecodeRuneInString(t.s[t.pos:])
	t.pos += size
	return r
}

func (t *tokenizer) readName() string {
	var b strings.Builder
	for t.pos < len(t.s) {
		c := t.s[t.pos]
		if isNameChar(c) {
			b.WriteByte(c)
			t.pos++
		} else if t.validEscape(0) {
			t.pos++
			b.WriteRune(t.readEscape())
		} else {
			break
		}
	}
	return b.String()
}

func (t *tokenizer) readNumber() (float64, bool) {
	start := t.pos
	isInt := true
	if c := t.peek(0); c == '+' || c == '-' {
		t.pos++
	}
	for isDigit(t.peek(0)) {
		t.pos++
	}
	if t.peek(0) == '.' && isDigit(t.peek(1)) {
		isInt = false
		t.pos++
		for isDigit(t.peek(0)) {
			t.pos++
		}
	}
	if c := t.peek(0); c == 'e' || c == 'E' {
		c2 := t.peek(1)
		if isDigit(c2) || ((c2 == '+' || c2 == '-') && isDigit(t.peek(2))) {
			isInt = false
			t.pos += 2
			for isDigit(t.peek(0)) {
				t.pos++
			}
		}
	}
	f, _ := strconv.ParseFloat(t.s[start:t.pos], 64)
	return f, isInt
}

func (t *tokenizer) next() (Token, bool) {
	s := t.s
	c := s[t.pos]
	switch {
	case c == ' ' || c == '\n' || c == '\t':
		for t.pos < len(s) && (s[t.pos] == ' ' || s[t.pos] == '\n' || s[t.pos] == '\t') {
			t.pos++
		}
		return Token{Type: TWhitespace}, true
	case c == '/' && t.peek(1) == '*':
		end := strings.Index(s[t.pos+2:], "*/")
		if end < 0 {
			t.pos = len(s)
		} else {
			t.pos += 2 + end + 2
		}
		return Token{}, false
	case c == '"' || c == '\'':
		return t.readString(c), true
	case c == '#':
		if t.pos+1 < len(s) && (isNameChar(s[t.pos+1]) || t.validEscape(1)) {
			t.pos++
			isID := t.startsIdent(0)
			name := t.readName()
			return Token{Type: THash, Value: name, IsID: isID}, true
		}
		t.pos++
		return Token{Type: TDelim, Value: "#"}, true
	case c == '(':
		t.pos++
		ch, _ := t.parseList(')')
		return Token{Type: TParenBlock, Children: ch}, true
	case c == '[':
		t.pos++
		ch, _ := t.parseList(']')
		return Token{Type: TSquareBlock, Children: ch}, true
	case c == '{':
		t.pos++
		ch, _ := t.parseList('}')
		return Token{Type: TCurlyBlock, Children: ch}, true
	case c == ')' || c == ']' || c == '}':
		t.pos++
		return Token{Type: TError, Value: string(c)}, true
	case c == ',':
		t.pos++
		return Token{Type: TComma}, true
	case c == ':':
		t.pos++
		return Token{Type: TColon}, true
	case c == ';':
		t.pos++
		return Token{Type: TSemicolon}, true
	case c == '<' && strings.HasPrefix(s[t.pos:], "<!--"):
		t.pos += 4
		return Token{Type: TCDO}, true
	case c == '-' && strings.HasPrefix(s[t.pos:], "-->"):
		t.pos += 3
		return Token{Type: TCDC}, true
	case c == '@':
		if t.startsIdent(1) {
			t.pos++
			name := t.readName()
			return Token{Type: TAtKeyword, Value: strings.ToLower(name), Raw: name}, true
		}
		t.pos++
		return Token{Type: TDelim, Value: "@"}, true
	}
	if t.startsNumber() {
		f, isInt := t.readNumber()
		if t.startsIdent(0) {
			unit := t.readName()
			return Token{Type: TDimension, Num: f, IsInt: isInt, Value: strings.ToLower(unit), Raw: unit}, true
		}
		if t.peek(0) == '%' {
			t.pos++
			return Token{Type: TPercentage, Num: f, IsInt: isInt}, true
		}
		return Token{Type: TNumber, Num: f, IsInt: isInt}, true
	}
	if t.startsIdent(0) {
		name := t.readName()
		if t.peek(0) == '(' {
			t.pos++
			lower := strings.ToLower(name)
			if lower == "url" {
				// Unquoted URL?
				p := t.pos
				for p < len(s) && (s[p] == ' ' || s[p] == '\n' || s[p] == '\t') {
					p++
				}
				if p < len(s) && s[p] != '"' && s[p] != '\'' {
					t.pos = p
					return t.readURL(), true
				}
			}
			ch, _ := t.parseList(')')
			return Token{Type: TFunction, Value: lower, Raw: name, Children: ch}, true
		}
		return Token{Type: TIdent, Value: strings.ToLower(name), Raw: name}, true
	}
	if c == '\\' {
		t.pos++
		return Token{Type: TDelim, Value: "\\"}, true
	}
	r, size := utf8.DecodeRuneInString(s[t.pos:])
	t.pos += size
	return Token{Type: TDelim, Value: string(r)}, true
}

func (t *tokenizer) readString(q byte) Token {
	t.pos++
	var b strings.Builder
	for t.pos < len(t.s) {
		c := t.s[t.pos]
		if c == q {
			t.pos++
			return Token{Type: TString, Value: b.String()}
		}
		if c == '\n' {
			return Token{Type: TBadString}
		}
		if c == '\\' {
			if t.pos+1 >= len(t.s) {
				t.pos++
				continue
			}
			if t.s[t.pos+1] == '\n' {
				t.pos += 2
				continue
			}
			t.pos++
			b.WriteRune(t.readEscape())
			continue
		}
		b.WriteByte(c)
		t.pos++
	}
	return Token{Type: TString, Value: b.String()}
}

func (t *tokenizer) readURL() Token {
	var b strings.Builder
	for t.pos < len(t.s) {
		c := t.s[t.pos]
		switch {
		case c == ')':
			t.pos++
			return Token{Type: TURL, Value: b.String()}
		case c == ' ' || c == '\n' || c == '\t':
			for t.pos < len(t.s) && (t.s[t.pos] == ' ' || t.s[t.pos] == '\n' || t.s[t.pos] == '\t') {
				t.pos++
			}
			if t.peek(0) == ')' {
				t.pos++
				return Token{Type: TURL, Value: b.String()}
			}
			t.skipBadURL()
			return Token{Type: TBadURL}
		case c == '"' || c == '\'' || c == '(':
			t.skipBadURL()
			return Token{Type: TBadURL}
		case c == '\\':
			if t.validEscape(0) {
				t.pos++
				b.WriteRune(t.readEscape())
			} else {
				t.skipBadURL()
				return Token{Type: TBadURL}
			}
		default:
			b.WriteByte(c)
			t.pos++
		}
	}
	return Token{Type: TURL, Value: b.String()}
}

func (t *tokenizer) skipBadURL() {
	for t.pos < len(t.s) {
		c := t.s[t.pos]
		t.pos++
		if c == ')' {
			return
		}
		if c == '\\' && t.pos < len(t.s) {
			t.pos++
		}
	}
}

// StripWS removes leading and trailing whitespace tokens.
func StripWS(ts []Token) []Token {
	for len(ts) > 0 && ts[0].Type == TWhitespace {
		ts = ts[1:]
	}
	for len(ts) > 0 && ts[len(ts)-1].Type == TWhitespace {
		ts = ts[:len(ts)-1]
	}
	return ts
}

// RemoveWS removes all whitespace tokens.
func RemoveWS(ts []Token) []Token {
	out := make([]Token, 0, len(ts))
	for _, t := range ts {
		if t.Type != TWhitespace {
			out = append(out, t)
		}
	}
	return out
}

// SplitOnComma splits tokens on top-level commas.
func SplitOnComma(ts []Token) [][]Token {
	var out [][]Token
	var cur []Token
	for _, t := range ts {
		if t.Type == TComma {
			out = append(out, cur)
			cur = nil
			continue
		}
		cur = append(cur, t)
	}
	out = append(out, cur)
	return out
}

// SplitOnWS splits tokens on top-level whitespace (removing it).
func SplitOnWS(ts []Token) []Token {
	return RemoveWS(ts)
}

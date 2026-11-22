package layout

import (
	"strings"
	"unicode"

	"github.com/esoteric-programmer/weasyprint-go/internal/css"
)

// ::first-letter and ::first-line.
//
// The styles of both pseudo-elements apply to the first formatted line of a
// block container, found by following first in-flow children through plain
// block boxes (like WeasyPrint, the styles of nested blocks are merged, the
// inner ones winning). The first letter is split into its own box when the
// box tree is built; the first line is laid out from a restyled copy of the
// line box (see lineIterator.next).

// firstPseudos walks the box tree and attaches the ::first-letter and
// ::first-line styles of block containers to their first line box.
func (b *Builder) firstPseudos(box *Box, letter, line []*css.Style) {
	if !box.IsBlockContainer() {
		letter, line = nil, nil
	} else if box.Element != nil && !box.Anonymous && box.Pseudo == "" {
		if s := b.R.PseudoStyle(box.Element, "first-letter"); s != nil && len(s.Declared) > 0 {
			letter = append(append([]*css.Style(nil), letter...), s)
		}
		if s := b.R.PseudoStyle(box.Element, "first-line"); s != nil && len(s.Declared) > 0 {
			line = append(append([]*css.Style(nil), line...), s)
		}
	}
	first := true
	for _, ch := range box.Children {
		if first && (ch.Kind == KLine || ch.IsInNormalFlow()) {
			first = false
			switch ch.Kind {
			case KLine:
				if len(letter) > 0 {
					b.firstLetter(ch, letter)
				}
				if len(line) > 0 {
					ch.FirstLine = line
				}
			case KBlock:
				b.firstPseudos(ch, letter, line)
				continue
			}
		}
		b.firstPseudos(ch, nil, nil)
	}
}

// mergedStyle returns an anonymous style inheriting from parent with the
// declared values of the given pseudo-element styles.
func mergedStyle(r *css.Resolver, parent *css.Style, styles []*css.Style, pseudo string) *css.Style {
	s := r.AnonymousStyle(parent)
	for _, ps := range styles {
		for _, id := range ps.Declared {
			s.Set(id, ps.Get(id))
		}
	}
	s.Pseudo = pseudo
	if pseudo == "first-letter" {
		// Like WeasyPrint, position is ignored on ::first-letter.
		s.Set(css.PPosition, css.Keyword("static"))
	}
	return s
}

// firstLetterText returns the first typographic letter unit of s: leading
// punctuation, one character and the punctuation following it.
func firstLetterText(s string) string {
	found := false
	for i, r := range s {
		if unicode.In(r, unicode.Ps, unicode.Pe, unicode.Pi, unicode.Pf, unicode.Po) {
			continue
		}
		if found {
			return s[:i]
		}
		found = true
	}
	return s
}

type letterResult uint8

const (
	letterNone    letterResult = iota // nothing found, go on with siblings
	letterDone                        // first letter box created
	letterBlocked                     // other content comes first
)

// firstLetter splits the first letter of the content of parent into a
// ::first-letter box.
func (b *Builder) firstLetter(parent *Box, styles []*css.Style) letterResult {
	for i := 0; i < len(parent.Children); i++ {
		ch := parent.Children[i]
		switch {
		case ch.RunningName != "" || ch.IsFloated() || ch.IsAbsolutelyPositioned():
			continue
		case ch.Kind == KInline && ch.Pseudo == "marker":
			continue
		case ch.Kind == KInline:
			if r := b.firstLetter(ch, styles); r != letterNone {
				return r
			}
			continue
		case ch.Kind != KText:
			return letterBlocked
		}
		text := ch.Text
		if isCollapsible(ch.Style.WhiteSpace()) {
			text = strings.TrimLeft(text, " \n")
		}
		if text == "" {
			continue
		}
		letter := firstLetterText(text)
		if strings.TrimLeft(letter, "\n") == "" {
			return letterBlocked
		}
		ls := mergedStyle(b.R, parent.Style, styles, "first-letter")
		ts := b.R.AnonymousStyle(ls)
		tb := &Box{Kind: KText, Style: ts, Element: ch.Element, Anonymous: true, RawText: letter,
			Text: transformText(letter, ts.Kw(css.PTextTransform), langCode(ts), capitalSharpS(b.Shaper, ts))}
		var lb *Box
		if ls.IsFloat() {
			ls.Set(css.PDisplay, css.Keyword("block"))
			lb = &Box{Kind: KBlock, Style: ls, Element: ch.Element, Pseudo: "first-letter",
				Children: []*Box{{Kind: KLine, Style: ts, Element: ch.Element, Anonymous: true, Children: []*Box{tb}}}}
		} else {
			ls.Set(css.PDisplay, css.Keyword("inline"))
			lb = &Box{Kind: KInline, Style: ls, Element: ch.Element, Pseudo: "first-letter", Children: []*Box{tb}}
		}
		lb.FirstLetter = styles
		children := append([]*Box(nil), parent.Children[:i]...)
		children = append(children, lb)
		if rest := text[len(letter):]; rest != "" {
			rb := ch.Copy()
			rb.Orig = nil
			rb.Text = rest
			if rb.RawText != "" {
				// Keep the untransformed text aligned with the remaining text.
				rr, n := []rune(rb.RawText), len([]rune(rest))
				if len(rr) == len([]rune(ch.Text)) {
					rb.RawText = string(rr[len(rr)-n:])
				} else {
					rb.RawText = ""
				}
			}
			children = append(children, rb)
		}
		parent.Children = append(children, parent.Children[i+1:]...)
		return letterDone
	}
	return letterNone
}

// firstLineClone returns a copy of a line box whose content is restyled to
// inherit from its ::first-line pseudo-element, wrapped in an inline box
// carrying the pseudo-element's own properties.
func (c *Context) firstLineClone(src *Box) *Box {
	if cl, ok := c.flClones[src]; ok {
		return cl
	}
	ws := mergedStyle(c.R, src.Style, src.FirstLine, "first-line")
	ws.Set(css.PDisplay, css.Keyword("inline"))
	wrapper := &Box{Kind: KInline, Style: ws, Element: src.Element, Pseudo: "first-line", Anonymous: true}
	wrapper.Children = c.restyleChildren(src.Children, src.Style, ws)
	cl := src.CopyWith([]*Box{wrapper})
	cl.FirstLine = nil
	c.flClones[src] = cl
	return cl
}

func (c *Context) restyleChildren(children []*Box, oldParent, newParent *css.Style) []*Box {
	out := make([]*Box, len(children))
	for i, ch := range children {
		out[i] = c.restyleBox(ch, oldParent, newParent)
	}
	return out
}

// restyleBox copies an inline-level box and its content with styles
// recomputed under a new parent style. Atomic and out-of-flow boxes are
// kept as they are.
func (c *Context) restyleBox(b *Box, oldParent, newParent *css.Style) *Box {
	if b.Kind != KText && b.Kind != KInline {
		return b
	}
	if b.IsFloated() || b.IsAbsolutelyPositioned() || b.RunningName != "" {
		return b
	}
	var ns *css.Style
	switch {
	case b.Style == oldParent:
		ns = newParent
	case b.FirstLetter != nil:
		ns = mergedStyle(c.R, newParent, b.FirstLetter, "first-letter")
		ns.Set(css.PDisplay, css.Keyword("inline"))
	case b.Element != nil && !b.Anonymous && b.Kind == KInline:
		ns = c.R.Restyle(b.Element, b.Pseudo, newParent)
		if ns.Kw(css.PDisplay) != b.Style.Kw(css.PDisplay) {
			ns = b.Style
		}
	case b.Anonymous:
		ns = c.R.AnonymousStyle(newParent)
	default:
		return b
	}
	nb := b.Copy()
	nb.Style = ns
	if b.Kind == KText {
		oldT, newT := b.Style.Kw(css.PTextTransform), ns.Kw(css.PTextTransform)
		if oldT != newT {
			raw := b.RawText
			if raw == "" {
				raw = b.Text
			}
			// Positions in the line must not move: keep the same length.
			if t := transformText(raw, newT, langCode(nb.Style), capitalSharpS(c.Shaper, nb.Style)); len([]rune(t)) == len([]rune(b.Text)) {
				nb.Text = t
			}
		}
	}
	nb.Children = c.restyleChildren(b.Children, b.Style, ns)
	return nb
}

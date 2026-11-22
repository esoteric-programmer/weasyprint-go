package css

import (
	"github.com/esoteric-programmer/weasyprint-go/internal/html"
)

// Style is a set of computed values.
type Style struct {
	vals    []Value
	Custom  map[string][]Token
	Pseudo  string
	Element *html.Node
	// IsRoot is true for the root element style.
	IsRoot bool
	// Declared lists the properties set by the cascade, for ::first-line
	// and ::first-letter styles.
	Declared []PropID
}

// LinkValue is the computed value of the link property.
type LinkValue struct {
	Internal bool
	Target   string // anchor name or absolute URL
}

// Get returns the computed value of a property.
func (s *Style) Get(id PropID) Value { return s.vals[id] }

// Set sets a computed value.
func (s *Style) Set(id PropID, v Value) { s.vals[id] = v }

// Copy returns a shallow copy.
func (s *Style) Copy() *Style {
	c := *s
	c.vals = append([]Value(nil), s.vals...)
	return &c
}

// Kw returns a keyword value, or "".
func (s *Style) Kw(id PropID) string {
	if k, ok := s.vals[id].(Keyword); ok {
		return string(k)
	}
	return ""
}

// Len returns a length value; ok is false for keywords.
func (s *Style) Len(id PropID) (Length, bool) {
	l, ok := s.vals[id].(Length)
	return l, ok
}

// IsAuto reports whether the value is the auto keyword.
func (s *Style) IsAuto(id PropID) bool { return s.Kw(id) == "auto" }

// Px returns a length resolved against a percentage base; keywords give 0.
func (s *Style) Px(id PropID, base float64) float64 {
	if l, ok := s.vals[id].(Length); ok {
		return l.Resolve(base)
	}
	return 0
}

// Num returns a number value.
func (s *Style) Num(id PropID) float64 {
	switch v := s.vals[id].(type) {
	case Number:
		return float64(v)
	case Length:
		return v.V
	}
	return 0
}

// Color returns a color value.
func (s *Style) Color(id PropID) Color {
	if c, ok := s.vals[id].(Color); ok {
		return c
	}
	if id != PColor {
		return s.Color(PColor)
	}
	return Color{R: 0, G: 0, B: 0, A: 1}
}

// FontSize returns the computed font size in px.
func (s *Style) FontSize() float64 { return s.Px(PFontSize, 0) }

// FontFamily returns the font family list.
func (s *Style) FontFamily() []string {
	if f, ok := s.vals[PFontFamily].([]string); ok {
		return f
	}
	return []string{"serif"}
}

// Display returns the display keyword.
func (s *Style) Display() string { return s.Kw(PDisplay) }

// Position returns the position keyword ("running" for running elements).
func (s *Style) Position() string {
	if _, ok := s.vals[PPosition].(Running); ok {
		return "running"
	}
	return s.Kw(PPosition)
}

// RunningName returns the name of a running element.
func (s *Style) RunningName() string {
	if r, ok := s.vals[PPosition].(Running); ok {
		return string(r)
	}
	return ""
}

// WhiteSpace returns the white-space keyword.
func (s *Style) WhiteSpace() string { return s.Kw(PWhiteSpace) }

// LineHeight returns the used line height for the style's font size.
// For "normal", 1.2 is used unless metric is provided.
func (s *Style) LineHeight(normal float64) float64 {
	switch v := s.vals[PLineHeight].(type) {
	case Number:
		return float64(v) * s.FontSize()
	case Length:
		return v.Resolve(s.FontSize())
	}
	return normal
}

// Content returns the content items (nil for normal/none).
func (s *Style) Content() []ContentItem {
	if c, ok := s.vals[PContent].([]ContentItem); ok {
		return c
	}
	return nil
}

// Counters returns counter operations for a counter property.
func (s *Style) Counters(id PropID) []CounterOp {
	if c, ok := s.vals[id].([]CounterOp); ok {
		return c
	}
	return nil
}

// Opacity returns the opacity.
func (s *Style) Opacity() float64 {
	if n, ok := s.vals[POpacity].(Number); ok {
		return float64(n)
	}
	return 1
}

// BorderStyle returns the border style of a side (0=top,1=right,2=bottom,3=left).
func (s *Style) BorderStyle(side int) string {
	return s.Kw([]PropID{PBorderTopStyle, PBorderRightStyle, PBorderBottomStyle, PBorderLeftStyle}[side])
}

// BorderWidth returns the border width of a side.
func (s *Style) BorderWidth(side int) float64 {
	return s.Px([]PropID{PBorderTopWidth, PBorderRightWidth, PBorderBottomWidth, PBorderLeftWidth}[side], 0)
}

// BorderColor returns the border color of a side.
func (s *Style) BorderColor(side int) Color {
	return s.Color([]PropID{PBorderTopColor, PBorderRightColor, PBorderBottomColor, PBorderLeftColor}[side])
}

// Radius returns a corner radius (0=top-left,1=top-right,2=bottom-right,3=bottom-left).
func (s *Style) Radius(corner int) [2]Length {
	if r, ok := s.vals[[]PropID{PBorderTopLeftRadius, PBorderTopRightRadius, PBorderBottomRightRadius, PBorderBottomLeftRadius}[corner]].([2]Length); ok {
		return r
	}
	return [2]Length{Px(0), Px(0)}
}

// IsFloat reports whether the box floats.
func (s *Style) IsFloat() bool {
	f := s.Kw(PFloat)
	return f == "left" || f == "right"
}

// IsAbsolute reports whether the box is absolutely positioned.
func (s *Style) IsAbsolute() bool {
	p := s.Kw(PPosition)
	return p == "absolute" || p == "fixed"
}

// TextDecorations returns the text decoration lines.
func (s *Style) TextDecorations() []string {
	if l, ok := s.vals[PTextDecorationLine].([]string); ok {
		return l
	}
	return nil
}

// Link returns the link target, if any.
func (s *Style) Link() *LinkValue {
	if l, ok := s.vals[PLink].(LinkValue); ok {
		return &l
	}
	return nil
}

// Anchor returns the anchor name, if any.
func (s *Style) Anchor() string {
	if a, ok := s.vals[PAnchor].(Str); ok {
		return string(a)
	}
	return ""
}

// TextAlign returns the used text-align for normal lines.
func (s *Style) TextAlign() string {
	a := s.Kw(PTextAlignAll)
	rtl := s.Kw(PDirection) == "rtl"
	switch a {
	case "start", "match-parent":
		if rtl {
			return "right"
		}
		return "left"
	case "end":
		if rtl {
			return "left"
		}
		return "right"
	}
	return a
}

// TextAlignLast returns the used text-align for the last line.
func (s *Style) TextAlignLast() string {
	a := s.Kw(PTextAlignLast)
	if a == "auto" {
		al := s.TextAlign()
		if al == "justify" {
			if s.Kw(PDirection) == "rtl" {
				return "right"
			}
			return "left"
		}
		return al
	}
	rtl := s.Kw(PDirection) == "rtl"
	switch a {
	case "start", "match-parent":
		if rtl {
			return "right"
		}
		return "left"
	case "end":
		if rtl {
			return "left"
		}
		return "right"
	}
	return a
}

// PageName returns the page name or "".
func (s *Style) PageName() string {
	if p, ok := s.vals[PPage].(Str); ok {
		return string(p)
	}
	return ""
}

// BookmarkLevel returns the bookmark level, or 0.
func (s *Style) BookmarkLevel() int {
	if n, ok := s.vals[PBookmarkLevel].(Number); ok {
		return int(n)
	}
	return 0
}

// Transforms returns the transform list.
func (s *Style) Transforms() []Transform {
	if t, ok := s.vals[PTransform].([]Transform); ok {
		return t
	}
	return nil
}

// Package layout builds the box tree from styled HTML and lays it out into
// pages, mirroring WeasyPrint's formatting_structure and layout packages.
package layout

import (
	"github.com/esoteric-programmer/weasyprint-go/internal/css"
	"github.com/esoteric-programmer/weasyprint-go/internal/html"
	"github.com/esoteric-programmer/weasyprint-go/internal/images"
	"github.com/esoteric-programmer/weasyprint-go/internal/text"
)

// Kind is the type of a box.
type Kind uint8

const (
	KBlock Kind = iota
	KInline
	KInlineBlock
	KText
	KLine
	KReplaced
	KTable
	KTableWrapper
	KRowGroup
	KRow
	KCell
	KColGroup
	KCol
	KCaption
	KFlex
	KPage
	KMargin
	KLeader
	KGrid
)

var kindNames = map[Kind]string{
	KBlock: "Block", KInline: "Inline", KInlineBlock: "InlineBlock", KText: "Text",
	KLine: "Line", KReplaced: "Replaced", KTable: "Table", KTableWrapper: "TableWrapper",
	KRowGroup: "RowGroup", KRow: "Row", KCell: "Cell", KColGroup: "ColGroup",
	KCol: "Col", KCaption: "Caption", KFlex: "Flex", KPage: "Page", KMargin: "Margin",
	KLeader: "Leader", KGrid: "Grid",
}

func (k Kind) String() string { return kindNames[k] }

// StringSetValue is a string-set assignment carried by a box.
type StringSetValue struct {
	Name  string
	Value string
}

// Box is a box of the formatting structure.
type Box struct {
	Kind      Kind
	Style     *css.Style
	Element   *html.Node
	Pseudo    string
	Anonymous bool
	IsInline  bool // inline-level atomic box (replaced, inline-table, inline-flex)
	Children  []*Box

	// Geometry, in CSS px, in page coordinates. X/Y is the margin-box
	// top-left corner; W/H is the content box size.
	X, Y           float64
	W, H           float64
	MT, MR, MB, ML float64
	PT, PR, PB, PL float64
	BT, BR, BB, BL float64
	// Baseline offset from Y, for inline-level boxes and lines.
	Baseline    float64
	HasBaseline bool

	// Text boxes.
	Text           string
	Shaped         *text.Shaped
	Start, End     int
	Hyphen         string // text appended when broken at a soft hyphen
	HyphenW        float64
	TabOrigin      float64 // origin of tab stops (start of the line)
	JustifySpacing float64
	Spec           text.Spec
	Metrics        text.Metrics

	// Replaced boxes.
	Image *images.Image

	// Fragmentation: decorations not drawn at the start / end.
	NoStartDecor, NoEndDecor bool

	// Outside list marker.
	Marker *Box

	// Tables.
	Colspan, Rowspan int
	GridX            int
	IsHeader         bool
	IsFooter         bool
	ColumnWidths     []float64
	ColumnPositions  []float64
	Span             int
	Collapse         *collapsedBorders
	RowHeights       []float64
	CellEmpty        bool
	CellBroken       bool // cell fragmented at a page break

	// Navigation.
	Link          *css.LinkValue
	Anchor        string
	BookmarkLabel string
	BookmarkLevel int
	BookmarkOpen  bool
	StringSets    []StringSetValue

	// Running element name, for boxes taken out of the flow.
	RunningName string
	// Deferred content (target-counter etc) to evaluate after layout.
	Deferred []css.ContentItem
	// Leader string, for leader boxes.
	Leader string
	// LeaderAnchor is the distance from the end of a leader box to the
	// point its pattern is aligned on.
	LeaderAnchor float64
	// EllipsisRight is the end of the available width of a line box whose
	// overflowing text ends with an ellipsis (text-overflow: ellipsis), or 0.
	EllipsisRight float64
	// FirstLine lists the ::first-line styles applying to the first line of
	// a line box (outer elements first).
	FirstLine []*css.Style
	// FirstLetter lists the ::first-letter styles a first-letter box was
	// built from (outer elements first).
	FirstLetter []*css.Style
	// RawText is the text of a text box before text-transform.
	RawText string
	// Footnote and float bookkeeping.
	IsFloatPlaceholder bool
	// Footnote called by a ::footnote-call box.
	Footnote *Box
	// Original box this one was derived from.
	Orig *Box
	// Clearance applied to a block.
	Clearance float64
	// Placeholder for an absolutely positioned box, laid out once its
	// containing block is known.
	IsAbsPlaceholder bool
	// Static position of an absolutely positioned box.
	StaticX, StaticY float64
	// Laid out from the original box (used to repeat fixed boxes).
	AbsSource *Box
	// Outside list marker.
	IsOutsideMarker bool
	// Column of a multi-column container.
	IsColumn bool
	// Line boxes: resume position, for orphans/widows handling.
	lineResume Skip
	// Index of the box in its parent before layout.
	index int
}

// Copy returns a shallow copy with its own children slice.
func (b *Box) Copy() *Box {
	c := *b
	c.Children = append([]*Box(nil), b.Children...)
	if c.Orig == nil {
		c.Orig = b
	}
	return &c
}

// CopyWith returns a copy with the given children.
func (b *Box) CopyWith(children []*Box) *Box {
	c := *b
	c.Children = children
	if c.Orig == nil {
		c.Orig = b
	}
	return &c
}

// Original returns the box this one was copied from (or itself).
func (b *Box) Original() *Box {
	if b.Orig != nil {
		return b.Orig
	}
	return b
}

func (b *Box) PaddingWidth() float64  { return b.W + b.PL + b.PR }
func (b *Box) PaddingHeight() float64 { return b.H + b.PT + b.PB }
func (b *Box) BorderWidth() float64   { return b.PaddingWidth() + b.BL + b.BR }
func (b *Box) BorderHeight() float64  { return b.PaddingHeight() + b.BT + b.BB }
func (b *Box) MarginWidth() float64   { return b.BorderWidth() + b.ML + b.MR }
func (b *Box) MarginHeight() float64  { return b.BorderHeight() + b.MT + b.MB }

func (b *Box) BorderBoxX() float64  { return b.X + b.ML }
func (b *Box) BorderBoxY() float64  { return b.Y + b.MT }
func (b *Box) PaddingBoxX() float64 { return b.BorderBoxX() + b.BL }
func (b *Box) PaddingBoxY() float64 { return b.BorderBoxY() + b.BT }
func (b *Box) ContentX() float64    { return b.PaddingBoxX() + b.PL }
func (b *Box) ContentY() float64    { return b.PaddingBoxY() + b.PT }

// Translate moves the box and its descendants.
func (b *Box) Translate(dx, dy float64) {
	if dx == 0 && dy == 0 {
		return
	}
	b.X += dx
	b.Y += dy
	for _, c := range b.Children {
		c.Translate(dx, dy)
	}
	if b.Marker != nil {
		b.Marker.Translate(dx, dy)
	}
}

// IsBlockLevel reports whether the box participates in a block formatting
// context as a block-level box.
func (b *Box) IsBlockLevel() bool {
	switch b.Kind {
	case KBlock, KTableWrapper, KFlex, KReplaced, KTable, KGrid:
		return !b.IsInline
	case KCaption, KRowGroup, KRow, KCell, KColGroup, KCol:
		return false
	}
	return false
}

// IsInlineLevel reports whether the box is inline-level.
func (b *Box) IsInlineLevel() bool {
	switch b.Kind {
	case KInline, KText, KInlineBlock, KLeader:
		return true
	case KReplaced, KTableWrapper, KFlex, KTable, KGrid:
		return b.IsInline
	}
	return false
}

// IsAtomicInline reports whether the box is an atomic inline-level box.
func (b *Box) IsAtomicInline() bool {
	switch b.Kind {
	case KInlineBlock:
		return true
	case KReplaced, KTableWrapper, KFlex, KTable, KGrid:
		return b.IsInline
	}
	return false
}

// IsBlockContainer reports whether the box contains blocks or lines.
func (b *Box) IsBlockContainer() bool {
	switch b.Kind {
	case KBlock, KInlineBlock, KCell, KCaption, KPage, KMargin:
		return true
	}
	return false
}

// IsOutOfFlow reports whether the box is floated or absolutely positioned.
func (b *Box) IsOutOfFlow() bool {
	if b.Style == nil || b.Kind == KText || b.Kind == KLine {
		return false
	}
	return b.Style.IsFloat() || b.Style.IsAbsolute()
}

// IsFloated reports whether the box floats.
func (b *Box) IsFloated() bool {
	return b.Style != nil && b.Kind != KText && b.Kind != KLine && b.Style.IsFloat()
}

// IsAbsolutelyPositioned reports whether the box is absolutely positioned.
func (b *Box) IsAbsolutelyPositioned() bool {
	return b.Style != nil && b.Kind != KText && b.Kind != KLine && b.Style.IsAbsolute()
}

// IsInNormalFlow reports whether the box is in the normal flow.
func (b *Box) IsInNormalFlow() bool { return !b.IsOutOfFlow() && b.RunningName == "" }

// IsTablePart reports whether the box is an internal table box.
func (b *Box) IsTablePart() bool {
	switch b.Kind {
	case KRowGroup, KRow, KCell, KColGroup, KCol, KCaption:
		return true
	}
	return false
}

// EstablishesBFC reports whether the box establishes a new block
// formatting context.
func (b *Box) EstablishesBFC() bool {
	if b.Style == nil {
		return false
	}
	switch b.Kind {
	case KInlineBlock, KCell, KCaption, KFlex, KTableWrapper, KTable, KPage, KMargin, KGrid:
		return true
	case KBlock:
		if b.Style.IsFloat() || b.Style.IsAbsolute() || b.Style.Kw(css.POverflow) != "visible" || b.Style.Display() == "flow-root" {
			return true
		}
	}
	return false
}

// AllText returns the concatenated text of a box subtree.
func (b *Box) AllText() string {
	if b.Kind == KText {
		if b.Shaped != nil {
			return string(b.Shaped.Runes[b.Start:b.End])
		}
		return b.Text
	}
	var s string
	for _, c := range b.Children {
		s += c.AllText()
	}
	return s
}

// Descendants calls f on b and all descendants (pre-order).
func (b *Box) Descendants(f func(*Box)) {
	f(b)
	for _, c := range b.Children {
		c.Descendants(f)
	}
	if b.Marker != nil {
		b.Marker.Descendants(f)
	}
}

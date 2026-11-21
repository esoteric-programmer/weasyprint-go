// Package html implements a tolerant HTML parser producing a simple DOM.
package html

import "strings"

// NodeType is the type of a DOM node.
type NodeType int

const (
	DocumentNode NodeType = iota
	ElementNode
	TextNode
	CommentNode
	DoctypeNode
)

// Attr is an element attribute.
type Attr struct {
	Name  string
	Value string
}

// Node is a DOM node.
type Node struct {
	Type  NodeType
	Tag   string // lowercase tag name for elements
	Attrs []Attr
	Data  string // text or comment content

	Parent, FirstChild, LastChild, PrevSibling, NextSibling *Node

	// Index is the position of this element in document order, set by
	// the parser; it can be used as a stable identifier.
	Index int
}

// Attr returns an attribute value.
func (n *Node) Attr(name string) (string, bool) {
	for _, a := range n.Attrs {
		if a.Name == name {
			return a.Value, true
		}
	}
	return "", false
}

// Get returns an attribute value, or "".
func (n *Node) Get(name string) string {
	v, _ := n.Attr(name)
	return v
}

// SetAttr sets an attribute.
func (n *Node) SetAttr(name, value string) {
	for i, a := range n.Attrs {
		if a.Name == name {
			n.Attrs[i].Value = value
			return
		}
	}
	n.Attrs = append(n.Attrs, Attr{name, value})
}

// AppendChild appends c as the last child of n.
func (n *Node) AppendChild(c *Node) {
	if c.Parent != nil {
		c.Parent.RemoveChild(c)
	}
	c.Parent = n
	c.PrevSibling = n.LastChild
	c.NextSibling = nil
	if n.LastChild != nil {
		n.LastChild.NextSibling = c
	} else {
		n.FirstChild = c
	}
	n.LastChild = c
}

// InsertBefore inserts c before ref (or appends if ref is nil).
func (n *Node) InsertBefore(c, ref *Node) {
	if ref == nil {
		n.AppendChild(c)
		return
	}
	if c.Parent != nil {
		c.Parent.RemoveChild(c)
	}
	c.Parent = n
	c.NextSibling = ref
	c.PrevSibling = ref.PrevSibling
	if ref.PrevSibling != nil {
		ref.PrevSibling.NextSibling = c
	} else {
		n.FirstChild = c
	}
	ref.PrevSibling = c
}

// RemoveChild removes c from n.
func (n *Node) RemoveChild(c *Node) {
	if c.PrevSibling != nil {
		c.PrevSibling.NextSibling = c.NextSibling
	} else {
		n.FirstChild = c.NextSibling
	}
	if c.NextSibling != nil {
		c.NextSibling.PrevSibling = c.PrevSibling
	} else {
		n.LastChild = c.PrevSibling
	}
	c.Parent, c.PrevSibling, c.NextSibling = nil, nil, nil
}

// Children returns the element children of n.
func (n *Node) Children() []*Node {
	var out []*Node
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == ElementNode {
			out = append(out, c)
		}
	}
	return out
}

// ParentElement returns the parent if it is an element.
func (n *Node) ParentElement() *Node {
	if n.Parent != nil && n.Parent.Type == ElementNode {
		return n.Parent
	}
	return nil
}

// PrevElement returns the previous element sibling.
func (n *Node) PrevElement() *Node {
	for c := n.PrevSibling; c != nil; c = c.PrevSibling {
		if c.Type == ElementNode {
			return c
		}
	}
	return nil
}

// NextElement returns the next element sibling.
func (n *Node) NextElement() *Node {
	for c := n.NextSibling; c != nil; c = c.NextSibling {
		if c.Type == ElementNode {
			return c
		}
	}
	return nil
}

// TextContent returns the concatenated text of all descendants.
func (n *Node) TextContent() string {
	if n.Type == TextNode {
		return n.Data
	}
	var b strings.Builder
	var walk func(*Node)
	walk = func(x *Node) {
		for c := x.FirstChild; c != nil; c = c.NextSibling {
			if c.Type == TextNode {
				b.WriteString(c.Data)
			} else if c.Type == ElementNode {
				walk(c)
			}
		}
	}
	walk(n)
	return b.String()
}

// Walk calls f for n and all its descendants in document order. If f
// returns false, the children of that node are skipped.
func (n *Node) Walk(f func(*Node) bool) {
	if !f(n) {
		return
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		c.Walk(f)
	}
}

// Root returns the root element (html) of a document node.
func (n *Node) Root() *Node {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == ElementNode {
			return c
		}
	}
	return nil
}

// Find returns the first descendant element with the given tag name.
func (n *Node) Find(tag string) *Node {
	var found *Node
	n.Walk(func(x *Node) bool {
		if found != nil {
			return false
		}
		if x.Type == ElementNode && x.Tag == tag && x != n {
			found = x
			return false
		}
		return true
	})
	return found
}

// FindAll returns all descendant elements with the given tag name.
func (n *Node) FindAll(tag string) []*Node {
	var out []*Node
	n.Walk(func(x *Node) bool {
		if x.Type == ElementNode && x.Tag == tag && x != n {
			out = append(out, x)
		}
		return true
	})
	return out
}

// Lang returns the language of an element, inherited from ancestors.
func (n *Node) Lang() string {
	for x := n; x != nil; x = x.Parent {
		if x.Type != ElementNode {
			continue
		}
		if v, ok := x.Attr("lang"); ok {
			return v
		}
		if v, ok := x.Attr("xml:lang"); ok {
			return v
		}
	}
	return ""
}

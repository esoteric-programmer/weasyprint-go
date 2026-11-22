package css

import "strings"

// Logical properties (margin-block-start, inline-size...). They are
// stored as properties of their own and used for the physical property
// they map to (for horizontal writing and the element's direction) when
// they come later in the cascade.

// logicalPhysical maps logical properties to their physical properties for
// left-to-right (index 0) and right-to-left (index 1) directions.
var logicalPhysical = map[PropID][2]PropID{}

// physicalLogical maps physical properties to their logical properties
// for each direction.
var physicalLogical = map[PropID][2]PropID{}

func init() {
	physical := func(name string) PropID {
		id, ok := LookupProp(name)
		if !ok {
			panic("unknown property " + name)
		}
		return id
	}
	add := func(logical string, ltr, rtl string) {
		p := Props[physical(ltr)]
		id := def(logical, p.Inherited, p.Initial, p.Parse)
		l, r := physical(ltr), physical(rtl)
		logicalPhysical[id] = [2]PropID{l, r}
		m := physicalLogical[l]
		m[0] = id
		physicalLogical[l] = m
		m = physicalLogical[r]
		m[1] = id
		physicalLogical[r] = m
	}
	for _, prefix := range []string{"margin-", "padding-", "inset-"} {
		phys := prefix
		if prefix == "inset-" {
			phys = ""
		}
		add(prefix+"block-start", phys+"top", phys+"top")
		add(prefix+"block-end", phys+"bottom", phys+"bottom")
		add(prefix+"inline-start", phys+"left", phys+"right")
		add(prefix+"inline-end", phys+"right", phys+"left")
	}
	for _, suffix := range []string{"-width", "-style", "-color"} {
		add("border-block-start"+suffix, "border-top"+suffix, "border-top"+suffix)
		add("border-block-end"+suffix, "border-bottom"+suffix, "border-bottom"+suffix)
		add("border-inline-start"+suffix, "border-left"+suffix, "border-right"+suffix)
		add("border-inline-end"+suffix, "border-right"+suffix, "border-left"+suffix)
	}
	for _, prefix := range []string{"", "min-", "max-"} {
		add(prefix+"inline-size", prefix+"width", prefix+"width")
		add(prefix+"block-size", prefix+"height", prefix+"height")
	}
	add("border-start-start-radius", "border-top-left-radius", "border-top-right-radius")
	add("border-start-end-radius", "border-top-right-radius", "border-top-left-radius")
	add("border-end-start-radius", "border-bottom-left-radius", "border-bottom-right-radius")
	add("border-end-end-radius", "border-bottom-right-radius", "border-bottom-left-radius")

	// Shorthands.
	two := func(name string, first, second string) {
		a, b := physical(first), physical(second)
		parse := Props[a].Parse
		shorthand(name, []PropID{a, b}, func(ts []Token, ctx *ParseCtx) ([]PropValue, bool) {
			if len(ts) < 1 || len(ts) > 2 {
				return nil, false
			}
			v1 := parse(ts[:1], ctx)
			v2 := v1
			if len(ts) == 2 {
				v2 = parse(ts[1:], ctx)
			}
			if v1 == nil || v2 == nil {
				return nil, false
			}
			return []PropValue{{a, v1}, {b, v2}}, true
		})
	}
	for _, p := range []string{"margin", "padding", "inset"} {
		two(p+"-block", p+"-block-start", p+"-block-end")
		two(p+"-inline", p+"-inline-start", p+"-inline-end")
	}
	for _, suffix := range []string{"-width", "-style", "-color"} {
		two("border-block"+suffix, "border-block-start"+suffix, "border-block-end"+suffix)
		two("border-inline"+suffix, "border-inline-start"+suffix, "border-inline-end"+suffix)
	}
	side := func(name string, sides ...string) {
		var ids []PropID
		for _, s := range sides {
			for _, suffix := range []string{"-width", "-style", "-color"} {
				ids = append(ids, physical(s+suffix))
			}
		}
		shorthand(name, ids, func(ts []Token, ctx *ParseCtx) ([]PropValue, bool) {
			w, st, c, ok := parseBorderParts(ts, ctx)
			if !ok {
				return nil, false
			}
			var out []PropValue
			for i := 0; i < len(ids); i += 3 {
				out = append(out, PropValue{ids[i], w}, PropValue{ids[i+1], st}, PropValue{ids[i+2], c})
			}
			return out, true
		})
	}
	for _, s := range []string{"border-block-start", "border-block-end", "border-inline-start", "border-inline-end"} {
		side(s, s)
	}
	side("border-block", "border-block-start", "border-block-end")
	side("border-inline", "border-inline-start", "border-inline-end")
}

// expandLogicalFour expands "margin: logical a b c d" (block-start,
// inline-start, block-end, inline-end), like WeasyPrint.
func expandLogicalFour(name string, ts []Token, ctx *ParseCtx) ([]PropValue, bool) {
	var ids []PropID
	for _, s := range []string{"block-start", "inline-start", "block-end", "inline-end"} {
		n := name + "-" + s
		if name == "inset" {
			n = "inset-" + s
		} else if strings.HasPrefix(name, "border-") {
			n = "border-" + s + strings.TrimPrefix(name, "border")
		}
		id, ok := LookupProp(n)
		if !ok {
			return nil, false
		}
		ids = append(ids, id)
	}
	return expandFour(ids, Props[ids[0]].Parse)(ts, ctx)
}

package layout

import (
	"fmt"
	"io"
	"strings"
)

// Dump writes a textual representation of a box tree (for debugging).
func Dump(w io.Writer, b *Box, depth int) {
	name := b.Kind.String()
	if b.Element != nil {
		name += " " + b.Element.Tag
	}
	if b.Pseudo != "" {
		name += "::" + b.Pseudo
	}
	extra := ""
	if b.Kind == KText && b.Shaped != nil {
		t := string(b.Shaped.Runes[b.Start:b.End])
		if len(t) > 30 {
			t = t[:30] + "…"
		}
		extra = fmt.Sprintf(" %q", t)
	}
	fmt.Fprintf(w, "%s%s x=%.2f y=%.2f w=%.2f h=%.2f m=(%.1f %.1f %.1f %.1f) p=(%.1f %.1f %.1f %.1f) b=(%.1f %.1f %.1f %.1f) bl=%.2f%s\n",
		strings.Repeat("  ", depth), name, b.X, b.Y, b.W, b.H, b.MT, b.MR, b.MB, b.ML, b.PT, b.PR, b.PB, b.PL, b.BT, b.BR, b.BB, b.BL, b.Baseline, extra)
	for _, ch := range b.Children {
		Dump(w, ch, depth+1)
	}
	if b.Marker != nil {
		Dump(w, b.Marker, depth+1)
	}
}

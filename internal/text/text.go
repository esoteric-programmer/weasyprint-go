// Package text shapes text into positioned glyphs with font fallback and
// computes line break opportunities.
package text

import (
	"strings"
	"sync"
	"unicode"

	"github.com/esoteric-programmer/weasyprint-go/internal/fonts"
)

// Spec describes the font and spacing used to shape text.
type Spec struct {
	Families      []string
	Weight        int
	Italic        bool
	Stretch       int
	Size          float64 // px
	LetterSpacing float64
	WordSpacing   float64
	SmallCaps     bool
	NoKerning     bool
	NoLigatures   bool
}

func (s Spec) key() string {
	var b strings.Builder
	b.WriteString(strings.Join(s.Families, ","))
	b.WriteByte('|')
	b.WriteString(itoa(s.Weight))
	if s.Italic {
		b.WriteString("i")
	}
	b.WriteString(itoa(s.Stretch))
	return b.String()
}

func itoa(i int) string {
	const digits = "0123456789"
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{digits[i%10]}, b...)
		i /= 10
	}
	return string(b)
}

// Glyph is a positioned glyph.
type Glyph struct {
	ID      uint16
	Face    *fonts.Face
	Size    float64 // font size in px
	Advance float64 // px, including spacing and kerning
	Start   int     // rune range in the shaped text
	End     int
}

// Shaped is the result of shaping a text.
type Shaped struct {
	Runes  []rune
	Glyphs []Glyph
	Pos    []float64 // x position before rune i; len(Runes)+1
	Spec   Spec
}

// Width returns the advance width of runes [start, end).
func (s *Shaped) Width(start, end int) float64 {
	return s.Pos[end] - s.Pos[start]
}

// Shaper shapes text using a font library.
type Shaper struct {
	Lib   *fonts.Library
	mu    sync.Mutex
	faces map[string][]*fonts.Face
}

// NewShaper returns a shaper.
func NewShaper(lib *fonts.Library) *Shaper {
	return &Shaper{Lib: lib, faces: map[string][]*fonts.Face{}}
}

// Faces returns the faces for a spec, one per available family.
func (s *Shaper) Faces(spec Spec) []*fonts.Face {
	key := spec.key()
	s.mu.Lock()
	if f, ok := s.faces[key]; ok {
		s.mu.Unlock()
		return f
	}
	s.mu.Unlock()
	req := fonts.Request{Weight: spec.Weight, Italic: spec.Italic, Stretch: spec.Stretch}
	var out []*fonts.Face
	seen := map[*fonts.Font]bool{}
	for _, fam := range spec.Families {
		if f := s.Lib.Resolve(fam, req); f != nil && !seen[f.Font] {
			seen[f.Font] = true
			out = append(out, f)
		}
	}
	if len(out) == 0 {
		if f := s.Lib.Resolve("serif", req); f != nil {
			out = append(out, f)
		}
	}
	s.mu.Lock()
	s.faces[key] = out
	s.mu.Unlock()
	return out
}

// Metrics are font metrics in px.
type Metrics struct {
	Ascent, Descent, LineGap float64
	XHeight, CapHeight       float64
	UnderlinePos             float64 // positive below baseline
	UnderlineThick           float64
	StrikeoutPos             float64 // positive above baseline
	StrikeoutThick           float64
}

// PrimaryMetrics returns the metrics of the first available face.
func (s *Shaper) PrimaryMetrics(spec Spec) Metrics {
	faces := s.Faces(spec)
	if len(faces) == 0 {
		return Metrics{Ascent: spec.Size * 0.8, Descent: spec.Size * 0.2, XHeight: spec.Size * 0.5, CapHeight: spec.Size * 0.7,
			UnderlinePos: spec.Size * 0.1, UnderlineThick: spec.Size / 16, StrikeoutPos: spec.Size * 0.3, StrikeoutThick: spec.Size / 16}
	}
	return FaceMetrics(faces[0].Font, spec.Size)
}

// FaceMetrics returns the metrics of a font at a size.
func FaceMetrics(f *fonts.Font, size float64) Metrics {
	k := size / f.UnitsPerEm
	return Metrics{
		Ascent:         f.Ascent * k,
		Descent:        f.Descent * k,
		LineGap:        f.LineGap * k,
		XHeight:        f.XHeight * k,
		CapHeight:      f.CapHeight * k,
		UnderlinePos:   -f.UnderlinePos * k,
		UnderlineThick: f.UnderlineThick * k,
		StrikeoutPos:   f.StrikeoutPos * k,
		StrikeoutThick: f.StrikeoutSize * k,
	}
}

func isDefaultIgnorable(r rune) bool {
	switch {
	case r == 0x00ad, r == 0x034f, r == 0x061c, r == 0x115f, r == 0x1160,
		r == 0x17b4, r == 0x17b5, r >= 0x180b && r <= 0x180f,
		r >= 0x200b && r <= 0x200f, r >= 0x202a && r <= 0x202e,
		r >= 0x2060 && r <= 0x206f, r == 0x3164, r >= 0xfe00 && r <= 0xfe0f,
		r == 0xfeff, r == 0xffa0, r >= 0xfff0 && r <= 0xfff8:
		return true
	}
	return false
}

// Shape shapes text.
func (s *Shaper) Shape(text string, spec Spec) *Shaped {
	runes := []rune(text)
	sh := &Shaped{Runes: runes, Pos: make([]float64, len(runes)+1), Spec: spec}
	if len(runes) == 0 {
		return sh
	}
	faces := s.Faces(spec)
	req := fonts.Request{Weight: spec.Weight, Italic: spec.Italic, Stretch: spec.Stretch}
	// Choose a face for each rune.
	runeFace := make([]*fonts.Face, len(runes))
	for i, r := range runes {
		var face *fonts.Face
		if isDefaultIgnorable(r) || r == '\n' || r == '\r' || r == '\t' {
			if i > 0 {
				face = runeFace[i-1]
			} else if len(faces) > 0 {
				face = faces[0]
			}
		} else {
			for _, f := range faces {
				if f.Font.HasRune(r) && (f.Info == nil || f.Info.Covers(r)) {
					face = f
					break
				}
			}
			// Combining marks stay with the base character's face.
			if face == nil && i > 0 && unicode.Is(unicode.Mn, r) && runeFace[i-1] != nil && runeFace[i-1].Font.HasRune(r) {
				face = runeFace[i-1]
			}
			if face == nil {
				face = s.Lib.Fallback(r, req)
			}
			if face == nil && len(faces) > 0 {
				face = faces[0]
			}
		}
		runeFace[i] = face
	}
	x := 0.0
	for i := 0; i < len(runes); {
		r := runes[i]
		face := runeFace[i]
		size := spec.Size
		cr := r
		if spec.SmallCaps && unicode.IsLower(r) {
			up := unicode.ToUpper(r)
			if face != nil && face.Font.HasRune(up) {
				cr = up
				size = spec.Size * 0.7
			}
		}
		g := Glyph{Face: face, Size: size, Start: i, End: i + 1}
		if face != nil && !isDefaultIgnorable(r) && r != '\n' && r != '\r' {
			if r == '\t' {
				g.ID = face.Font.GlyphIndex(' ')
			} else {
				g.ID = face.Font.GlyphIndex(cr)
			}
			if r == 0xa0 && g.ID == 0 {
				g.ID = face.Font.GlyphIndex(' ')
			}
			g.Advance = face.Font.Advance(g.ID) * size / face.Font.UnitsPerEm
			// Ligatures.
			if !spec.NoLigatures && spec.LetterSpacing == 0 && !spec.SmallCaps {
				if ligs := face.Font.Ligatures(g.ID); len(ligs) > 0 {
					for _, lig := range ligs {
						n := len(lig.Components)
						if i+1+n > len(runes) {
							continue
						}
						ok := true
						for k, comp := range lig.Components {
							rr := runes[i+1+k]
							if runeFace[i+1+k] != face || face.Font.GlyphIndex(rr) != comp {
								ok = false
								break
							}
						}
						if ok {
							g.ID = lig.Glyph
							g.End = i + 1 + n
							g.Advance = face.Font.Advance(g.ID) * size / face.Font.UnitsPerEm
							break
						}
					}
				}
			}
		} else {
			g.Advance = 0
		}
		if r != '\n' && !isDefaultIgnorable(r) {
			g.Advance += spec.LetterSpacing
		}
		if r == ' ' || r == 0xa0 || r == '\t' {
			g.Advance += spec.WordSpacing
		}
		// Kerning with the previous glyph.
		if !spec.NoKerning && len(sh.Glyphs) > 0 {
			prev := &sh.Glyphs[len(sh.Glyphs)-1]
			if prev.Face == face && face != nil && prev.Size == size && prev.ID != 0 && g.ID != 0 {
				if k := face.Font.Kern(prev.ID, g.ID); k != 0 {
					adj := k * size / face.Font.UnitsPerEm
					prev.Advance += adj
					x += adj
					sh.Pos[i] = x
				}
			}
		}
		n := g.End - g.Start
		for k := 0; k < n; k++ {
			sh.Pos[i+k] = x + g.Advance*float64(k)/float64(n)
		}
		x += g.Advance
		sh.Glyphs = append(sh.Glyphs, g)
		i = g.End
	}
	sh.Pos[len(runes)] = x
	return sh
}

// GlyphsInRange returns the glyphs covering runes [start, end).
func (s *Shaped) GlyphsInRange(start, end int) []Glyph {
	var out []Glyph
	for _, g := range s.Glyphs {
		if g.Start >= start && g.End <= end {
			out = append(out, g)
		} else if g.Start < end && g.End > start {
			// Ligature cut by the range: keep it if it starts inside.
			if g.Start >= start {
				out = append(out, g)
			}
		}
	}
	return out
}

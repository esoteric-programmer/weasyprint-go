// Package text shapes text into positioned glyphs with font fallback and
// computes line break opportunities.
package text

import (
	"github.com/esoteric-programmer/weasyprint-go/internal/bidi"
	"math"
	"strconv"
	"strings"
	"sync"
	"unicode"

	"github.com/esoteric-programmer/weasyprint-go/internal/fonts"
	"github.com/esoteric-programmer/weasyprint-go/internal/otl"
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
	// Caps is the font-variant-caps value ("" or "normal" for none):
	// the font's features are used, or small caps are emulated like
	// Pango does.
	Caps string
	// Features are OpenType feature settings (font-variant-* and
	// font-feature-settings), applied after the default features.
	Features []otl.Feature
	// FamilyFeatures are features for faces of some families (lowercase
	// names), from font-variant-alternates and @font-feature-values.
	FamilyFeatures map[string][]otl.Feature
	// Variations are font-variation-settings axis values;
	// NoOpticalSizing is font-optical-sizing: none.
	Variations      map[string]float64
	NoOpticalSizing bool
	RTL             bool   // base direction of the text
	Lang            string // language of the text
}

func (s Spec) key() string {
	var b strings.Builder
	b.WriteString(strings.Join(s.Families, ","))
	b.WriteByte('|')
	b.WriteString(itoa(s.Weight))
	if s.RTL {
		b.WriteString("r")
	}
	b.WriteString(s.Lang)
	if s.Italic {
		b.WriteString("i")
	}
	b.WriteString(itoa(s.Stretch))
	// Variable fonts: optical size and variation settings.
	b.WriteString("|" + strconv.FormatFloat(s.Size, 'g', 6, 64))
	if s.NoOpticalSizing {
		b.WriteString("n")
	}
	b.WriteString(fonts.VariationKey(s.Variations))
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
	// XOff and YOff offset the glyph from its pen position (px, y up),
	// for attached marks.
	XOff, YOff float64
	// Start and End are the rune range of the glyph's cluster in the
	// shaped text: all the glyphs of a cluster have the same range.
	Start int
	End   int
	// Char is the character of the glyph in clusters of several glyphs
	// (0 if unknown), for text extraction.
	Char rune
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
	Lib         *fonts.Library
	mu          sync.Mutex
	faces       map[string][]*fonts.Face
	strong      map[string][]bool
	scriptFaces map[string][]*fonts.Face
	fallbacks   map[string]*fonts.Face
}

// NewShaper returns a shaper.
func NewShaper(lib *fonts.Library) *Shaper {
	return &Shaper{Lib: lib, faces: map[string][]*fonts.Face{}, strong: map[string][]bool{},
		scriptFaces: map[string][]*fonts.Face{}, fallbacks: map[string]*fonts.Face{}}
}

// fallback finds a face for a rune that the faces of a spec do not cover:
// emoji fonts for emoji, then the families of the spec in fontconfig order,
// then any font covering the rune.
func (s *Shaper) fallback(spec Spec, r rune, req fonts.Request) *fonts.Face {
	key := spec.key() + "|" + string(r)
	s.mu.Lock()
	if f, ok := s.fallbacks[key]; ok {
		s.mu.Unlock()
		return f
	}
	s.mu.Unlock()
	var face *fonts.Face
	if isEmojiPresentation(r) {
		if fams, ok := s.Lib.OrderedFamilies([]string{"emoji"}); ok {
			face = s.Lib.FallbackInFamilies(fams, r, req)
		}
	}
	if face == nil {
		if fams, ok := s.Lib.OrderedFamilies(spec.Families); ok {
			face = s.Lib.FallbackInFamilies(fams, r, req)
		}
	}
	if face == nil {
		face = s.Lib.Fallback(r, req)
	}
	face = vary(face, spec)
	s.mu.Lock()
	s.fallbacks[key] = face
	s.mu.Unlock()
	return face
}

// isEmojiPresentation reports whether a rune is displayed as an emoji by
// default (Emoji_Presentation).
func isEmojiPresentation(r rune) bool {
	switch {
	case r >= 0x1f300 && r <= 0x1f64f, r >= 0x1f680 && r <= 0x1f6ff, r >= 0x1f900 && r <= 0x1faff,
		r >= 0x1f1e6 && r <= 0x1f1ff, r == 0x1f004, r == 0x1f0cf, r == 0x1f18e, r >= 0x1f191 && r <= 0x1f19a,
		r >= 0x1f201 && r <= 0x1f251:
		return true
	}
	switch r {
	case 0x231a, 0x231b, 0x23e9, 0x23ea, 0x23eb, 0x23ec, 0x23f0, 0x23f3, 0x25fd, 0x25fe, 0x2614, 0x2615,
		0x267f, 0x2693, 0x26a1, 0x26aa, 0x26ab, 0x26bd, 0x26be, 0x26c4, 0x26c5, 0x26ce, 0x26d4, 0x26ea,
		0x26f2, 0x26f3, 0x26f5, 0x26fa, 0x26fd, 0x2705, 0x270a, 0x270b, 0x2728, 0x274c, 0x274e, 0x2753,
		0x2754, 0x2755, 0x2757, 0x2795, 0x2796, 0x2797, 0x27b0, 0x27bf, 0x2b1b, 0x2b1c, 0x2b50, 0x2b55:
		return true
	}
	return r >= 0x2648 && r <= 0x2653
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
	var strong []bool
	seen := map[*fonts.FaceInfo]bool{}
	if ordered, ok := s.Lib.OrderedFamilyValues(spec.Families); ok {
		// Families in fontconfig order, as Pango selects them. Their
		// fonts are loaded when used.
		for _, fam := range ordered {
			if f := s.Lib.ResolveLazy(fam.Name, req); f != nil && !seen[f.Info] {
				seen[f.Info] = true
				out = append(out, vary(f, spec))
				strong = append(strong, fam.Strong)
				if len(out) >= 12 {
					break
				}
			}
		}
	} else {
		for _, fam := range spec.Families {
			if f := s.Lib.Resolve(fam, req); f != nil && !seen[f.Info] {
				seen[f.Info] = true
				out = append(out, vary(f, spec))
			}
		}
	}
	if len(out) == 0 {
		if f := s.Lib.Resolve("serif", req); f != nil {
			out = append(out, vary(f, spec))
		}
	}
	for len(strong) < len(out) {
		strong = append(strong, true)
	}
	s.mu.Lock()
	s.faces[key] = out
	s.strong[key] = strong
	s.mu.Unlock()
	return out
}

// facesForScript returns the faces of a spec sorted for a script run.
func (s *Shaper) facesForScript(spec Spec, sc int) []*fonts.Face {
	faces := s.Faces(spec)
	key := spec.key() + "|" + itoa(sc)
	s.mu.Lock()
	if f, ok := s.scriptFaces[key]; ok {
		s.mu.Unlock()
		return f
	}
	strong := s.strong[spec.key()]
	s.mu.Unlock()
	out := faces
	if len(strong) == len(faces) {
		out = sortForScript(faces, strong, sc)
	}
	s.mu.Lock()
	s.scriptFaces[key] = out
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
	// Like WeasyPrint's strut (the layout of a space): the first face for
	// the script of the language.
	faces := s.facesForScript(spec, langScript(spec.Lang))
	if len(faces) == 0 {
		return Metrics{Ascent: spec.Size * 0.8, Descent: spec.Size * 0.2, XHeight: spec.Size * 0.5, CapHeight: spec.Size * 0.7,
			UnderlinePos: spec.Size * 0.1, UnderlineThick: spec.Size / 16, StrikeoutPos: spec.Size * 0.3, StrikeoutThick: spec.Size / 16}
	}
	if f := firstLoaded(faces); f != nil {
		return FaceMetrics(f.Font, spec.Size)
	}
	return Metrics{Ascent: spec.Size * 0.8, Descent: spec.Size * 0.2, XHeight: spec.Size * 0.5, CapHeight: spec.Size * 0.7,
		UnderlinePos: spec.Size * 0.1, UnderlineThick: spec.Size / 16, StrikeoutPos: spec.Size * 0.3, StrikeoutThick: spec.Size / 16}
}

// firstLoaded returns the first face whose font can be loaded.
func firstLoaded(faces []*fonts.Face) *fonts.Face {
	for _, f := range faces {
		if f.Loaded() {
			return f
		}
	}
	return nil
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
	// Bidi levels: like Pango items, runs of one level choose their fonts
	// separately.
	var levels []uint8
	if spec.RTL || bidi.HasRTL(runes) {
		base := 0
		if spec.RTL {
			base = 1
		}
		levels = bidi.Levels(runes, base)
	}
	sameRun := func(i int) bool { return levels == nil || levels[i] == levels[i-1] }
	// Scripts are resolved within each bidi level run (Pango items).
	var runScript []int
	if levels == nil {
		runScript = runScripts(runes, spec.Lang)
	} else {
		runScript = make([]int, 0, len(runes))
		for a := 0; a < len(runes); {
			b := a + 1
			for b < len(runes) && levels[b] == levels[a] {
				b++
			}
			runScript = append(runScript, runScripts(runes[a:b], spec.Lang)...)
			a = b
		}
	}
	// Choose a face for each rune.
	runeFace := make([]*fonts.Face, len(runes))
	for i, r := range runes {
		var face *fonts.Face
		if isDefaultIgnorable(r) || r == '\n' || r == '\r' || r == '\t' {
			if i > 0 {
				face = runeFace[i-1]
			} else {
				face = firstLoaded(faces)
			}
		} else if i > 0 && runeFace[i-1] != nil && isNonGraphic(r) && sameRun(i) && runeFace[i-1].Font.HasRune(r) {
			// Like Pango, spaces keep the font of the preceding text.
			face = runeFace[i-1]
		} else {
			for _, f := range s.facesForScript(spec, runScript[i]) {
				if f.HasRune(r) && (f.Info == nil || f.Info.Covers(r)) && f.Loaded() {
					face = f
					break
				}
			}
			// Combining marks stay with the base character's face.
			if face == nil && i > 0 && unicode.Is(unicode.Mn, r) && runeFace[i-1] != nil && runeFace[i-1].Font.HasRune(r) {
				face = runeFace[i-1]
			}
			if face == nil {
				face = s.fallback(spec, r, req)
			}
			if face == nil {
				face = firstLoaded(faces)
			}
		}
		runeFace[i] = face
	}
	s.clusterFaces(spec, runes, runeFace, sameRun, runScript)
	s.shapeRuns(sh, runeFace, levels, runScript)
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

// isNonGraphic reports whether a rune is not "graphic" in GLib's sense
// (spaces, separators and controls), for which Pango keeps the current
// font.
func isNonGraphic(r rune) bool {
	return unicode.In(r, unicode.Zs, unicode.Zl, unicode.Zp, unicode.Cc, unicode.Cf)
}

// pangoUnits rounds a length to Pango units (1/1024 px), as glyph
// positions from HarfBuzz are in Pango.
func pangoUnits(v float64) float64 {
	return math.Round(v*1024) / 1024
}

// vary returns the instance of a variable face for a spec: the weight,
// width, italic and optical size axes follow the font properties (as
// CSS Fonts 4 and fontconfig set them), then font-variation-settings.
func vary(f *fonts.Face, spec Spec) *fonts.Face {
	if f == nil || f.Info == nil || len(f.Info.Axes) == 0 {
		return f
	}
	fi := f.Info
	v := map[string]float64{}
	if _, _, ok := fi.AxisRange("wght"); ok {
		w := spec.Weight
		if w == 0 {
			w = 400
		}
		v["wght"] = float64(w)
	}
	if _, _, ok := fi.AxisRange("wdth"); ok {
		v["wdth"] = fonts.StretchPercent(spec.Stretch)
	}
	if _, _, ok := fi.AxisRange("opsz"); ok && !spec.NoOpticalSizing && spec.Size > 0 {
		v["opsz"] = spec.Size
	}
	if spec.Italic {
		if _, hi, ok := fi.AxisRange("ital"); ok && hi >= 1 {
			v["ital"] = 1
		} else if lo, _, ok := fi.AxisRange("slnt"); ok && !fi.Italic {
			v["slnt"] = math.Max(lo, -14)
		}
	}
	for k, val := range spec.Variations {
		v[k] = val
	}
	return f.WithVariations(v)
}

package draw

import (
	_ "embed"
	"encoding/binary"
	"errors"
	"strings"
	"unicode/utf16"
)

// srgbProfile is the sRGB ICC profile used for the "srgb" output intent,
// the same file as WeasyPrint's (sRGB2014.icc, from the International
// Color Consortium, freely redistributable).
//
//go:embed sRGB2014.icc
var srgbProfile []byte

// ColorProfile is an ICC color profile, from the srgb output intent or an
// @color-profile rule.
type ColorProfile struct {
	Data []byte
	// Name is the model or description of the profile, used as output
	// condition identifier.
	Name string
	// Components is the number of color components.
	Components int
}

// ParseICC reads the name and number of components of an ICC profile.
func ParseICC(data []byte) (*ColorProfile, error) {
	if len(data) < 132 || string(data[36:40]) != "acsp" {
		return nil, errors.New("not an ICC profile")
	}
	p := &ColorProfile{Data: data}
	switch strings.TrimSpace(string(data[16:20])) {
	case "GRAY":
		p.Components = 1
	case "CMYK":
		p.Components = 4
	default:
		p.Components = 3
	}
	tags := map[string][]byte{}
	n := int(binary.BigEndian.Uint32(data[128:132]))
	for i := 0; i < n && 144+12*i <= len(data); i++ {
		e := data[132+12*i:]
		off, size := int(binary.BigEndian.Uint32(e[4:8])), int(binary.BigEndian.Uint32(e[8:12]))
		if off >= 0 && size >= 0 && off+size <= len(data) {
			tags[string(e[:4])] = data[off : off+size]
		}
	}
	// Like Pillow's profile.model or profile_description.
	p.Name = iccText(tags["dmdd"])
	if p.Name == "" {
		p.Name = iccText(tags["desc"])
	}
	return p, nil
}

// iccText decodes a textDescriptionType, multiLocalizedUnicodeType or
// textType tag.
func iccText(t []byte) string {
	if len(t) < 12 {
		return ""
	}
	switch string(t[:4]) {
	case "desc":
		n := int(binary.BigEndian.Uint32(t[8:12]))
		if 12+n > len(t) {
			return ""
		}
		return strings.TrimRight(string(t[12:12+n]), "\x00")
	case "mluc":
		if len(t) < 28 {
			return ""
		}
		l, off := int(binary.BigEndian.Uint32(t[20:24])), int(binary.BigEndian.Uint32(t[24:28]))
		if off+l > len(t) {
			return ""
		}
		u := make([]uint16, l/2)
		for i := range u {
			u[i] = binary.BigEndian.Uint16(t[off+2*i:])
		}
		return strings.TrimRight(string(utf16.Decode(u)), "\x00")
	case "text":
		return strings.TrimRight(string(t[8:]), "\x00")
	}
	return ""
}

// SRGBProfile returns the sRGB profile.
func SRGBProfile() *ColorProfile {
	p, _ := ParseICC(srgbProfile)
	return p
}

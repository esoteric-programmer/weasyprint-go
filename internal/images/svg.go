package images

import "errors"

// SVG is a parsed SVG image.
type SVG struct {
	Width, Height float64
	ViewBox       [4]float64
	fetch         func(string) *Image
}

// ParseSVG parses an SVG document.
func ParseSVG(data []byte, url string) (*SVG, error) {
	return nil, errors.New("SVG images are not supported yet")
}

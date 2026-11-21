// Package images decodes raster images for embedding in PDF files.
package images

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/draw"
	_ "image/gif" // register GIF
	"image/jpeg"
	_ "image/png" // register PNG
	"strings"
	"sync"

	"github.com/esoteric-programmer/weasyprint-go/internal/fetch"
)

// Image is a decoded image.
type Image struct {
	URL    string
	Width  int // intrinsic size in pixels
	Height int

	// JPEG images are embedded as-is.
	JPEG       []byte
	Components int  // 1 gray, 3 RGB, 4 CMYK
	InvertCMYK bool // Adobe CMYK JPEGs

	// Other images are decoded to 8-bit samples.
	Pixels []byte // Components samples per pixel
	Alpha  []byte // nil if opaque

	// SVG images.
	SVG *SVG

	// Ref caches the PDF object reference once written.
	Ref any
}

// IntrinsicSize returns the intrinsic size in CSS px.
func (im *Image) IntrinsicSize() (float64, float64, bool) {
	if im.SVG != nil {
		return im.SVG.Width, im.SVG.Height, im.SVG.Width > 0 && im.SVG.Height > 0
	}
	return float64(im.Width), float64(im.Height), true
}

// Ratio returns the intrinsic aspect ratio (width / height).
func (im *Image) Ratio() float64 {
	if im.SVG != nil {
		if im.SVG.Width > 0 && im.SVG.Height > 0 {
			return im.SVG.Width / im.SVG.Height
		}
		if im.SVG.ViewBox[2] > 0 && im.SVG.ViewBox[3] > 0 {
			return im.SVG.ViewBox[2] / im.SVG.ViewBox[3]
		}
		return 0
	}
	if im.Height == 0 {
		return 1
	}
	return float64(im.Width) / float64(im.Height)
}

// Decode decodes image data.
func Decode(data []byte, mimeType, url string) (*Image, error) {
	trimmed := bytes.TrimSpace(data)
	if strings.Contains(mimeType, "svg") || bytes.HasPrefix(trimmed, []byte("<svg")) || (bytes.HasPrefix(trimmed, []byte("<?xml")) && bytes.Contains(data[:min(len(data), 1024)], []byte("<svg"))) {
		svg, err := ParseSVG(data, url)
		if err != nil {
			return nil, err
		}
		return &Image{URL: url, SVG: svg}, nil
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	im := &Image{URL: url, Width: cfg.Width, Height: cfg.Height}
	if format == "jpeg" {
		im.JPEG = data
		switch cfg.ColorModel {
		case color.GrayModel:
			im.Components = 1
		case color.CMYKModel:
			im.Components = 4
			im.InvertCMYK = bytes.Contains(data[:min(len(data), 4096)], []byte("Adobe"))
		default:
			im.Components = 3
		}
		// Progressive or unusual JPEGs are fine in PDF; validate decoding.
		if _, err := jpeg.DecodeConfig(bytes.NewReader(data)); err != nil {
			return nil, err
		}
		return im, nil
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w == 0 || h == 0 {
		return nil, errors.New("empty image")
	}
	gray := false
	switch img.ColorModel() {
	case color.GrayModel, color.Gray16Model:
		gray = true
	}
	if gray {
		im.Components = 1
		im.Pixels = make([]byte, w*h)
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				c := color.GrayModel.Convert(img.At(b.Min.X+x, b.Min.Y+y)).(color.Gray)
				im.Pixels[y*w+x] = c.Y
			}
		}
		return im, nil
	}
	rgba := image.NewNRGBA(image.Rect(0, 0, w, h))
	draw.Draw(rgba, rgba.Bounds(), img, b.Min, draw.Src)
	im.Components = 3
	im.Pixels = make([]byte, 3*w*h)
	alpha := make([]byte, w*h)
	opaque := true
	for i := 0; i < w*h; i++ {
		im.Pixels[3*i] = rgba.Pix[4*i]
		im.Pixels[3*i+1] = rgba.Pix[4*i+1]
		im.Pixels[3*i+2] = rgba.Pix[4*i+2]
		a := rgba.Pix[4*i+3]
		alpha[i] = a
		if a != 255 {
			opaque = false
		}
	}
	if !opaque {
		im.Alpha = alpha
	}
	return im, nil
}

// Cache loads and caches images by URL.
type Cache struct {
	mu      sync.Mutex
	images  map[string]*Image
	failed  map[string]bool
	Fetcher fetch.Fetcher
	Warn    func(string)
}

// NewCache returns an image cache.
func NewCache(f fetch.Fetcher) *Cache {
	return &Cache{images: map[string]*Image{}, failed: map[string]bool{}, Fetcher: f}
}

// Get returns the image at url, or nil if it cannot be loaded.
func (c *Cache) Get(url string) *Image {
	c.mu.Lock()
	defer c.mu.Unlock()
	if im, ok := c.images[url]; ok {
		return im
	}
	if c.failed[url] {
		return nil
	}
	res, err := c.Fetcher(url)
	if err != nil {
		c.failed[url] = true
		if c.Warn != nil {
			c.Warn("Failed to load image at " + shorten(url) + ": " + err.Error())
		}
		return nil
	}
	im, err := Decode(res.Data, res.MimeType, url)
	if err != nil {
		c.failed[url] = true
		if c.Warn != nil {
			c.Warn("Failed to load image at " + shorten(url) + ": " + err.Error())
		}
		return nil
	}
	if im.SVG != nil {
		im.SVG.fetch = c.Get
	}
	c.images[url] = im
	return im
}

func shorten(s string) string {
	if len(s) > 80 {
		return s[:77] + "..."
	}
	return s
}

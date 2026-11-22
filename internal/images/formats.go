package images

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"io"
	"slices"
	"sort"
)

// Decoders of the rarer raster formats read by Pillow (and thus by
// WeasyPrint). Formats with a signature are registered with the image
// package; the others are tried in order by sniffers.

type bytesDecoder func(data []byte) (image.Image, error)

// registerBytes registers a decoder working on the whole file.
func registerBytes(name, magic string, dec bytesDecoder) {
	image.RegisterFormat(name, magic, func(r io.Reader) (image.Image, error) {
		data, err := io.ReadAll(r)
		if err != nil {
			return nil, err
		}
		return dec(data)
	}, func(r io.Reader) (image.Config, error) {
		data, err := io.ReadAll(r)
		if err != nil {
			return image.Config{}, err
		}
		img, err := dec(data)
		if err != nil {
			return image.Config{}, err
		}
		b := img.Bounds()
		return image.Config{ColorModel: img.ColorModel(), Width: b.Dx(), Height: b.Dy()}, nil
	})
}

// sniffer is a decoder of a format without a reliable signature.
type sniffer struct {
	name   string
	accept func(data []byte) bool
	decode bytesDecoder
}

// snifferOrder is the order in which Pillow tries the plugins of the
// formats without a reliable signature.
var snifferOrder = []string{"dib", "gbr", "im", "imt", "mcidas", "pcd", "spider", "tga", "xbm"}

// sniffers are tried in order, like Pillow's plugins, when no registered
// format matches.
var sniffers []sniffer

func decodeSniffed(data []byte) (image.Image, error) {
	sort.SliceStable(sniffers, func(i, j int) bool {
		return slices.Index(snifferOrder, sniffers[i].name) < slices.Index(snifferOrder, sniffers[j].name)
	})
	for _, s := range sniffers {
		if s.accept(data) {
			if img, err := s.decode(data); err == nil {
				return img, nil
			}
		}
	}
	return nil, image.ErrFormat
}

var errCorrupt = errors.New("corrupt image data")

func le16(d []byte, o int) int { return int(binary.LittleEndian.Uint16(d[o:])) }
func le32(d []byte, o int) int { return int(binary.LittleEndian.Uint32(d[o:])) }
func be16(d []byte, o int) int { return int(binary.BigEndian.Uint16(d[o:])) }
func be32(d []byte, o int) int { return int(binary.BigEndian.Uint32(d[o:])) }

// checkSize rejects empty and huge images.
func checkSize(w, h int) error {
	if w <= 0 || h <= 0 || w > 1<<16 || h > 1<<16 || w*h > 1<<28 {
		return errors.New("invalid image size")
	}
	return nil
}

func newNRGBA(w, h int) *image.NRGBA { return image.NewNRGBA(image.Rect(0, 0, w, h)) }
func newGray(w, h int) *image.Gray   { return image.NewGray(image.Rect(0, 0, w, h)) }

// grayAlpha returns an NRGBA image from gray and alpha planes.
func grayAlpha(w, h int, g, a []byte) *image.NRGBA {
	img := newNRGBA(w, h)
	for i := 0; i < w*h; i++ {
		img.Pix[4*i], img.Pix[4*i+1], img.Pix[4*i+2], img.Pix[4*i+3] = g[i], g[i], g[i], a[i]
	}
	return img
}

// paletted returns an NRGBA image of palette indices.
func paletted(w, h int, idx []byte, pal []color.NRGBA) *image.NRGBA {
	img := newNRGBA(w, h)
	for i := 0; i < w*h && i < len(idx); i++ {
		var c color.NRGBA
		if int(idx[i]) < len(pal) {
			c = pal[idx[i]]
		} else {
			c = color.NRGBA{A: 255}
		}
		img.Pix[4*i], img.Pix[4*i+1], img.Pix[4*i+2], img.Pix[4*i+3] = c.R, c.G, c.B, c.A
	}
	return img
}

// rgbPalette reads n RGB triplets.
func rgbPalette(d []byte, n int) []color.NRGBA {
	pal := make([]color.NRGBA, 256)
	for i := 0; i < n && 3*i+2 < len(d) && i < 256; i++ {
		pal[i] = color.NRGBA{d[3*i], d[3*i+1], d[3*i+2], 255}
	}
	for i := n; i < 256; i++ {
		pal[i] = color.NRGBA{A: 255}
	}
	return pal
}

// flipV flips rows of a buffer with the given stride in place.
func flipV(pix []byte, stride, h int) {
	tmp := make([]byte, stride)
	for y := 0; y < h/2; y++ {
		a, b := pix[y*stride:(y+1)*stride], pix[(h-1-y)*stride:(h-y)*stride]
		copy(tmp, a)
		copy(a, b)
		copy(b, tmp)
	}
}

func hasPrefix(d []byte, p string) bool { return bytes.HasPrefix(d, []byte(p)) }

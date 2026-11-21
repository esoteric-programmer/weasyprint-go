package images

import (
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"io"
	"math/bits"
)

// BMP decoder: Windows and OS/2 bitmaps with 1, 2, 4, 8, 16, 24 and 32 bits
// per pixel, RLE4/RLE8 compression and bit fields (with alpha).

func init() {
	image.RegisterFormat("bmp", "BM", decodeBMP, decodeBMPConfig)
}

type bmpHeader struct {
	offset         int
	width, height  int
	topDown        bool
	bpp            int
	compression    uint32
	masks          [4]uint32
	palette        color.Palette
	headerSize     int
	hasAlphaMask   bool
	paletteEntries int
}

func readBMPHeader(data []byte) (*bmpHeader, error) {
	if len(data) < 26 || data[0] != 'B' || data[1] != 'M' {
		return nil, errors.New("bmp: invalid header")
	}
	h := &bmpHeader{offset: int(binary.LittleEndian.Uint32(data[10:]))}
	h.headerSize = int(binary.LittleEndian.Uint32(data[14:]))
	d := data[14:]
	if h.headerSize == 12 {
		// OS/2 BITMAPCOREHEADER.
		h.width = int(binary.LittleEndian.Uint16(d[4:]))
		h.height = int(int16(binary.LittleEndian.Uint16(d[6:])))
		h.bpp = int(binary.LittleEndian.Uint16(d[10:]))
	} else {
		if len(d) < 40 {
			return nil, errors.New("bmp: truncated header")
		}
		h.width = int(int32(binary.LittleEndian.Uint32(d[4:])))
		h.height = int(int32(binary.LittleEndian.Uint32(d[8:])))
		h.bpp = int(binary.LittleEndian.Uint16(d[14:]))
		h.compression = binary.LittleEndian.Uint32(d[16:])
		h.paletteEntries = int(binary.LittleEndian.Uint32(d[32:]))
	}
	if h.height < 0 {
		h.height, h.topDown = -h.height, true
	}
	if h.width <= 0 || h.height <= 0 || h.width > 1<<16 || h.height > 1<<16 {
		return nil, errors.New("bmp: invalid size")
	}
	switch h.compression {
	case 0, 1, 2: // RGB, RLE8, RLE4
	case 3, 6: // BITFIELDS, ALPHABITFIELDS
		if h.headerSize >= 52 && len(d) >= 56 {
			for i := 0; i < 4 && 40+4*i+4 <= len(d); i++ {
				h.masks[i] = binary.LittleEndian.Uint32(d[40+4*i:])
			}
			h.hasAlphaMask = h.headerSize >= 56 && h.masks[3] != 0
		} else if len(d) >= 40+12 {
			// Masks follow a BITMAPINFOHEADER.
			for i := 0; i < 3; i++ {
				h.masks[i] = binary.LittleEndian.Uint32(d[40+4*i:])
			}
			if h.compression == 6 && len(d) >= 40+16 {
				h.masks[3] = binary.LittleEndian.Uint32(d[52:])
				h.hasAlphaMask = h.masks[3] != 0
			}
		}
	default:
		return nil, errors.New("bmp: unsupported compression")
	}
	if h.bpp <= 8 {
		n := h.paletteEntries
		if n == 0 || n > 1<<h.bpp {
			n = 1 << h.bpp
		}
		entry := 4
		if h.headerSize == 12 {
			entry = 3
		}
		p := 14 + h.headerSize
		if h.compression == 3 && h.headerSize == 40 {
			p += 12
		}
		for i := 0; i < n; i++ {
			q := p + entry*i
			if q+3 > len(data) {
				break
			}
			h.palette = append(h.palette, color.RGBA{data[q+2], data[q+1], data[q], 255})
		}
		for len(h.palette) < 1<<h.bpp {
			h.palette = append(h.palette, color.RGBA{0, 0, 0, 255})
		}
	}
	return h, nil
}

func decodeBMPConfig(r io.Reader) (image.Config, error) {
	data, err := io.ReadAll(io.LimitReader(r, 1024))
	if err != nil {
		return image.Config{}, err
	}
	h, err := readBMPHeader(data)
	if err != nil {
		return image.Config{}, err
	}
	return image.Config{ColorModel: color.NRGBAModel, Width: h.width, Height: h.height}, nil
}

func decodeBMP(r io.Reader) (image.Image, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	h, err := readBMPHeader(data)
	if err != nil {
		return nil, err
	}
	if h.offset > len(data) {
		return nil, errors.New("bmp: invalid offset")
	}
	img := image.NewNRGBA(image.Rect(0, 0, h.width, h.height))
	pix := data[h.offset:]
	set := func(x, y int, c color.RGBA) {
		if !h.topDown {
			y = h.height - 1 - y
		}
		if x < 0 || x >= h.width || y < 0 || y >= h.height {
			return
		}
		i := img.PixOffset(x, y)
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = c.R, c.G, c.B, c.A
	}
	if h.compression == 1 || h.compression == 2 {
		// Run-length encoding; pixels not set have the first color.
		for yy := 0; yy < h.height; yy++ {
			for xx := 0; xx < h.width; xx++ {
				set(xx, yy, h.palette[0].(color.RGBA))
			}
		}
		x, y, p := 0, 0, 0
		four := h.compression == 2
		for p+1 < len(pix) && y < h.height {
			n, v := int(pix[p]), pix[p+1]
			p += 2
			if n > 0 {
				for i := 0; i < n; i++ {
					idx := int(v)
					if four {
						idx = int(v>>4) & 15
						if i%2 == 1 {
							idx = int(v) & 15
						}
					}
					set(x, y, h.palette[idx].(color.RGBA))
					x++
				}
				continue
			}
			switch v {
			case 0:
				x, y = 0, y+1
			case 1:
				y = h.height
			case 2:
				if p+1 < len(pix) {
					x += int(pix[p])
					y += int(pix[p+1])
					p += 2
				}
			default:
				count := int(v)
				for i := 0; i < count; i++ {
					var idx int
					if four {
						b := pix[min(p+i/2, len(pix)-1)]
						if i%2 == 0 {
							idx = int(b >> 4)
						} else {
							idx = int(b & 15)
						}
					} else if p+i < len(pix) {
						idx = int(pix[p+i])
					}
					set(x, y, h.palette[idx].(color.RGBA))
					x++
				}
				nbytes := count
				if four {
					nbytes = (count + 1) / 2
				}
				p += nbytes + nbytes%2
			}
		}
		return img, nil
	}
	stride := ((h.width*h.bpp + 31) / 32) * 4
	masks := h.masks
	if h.compression == 0 {
		switch h.bpp {
		case 16:
			masks = [4]uint32{0x7c00, 0x3e0, 0x1f, 0}
		case 32:
			masks = [4]uint32{0xff0000, 0xff00, 0xff, 0}
		}
	}
	channel := func(v, mask uint32) uint8 {
		if mask == 0 {
			return 0
		}
		shift := bits.TrailingZeros32(mask)
		width := bits.OnesCount32(mask)
		c := (v & mask) >> shift
		if width >= 8 {
			return uint8(c >> (width - 8))
		}
		return uint8(c * 255 / (1<<width - 1))
	}
	anyAlpha := false
	for y := 0; y < h.height; y++ {
		row := y * stride
		if row >= len(pix) {
			break
		}
		line := pix[row:min(row+stride, len(pix))]
		for x := 0; x < h.width; x++ {
			var c color.RGBA
			switch h.bpp {
			case 1, 2, 4, 8:
				bit := x * h.bpp
				if bit/8 >= len(line) {
					continue
				}
				idx := int(line[bit/8]>>(8-h.bpp-bit%8)) & (1<<h.bpp - 1)
				c = h.palette[idx].(color.RGBA)
			case 24:
				if 3*x+2 >= len(line) {
					continue
				}
				c = color.RGBA{line[3*x+2], line[3*x+1], line[3*x], 255}
			case 16, 32:
				n := h.bpp / 8
				if n*x+n > len(line) {
					continue
				}
				var v uint32
				if n == 2 {
					v = uint32(binary.LittleEndian.Uint16(line[2*x:]))
				} else {
					v = binary.LittleEndian.Uint32(line[4*x:])
				}
				c = color.RGBA{channel(v, masks[0]), channel(v, masks[1]), channel(v, masks[2]), 255}
				if masks[3] != 0 {
					c.A = channel(v, masks[3])
					if c.A != 0 {
						anyAlpha = true
					}
				}
			default:
				return nil, errors.New("bmp: unsupported bit depth")
			}
			set(x, y, c)
		}
	}
	if masks[3] != 0 && !anyAlpha {
		// An all-zero alpha channel is ignored, like Pillow and browsers.
		for i := 3; i < len(img.Pix); i += 4 {
			img.Pix[i] = 255
		}
	}
	return img, nil
}

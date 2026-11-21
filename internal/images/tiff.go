package images

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"io"
)

// TIFF decoder: baseline TIFF (bilevel, grayscale, palette, RGB, CMYK and
// YCbCr-free JPEG) with no, PackBits, LZW, Deflate or JPEG compression,
// strips or tiles, 1 to 16 bits per sample, horizontal predictor and
// alpha. Only the first image of the file is decoded.

func init() {
	image.RegisterFormat("tiff", "II*\x00", decodeTIFF, decodeTIFFConfig)
	image.RegisterFormat("tiff", "MM\x00*", decodeTIFF, decodeTIFFConfig)
}

type tiffIFD struct {
	bo              binary.ByteOrder
	data            []byte
	width, height   int
	bits            []int
	samples         int
	compression     int
	photometric     int
	predictor       int
	planar          int
	offsets, counts []int
	rowsPerStrip    int
	tileW, tileH    int
	colorMap        []int
	extra           []int
	sampleFormat    int
	jpegTables      []byte
	fillOrder       int
	faxOptions      int
}

func parseTIFF(data []byte) (*tiffIFD, error) {
	if len(data) < 8 {
		return nil, errors.New("tiff: truncated")
	}
	t := &tiffIFD{data: data, samples: 1, compression: 1, planar: 1, predictor: 1, fillOrder: 1}
	switch string(data[:2]) {
	case "II":
		t.bo = binary.LittleEndian
	case "MM":
		t.bo = binary.BigEndian
	default:
		return nil, errors.New("tiff: invalid header")
	}
	off := int(t.bo.Uint32(data[4:]))
	if off+2 > len(data) {
		return nil, errors.New("tiff: invalid IFD offset")
	}
	n := int(t.bo.Uint16(data[off:]))
	typeSize := map[uint16]int{1: 1, 2: 1, 3: 2, 4: 4, 5: 8, 6: 1, 7: 1, 8: 2, 9: 4, 10: 8, 11: 4, 12: 8, 16: 8}
	for i := 0; i < n; i++ {
		e := off + 2 + 12*i
		if e+12 > len(data) {
			break
		}
		tag, typ := t.bo.Uint16(data[e:]), t.bo.Uint16(data[e+2:])
		count := int(t.bo.Uint32(data[e+4:]))
		size := typeSize[typ]
		if size == 0 || count < 0 || count > len(data) {
			continue
		}
		raw := data[e+8 : e+12]
		if size*count > 4 {
			p := int(t.bo.Uint32(data[e+8:]))
			if p < 0 || p+size*count > len(data) {
				continue
			}
			raw = data[p : p+size*count]
		}
		vals := func() []int {
			out := make([]int, count)
			for k := range out {
				switch size {
				case 1:
					out[k] = int(raw[k])
				case 2:
					out[k] = int(t.bo.Uint16(raw[2*k:]))
				case 4:
					out[k] = int(t.bo.Uint32(raw[4*k:]))
				case 8:
					out[k] = int(t.bo.Uint32(raw[8*k:]))
				}
			}
			return out
		}
		first := func() int {
			if v := vals(); len(v) > 0 {
				return v[0]
			}
			return 0
		}
		switch tag {
		case 256:
			t.width = first()
		case 257:
			t.height = first()
		case 258:
			t.bits = vals()
		case 259:
			t.compression = first()
		case 262:
			t.photometric = first()
		case 266:
			t.fillOrder = first()
		case 273, 324:
			t.offsets = vals()
		case 277:
			t.samples = first()
		case 278:
			t.rowsPerStrip = first()
		case 279, 325:
			t.counts = vals()
		case 284:
			t.planar = first()
		case 317:
			t.predictor = first()
		case 320:
			t.colorMap = vals()
		case 322:
			t.tileW = first()
		case 323:
			t.tileH = first()
		case 338:
			t.extra = vals()
		case 339:
			t.sampleFormat = first()
		case 347:
			t.jpegTables = append([]byte(nil), raw...)
		case 292, 293:
			t.faxOptions = first()
		}
	}
	if t.width <= 0 || t.height <= 0 || t.width > 1<<16 || t.height > 1<<16 {
		return nil, errors.New("tiff: invalid size")
	}
	if len(t.bits) == 0 {
		t.bits = []int{1}
	}
	for len(t.bits) < t.samples {
		t.bits = append(t.bits, t.bits[0])
	}
	if t.rowsPerStrip <= 0 || t.rowsPerStrip > t.height {
		t.rowsPerStrip = t.height
	}
	return t, nil
}

func decodeTIFFConfig(r io.Reader) (image.Config, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return image.Config{}, err
	}
	t, err := parseTIFF(data)
	if err != nil {
		return image.Config{}, err
	}
	return image.Config{ColorModel: color.NRGBAModel, Width: t.width, Height: t.height}, nil
}

// decompress returns the uncompressed data of a strip or tile.
func (t *tiffIFD) decompress(src []byte, size int) ([]byte, error) {
	if t.fillOrder == 2 {
		rev := make([]byte, len(src))
		for i, b := range src {
			rev[i] = reverseBits(b)
		}
		src = rev
	}
	switch t.compression {
	case 1:
		return src, nil
	case 32773:
		var out []byte
		for i := 0; i < len(src) && len(out) < size; {
			n := int(int8(src[i]))
			i++
			switch {
			case n >= 0:
				end := min(i+n+1, len(src))
				out = append(out, src[i:end]...)
				i = end
			case n != -128:
				if i < len(src) {
					for k := 0; k < 1-n; k++ {
						out = append(out, src[i])
					}
				}
				i++
			}
		}
		return out, nil
	case 5:
		return tiffLZW(src, size), nil
	case 2, 3, 4:
		// CCITT fax: rows packed with 1 bit per pixel.
		w := t.width
		if t.tileW > 0 {
			w = t.tileW
		}
		rb := (w + 7) / 8
		rows, err := decodeFax(src, w, size/rb, t.compression, t.faxOptions)
		if err != nil {
			return nil, err
		}
		out := make([]byte, rb*len(rows))
		for y, row := range rows {
			for x, v := range row {
				// Like libtiff: black runs are 1 bits, interpreted with
				// the photometric interpretation.
				if v == 1 {
					out[y*rb+x/8] |= 0x80 >> (x % 8)
				}
			}
		}
		return out, nil
	case 8, 32946:
		zr, err := zlib.NewReader(bytes.NewReader(src))
		if err != nil {
			return nil, err
		}
		return io.ReadAll(zr)
	}
	return nil, errors.New("tiff: unsupported compression")
}

func reverseBits(b byte) byte {
	b = b>>4 | b<<4
	b = (b&0xcc)>>2 | (b&0x33)<<2
	return (b&0xaa)>>1 | (b&0x55)<<1
}

// tiffLZW decodes TIFF's LZW variant (MSB first, early code width
// change).
func tiffLZW(src []byte, size int) []byte {
	out := make([]byte, 0, size)
	var dict [][]byte
	reset := func() {
		dict = dict[:0]
		for i := 0; i < 256; i++ {
			dict = append(dict, []byte{byte(i)})
		}
		dict = append(dict, nil, nil) // clear, end
	}
	reset()
	width := 9
	var acc uint32
	nbits := 0
	pos := 0
	var prev []byte
	for {
		for nbits < width && pos < len(src) {
			acc = acc<<8 | uint32(src[pos])
			pos++
			nbits += 8
		}
		if nbits < width {
			break
		}
		code := int(acc>>(nbits-width)) & (1<<width - 1)
		nbits -= width
		if code == 256 {
			reset()
			width = 9
			prev = nil
			continue
		}
		if code == 257 {
			break
		}
		var entry []byte
		switch {
		case code < len(dict):
			entry = dict[code]
		case code == len(dict) && prev != nil:
			entry = append(append([]byte{}, prev...), prev[0])
		default:
			return out
		}
		out = append(out, entry...)
		if prev != nil {
			dict = append(dict, append(append([]byte{}, prev...), entry[0]))
		}
		prev = entry
		if len(dict)+1 >= 1<<width && width < 12 {
			width++
		}
		if len(out) >= size {
			break
		}
	}
	return out
}

func decodeTIFF(r io.Reader) (image.Image, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	t, err := parseTIFF(data)
	if err != nil {
		return nil, err
	}
	if t.compression == 7 || t.compression == 6 {
		return t.decodeJPEG()
	}
	if t.planar != 1 && t.samples > 1 {
		return nil, errors.New("tiff: planar configuration not supported")
	}
	bps := t.bits[0]
	for _, b := range t.bits {
		if b != bps {
			return nil, errors.New("tiff: mixed bits per sample")
		}
	}
	if bps != 1 && bps != 2 && bps != 4 && bps != 8 && bps != 16 {
		return nil, errors.New("tiff: unsupported bits per sample")
	}
	// Samples of the whole image, 16 bits.
	w, h, spp := t.width, t.height, t.samples
	samples := make([]uint16, w*h*spp)
	rowBytes := func(width int) int { return (width*spp*bps + 7) / 8 }
	readBlock := func(block []byte, bw, bh, x0, y0 int) {
		rb := rowBytes(bw)
		for y := 0; y < bh; y++ {
			if y0+y >= h {
				break
			}
			row := block[min(y*rb, len(block)):min((y+1)*rb, len(block))]
			if t.predictor == 2 {
				predictRow(row, bw, spp, bps, t.bo)
			}
			for x := 0; x < bw; x++ {
				if x0+x >= w {
					break
				}
				for s := 0; s < spp; s++ {
					i := x*spp + s
					var v uint16
					switch bps {
					case 16:
						if 2*i+1 < len(row) {
							v = t.bo.Uint16(row[2*i:])
						}
					case 8:
						if i < len(row) {
							v = uint16(row[i])
						}
					default:
						bit := i * bps
						if bit/8 < len(row) {
							v = uint16(row[bit/8]>>(8-bps-bit%8)) & (1<<bps - 1)
						}
					}
					samples[((y0+y)*w+x0+x)*spp+s] = v
				}
			}
		}
	}
	if t.tileW > 0 && t.tileH > 0 {
		across := (w + t.tileW - 1) / t.tileW
		for i, off := range t.offsets {
			if i >= len(t.counts) || off+t.counts[i] > len(data) {
				break
			}
			block, err := t.decompress(data[off:off+t.counts[i]], rowBytes(t.tileW)*t.tileH)
			if err != nil {
				return nil, err
			}
			readBlock(block, t.tileW, t.tileH, (i%across)*t.tileW, (i/across)*t.tileH)
		}
	} else {
		for i, off := range t.offsets {
			count := len(data) - off
			if i < len(t.counts) {
				count = t.counts[i]
			}
			if off < 0 || off+count > len(data) {
				break
			}
			block, err := t.decompress(data[off:off+count], rowBytes(w)*t.rowsPerStrip)
			if err != nil {
				return nil, err
			}
			readBlock(block, w, t.rowsPerStrip, 0, i*t.rowsPerStrip)
		}
	}
	max := float64(uint32(1)<<bps - 1)
	scale := func(v uint16) uint8 { return uint8(float64(v)*255/max + 0.5) }
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	alpha := -1
	premultiplied := false
	colorSamples := map[int]int{0: 1, 1: 1, 2: 3, 3: 1, 5: 4}[t.photometric]
	if colorSamples == 0 {
		colorSamples = 1
	}
	if spp > colorSamples {
		alpha = colorSamples
		premultiplied = len(t.extra) > 0 && t.extra[0] == 1
	}
	for p := 0; p < w*h; p++ {
		s := samples[p*spp : (p+1)*spp]
		var c color.NRGBA
		c.A = 255
		switch t.photometric {
		case 0, 1:
			g := scale(s[0])
			if t.photometric == 0 {
				g = 255 - g
			}
			c.R, c.G, c.B = g, g, g
		case 2:
			c.R, c.G, c.B = scale(s[0]), scale(s[1%spp]), scale(s[2%spp])
		case 3:
			n := 1 << bps
			idx := int(s[0])
			if len(t.colorMap) >= 3*n && idx < n {
				c.R = uint8(t.colorMap[idx] >> 8)
				c.G = uint8(t.colorMap[n+idx] >> 8)
				c.B = uint8(t.colorMap[2*n+idx] >> 8)
			}
		case 5:
			if spp >= 4 {
				k := 255 - int(scale(s[3]))
				c.R = uint8((255 - int(scale(s[0]))) * k / 255)
				c.G = uint8((255 - int(scale(s[1]))) * k / 255)
				c.B = uint8((255 - int(scale(s[2]))) * k / 255)
			}
		default:
			return nil, errors.New("tiff: unsupported photometric interpretation")
		}
		if alpha >= 0 && alpha < spp {
			c.A = scale(s[alpha])
			if premultiplied && c.A > 0 && c.A < 255 {
				c.R = uint8(min(255, int(c.R)*255/int(c.A)))
				c.G = uint8(min(255, int(c.G)*255/int(c.A)))
				c.B = uint8(min(255, int(c.B)*255/int(c.A)))
			}
		}
		img.Pix[4*p], img.Pix[4*p+1], img.Pix[4*p+2], img.Pix[4*p+3] = c.R, c.G, c.B, c.A
	}
	return img, nil
}

// predictRow undoes the horizontal differencing predictor.
func predictRow(row []byte, width, spp, bps int, bo binary.ByteOrder) {
	switch bps {
	case 8:
		for i := spp; i < width*spp && i < len(row); i++ {
			row[i] += row[i-spp]
		}
	case 16:
		for i := spp; i < width*spp && 2*i+1 < len(row); i++ {
			bo.PutUint16(row[2*i:], bo.Uint16(row[2*i:])+bo.Uint16(row[2*(i-spp):]))
		}
	}
}

// decodeJPEG decodes a TIFF whose strips are JPEG images (new-style JPEG
// compression with one strip or tile).
func (t *tiffIFD) decodeJPEG() (image.Image, error) {
	if len(t.offsets) == 0 {
		return nil, errors.New("tiff: no data")
	}
	img := image.NewNRGBA(image.Rect(0, 0, t.width, t.height))
	across := 1
	bw, bh := t.width, t.rowsPerStrip
	if t.tileW > 0 && t.tileH > 0 {
		bw, bh = t.tileW, t.tileH
		across = (t.width + bw - 1) / bw
	}
	for i, off := range t.offsets {
		if i >= len(t.counts) || off+t.counts[i] > len(t.data) {
			break
		}
		block := t.data[off : off+t.counts[i]]
		if len(t.jpegTables) > 4 && len(block) > 2 {
			// Insert the shared tables after the SOI marker.
			tables := t.jpegTables[2 : len(t.jpegTables)-2]
			block = append(append(append([]byte{}, block[:2]...), tables...), block[2:]...)
		}
		part, err := jpeg.Decode(bytes.NewReader(block))
		if err != nil {
			return nil, err
		}
		x0, y0 := (i%across)*bw, (i/across)*bh
		b := part.Bounds()
		for y := 0; y < b.Dy(); y++ {
			for x := 0; x < b.Dx(); x++ {
				if x0+x < t.width && y0+y < t.height {
					img.Set(x0+x, y0+y, part.At(b.Min.X+x, b.Min.Y+y))
				}
			}
		}
	}
	return img, nil
}

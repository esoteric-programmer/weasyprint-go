package images

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"math"
	"regexp"
	"strconv"
	"strings"
)

func init() {
	registerBytes("icns", "icns", decodeICNS)
	registerBytes("msp", "DanM", decodeMSP)
	registerBytes("msp", "LinS", decodeMSP)
	registerBytes("pixar", "\x80\xe8\x00\x00", decodePixar)
	registerBytes("xvthumb", "P7 332", decodeXVThumb)
	registerBytes("fits", "SIMPLE", decodeFITS)
	registerBytes("ftex", "FTEX", decodeFTEX)
	registerBytes("blp", "BLP1", decodeBLP)
	registerBytes("blp", "BLP2", decodeBLP)
	registerBytes("dds", "DDS ", decodeDDS)
	registerBytes("psd", "8BPS", decodePSD)
	registerBytes("fli", "????\x11\xaf", decodeFLI)
	registerBytes("fli", "????\x12\xaf", decodeFLI)
	sniffers = append(sniffers,
		sniffer{"gbr", func(d []byte) bool {
			return len(d) >= 8 && be32(d, 0) >= 20 && (be32(d, 4) == 1 || be32(d, 4) == 2)
		}, decodeGBR},
		sniffer{"im", func(d []byte) bool { return bytes.IndexByte(d[:min(len(d), 100)], '\n') >= 0 }, decodeIM},
		sniffer{"imt", func(d []byte) bool { return bytes.IndexByte(d[:min(len(d), 100)], '\n') >= 0 }, decodeIMT},
		sniffer{"mcidas", func(d []byte) bool { return hasPrefix(d, "\x00\x00\x00\x00\x00\x00\x00\x04") }, decodeMcIdas},
		sniffer{"pcd", func(d []byte) bool { return len(d) > 2052 && hasPrefix(d[2048:], "PCD_") }, decodePCD},
		sniffer{"spider", func(d []byte) bool { return spiderHeader(d) > 0 }, decodeSpider},
		sniffer{"iptc", func(d []byte) bool { return len(d) > 5 && d[0] == 0x1c }, decodeIPTC},
	)
	snifferOrder = append(snifferOrder, "iptc")
}

// ---------------------------------------------------------------- ICNS

var icnsSizes = []struct {
	size  [3]int
	types []string
}{
	{[3]int{512, 512, 2}, []string{"ic10"}},
	{[3]int{512, 512, 1}, []string{"ic09"}},
	{[3]int{256, 256, 2}, []string{"ic14"}},
	{[3]int{256, 256, 1}, []string{"ic08"}},
	{[3]int{128, 128, 2}, []string{"ic13"}},
	{[3]int{128, 128, 1}, []string{"ic07", "it32", "t8mk"}},
	{[3]int{64, 64, 1}, []string{"icp6"}},
	{[3]int{48, 48, 1}, []string{"ih32", "h8mk"}},
	{[3]int{32, 32, 2}, []string{"ic12"}},
	{[3]int{32, 32, 1}, []string{"icp5", "il32", "l8mk"}},
	{[3]int{16, 16, 2}, []string{"ic11"}},
	{[3]int{16, 16, 1}, []string{"icp4", "is32", "s8mk"}},
}

func decodeICNS(d []byte) (image.Image, error) {
	if len(d) < 8 {
		return nil, errCorrupt
	}
	chunks := map[string][]byte{}
	end := min(be32(d, 4), len(d))
	for p := 8; p+8 <= end; {
		n := be32(d, p+4)
		if n < 8 || p+n > len(d) {
			break
		}
		chunks[string(d[p:p+4])] = d[p+8 : p+n]
		p += n
	}
	// The largest size, like Pillow.
	for _, s := range icnsSizes {
		found := false
		for _, t := range s.types {
			if chunks[t] != nil {
				found = true
			}
		}
		if !found {
			continue
		}
		w := s.size[0] * s.size[2]
		for _, t := range s.types {
			c := chunks[t]
			if c == nil || strings.HasSuffix(t, "mk") || t == "it32" || strings.HasSuffix(t, "32") {
				continue
			}
			img, _, err := image.Decode(bytes.NewReader(c))
			if err != nil {
				return nil, err
			}
			return img, nil
		}
		for _, t := range s.types {
			c := chunks[t]
			if c == nil || !strings.HasSuffix(t, "32") {
				continue
			}
			if t == "it32" {
				if len(c) < 4 {
					return nil, errCorrupt
				}
				c = c[4:]
			}
			img := newNRGBA(w, w)
			n := w * w
			if len(c) == 3*n {
				for i := 0; i < n; i++ {
					img.Pix[4*i], img.Pix[4*i+1], img.Pix[4*i+2], img.Pix[4*i+3] = c[3*i], c[3*i+1], c[3*i+2], 255
				}
			} else {
				p := 0
				for band := 0; band < 3; band++ {
					i := 0
					for i < n && p < len(c) {
						b := int(c[p])
						p++
						if b&0x80 != 0 {
							if p >= len(c) {
								return nil, errCorrupt
							}
							for k := 0; k < b-125 && i < n; k++ {
								img.Pix[4*i+band] = c[p]
								i++
							}
							p++
						} else {
							for k := 0; k <= b && i < n && p < len(c); k++ {
								img.Pix[4*i+band] = c[p]
								i++
								p++
							}
						}
					}
					if i != n {
						return nil, errCorrupt
					}
				}
				for i := 0; i < n; i++ {
					img.Pix[4*i+3] = 255
				}
			}
			for _, mt := range s.types {
				if m := chunks[mt]; m != nil && strings.HasSuffix(mt, "mk") && len(m) >= n {
					for i := 0; i < n; i++ {
						img.Pix[4*i+3] = m[i]
					}
				}
			}
			return img, nil
		}
	}
	return nil, errors.New("no supported icon in ICNS file")
}

// ---------------------------------------------------------------- MSP

func decodeMSP(d []byte) (image.Image, error) {
	if len(d) < 32 {
		return nil, errCorrupt
	}
	sum := 0
	for i := 0; i < 32; i += 2 {
		sum ^= le16(d, i)
	}
	if sum != 0 {
		return nil, errors.New("bad MSP checksum")
	}
	w, h := le16(d, 4), le16(d, 6)
	if err := checkSize(w, h); err != nil {
		return nil, err
	}
	stride := (w + 7) / 8
	var raw []byte
	if hasPrefix(d, "DanM") {
		raw = d[32:]
	} else {
		if 32+2*h > len(d) {
			return nil, errCorrupt
		}
		p := 32 + 2*h
		for y := 0; y < h; y++ {
			n := le16(d, 32+2*y)
			row := make([]byte, 0, stride)
			if n == 0 {
				row = bytes.Repeat([]byte{0xff}, stride)
			}
			if p+n > len(d) {
				return nil, errCorrupt
			}
			r := d[p : p+n]
			p += n
			for i := 0; i < len(r); {
				t := int(r[i])
				i++
				if t == 0 {
					if i+2 > len(r) {
						return nil, errCorrupt
					}
					for k := 0; k < int(r[i]); k++ {
						row = append(row, r[i+1])
					}
					i += 2
				} else {
					row = append(row, r[i:min(i+t, len(r))]...)
					i += t
				}
			}
			for len(row) < stride {
				row = append(row, 0xff)
			}
			raw = append(raw, row[:stride]...)
		}
	}
	return bilevel(raw, w, h, stride, false)
}

// bilevel decodes packed 1-bit rows; set bits are white unless inverted.
func bilevel(raw []byte, w, h, stride int, setIsBlack bool) (*image.Gray, error) {
	if len(raw) < stride*h {
		return nil, errCorrupt
	}
	img := newGray(w, h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if (raw[y*stride+x/8]&(0x80>>uint(x&7)) != 0) != setIsBlack {
				img.Pix[y*w+x] = 255
			}
		}
	}
	return img, nil
}

// ---------------------------------------------------------------- Pixar, XV thumbnails

func decodePixar(d []byte) (image.Image, error) {
	if len(d) < 512 {
		return nil, errCorrupt
	}
	w, h := le16(d, 418), le16(d, 416)
	if le16(d, 424) != 14 || le16(d, 426) != 2 {
		return nil, errors.New("unsupported PIXAR mode")
	}
	if err := checkSize(w, h); err != nil {
		return nil, err
	}
	if 1024+3*w*h > len(d) {
		return nil, errCorrupt
	}
	img := newNRGBA(w, h)
	for i := 0; i < w*h; i++ {
		img.Pix[4*i], img.Pix[4*i+1], img.Pix[4*i+2], img.Pix[4*i+3] = d[1024+3*i], d[1025+3*i], d[1026+3*i], 255
	}
	return img, nil
}

func decodeXVThumb(d []byte) (image.Image, error) {
	lines := bytes.SplitAfter(d, []byte("\n"))
	p := 0
	li := 0
	if len(lines) == 0 {
		return nil, errCorrupt
	}
	p += len(lines[0])
	li = 1
	var dims []string
	for ; li < len(lines); li++ {
		p += len(lines[li])
		if len(lines[li]) > 0 && lines[li][0] != '#' {
			dims = strings.Fields(string(lines[li]))
			break
		}
	}
	if len(dims) < 2 {
		return nil, errCorrupt
	}
	w, _ := strconv.Atoi(dims[0])
	h, _ := strconv.Atoi(dims[1])
	if err := checkSize(w, h); err != nil {
		return nil, err
	}
	if p+w*h > len(d) {
		return nil, errCorrupt
	}
	img := newNRGBA(w, h)
	for i := 0; i < w*h; i++ {
		v := int(d[p+i])
		img.Pix[4*i], img.Pix[4*i+1], img.Pix[4*i+2], img.Pix[4*i+3] = byte((v>>5)*255/7), byte((v>>2&7)*255/7), byte((v&3)*255/3), 255
	}
	return img, nil
}

// ---------------------------------------------------------------- IM (IFUNC), IMT

var imLine = regexp.MustCompile(`^([A-Za-z][^:]*):[ \t]*(.*?)[ \t]*$`)

func decodeIM(d []byte) (image.Image, error) {
	info := map[string]string{}
	p := 0
	n := 0
	for p < len(d) {
		if d[p] == '\r' {
			p++
			continue
		}
		if d[p] == 0 || d[p] == 0x1a {
			break
		}
		e := bytes.IndexByte(d[p:], '\n')
		if e < 0 || e > 100 {
			return nil, errCorrupt
		}
		line := strings.TrimRight(string(d[p:p+e]), "\r")
		p += e + 1
		m := imLine.FindStringSubmatch(line)
		if m == nil {
			return nil, errCorrupt
		}
		info[m[1]] = m[2]
		switch m[1] {
		case "Comment", "Date", "Digitalization equipment", "File size (no of images)", "Lut", "Name", "Scale (x,y)", "Image size (x*y)", "Image type":
			n++
		}
	}
	if n == 0 {
		return nil, errCorrupt
	}
	for p < len(d) && d[p] != 0x1a {
		p++
	}
	if p >= len(d) {
		return nil, errCorrupt
	}
	p++
	w, h := 512, 512
	if s, ok := info["Image size (x*y)"]; ok {
		f := strings.FieldsFunc(s, func(r rune) bool { return r == '*' || r == ',' })
		if len(f) != 2 {
			return nil, errCorrupt
		}
		w, _ = strconv.Atoi(f[0])
		h, _ = strconv.Atoi(f[1])
	}
	if err := checkSize(w, h); err != nil {
		return nil, err
	}
	var lut []byte
	if _, ok := info["Lut"]; ok {
		if p+768 > len(d) {
			return nil, errCorrupt
		}
		lut = d[p : p+768]
		p += 768
	}
	typ := info["Image type"]
	if typ == "" {
		typ = "Greyscale image"
	}
	data := d[p:]
	// Rows are stored bottom to top.
	row := func(y, stride int) []byte {
		o := (h - 1 - y) * stride
		if o+stride > len(data) {
			return nil
		}
		return data[o : o+stride]
	}
	if typ == "CMYK image" {
		cmyk := image.NewCMYK(image.Rect(0, 0, w, h))
		for y := 0; y < h; y++ {
			r := row(y, 4*w)
			if r == nil {
				return nil, errCorrupt
			}
			for x := 0; x < w; x++ {
				for k := 0; k < 4; k++ {
					cmyk.Pix[4*(y*w+x)+k] = r[k*w+x]
				}
			}
		}
		return cmyk, nil
	}
	img := newNRGBA(w, h)
	set := func(x, y int, r, g, b, a byte) {
		o := 4 * (y*w + x)
		img.Pix[o], img.Pix[o+1], img.Pix[o+2], img.Pix[o+3] = r, g, b, a
	}
	switch typ {
	case "0 1 image", "L 1 image", "B1 image":
		stride := (w + 7) / 8
		for y := 0; y < h; y++ {
			r := row(y, stride)
			if r == nil {
				return nil, errCorrupt
			}
			for x := 0; x < w; x++ {
				v := byte(0)
				if r[x/8]&(0x80>>uint(x&7)) != 0 {
					v = 255
				}
				set(x, y, v, v, v, 255)
			}
		}
	case "Greyscale image", "Grayscale image", "L 8 image":
		palette := lut != nil && !imGrayLUT(lut)
		for y := 0; y < h; y++ {
			r := row(y, w)
			if r == nil {
				return nil, errCorrupt
			}
			for x := 0; x < w; x++ {
				v := r[x]
				if palette {
					set(x, y, lut[v], lut[256+int(v)], lut[512+int(v)], 255)
				} else {
					set(x, y, v, v, v, 255)
				}
			}
		}
	case "RGB image", "RGBA image", "RGBX image", "CMYK image", "LA image", "PA image", "YCC image":
		// Line-interleaved channels.
		nc := map[string]int{"RGB image": 3, "RGBA image": 4, "RGBX image": 4, "CMYK image": 4, "LA image": 2, "PA image": 2, "YCC image": 3}[typ]
		for y := 0; y < h; y++ {
			r := row(y, nc*w)
			if r == nil {
				return nil, errCorrupt
			}
			for x := 0; x < w; x++ {
				c := func(i int) byte { return r[i*w+x] }
				switch typ {
				case "RGB image", "RGBX image":
					set(x, y, c(0), c(1), c(2), 255)
				case "RGBA image":
					set(x, y, c(0), c(1), c(2), c(3))
				case "CMYK image":
					k := 255 - int(c(3))
					set(x, y, byte((255-int(c(0)))*k/255), byte((255-int(c(1)))*k/255), byte((255-int(c(2)))*k/255), 255)
				case "LA image":
					set(x, y, c(0), c(0), c(0), c(1))
				case "PA image":
					v := int(c(0))
					if lut != nil {
						set(x, y, lut[v], lut[256+v], lut[512+v], c(1))
					} else {
						set(x, y, c(0), c(0), c(0), c(1))
					}
				case "YCC image":
					rr, gg, bb := color.YCbCrToRGB(c(0), c(1), c(2))
					set(x, y, rr, gg, bb, 255)
				}
			}
		}
	default:
		return nil, errors.New("unsupported IM image type")
	}
	return img, nil
}

func imGrayLUT(lut []byte) bool {
	for i := 0; i < 256; i++ {
		if lut[i] != lut[256+i] || lut[i] != lut[512+i] {
			return false
		}
	}
	return true
}

func decodeIMT(d []byte) (image.Image, error) {
	w, h, gray := 0, 0, false
	p := 0
	for p < len(d) {
		if d[p] == 0x0c {
			p++
			break
		}
		e := bytes.IndexByte(d[p:], '\n')
		if e < 0 {
			return nil, errCorrupt
		}
		line := string(d[p : p+e])
		p += e + 1
		if len(line) == 0 || len(line) > 100 {
			return nil, errCorrupt
		}
		if line[0] == '*' {
			continue
		}
		f := strings.SplitN(line, " ", 2)
		if len(f) != 2 {
			return nil, errCorrupt
		}
		switch f[0] {
		case "width":
			w, _ = strconv.Atoi(strings.TrimSpace(f[1]))
		case "height":
			h, _ = strconv.Atoi(strings.TrimSpace(f[1]))
		case "pixel":
			gray = strings.TrimSpace(f[1]) == "n8"
		}
	}
	if !gray {
		return nil, errCorrupt
	}
	if err := checkSize(w, h); err != nil {
		return nil, err
	}
	if p+w*h > len(d) {
		return nil, errCorrupt
	}
	img := newGray(w, h)
	copy(img.Pix, d[p:p+w*h])
	return img, nil
}

// ---------------------------------------------------------------- McIdas, Spider, FITS, GBR

func decodeMcIdas(d []byte) (image.Image, error) {
	if len(d) < 256 {
		return nil, errCorrupt
	}
	word := func(i int) int { return int(int32(binary.BigEndian.Uint32(d[4*(i-1):]))) }
	bpp, w, h := word(11), word(10), word(9)
	if err := checkSize(w, h); err != nil {
		return nil, err
	}
	if bpp != 1 && bpp != 2 && bpp != 4 {
		return nil, errors.New("unsupported McIdas depth")
	}
	off := word(34) + word(15)
	stride := word(15) + w*bpp*word(14)
	if off < 0 || off+stride*(h-1)+w*bpp > len(d) {
		return nil, errCorrupt
	}
	img := newGray(w, h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			o := off + y*stride + x*bpp
			switch bpp {
			case 1:
				img.Pix[y*w+x] = d[o]
			case 2:
				img.Pix[y*w+x] = d[o] // high byte
			case 4:
				img.Pix[y*w+x] = clamp8(float64(int32(binary.BigEndian.Uint32(d[o:]))))
			}
		}
	}
	return img, nil
}

func spiderHeader(d []byte) int {
	if len(d) < 108 {
		return 0
	}
	for _, order := range []binary.ByteOrder{binary.BigEndian, binary.LittleEndian} {
		h := make([]float64, 28)
		for i := 1; i <= 27; i++ {
			h[i] = float64(math.Float32frombits(order.Uint32(d[4*(i-1):])))
		}
		ok := true
		for _, i := range []int{1, 2, 5, 12, 13, 22, 23} {
			if h[i] != math.Trunc(h[i]) || math.IsInf(h[i], 0) || math.IsNaN(h[i]) {
				ok = false
			}
		}
		if !ok {
			continue
		}
		switch int(h[5]) {
		case 1, 3, -11, -12, -21, -22:
		default:
			continue
		}
		if int(h[22]) != int(h[13])*int(h[23]) || int(h[22]) <= 0 {
			continue
		}
		return int(h[22])
	}
	return 0
}

// decodeSpider decodes a SPIDER image; the floating point samples are
// scaled from their range to 0–255, like Pillow's convert2byte.
func decodeSpider(d []byte) (image.Image, error) {
	hdr := spiderHeader(d)
	if hdr == 0 {
		return nil, errCorrupt
	}
	var order binary.ByteOrder = binary.BigEndian
	val := func(o binary.ByteOrder, i int) float64 {
		return float64(math.Float32frombits(o.Uint32(d[4*(i-1):])))
	}
	if !(val(order, 5) == 1 || val(order, 5) == 3 || val(order, 5) < 0) || int(val(order, 22)) != hdr {
		order = binary.LittleEndian
	}
	if int(val(order, 5)) != 1 {
		return nil, errors.New("unsupported SPIDER image")
	}
	w, h := int(val(order, 12)), int(val(order, 2))
	if err := checkSize(w, h); err != nil {
		return nil, err
	}
	off := hdr
	istack, imgnum := int(val(order, 24)), int(val(order, 27))
	if istack > 0 && imgnum == 0 {
		off = 2 * hdr
	}
	if off+4*w*h > len(d) {
		return nil, errCorrupt
	}
	vals := make([]float64, w*h)
	for i := range vals {
		vals[i] = float64(math.Float32frombits(order.Uint32(d[off+4*i:])))
	}
	return normalized(w, h, vals), nil
}

// normalized maps samples from their range to 0–255.
func normalized(w, h int, vals []float64) *image.Gray {
	lo, hi := math.Inf(1), math.Inf(-1)
	for _, v := range vals {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			continue
		}
		lo, hi = math.Min(lo, v), math.Max(hi, v)
	}
	img := newGray(w, h)
	if hi > lo {
		for i, v := range vals {
			img.Pix[i] = clamp8(math.Round((v - lo) * 255 / (hi - lo)))
		}
	}
	return img
}

// decodeFITS decodes the primary image of a FITS file. 8-bit images are
// used as they are, the others are scaled from their range (Pillow reads
// big-endian samples as little-endian ones).
func decodeFITS(d []byte) (image.Image, error) {
	hdr := map[string]string{}
	p := 0
	for {
		if p+80 > len(d) {
			return nil, errCorrupt
		}
		card := string(d[p : p+80])
		p += 80
		key := strings.TrimSpace(card[:8])
		if key == "END" {
			break
		}
		v := card[8:]
		if i := strings.IndexByte(v, '/'); i >= 0 {
			v = v[:i]
		}
		v = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(v), "="))
		if _, ok := hdr[key]; !ok {
			hdr[key] = strings.TrimSpace(v)
		}
	}
	p = (p + 2879) / 2880 * 2880
	if hdr["SIMPLE"] != "T" {
		return nil, errCorrupt
	}
	atoi := func(k string) int { v, _ := strconv.Atoi(hdr[k]); return v }
	atof := func(k string, def float64) float64 {
		if v, err := strconv.ParseFloat(strings.Replace(hdr[k], "D", "E", 1), 64); err == nil {
			return v
		}
		return def
	}
	naxis := atoi("NAXIS")
	var w, h int
	switch {
	case naxis == 1:
		w, h = 1, atoi("NAXIS1")
	case naxis >= 2:
		w, h = atoi("NAXIS1"), atoi("NAXIS2")
	default:
		return nil, errors.New("FITS file without image")
	}
	if err := checkSize(w, h); err != nil {
		return nil, err
	}
	bitpix := atoi("BITPIX")
	size := max(bitpix, -bitpix) / 8
	if size == 0 || p+size*w*h > len(d) {
		return nil, errCorrupt
	}
	bscale, bzero := atof("BSCALE", 1), atof("BZERO", 0)
	vals := make([]float64, w*h)
	// Rows are stored bottom to top.
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			o := p + size*(y*w+x)
			var v float64
			switch bitpix {
			case 8:
				v = float64(d[o])
			case 16:
				v = float64(int16(binary.BigEndian.Uint16(d[o:])))
			case 32:
				v = float64(int32(binary.BigEndian.Uint32(d[o:])))
			case 64:
				v = float64(int64(binary.BigEndian.Uint64(d[o:])))
			case -32:
				v = float64(math.Float32frombits(binary.BigEndian.Uint32(d[o:])))
			case -64:
				v = math.Float64frombits(binary.BigEndian.Uint64(d[o:]))
			default:
				return nil, errors.New("unsupported FITS BITPIX")
			}
			vals[(h-1-y)*w+x] = v*bscale + bzero
		}
	}
	if bitpix == 8 {
		img := newGray(w, h)
		for i, v := range vals {
			img.Pix[i] = clamp8(v)
		}
		return img, nil
	}
	return normalized(w, h, vals), nil
}

func decodeGBR(d []byte) (image.Image, error) {
	hs, version := be32(d, 0), be32(d, 4)
	if len(d) < 20 {
		return nil, errCorrupt
	}
	w, h, depth := be32(d, 8), be32(d, 12), be32(d, 16)
	if err := checkSize(w, h); err != nil || (depth != 1 && depth != 4) {
		return nil, errCorrupt
	}
	if version == 2 && (len(d) < 28 || string(d[20:24]) != "GIMP") {
		return nil, errCorrupt
	}
	if hs+w*h*depth > len(d) {
		return nil, errCorrupt
	}
	px := d[hs : hs+w*h*depth]
	if depth == 1 {
		img := newGray(w, h)
		copy(img.Pix, px)
		return img, nil
	}
	img := newNRGBA(w, h)
	copy(img.Pix, px)
	return img, nil
}

// ---------------------------------------------------------------- textures

func decodeFTEX(d []byte) (image.Image, error) {
	if len(d) < 32 {
		return nil, errCorrupt
	}
	w, h := le32(d, 8), le32(d, 12)
	if err := checkSize(w, h); err != nil {
		return nil, err
	}
	format, where := le32(d, 24), le32(d, 28)
	if where+4 > len(d) {
		return nil, errCorrupt
	}
	n := le32(d, where)
	data := d[where+4 : min(len(d), where+4+n)]
	switch format {
	case 0:
		return decodeBCn(data, w, h, 1, false)
	case 1:
		if len(data) < 3*w*h {
			return nil, errCorrupt
		}
		img := newNRGBA(w, h)
		for i := 0; i < w*h; i++ {
			img.Pix[4*i], img.Pix[4*i+1], img.Pix[4*i+2], img.Pix[4*i+3] = data[3*i], data[3*i+1], data[3*i+2], 255
		}
		return img, nil
	}
	return nil, errors.New("unsupported FTEX format")
}

func decodeBLP(d []byte) (image.Image, error) {
	if len(d) < 20 {
		return nil, errCorrupt
	}
	blp1 := hasPrefix(d, "BLP1")
	compression := le32(d, 4)
	var alpha bool
	var encoding, alphaEnc, w, h, hdr int
	if blp1 {
		if len(d) < 28 {
			return nil, errCorrupt
		}
		alpha = le32(d, 8) != 0
		w, h = le32(d, 12), le32(d, 16)
		encoding = le32(d, 20)
		hdr = 28
	} else {
		encoding, alpha, alphaEnc = int(d[8]), d[9] != 0, int(d[10])
		w, h = le32(d, 12), le32(d, 16)
		hdr = 20
	}
	if err := checkSize(w, h); err != nil {
		return nil, err
	}
	if hdr+128 > len(d) {
		return nil, errCorrupt
	}
	off0, len0 := le32(d, hdr), le32(d, hdr+64)
	p := hdr + 128
	readPalette := func() []color.NRGBA {
		pal := make([]color.NRGBA, 256)
		for i := 0; i < 256 && p+4 <= len(d); i++ {
			pal[i] = color.NRGBA{d[p+2], d[p+1], d[p], d[p+3]}
			p += 4
		}
		return pal
	}
	bgra := func(pal []color.NRGBA, start int) (image.Image, error) {
		if start < 0 || start+len0 > len(d) {
			return nil, errCorrupt
		}
		img := newNRGBA(w, h)
		for i := 0; i < w*h && i < len0; i++ {
			c := pal[d[start+i]]
			if !alpha {
				c.A = 255
			}
			img.Pix[4*i], img.Pix[4*i+1], img.Pix[4*i+2], img.Pix[4*i+3] = c.R, c.G, c.B, c.A
		}
		return img, nil
	}
	if blp1 {
		switch {
		case compression == 0:
			// JPEG data sharing a header; the channels are BGR.
			if p+4 > len(d) {
				return nil, errCorrupt
			}
			hl := le32(d, p)
			p += 4
			if p+hl > len(d) || off0+len0 > len(d) || off0 < 0 {
				return nil, errCorrupt
			}
			jp := append(append([]byte(nil), d[p:p+hl]...), d[off0:off0+len0]...)
			src, err := jpeg.Decode(bytes.NewReader(jp))
			if err != nil {
				return nil, err
			}
			b := src.Bounds()
			img := newNRGBA(b.Dx(), b.Dy())
			for y := 0; y < b.Dy(); y++ {
				for x := 0; x < b.Dx(); x++ {
					c := color.NRGBAModel.Convert(src.At(b.Min.X+x, b.Min.Y+y)).(color.NRGBA)
					o := img.PixOffset(x, y)
					img.Pix[o], img.Pix[o+1], img.Pix[o+2], img.Pix[o+3] = c.B, c.G, c.R, 255
				}
			}
			return img, nil
		case compression == 1 && (encoding == 4 || encoding == 5):
			// Like Pillow, the indices follow the palette (Pillow writes
			// a wrong offset).
			pal := readPalette()
			return bgra(pal, p)
		}
		return nil, errors.New("unsupported BLP1 encoding")
	}
	if compression != 1 {
		return nil, errors.New("unsupported BLP2 compression")
	}
	pal := readPalette()
	switch encoding {
	case 1:
		return bgra(pal, off0)
	case 2:
		if off0 < 0 || off0 >= len(d) {
			return nil, errCorrupt
		}
		switch alphaEnc {
		case 0:
			return decodeBCn(d[off0:], w, h, 1, !alpha)
		case 1:
			return decodeBCn(d[off0:], w, h, 2, false)
		case 7:
			return decodeBCn(d[off0:], w, h, 3, false)
		}
	}
	return nil, errors.New("unsupported BLP2 encoding")
}

func decodeDDS(d []byte) (image.Image, error) {
	if len(d) < 128 || le32(d, 4) != 124 {
		return nil, errCorrupt
	}
	h, w := le32(d, 12), le32(d, 16)
	if err := checkSize(w, h); err != nil {
		return nil, err
	}
	pfFlags, fourcc, bitcount := le32(d, 80), string(d[84:88]), le32(d, 88)
	data := d[128:]
	switch {
	case pfFlags&0x40 != 0: // RGB
		n := 3
		if pfFlags&0x1 != 0 {
			n = 4
		}
		var masks [4]uint32
		for i := 0; i < n; i++ {
			masks[i] = binary.LittleEndian.Uint32(d[92+4*i:])
		}
		bc := bitcount / 8
		if bc == 0 || bc > 4 || len(data) < bc*w*h {
			return nil, errCorrupt
		}
		img := newNRGBA(w, h)
		for i := 0; i < w*h; i++ {
			var v uint32
			for k := 0; k < bc; k++ {
				v |= uint32(data[bc*i+k]) << (8 * uint(k))
			}
			for c := 0; c < 4; c++ {
				if c >= n {
					img.Pix[4*i+c] = 255
					continue
				}
				m := masks[c]
				if m == 0 {
					continue
				}
				off := 0
				for (m>>uint(off+1))<<uint(off+1) == m {
					off++
				}
				img.Pix[4*i+c] = byte(float64((v&m)>>uint(off)) / float64(m>>uint(off)) * 255)
			}
		}
		return img, nil
	case pfFlags&0x20000 != 0: // luminance
		if bitcount == 8 && len(data) >= w*h {
			img := newGray(w, h)
			copy(img.Pix, data)
			return img, nil
		}
		if bitcount == 16 && pfFlags&1 != 0 && len(data) >= 2*w*h {
			l, a := make([]byte, w*h), make([]byte, w*h)
			for i := range l {
				l[i], a[i] = data[2*i], data[2*i+1]
			}
			return grayAlpha(w, h, l, a), nil
		}
	case pfFlags&0x20 != 0: // 8-bit palette
		if len(data) < 1024+w*h {
			return nil, errCorrupt
		}
		pal := make([]color.NRGBA, 256)
		for i := range pal {
			pal[i] = color.NRGBA{data[4*i], data[4*i+1], data[4*i+2], data[4*i+3]}
		}
		return paletted(w, h, data[1024:], pal), nil
	case pfFlags&0x4 != 0: // compressed
		switch fourcc {
		case "DXT1":
			return decodeBCn(data, w, h, 1, false)
		case "DXT3":
			return decodeBCn(data, w, h, 2, false)
		case "DXT5":
			return decodeBCn(data, w, h, 3, false)
		case "BC4U", "ATI1":
			return decodeBCn(data, w, h, 4, false)
		case "BC5U", "ATI2":
			return decodeBCn(data, w, h, 5, false)
		case "BC5S":
			return decodeBCn(data, w, h, -5, false)
		case "DX10":
			if len(data) < 20 {
				return nil, errCorrupt
			}
			dxgi := le32(data, 0)
			data = data[20:]
			switch dxgi {
			case 70, 71: // BC1
				return decodeBCn(data, w, h, 1, false)
			case 73, 74: // BC2
				return decodeBCn(data, w, h, 2, false)
			case 76, 77: // BC3
				return decodeBCn(data, w, h, 3, false)
			case 79, 80: // BC4
				return decodeBCn(data, w, h, 4, false)
			case 82, 83: // BC5
				return decodeBCn(data, w, h, 5, false)
			case 84: // BC5 signed
				return decodeBCn(data, w, h, -5, false)
			case 95, 96: // BC6H
				return decodeBC6H(data, w, h, dxgi == 96)
			case 97, 98, 99: // BC7
				return decodeBC7(data, w, h)
			case 27, 28, 29: // R8G8B8A8
				if len(data) < 4*w*h {
					return nil, errCorrupt
				}
				img := newNRGBA(w, h)
				copy(img.Pix, data)
				return img, nil
			}
			return nil, errors.New("unsupported DDS DXGI format")
		}
	}
	return nil, errors.New("unsupported DDS pixel format")
}

// ---------------------------------------------------------------- PSD

// decodePSD decodes the composite image of a Photoshop file.
func decodePSD(d []byte) (image.Image, error) {
	if len(d) < 26 || be16(d, 4) != 1 {
		return nil, errCorrupt
	}
	channels, h, w, bits, mode := be16(d, 12), be32(d, 14), be32(d, 18), be16(d, 22), be16(d, 24)
	if err := checkSize(w, h); err != nil {
		return nil, err
	}
	need := map[int]int{0: 1, 1: 1, 2: 1, 3: 3, 4: 4, 7: 1, 8: 1, 9: 3}
	nc, ok := need[mode]
	if !ok || (bits != 8 && bits != 16 && !(bits == 1 && mode == 0)) || (bits == 1 && mode != 0) || channels < nc {
		return nil, errors.New("unsupported PSD mode")
	}
	if mode == 3 && channels >= 4 {
		nc = 4
	}
	p := 26
	section := func() []byte {
		if p+4 > len(d) {
			return nil
		}
		n := be32(d, p)
		p += 4
		if n < 0 || p+n > len(d) {
			p = len(d)
			return nil
		}
		s := d[p : p+n]
		p += n
		return s
	}
	cmData := section()
	section() // image resources
	section() // layers and masks
	if p+2 > len(d) {
		return nil, errCorrupt
	}
	compression := be16(d, p)
	p += 2
	bpr := (w*bits + 7) / 8 // bytes per row
	planes := make([][]byte, nc)
	switch compression {
	case 0:
		for c := 0; c < nc; c++ {
			if p+bpr*h > len(d) {
				return nil, errCorrupt
			}
			planes[c] = d[p : p+bpr*h]
			p += bpr * h
		}
	case 1:
		counts := p
		p += 2 * channels * h
		for c := 0; c < nc; c++ {
			plane := make([]byte, 0, bpr*h)
			for y := 0; y < h; y++ {
				if counts+2*(c*h+y)+2 > len(d) {
					return nil, errCorrupt
				}
				n := be16(d, counts+2*(c*h+y))
				if p+n > len(d) {
					return nil, errCorrupt
				}
				row := unpackBits(d[p:p+n], bpr)
				p += n
				plane = append(plane, row...)
			}
			planes[c] = plane
		}
	default:
		return nil, errors.New("unsupported PSD compression")
	}
	sample := func(c, i int) byte {
		if bits == 16 {
			return planes[c][2*i]
		}
		return planes[c][i]
	}
	if bits == 1 {
		return bilevel(planes[0], w, h, bpr, true)
	}
	if mode == 4 {
		// Inverted CMYK samples, kept in CMYK.
		cmyk := image.NewCMYK(image.Rect(0, 0, w, h))
		for i := 0; i < w*h; i++ {
			for k := 0; k < 4; k++ {
				cmyk.Pix[4*i+k] = 255 - sample(k, i)
			}
		}
		return cmyk, nil
	}
	img := newNRGBA(w, h)
	for i := 0; i < w*h; i++ {
		var r, g, b, a byte = 0, 0, 0, 255
		switch mode {
		case 0, 1, 7, 8:
			r = sample(0, i)
			g, b = r, r
		case 2:
			v := int(sample(0, i))
			if len(cmData) == 768 {
				r, g, b = cmData[v], cmData[256+v], cmData[512+v]
			} else {
				r, g, b = byte(v), byte(v), byte(v)
			}
		case 3:
			r, g, b = sample(0, i), sample(1, i), sample(2, i)
			if nc == 4 {
				a = sample(3, i)
			}
		case 4:
			// Inverted CMYK.
			k := int(sample(3, i))
			r = byte(int(sample(0, i)) * k / 255)
			g = byte(int(sample(1, i)) * k / 255)
			b = byte(int(sample(2, i)) * k / 255)
		case 9:
			r, g, b = labToSRGB(float64(sample(0, i))*100/255, float64(sample(1, i))-128, float64(sample(2, i))-128)
		}
		img.Pix[4*i], img.Pix[4*i+1], img.Pix[4*i+2], img.Pix[4*i+3] = r, g, b, a
	}
	return img, nil
}

// unpackBits decodes PackBits data.
func unpackBits(src []byte, n int) []byte {
	out := make([]byte, 0, n)
	for i := 0; i < len(src) && len(out) < n; {
		c := int(int8(src[i]))
		i++
		switch {
		case c >= 0:
			e := min(i+c+1, len(src))
			out = append(out, src[i:e]...)
			i = e
		case c != -128:
			if i < len(src) {
				for k := 0; k < 1-c; k++ {
					out = append(out, src[i])
				}
				i++
			}
		}
	}
	for len(out) < n {
		out = append(out, 0)
	}
	return out[:n]
}

// labToSRGB converts a CIE Lab (D50) color to sRGB.
func labToSRGB(l, a, b float64) (byte, byte, byte) {
	fy := (l + 16) / 116
	fx := fy + a/500
	fz := fy - b/200
	finv := func(t float64) float64 {
		if t > 6.0/29 {
			return t * t * t
		}
		return 3 * (6.0 / 29) * (6.0 / 29) * (t - 4.0/29)
	}
	x, y, z := 0.9642*finv(fx), finv(fy), 0.8249*finv(fz)
	// Bradford D50 to D65, then XYZ to linear sRGB.
	x, y, z = 0.9555766*x-0.0230393*y+0.0631636*z, -0.0282895*x+1.0099416*y+0.0210077*z, 0.0122982*x-0.0204830*y+1.3299098*z
	rl := 3.2404542*x - 1.5371385*y - 0.4985314*z
	gl := -0.9692660*x + 1.8760108*y + 0.0415560*z
	bl := 0.0556434*x - 0.2040259*y + 1.0572252*z
	enc := func(v float64) byte {
		if v <= 0.0031308 {
			v *= 12.92
		} else {
			v = 1.055*math.Pow(v, 1/2.4) - 0.055
		}
		return clamp8(math.Round(v * 255))
	}
	return enc(rl), enc(gl), enc(bl)
}

// ---------------------------------------------------------------- FLI/FLC

// decodeFLI decodes the first frame of an Autodesk animation.
func decodeFLI(d []byte) (image.Image, error) {
	if len(d) < 128 || (le16(d, 14) != 0 && le16(d, 14) != 3) {
		return nil, errCorrupt
	}
	w, h := le16(d, 8), le16(d, 10)
	if err := checkSize(w, h); err != nil {
		return nil, err
	}
	pal := make([]color.NRGBA, 256)
	for i := range pal {
		pal[i] = color.NRGBA{byte(i), byte(i), byte(i), 255}
	}
	idx := make([]byte, w*h)
	p := 128
	// Skip a prefix chunk.
	if p+16 <= len(d) && le16(d, p+4) == 0xf100 {
		p += le32(d, p)
	}
	if p+16 > len(d) || le16(d, p+4) != 0xf1fa {
		return nil, errCorrupt
	}
	frameEnd := min(p+le32(d, p), len(d))
	nsub := le16(d, p+6)
	q := p + 16
	for s := 0; s < nsub && q+6 <= frameEnd; s++ {
		size, typ := le32(d, q), le16(d, q+4)
		if size < 6 || q+size > len(d) {
			break
		}
		c := d[q+6 : q+size]
		switch typ {
		case 4, 11: // palette
			shift := uint(0)
			if typ == 11 {
				shift = 2
			}
			if len(c) < 2 {
				break
			}
			i, k := 0, 2
			for pk := 0; pk < le16(c, 0) && k+2 <= len(c); pk++ {
				i += int(c[k])
				n := int(c[k+1])
				if n == 0 {
					n = 256
				}
				k += 2
				for j := 0; j < n && k+3 <= len(c) && i < 256; j++ {
					pal[i] = color.NRGBA{c[k] << shift, c[k+1] << shift, c[k+2] << shift, 255}
					i++
					k += 3
				}
			}
		case 13: // black
			for i := range idx {
				idx[i] = 0
			}
		case 16: // copy
			copy(idx, c)
		case 15: // byte run
			k := 0
			for y := 0; y < h && k < len(c); y++ {
				k++ // packet count (ignored)
				x := 0
				for x < w && k < len(c) {
					n := int(int8(c[k]))
					k++
					if n > 0 {
						if k >= len(c) {
							break
						}
						for j := 0; j < n && x < w; j++ {
							idx[y*w+x] = c[k]
							x++
						}
						k++
					} else {
						for j := 0; j < -n && x < w && k < len(c); j++ {
							idx[y*w+x] = c[k]
							x++
							k++
						}
					}
				}
			}
		case 12: // byte delta (LC)
			if len(c) < 4 {
				break
			}
			y0, lines := le16(c, 0), le16(c, 2)
			k := 4
			for y := y0; y < y0+lines && y < h && k < len(c); y++ {
				packets := int(c[k])
				k++
				x := 0
				for pk := 0; pk < packets && k+2 <= len(c); pk++ {
					x += int(c[k])
					n := int(int8(c[k+1]))
					k += 2
					if n > 0 {
						for j := 0; j < n && k < len(c); j++ {
							if x < w {
								idx[y*w+x] = c[k]
							}
							x++
							k++
						}
					} else if k < len(c) {
						for j := 0; j < -n; j++ {
							if x < w {
								idx[y*w+x] = c[k]
							}
							x++
						}
						k++
					}
				}
			}
		case 7: // word delta (SS2)
			if len(c) < 2 {
				break
			}
			lines := le16(c, 0)
			k := 2
			y := 0
			for l := 0; l < lines && k+2 <= len(c) && y < h; {
				v := int(int16(le16(c, k)))
				k += 2
				switch {
				case v&0xc000 == 0xc000:
					y -= v
					continue
				case v&0xc000 == 0x8000:
					if w > 0 {
						idx[y*w+w-1] = byte(v)
					}
					continue
				}
				x := 0
				for pk := 0; pk < v && k+2 <= len(c); pk++ {
					x += int(c[k])
					n := int(int8(c[k+1]))
					k += 2
					if n > 0 {
						for j := 0; j < n && k+2 <= len(c); j++ {
							if x+1 < w {
								idx[y*w+x], idx[y*w+x+1] = c[k], c[k+1]
							}
							x += 2
							k += 2
						}
					} else if k+2 <= len(c) {
						for j := 0; j < -n; j++ {
							if x+1 < w {
								idx[y*w+x], idx[y*w+x+1] = c[k], c[k+1]
							}
							x += 2
						}
						k += 2
					}
				}
				y++
				l++
			}
		}
		q += size
	}
	return paletted(w, h, idx, pal), nil
}

// ---------------------------------------------------------------- PhotoCD

// decodePCD decodes the base (768×512) image of a Kodak PhotoCD file,
// converting its PhotoYCC colors to RGB.
func decodePCD(d []byte) (image.Image, error) {
	const w, h = 768, 512
	off := 96 * 2048
	if off+h/2*3*w > len(d) {
		return nil, errCorrupt
	}
	img := newNRGBA(w, h)
	for y := 0; y < h; y += 2 {
		chunk := d[off+y/2*3*w:]
		for r := 0; r < 2; r++ {
			for x := 0; x < w; x++ {
				l := 1.3584 * float64(chunk[r*w+x])
				c1 := 2.2179 * (float64(chunk[2*w+x/2]) - 156)
				c2 := 1.8215 * (float64(chunk[2*w+w/2+x/2]) - 137)
				o := img.PixOffset(x, y+r)
				img.Pix[o] = clamp8(math.Round(l + c2))
				img.Pix[o+1] = clamp8(math.Round(l - 0.194*c1 - 0.509*c2))
				img.Pix[o+2] = clamp8(math.Round(l + c1))
				img.Pix[o+3] = 255
			}
		}
	}
	switch d[2048+1538] & 3 {
	case 1:
		return rotate90(img, false), nil
	case 3:
		return rotate90(img, true), nil
	}
	return img, nil
}

// rotate90 rotates an image a quarter turn counterclockwise (or
// clockwise).
func rotate90(src *image.NRGBA, cw bool) *image.NRGBA {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	out := newNRGBA(h, w)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var nx, ny int
			if cw {
				nx, ny = h-1-y, x
			} else {
				nx, ny = y, w-1-x
			}
			copy(out.Pix[out.PixOffset(nx, ny):out.PixOffset(nx, ny)+4], src.Pix[src.PixOffset(x, y):src.PixOffset(x, y)+4])
		}
	}
	return out
}

// ---------------------------------------------------------------- IPTC/NAA

func decodeIPTC(d []byte) (image.Image, error) {
	info := map[[2]int][][]byte{}
	p := 0
	field := func() ([2]int, int, bool) {
		if p+5 > len(d) || d[p] != 0x1c {
			return [2]int{}, 0, false
		}
		tag := [2]int{int(d[p+1]), int(d[p+2])}
		size := int(d[p+3])
		p += 5
		switch {
		case size > 132:
			return tag, 0, false
		case size == 128:
			size = 0
		case size > 128:
			n := size - 128
			if p+n > len(d) {
				return tag, 0, false
			}
			size = 0
			for _, b := range d[p : p+n] {
				size = size<<8 | int(b)
			}
			p += n
		default:
			size = be16(d, p-2)
		}
		return tag, size, true
	}
	var data []byte
	for {
		tag, size, ok := field()
		if !ok {
			break
		}
		if p+size > len(d) {
			return nil, errCorrupt
		}
		if tag == [2]int{8, 10} {
			data = append(data, d[p:p+size]...)
		} else {
			info[tag] = append(info[tag], d[p:p+size])
		}
		p += size
	}
	geti := func(t [2]int) int {
		v := 0
		if len(info[t]) > 0 {
			for _, b := range info[t][0] {
				v = v<<8 | int(b)
			}
		}
		return v
	}
	lc := info[[2]int{3, 60}]
	if len(lc) == 0 || len(lc[0]) < 2 || data == nil {
		return nil, errCorrupt
	}
	layers, component := int(lc[0][0]), lc[0][1]
	w, h := geti([2]int{3, 20}), geti([2]int{3, 30})
	if err := checkSize(w, h); err != nil {
		return nil, err
	}
	var plane image.Image
	switch geti([2]int{3, 120}) {
	case 1:
		if len(data) < w*h {
			return nil, errCorrupt
		}
		g := newGray(w, h)
		copy(g.Pix, data)
		plane = g
	case 5:
		img, err := jpeg.Decode(bytes.NewReader(data))
		if err != nil {
			return nil, err
		}
		plane = img
	default:
		return nil, errors.New("unsupported IPTC compression")
	}
	if layers == 1 && component == 0 {
		return plane, nil
	}
	band := 0
	if b := info[[2]int{3, 65}]; len(b) > 0 && len(b[0]) > 0 {
		band = int(b[0][0]) - 1
	}
	// One band of an RGB or CMYK image, the others being 0.
	img := newNRGBA(w, h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			v := color.GrayModel.Convert(plane.At(x, y)).(color.Gray).Y
			o := img.PixOffset(x, y)
			if layers == 4 {
				var cmyk [4]byte
				if band >= 0 && band < 4 {
					cmyk[band] = v
				}
				r, g, b := color.CMYKToRGB(cmyk[0], cmyk[1], cmyk[2], cmyk[3])
				img.Pix[o], img.Pix[o+1], img.Pix[o+2] = r, g, b
			} else if band >= 0 && band < 3 {
				img.Pix[o+band] = v
			}
			img.Pix[o+3] = 255
		}
	}
	return img, nil
}

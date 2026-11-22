package images

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"math"
	"regexp"
	"strconv"
	"strings"
)

func init() {
	registerBytes("qoi", "qoif", decodeQOI)
	for _, m := range []string{"P1", "P2", "P3", "P4", "P5", "P6", "Pf", "PF"} {
		registerBytes("pnm", m, decodePNM)
	}
	registerBytes("xpm", "/* XPM */", decodeXPM)
	registerBytes("dcx", "\xb1\x68\xde\x3a", decodeDCX)
	for _, v := range []string{"\x0a\x00", "\x0a\x02", "\x0a\x03", "\x0a\x05"} {
		registerBytes("pcx", v, decodePCX)
	}
	registerBytes("sgi", "\x01\xda", decodeSGI)
	registerBytes("sun", "\x59\xa6\x6a\x95", decodeSun)
	registerBytes("ico", "\x00\x00\x01\x00", decodeICO)
	registerBytes("cur", "\x00\x00\x02\x00", decodeCUR)
	sniffers = append(sniffers,
		sniffer{"xbm", func(d []byte) bool { return hasPrefix(bytes.TrimLeft(d, " \t\r\n\f\v"), "#define") }, decodeXBM},
		sniffer{"dib", func(d []byte) bool {
			if len(d) < 4 {
				return false
			}
			switch le32(d, 0) {
			case 12, 40, 52, 56, 64, 108, 124:
				return true
			}
			return false
		}, func(d []byte) (image.Image, error) { return decodeDIB(d, 0) }},
		sniffer{"tga", func(d []byte) bool { return len(d) >= 18 }, decodeTGA},
	)
}

// ---------------------------------------------------------------- QOI

func decodeQOI(d []byte) (image.Image, error) {
	if len(d) < 14 {
		return nil, errCorrupt
	}
	w, h := be32(d, 4), be32(d, 8)
	if err := checkSize(w, h); err != nil {
		return nil, err
	}
	channels := d[12]
	img := newNRGBA(w, h)
	var seen [64][4]byte
	prev := [4]byte{0, 0, 0, 255}
	p := 14
	n := w * h
	for i := 0; i < n; {
		if p >= len(d) {
			return nil, errCorrupt
		}
		b := d[p]
		p++
		v := prev
		run := 1
		switch {
		case b == 0xfe:
			if p+3 > len(d) {
				return nil, errCorrupt
			}
			v[0], v[1], v[2] = d[p], d[p+1], d[p+2]
			p += 3
		case b == 0xff:
			if p+4 > len(d) {
				return nil, errCorrupt
			}
			copy(v[:], d[p:p+4])
			p += 4
		case b>>6 == 0:
			// Like Pillow, missing entries are transparent black.
			v = seen[b&63]
		case b>>6 == 1:
			v[0] = prev[0] + (b>>4)&3 - 2
			v[1] = prev[1] + (b>>2)&3 - 2
			v[2] = prev[2] + b&3 - 2
		case b>>6 == 2:
			if p >= len(d) {
				return nil, errCorrupt
			}
			b2 := d[p]
			p++
			dg := int(b&63) - 32
			v[0] = byte(int(prev[0]) + dg + int(b2>>4) - 8)
			v[1] = byte(int(prev[1]) + dg)
			v[2] = byte(int(prev[2]) + dg + int(b2&15) - 8)
		default:
			run = int(b&63) + 1
		}
		if run == 1 {
			seen[(int(v[0])*3+int(v[1])*5+int(v[2])*7+int(v[3])*11)%64] = v
		}
		for k := 0; k < run && i < n; k++ {
			copy(img.Pix[4*i:], v[:])
			if channels == 3 {
				img.Pix[4*i+3] = 255
			}
			i++
		}
		prev = v
	}
	return img, nil
}

// ---------------------------------------------------------------- PNM

func decodePNM(d []byte) (image.Image, error) {
	p := 2
	magic := string(d[:2])
	token := func() (string, error) {
		for p < len(d) {
			c := d[p]
			if c == '#' {
				for p < len(d) && d[p] != '\n' && d[p] != '\r' {
					p++
				}
				continue
			}
			if c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\v' || c == '\f' {
				p++
				continue
			}
			break
		}
		s := p
		for p < len(d) && !strings.ContainsRune(" \t\n\r\v\f#", rune(d[p])) {
			p++
		}
		if s == p || p-s > 10 {
			return "", errCorrupt
		}
		return string(d[s:p]), nil
	}
	num := func() (int, error) {
		t, err := token()
		if err != nil {
			return 0, err
		}
		return strconv.Atoi(t)
	}
	w, err := num()
	if err != nil {
		return nil, err
	}
	h, err := num()
	if err != nil {
		return nil, err
	}
	if err := checkSize(w, h); err != nil {
		return nil, err
	}
	switch magic {
	case "P1", "P4":
		img := newGray(w, h)
		if magic == "P4" {
			p++
			stride := (w + 7) / 8
			if p+stride*h > len(d) {
				return nil, errCorrupt
			}
			for y := 0; y < h; y++ {
				for x := 0; x < w; x++ {
					if d[p+y*stride+x/8]&(0x80>>uint(x&7)) == 0 {
						img.Pix[y*w+x] = 255
					}
				}
			}
			return img, nil
		}
		i := 0
		for ; p < len(d) && i < w*h; p++ {
			switch d[p] {
			case '0':
				img.Pix[i] = 255
				i++
			case '1':
				i++
			case '#':
				for p < len(d) && d[p] != '\n' && d[p] != '\r' {
					p++
				}
			case ' ', '\t', '\n', '\r', '\v', '\f':
			default:
				return nil, errCorrupt
			}
		}
		return img, nil
	case "Pf", "PF":
		t, err := token()
		if err != nil {
			return nil, err
		}
		scale, err := strconv.ParseFloat(t, 64)
		if err != nil || scale == 0 || math.IsInf(scale, 0) || math.IsNaN(scale) {
			return nil, errCorrupt
		}
		p++
		bands := 1
		if magic == "PF" {
			bands = 3
		}
		if p+4*bands*w*h > len(d) {
			return nil, errCorrupt
		}
		var order binary.ByteOrder = binary.BigEndian
		if scale < 0 {
			order = binary.LittleEndian
		}
		// Rows are stored bottom to top; samples (usually between 0 and
		// 1) are clamped.
		img := newNRGBA(w, h)
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				o := img.PixOffset(x, h-1-y)
				for c := 0; c < 3; c++ {
					k := c
					if bands == 1 {
						k = 0
					}
					f := math.Float32frombits(order.Uint32(d[p+4*(bands*(y*w+x)+k):]))
					img.Pix[o+c] = clamp8(math.Round(float64(f) * 255))
				}
				img.Pix[o+3] = 255
			}
		}
		if bands == 1 {
			g := newGray(w, h)
			for i := range g.Pix {
				g.Pix[i] = img.Pix[4*i]
			}
			return g, nil
		}
		return img, nil
	}
	maxval, err := num()
	if err != nil {
		return nil, err
	}
	if maxval <= 0 || maxval >= 65536 {
		return nil, errCorrupt
	}
	bands := 1
	if magic == "P3" || magic == "P6" {
		bands = 3
	}
	n := w * h * bands
	vals := make([]int, 0, n)
	if magic == "P2" || magic == "P3" {
		for len(vals) < n {
			t, err := token()
			if err != nil {
				break
			}
			v, err := strconv.Atoi(t)
			if err != nil || v < 0 || v > maxval {
				return nil, errCorrupt
			}
			vals = append(vals, v)
		}
	} else {
		p++
		size := 1
		if maxval > 255 {
			size = 2
		}
		for i := 0; i < n && p+size <= len(d); i++ {
			if size == 1 {
				vals = append(vals, int(d[p]))
			} else {
				vals = append(vals, be16(d, p))
			}
			p += size
		}
	}
	for len(vals) < n {
		vals = append(vals, 0)
	}
	// Samples are scaled to 8 bits (Pillow keeps 16-bit gray samples,
	// which WeasyPrint then clips).
	scale := func(v int) byte {
		if v > maxval {
			v = maxval
		}
		// Rounded half to even, like Pillow.
		return byte(math.RoundToEven(float64(v) * 255 / float64(maxval)))
	}
	if bands == 1 {
		img := newGray(w, h)
		for i := range img.Pix {
			img.Pix[i] = scale(vals[i])
		}
		return img, nil
	}
	img := newNRGBA(w, h)
	for i := 0; i < w*h; i++ {
		img.Pix[4*i], img.Pix[4*i+1], img.Pix[4*i+2], img.Pix[4*i+3] = scale(vals[3*i]), scale(vals[3*i+1]), scale(vals[3*i+2]), 255
	}
	return img, nil
}

func clamp8(v float64) byte {
	switch {
	case v <= 0 || math.IsNaN(v):
		return 0
	case v >= 255:
		return 255
	}
	return byte(v)
}

// ---------------------------------------------------------------- XBM

var xbmHead = regexp.MustCompile(`^\s*#define[ \t]+.*_width[ \t]+([0-9]+)[\r\n]+#define[ \t]+.*_height[ \t]+([0-9]+)[\r\n]+(?:#define[ \t]+[^_]*_x_hot[ \t]+[0-9]+[\r\n]+#define[ \t]+[^_]*_y_hot[ \t]+[0-9]+[\r\n]+)?`)

func decodeXBM(d []byte) (image.Image, error) {
	m := xbmHead.FindSubmatchIndex(d[:min(len(d), 512)])
	if m == nil {
		return nil, errCorrupt
	}
	w, _ := strconv.Atoi(string(d[m[2]:m[3]]))
	h, _ := strconv.Atoi(string(d[m[4]:m[5]]))
	if err := checkSize(w, h); err != nil {
		return nil, err
	}
	i := bytes.Index(d, []byte("_bits[]"))
	if i < 0 {
		return nil, errCorrupt
	}
	body := d[i:]
	if j := bytes.IndexByte(body, '{'); j >= 0 {
		body = body[j+1:]
	}
	stride := (w + 7) / 8
	data := make([]byte, 0, stride*h)
	for _, f := range bytes.FieldsFunc(body, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\n' || r == '\r' || r == '\t' || r == '}' || r == ';'
	}) {
		if len(data) == stride*h {
			break
		}
		v, err := strconv.ParseUint(string(f), 0, 16)
		if err != nil {
			continue
		}
		data = append(data, byte(v))
	}
	if len(data) < stride*h {
		return nil, errCorrupt
	}
	img := newGray(w, h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			// Set bits are the foreground (black).
			if data[y*stride+x/8]&(1<<uint(x&7)) == 0 {
				img.Pix[y*w+x] = 255
			}
		}
	}
	return img, nil
}

// ---------------------------------------------------------------- XPM

var xpmHead = regexp.MustCompile(`"([0-9]+) ([0-9]+) ([0-9]+) ([0-9]+)`)

func decodeXPM(d []byte) (image.Image, error) {
	lines := bytes.Split(d, []byte("\n"))
	li := 0
	var m [][]byte
	for ; li < len(lines); li++ {
		if m = xpmHead.FindSubmatch(lines[li]); m != nil {
			li++
			break
		}
	}
	if m == nil {
		return nil, errCorrupt
	}
	w, _ := strconv.Atoi(string(m[1]))
	h, _ := strconv.Atoi(string(m[2]))
	ncolors, _ := strconv.Atoi(string(m[3]))
	cpp, _ := strconv.Atoi(string(m[4]))
	if err := checkSize(w, h); err != nil || cpp < 1 || ncolors < 1 {
		return nil, errCorrupt
	}
	// Quoted strings, in order.
	var strs [][]byte
	for ; li < len(lines); li++ {
		l := lines[li]
		a := bytes.IndexByte(l, '"')
		b := bytes.LastIndexByte(l, '"')
		if a >= 0 && b > a {
			strs = append(strs, l[a+1:b])
		}
	}
	if len(strs) < ncolors+h {
		return nil, errCorrupt
	}
	pal := map[string]color.NRGBA{}
	for _, s := range strs[:ncolors] {
		if len(s) < cpp {
			return nil, errCorrupt
		}
		key := string(s[:cpp])
		f := strings.Fields(string(s[cpp:]))
		found := false
		for i := 0; i+1 < len(f); i += 2 {
			if f[i] != "c" {
				continue
			}
			c, ok := xpmColor(strings.Join(f[i+1:], " "))
			if !ok {
				c, ok = xpmColor(f[i+1])
			}
			if !ok {
				return nil, errors.New("unsupported XPM color")
			}
			pal[key] = c
			found = true
			break
		}
		if !found {
			return nil, errors.New("unsupported XPM color")
		}
	}
	img := newNRGBA(w, h)
	for y := 0; y < h; y++ {
		row := strs[ncolors+y]
		for x := 0; x < w; x++ {
			if (x+1)*cpp > len(row) {
				break
			}
			c, ok := pal[string(row[x*cpp:(x+1)*cpp])]
			if !ok {
				return nil, errCorrupt
			}
			o := 4 * (y*w + x)
			img.Pix[o], img.Pix[o+1], img.Pix[o+2], img.Pix[o+3] = c.R, c.G, c.B, c.A
		}
	}
	return img, nil
}

// xpmColor parses an XPM color: None or a hexadecimal color.
func xpmColor(s string) (color.NRGBA, bool) {
	if strings.EqualFold(s, "none") {
		return color.NRGBA{}, true
	}
	if !strings.HasPrefix(s, "#") {
		return color.NRGBA{}, false
	}
	h := s[1:]
	v, err := strconv.ParseUint(h, 16, 64)
	if err != nil {
		return color.NRGBA{}, false
	}
	switch len(h) {
	case 3:
		return color.NRGBA{byte(v>>8&15) * 17, byte(v>>4&15) * 17, byte(v&15) * 17, 255}, true
	case 6:
		return color.NRGBA{byte(v >> 16), byte(v >> 8), byte(v), 255}, true
	case 12:
		return color.NRGBA{byte(v >> 40), byte(v >> 24), byte(v >> 8), 255}, true
	}
	return color.NRGBA{}, false
}

// ---------------------------------------------------------------- PCX

func decodePCX(d []byte) (image.Image, error) {
	if len(d) < 128 {
		return nil, errCorrupt
	}
	x0, y0, x1, y1 := le16(d, 4), le16(d, 6), le16(d, 8)+1, le16(d, 10)+1
	if x1 <= x0 || y1 <= y0 {
		return nil, errCorrupt
	}
	w, h := x1-x0, y1-y0
	if err := checkSize(w, h); err != nil {
		return nil, err
	}
	version, bits, planes, provided := d[1], int(d[3]), int(d[65]), le16(d, 66)
	stride := (w*bits + 7) / 8
	if provided != stride {
		stride += stride % 2
	}
	line := planes * stride
	// Run-length decoding.
	raw := make([]byte, 0, line*h)
	for p := 128; len(raw) < line*h && p < len(d); {
		b := d[p]
		p++
		if b >= 0xc0 && p < len(d) {
			for k := 0; k < int(b&0x3f); k++ {
				raw = append(raw, d[p])
			}
			p++
		} else {
			raw = append(raw, b)
		}
	}
	for len(raw) < line*h {
		raw = append(raw, 0)
	}
	switch {
	case bits == 1 && planes == 1:
		img := newGray(w, h)
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				if raw[y*line+x/8]&(0x80>>uint(x&7)) != 0 {
					img.Pix[y*w+x] = 255
				}
			}
		}
		return img, nil
	case bits == 1 && (planes == 2 || planes == 4):
		pal := rgbPalette(d[16:64], 16)
		idx := make([]byte, w*h)
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				var v byte
				for pl := 0; pl < planes; pl++ {
					if raw[y*line+pl*stride+x/8]&(0x80>>uint(x&7)) != 0 {
						v |= 1 << uint(pl)
					}
				}
				idx[y*w+x] = v
			}
		}
		return paletted(w, h, idx, pal), nil
	case version == 5 && bits == 8 && planes == 1:
		idx := make([]byte, w*h)
		for y := 0; y < h; y++ {
			copy(idx[y*w:(y+1)*w], raw[y*line:])
		}
		if len(d) >= 769 && d[len(d)-769] == 12 {
			pal := rgbPalette(d[len(d)-768:], 256)
			return paletted(w, h, idx, pal), nil
		}
		g := newGray(w, h)
		copy(g.Pix, idx)
		return g, nil
	case version == 5 && bits == 8 && planes == 3:
		img := newNRGBA(w, h)
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				o := 4 * (y*w + x)
				img.Pix[o], img.Pix[o+1], img.Pix[o+2], img.Pix[o+3] = raw[y*line+x], raw[y*line+stride+x], raw[y*line+2*stride+x], 255
			}
		}
		return img, nil
	}
	return nil, errors.New("unsupported PCX mode")
}

func decodeDCX(d []byte) (image.Image, error) {
	if len(d) < 8 {
		return nil, errCorrupt
	}
	off := le32(d, 4)
	if off <= 0 || off >= len(d) {
		return nil, errCorrupt
	}
	return decodePCX(d[off:])
}

// ---------------------------------------------------------------- SGI

func decodeSGI(d []byte) (image.Image, error) {
	if len(d) < 512 {
		return nil, errCorrupt
	}
	comp, bpc := d[2], int(d[3])
	dim := be16(d, 4)
	w, h, z := be16(d, 6), be16(d, 8), be16(d, 10)
	if err := checkSize(w, h); err != nil {
		return nil, err
	}
	if (bpc != 1 && bpc != 2) || dim < 1 || dim > 3 || (z != 1 && z != 3 && z != 4) || (dim < 3 && z != 1) {
		return nil, errors.New("unsupported SGI image mode")
	}
	// planes[c][row] holds 8-bit samples (16-bit ones keep their high
	// byte, like Pillow); rows are stored bottom to top.
	planes := make([][]byte, z)
	for c := range planes {
		planes[c] = make([]byte, w*h)
	}
	if comp == 0 {
		for c := 0; c < z; c++ {
			for row := 0; row < h; row++ {
				for x := 0; x < w; x++ {
					o := 512 + ((c*h+row)*w+x)*bpc
					if o+bpc > len(d) {
						return nil, errCorrupt
					}
					planes[c][(h-1-row)*w+x] = d[o]
				}
			}
		}
	} else {
		tab := 512
		if tab+8*h*z > len(d) {
			return nil, errCorrupt
		}
		for c := 0; c < z; c++ {
			for row := 0; row < h; row++ {
				start := be32(d, tab+4*(c*h+row))
				out := planes[c][(h-1-row)*w : (h-row)*w]
				x := 0
				p := start
				get := func() (int, bool) {
					if p+bpc > len(d) {
						return 0, false
					}
					v := int(d[p])
					if bpc == 2 {
						v = be16(d, p)
					}
					p += bpc
					return v, true
				}
				for {
					v, ok := get()
					if !ok {
						return nil, errCorrupt
					}
					n := v & 0x7f
					if n == 0 {
						break
					}
					if v&0x80 != 0 {
						for k := 0; k < n; k++ {
							s, ok := get()
							if !ok {
								return nil, errCorrupt
							}
							if x < w {
								out[x] = byte(s >> (8 * (bpc - 1)))
							}
							x++
						}
					} else {
						s, ok := get()
						if !ok {
							return nil, errCorrupt
						}
						for k := 0; k < n; k++ {
							if x < w {
								out[x] = byte(s >> (8 * (bpc - 1)))
							}
							x++
						}
					}
				}
			}
		}
	}
	if z == 1 {
		g := newGray(w, h)
		copy(g.Pix, planes[0])
		return g, nil
	}
	img := newNRGBA(w, h)
	for i := 0; i < w*h; i++ {
		img.Pix[4*i], img.Pix[4*i+1], img.Pix[4*i+2], img.Pix[4*i+3] = planes[0][i], planes[1][i], planes[2][i], 255
		if z == 4 {
			img.Pix[4*i+3] = planes[3][i]
		}
	}
	return img, nil
}

// ---------------------------------------------------------------- Sun raster

func decodeSun(d []byte) (image.Image, error) {
	if len(d) < 32 {
		return nil, errCorrupt
	}
	w, h, depth := be32(d, 4), be32(d, 8), be32(d, 12)
	ftype, ptype, plen := be32(d, 20), be32(d, 24), be32(d, 28)
	if err := checkSize(w, h); err != nil {
		return nil, err
	}
	switch depth {
	case 1, 4, 8, 24, 32:
	default:
		return nil, errors.New("unsupported Sun raster depth")
	}
	off := 32
	var pal []color.NRGBA
	if plen > 0 {
		if plen > 1024 || ptype != 1 || off+plen > len(d) {
			return nil, errors.New("unsupported Sun raster palette")
		}
		// Planar palette: all reds, then greens, then blues.
		n := plen / 3
		pal = make([]color.NRGBA, 256)
		for i := range pal {
			pal[i] = color.NRGBA{A: 255}
		}
		for i := 0; i < n && i < 256; i++ {
			pal[i] = color.NRGBA{d[off+i], d[off+n+i], d[off+2*n+i], 255}
		}
		off += plen
	}
	stride := ((w*depth + 15) / 16) * 2
	var raw []byte
	switch ftype {
	case 0, 1, 3, 4, 5:
		raw = d[off:]
	case 2:
		// Run-length encoding (0x80 count value); like Pillow, the rows
		// are not padded in the decoded data.
		stride = (w*depth + 7) / 8
		for p := off; p < len(d) && len(raw) < stride*h; {
			b := d[p]
			p++
			if b != 0x80 {
				raw = append(raw, b)
				continue
			}
			if p >= len(d) {
				break
			}
			n := int(d[p])
			p++
			if n == 0 {
				raw = append(raw, 0x80)
				continue
			}
			if p >= len(d) {
				break
			}
			for k := 0; k <= n; k++ {
				raw = append(raw, d[p])
			}
			p++
		}
	default:
		return nil, errors.New("unsupported Sun raster type")
	}
	if len(raw) < stride*(h-1)+(w*depth+7)/8 {
		return nil, errCorrupt
	}
	switch depth {
	case 1, 4, 8:
		idx := make([]byte, w*h)
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				row := raw[y*stride:]
				switch depth {
				case 1:
					// Set bits are black.
					if row[x/8]&(0x80>>uint(x&7)) == 0 {
						idx[y*w+x] = 255
					}
				case 4:
					idx[y*w+x] = (row[x/2] >> (4 * uint(1-x&1))) & 15 * 17
				case 8:
					idx[y*w+x] = row[x]
				}
			}
		}
		if pal != nil && depth == 8 {
			return paletted(w, h, idx, pal), nil
		}
		g := newGray(w, h)
		copy(g.Pix, idx)
		return g, nil
	}
	img := newNRGBA(w, h)
	bpp := depth / 8
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			s := raw[y*stride+x*bpp:]
			o := 4 * (y*w + x)
			// 32-bit pixels are BGRX or RGBX, like Pillow.
			if ftype == 3 {
				img.Pix[o], img.Pix[o+1], img.Pix[o+2] = s[0], s[1], s[2]
			} else {
				img.Pix[o], img.Pix[o+1], img.Pix[o+2] = s[2], s[1], s[0]
			}
			img.Pix[o+3] = 255
		}
	}
	return img, nil
}

// ---------------------------------------------------------------- TGA

func decodeTGA(d []byte) (image.Image, error) {
	if len(d) < 18 {
		return nil, errCorrupt
	}
	idLen, cmType, imType := int(d[0]), d[1], d[2]
	w, h := le16(d, 12), le16(d, 14)
	depth, flags := int(d[16]), d[17]
	if cmType > 1 || w <= 0 || h <= 0 {
		return nil, errCorrupt
	}
	switch depth {
	case 1, 8, 16, 24, 32:
	default:
		return nil, errCorrupt
	}
	if err := checkSize(w, h); err != nil {
		return nil, err
	}
	kind := imType & 7
	switch {
	case kind == 3 && (depth == 1 || depth == 8 || depth == 16):
	case kind == 1 && depth == 8:
	case kind == 2 && (depth == 16 || depth == 24 || depth == 32):
	default:
		return nil, errors.New("unsupported TGA mode")
	}
	if imType&^8 != 1 && imType&^8 != 2 && imType&^8 != 3 {
		return nil, errors.New("unsupported TGA mode")
	}
	p := 18 + idLen
	var pal []color.NRGBA
	if cmType == 1 {
		start, size, mapDepth := le16(d, 3), le16(d, 5), int(d[7])
		bpe := mapDepth / 8
		if mapDepth != 16 && mapDepth != 24 && mapDepth != 32 {
			return nil, errors.New("unsupported TGA map depth")
		}
		if p+bpe*size > len(d) {
			return nil, errCorrupt
		}
		pal = make([]color.NRGBA, start+size)
		for i := range pal {
			pal[i] = color.NRGBA{A: 255}
		}
		for i := 0; i < size; i++ {
			pal[start+i] = tgaPixel(d[p+bpe*i:], mapDepth)
		}
		p += bpe * size
	}
	bpp := (depth + 7) / 8
	if depth == 1 {
		bpp = 0
	}
	n := w * h
	var raw []byte
	if imType&8 != 0 && bpp > 0 {
		raw = make([]byte, 0, n*bpp)
		for len(raw) < n*bpp && p < len(d) {
			c := int(d[p])
			p++
			cnt := c&0x7f + 1
			if c&0x80 != 0 {
				if p+bpp > len(d) {
					break
				}
				for k := 0; k < cnt; k++ {
					raw = append(raw, d[p:p+bpp]...)
				}
				p += bpp
			} else {
				if p+cnt*bpp > len(d) {
					cnt = (len(d) - p) / bpp
				}
				raw = append(raw, d[p:p+cnt*bpp]...)
				p += cnt * bpp
			}
		}
		if len(raw) > n*bpp {
			raw = raw[:n*bpp]
		}
	} else if bpp > 0 {
		if p+n*bpp > len(d) {
			return nil, errCorrupt
		}
		raw = d[p : p+n*bpp]
	} else {
		stride := (w + 7) / 8
		if p+stride*h > len(d) {
			return nil, errCorrupt
		}
		raw = make([]byte, n)
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				if d[p+y*stride+x/8]&(0x80>>uint(x&7)) != 0 {
					raw[y*w+x] = 255
				}
			}
		}
		bpp = 1
	}
	for len(raw) < n*bpp {
		raw = append(raw, 0)
	}
	img := newNRGBA(w, h)
	// The alpha channel is ignored when the extension area says so.
	ignoreAlpha := false
	if (depth == 32 || depth == 16) && kind == 2 && len(d) >= 26 && bytes.HasSuffix(d, []byte("TRUEVISION-XFILE.\x00")) {
		if ext := le32(d, len(d)-26); ext > 0 && ext+494 < len(d) && d[ext+494] == 0 {
			ignoreAlpha = true
		}
	}
	for i := 0; i < n; i++ {
		var c color.NRGBA
		s := raw[i*bpp:]
		_ = s
		switch {
		case kind == 1 && pal != nil:
			if int(s[0]) < len(pal) {
				c = pal[s[0]]
			} else {
				c = color.NRGBA{A: 255}
			}
		case kind == 1 || (kind == 3 && depth != 16):
			c = color.NRGBA{s[0], s[0], s[0], 255}
		case kind == 3:
			c = color.NRGBA{s[0], s[0], s[0], s[1]}
		default:
			c = tgaPixel(s, depth)
			if depth == 16 && flags&0x0f == 0 {
				// No attribute bits: the top bit is not an alpha bit.
				c.A = 255
			}
		}
		if ignoreAlpha {
			c.A = 255
		}
		img.Pix[4*i], img.Pix[4*i+1], img.Pix[4*i+2], img.Pix[4*i+3] = c.R, c.G, c.B, c.A
	}
	// Rows are bottom to top unless the descriptor says otherwise.
	if flags&0x20 == 0 {
		flipV(img.Pix, 4*w, h)
	}
	if flags&0x10 != 0 {
		for y := 0; y < h; y++ {
			row := img.Pix[4*w*y : 4*w*(y+1)]
			for a, b := 0, w-1; a < b; a, b = a+1, b-1 {
				for k := 0; k < 4; k++ {
					row[4*a+k], row[4*b+k] = row[4*b+k], row[4*a+k]
				}
			}
		}
	}
	return img, nil
}

// tgaPixel reads a BGR(A) pixel of 16, 24 or 32 bits.
func tgaPixel(s []byte, depth int) color.NRGBA {
	switch depth {
	case 16:
		v := int(s[0]) | int(s[1])<<8
		ex := func(c int) byte { return byte((c << 3) | (c >> 2)) }
		a := byte(255)
		if v&0x8000 == 0 {
			a = 0
		}
		return color.NRGBA{ex(v >> 10 & 31), ex(v >> 5 & 31), ex(v & 31), a}
	case 24:
		return color.NRGBA{s[2], s[1], s[0], 255}
	}
	return color.NRGBA{s[2], s[1], s[0], s[3]}
}

// ---------------------------------------------------------------- ICO, CUR, DIB

// decodeDIB decodes a device independent bitmap (a BMP file without its
// file header). If height is not zero, it replaces the header height.
func decodeDIB(dib []byte, height int) (image.Image, error) {
	if len(dib) < 12 {
		return nil, errCorrupt
	}
	hs := le32(dib, 0)
	if hs < 12 || hs > len(dib) {
		return nil, errCorrupt
	}
	dib = append([]byte(nil), dib...)
	bpp, ncolors, compression := 0, 0, 0
	if hs == 12 {
		bpp = le16(dib, 10)
		if height != 0 {
			binary.LittleEndian.PutUint16(dib[6:], uint16(height))
		}
	} else {
		if len(dib) < 40 {
			return nil, errCorrupt
		}
		bpp = le16(dib, 14)
		compression = le32(dib, 16)
		ncolors = le32(dib, 32)
		if height != 0 {
			binary.LittleEndian.PutUint32(dib[8:], uint32(height))
		}
	}
	palSize := 0
	if bpp <= 8 {
		if ncolors == 0 || ncolors > 1<<uint(bpp) {
			ncolors = 1 << uint(bpp)
		}
		if hs == 12 {
			palSize = 3 * ncolors
		} else {
			palSize = 4 * ncolors
		}
	}
	if hs == 40 && (compression == 3 || compression == 6) {
		palSize += 12
		if compression == 6 {
			palSize += 4
		}
	}
	file := make([]byte, 14, 14+len(dib))
	file[0], file[1] = 'B', 'M'
	binary.LittleEndian.PutUint32(file[2:], uint32(14+len(dib)))
	binary.LittleEndian.PutUint32(file[10:], uint32(14+hs+palSize))
	return decodeBMP(bytes.NewReader(append(file, dib...)))
}

type iconEntry struct {
	w, h, depth, bpp, size, offset int
}

func iconEntries(d []byte) ([]iconEntry, error) {
	if len(d) < 6 {
		return nil, errCorrupt
	}
	n := le16(d, 4)
	var es []iconEntry
	for i := 0; i < n; i++ {
		o := 6 + 16*i
		if o+16 > len(d) {
			return nil, errCorrupt
		}
		e := iconEntry{w: int(d[o]), h: int(d[o+1]), bpp: le16(d, o+6), size: le32(d, o+8), offset: le32(d, o+12)}
		if e.w == 0 {
			e.w = 256
		}
		if e.h == 0 {
			e.h = 256
		}
		e.depth = e.bpp
		if e.depth == 0 {
			if nc := int(d[o+2]); nc != 0 {
				e.depth = int(math.Ceil(math.Log2(float64(nc))))
			} else {
				e.depth = 256
			}
		}
		if e.offset < 0 || e.offset >= len(d) {
			continue
		}
		es = append(es, e)
	}
	if len(es) == 0 {
		return nil, errCorrupt
	}
	return es, nil
}

// decodeICO decodes the largest image of an icon, with the highest color
// depth (Pillow takes the lowest).
func decodeICO(d []byte) (image.Image, error) {
	es, err := iconEntries(d)
	if err != nil {
		return nil, err
	}
	best := es[0]
	for _, e := range es[1:] {
		if e.w*e.h > best.w*best.h || (e.w*e.h == best.w*best.h && e.depth > best.depth) {
			best = e
		}
	}
	return iconImage(d, best)
}

func iconImage(d []byte, e iconEntry) (image.Image, error) {
	data := d[e.offset:]
	if e.size > 0 && e.size < len(data) {
		data = data[:e.size]
	}
	if hasPrefix(data, "\x89PNG\r\n\x1a\n") {
		img, _, err := image.Decode(bytes.NewReader(data))
		return img, err
	}
	if len(data) < 40 {
		return nil, errCorrupt
	}
	hh := int(int32(binary.LittleEndian.Uint32(data[8:])))
	h := hh / 2
	img, err := decodeDIB(data, h)
	if err != nil {
		return nil, err
	}
	b := img.Bounds()
	w := b.Dx()
	out := newNRGBA(w, h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := color.NRGBAModel.Convert(img.At(b.Min.X+x, b.Min.Y+y)).(color.NRGBA)
			o := out.PixOffset(x, y)
			out.Pix[o], out.Pix[o+1], out.Pix[o+2], out.Pix[o+3] = c.R, c.G, c.B, 255
		}
	}
	hs := le32(data, 0)
	bpp := le16(data, 14)
	if bpp == 32 {
		// Alpha from the fourth byte of each pixel, bottom-up.
		p := hs
		if le32(data, 16) == 3 {
			p += 12
		}
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				o := p + 4*((h-1-y)*w+x) + 3
				if o < len(data) {
					out.Pix[out.PixOffset(x, y)+3] = data[o]
				}
			}
		}
		return out, nil
	}
	// AND mask at the end of the data: set bits are transparent.
	stride := ((w + 31) / 32) * 4
	maskOff := len(data) - stride*h
	if e.size > 0 && e.size <= len(d)-e.offset {
		maskOff = e.size - stride*h
	}
	if maskOff >= 0 {
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				if data[maskOff+(h-1-y)*stride+x/8]&(0x80>>uint(x&7)) != 0 {
					out.Pix[out.PixOffset(x, y)+3] = 0
				}
			}
		}
	}
	return out, nil
}

// decodeCUR decodes a cursor like Pillow: the first entry larger than the
// others in both dimensions.
func decodeCUR(d []byte) (image.Image, error) {
	es, err := iconEntries(d)
	if err != nil {
		return nil, err
	}
	best := es[0]
	for _, e := range es[1:] {
		if e.w > best.w && e.h > best.h {
			best = e
		}
	}
	return iconImage(d, best)
}

package images

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image/jpeg"
)

// Orientation transforms an image: a clockwise rotation in degrees (0,
// 90, 180 or 270) followed by a horizontal flip. FromImage uses the EXIF
// orientation of the image instead (image-orientation: from-image).
type Orientation struct {
	FromImage bool
	Angle     int
	Flip      bool
}

// exifOrientation returns the EXIF orientation (1 to 8) of JPEG, PNG and
// WebP files, or 0. Like Pillow, other formats are not rotated.
func exifOrientation(data []byte) int {
	var exif []byte
	switch {
	case len(data) > 4 && data[0] == 0xff && data[1] == 0xd8:
		for p := 2; p+4 <= len(data) && data[p] == 0xff; {
			marker := data[p+1]
			if marker == 0xd8 || (marker >= 0xd0 && marker <= 0xd7) || marker == 0x01 {
				p += 2
				continue
			}
			if marker == 0xda || marker == 0xd9 {
				break
			}
			n := int(binary.BigEndian.Uint16(data[p+2:]))
			if p+2+n > len(data) {
				break
			}
			seg := data[p+4 : p+2+n]
			if marker == 0xe1 && bytes.HasPrefix(seg, []byte("Exif\x00\x00")) {
				exif = seg[6:]
				break
			}
			p += 2 + n
		}
	case bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n")):
		for p := 8; p+12 <= len(data); {
			n := int(binary.BigEndian.Uint32(data[p:]))
			if n < 0 || p+12+n > len(data) {
				break
			}
			if string(data[p+4:p+8]) == "eXIf" {
				exif = data[p+8 : p+8+n]
				break
			}
			if string(data[p+4:p+8]) == "IDAT" {
				break
			}
			p += 12 + n
		}
	case len(data) > 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WEBP":
		if chunks, _ := webpChunks(data); chunks != nil {
			for _, c := range chunks {
				if c.id == "EXIF" {
					exif = bytes.TrimPrefix(c.data, []byte("Exif\x00\x00"))
				}
			}
		}
	}
	if len(exif) < 8 {
		return 0
	}
	var bo binary.ByteOrder
	switch string(exif[:2]) {
	case "II":
		bo = binary.LittleEndian
	case "MM":
		bo = binary.BigEndian
	default:
		return 0
	}
	off := int(bo.Uint32(exif[4:]))
	if off < 0 || off+2 > len(exif) {
		return 0
	}
	n := int(bo.Uint16(exif[off:]))
	for i := 0; i < n; i++ {
		e := off + 2 + 12*i
		if e+12 > len(exif) {
			break
		}
		if bo.Uint16(exif[e:]) == 0x0112 {
			v := int(bo.Uint16(exif[e+8:]))
			if v >= 1 && v <= 8 {
				return v
			}
		}
	}
	return 0
}

// exifTransform returns the rotation and flip equivalent to an EXIF
// orientation.
func exifTransform(o int) (angle int, flip bool) {
	switch o {
	case 2:
		return 0, true
	case 3:
		return 180, false
	case 4:
		return 180, true
	case 5:
		return 90, true
	case 6:
		return 90, false
	case 7:
		return 270, true
	case 8:
		return 270, false
	}
	return 0, false
}

// Oriented returns the image transformed by o, or im itself when nothing
// changes.
func (im *Image) Oriented(o Orientation) *Image {
	if im == nil || im.SVG != nil {
		return im
	}
	angle, flip := o.Angle, o.Flip
	if o.FromImage {
		angle, flip = exifTransform(im.exif)
	}
	if angle == 0 && !flip {
		return im
	}
	pixels, comps, alpha := im.Pixels, im.Components, im.Alpha
	if im.JPEG != nil {
		// Transformed JPEG images are embedded decoded.
		img, err := jpeg.Decode(bytes.NewReader(im.JPEG))
		if err != nil {
			return im
		}
		d, err := fromImage(img, im.URL)
		if err != nil {
			return im
		}
		pixels, comps, alpha = d.Pixels, d.Components, d.Alpha
	}
	w, h := im.Width, im.Height
	out := &Image{URL: fmt.Sprintf("%s#orientation=%d,%t", im.URL, angle, flip), Components: comps}
	nw, nh := w, h
	if angle == 90 || angle == 270 {
		nw, nh = h, w
	}
	out.Width, out.Height = nw, nh
	// Source pixel of destination (x, y).
	src := func(x, y int) (int, int) {
		if flip {
			x = nw - 1 - x
		}
		switch angle {
		case 90:
			return y, h - 1 - x
		case 180:
			return w - 1 - x, h - 1 - y
		case 270:
			return w - 1 - y, x
		}
		return x, y
	}
	transform := func(data []byte, n int) []byte {
		if data == nil {
			return nil
		}
		res := make([]byte, len(data))
		for y := 0; y < nh; y++ {
			for x := 0; x < nw; x++ {
				sx, sy := src(x, y)
				copy(res[(y*nw+x)*n:(y*nw+x+1)*n], data[(sy*w+sx)*n:(sy*w+sx+1)*n])
			}
		}
		return res
	}
	out.Pixels = transform(pixels, comps)
	out.Alpha = transform(alpha, 1)
	return out
}

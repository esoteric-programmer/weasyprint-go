package images

import (
	"bytes"
	"encoding/binary"
	"errors"
	"strconv"
	"strings"
)

// EPS is an Encapsulated PostScript image, rendered as vector graphics.
type EPS struct {
	Program []byte
	// BBox is the %%BoundingBox (integers, like Pillow).
	BBox [4]float64
	// Size is the intrinsic size in pixels: the bounding box, or the
	// %ImageData size of Photoshop files (like Pillow).
	Size [2]int
}

// IsEPS reports whether data looks like PostScript (or a DOS EPS
// binary file), as Pillow accepts it.
func IsEPS(data []byte) bool {
	return bytes.HasPrefix(data, []byte("%!PS")) || bytes.HasPrefix(data, []byte{0xC5, 0xD0, 0xD3, 0xC6})
}

// ParseEPS extracts the PostScript program and the bounding box.
func ParseEPS(data []byte) (*EPS, error) {
	prog := data
	if bytes.HasPrefix(data, []byte{0xC5, 0xD0, 0xD3, 0xC6}) {
		if len(data) < 30 {
			return nil, errors.New("eps: truncated DOS header")
		}
		off := binary.LittleEndian.Uint32(data[4:])
		n := binary.LittleEndian.Uint32(data[8:])
		if uint64(off)+uint64(n) > uint64(len(data)) {
			return nil, errors.New("eps: bad DOS header")
		}
		prog = data[off : off+n]
	}
	if !bytes.HasPrefix(prog, []byte("%!PS")) {
		return nil, errors.New("eps: not a PostScript file")
	}
	e := &EPS{Program: prog}
	box, ok := findBBox(prog, false)
	if !ok {
		box, ok = findBBox(prog, true)
	}
	if !ok {
		return nil, errors.New("eps: cannot determine the bounding box")
	}
	e.BBox = box
	e.Size = [2]int{int(box[2] - box[0]), int(box[3] - box[1])}
	if w, h, ok := imageDataSize(prog); ok {
		e.Size = [2]int{w, h}
	}
	return e, nil
}

// imageDataSize reads the %ImageData comment of Photoshop files (columns,
// rows, bit depth, mode), which Pillow uses as the image size.
func imageDataSize(p []byte) (int, int, bool) {
	k := bytes.Index(p, []byte("%ImageData:"))
	if k < 0 || k > 0 && p[k-1] != '\n' && p[k-1] != '\r' {
		return 0, 0, false
	}
	line := p[k+len("%ImageData:"):]
	if e := bytes.IndexAny(line, "\r\n"); e >= 0 {
		line = line[:e]
	}
	f := strings.Fields(string(line))
	if len(f) < 4 {
		return 0, 0, false
	}
	var v [4]int
	for i := range v {
		n, err := strconv.Atoi(f[i])
		if err != nil {
			return 0, 0, false
		}
		v[i] = n
	}
	if v[0] <= 0 || v[1] <= 0 || v[2] != 1 && v[2] != 8 || v[2] == 8 && (v[3] < 1 || v[3] > 4) {
		return 0, 0, false
	}
	return v[0], v[1], true
}

// findBBox reads %%BoundingBox in the header (or the trailer).
func findBBox(p []byte, trailer bool) ([4]float64, bool) {
	lines := bytes.Split(p, []byte("\n"))
	if trailer {
		for i, j := 0, len(lines)-1; i < j; i, j = i+1, j-1 {
			lines[i], lines[j] = lines[j], lines[i]
		}
	}
	for i, l := range lines {
		if i > 2000 {
			break
		}
		l = bytes.TrimRight(l, "\r")
		// Old Mac files use CR line ends.
		for _, part := range bytes.Split(l, []byte("\r")) {
			s := string(part)
			if !strings.HasPrefix(s, "%%BoundingBox:") {
				if !trailer && (strings.HasPrefix(s, "%%EndComments") || len(s) > 0 && s[0] != '%') {
					return [4]float64{}, false
				}
				continue
			}
			f := strings.Fields(s[len("%%BoundingBox:"):])
			if len(f) < 4 {
				continue
			}
			var b [4]float64
			ok := true
			for k := 0; k < 4; k++ {
				v, err := strconv.ParseFloat(f[k], 64)
				if err != nil {
					ok = false
					break
				}
				b[k] = float64(int(v))
			}
			if ok {
				return b, true
			}
		}
	}
	return [4]float64{}, false
}

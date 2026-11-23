package images

import (
	"encoding/binary"
	"testing"
)

func TestParseEPS(t *testing.T) {
	e, err := ParseEPS([]byte("%!PS-Adobe-3.0 EPSF-3.0\n%%BoundingBox: 1.9 2 101.7 52\n%%EndComments\nshowpage\n"))
	if err != nil || e.BBox != [4]float64{1, 2, 101, 52} || e.Size != [2]int{100, 50} {
		t.Errorf("header bbox: %+v %v", e, err)
	}
	e, err = ParseEPS([]byte("%!PS-Adobe-3.0 EPSF-3.0\n%%BoundingBox: (atend)\n%%EndComments\nshowpage\n%%Trailer\n%%BoundingBox: 0 0 10 20\n"))
	if err != nil || e.BBox != [4]float64{0, 0, 10, 20} {
		t.Errorf("trailer bbox: %+v %v", e, err)
	}
	// Photoshop: %ImageData gives the size.
	e, err = ParseEPS([]byte("%!PS-Adobe-3.0 EPSF-3.0\n%%BoundingBox: 0 0 72 36\n%%EndComments\n%ImageData: 300 150 8 3 1 300 2 \"beginimage\"\n"))
	if err != nil || e.Size != [2]int{300, 150} {
		t.Errorf("image data size: %+v %v", e, err)
	}
	// DOS EPS binary header.
	ps := []byte("%!PS-Adobe-3.0 EPSF-3.0\n%%BoundingBox: 0 0 5 5\n")
	dos := make([]byte, 30)
	copy(dos, []byte{0xC5, 0xD0, 0xD3, 0xC6})
	binary.LittleEndian.PutUint32(dos[4:], 30)
	binary.LittleEndian.PutUint32(dos[8:], uint32(len(ps)))
	e, err = ParseEPS(append(dos, ps...))
	if err != nil || e.BBox[2] != 5 || !IsEPS(dos) {
		t.Errorf("DOS EPS: %+v %v", e, err)
	}
	if _, err := ParseEPS([]byte("%!PS-Adobe-3.0\nshowpage\n")); err == nil {
		t.Errorf("no bounding box: no error")
	}
}

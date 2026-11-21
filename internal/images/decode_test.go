package images

import (
	"crypto/md5"
	"encoding/hex"
	"image"
	"image/color"
	"os"
	"testing"
)

// The expected pixels are those decoded by Pillow (libtiff, libwebp).
var decodeCases = []struct {
	file string
	w, h int
	md5  string
}{
	{"alpha_lossless.webp", 37, 23, "5bb5ed12bde71ac22f6afa1fc6cb2878"},
	{"alpha_lossy.webp", 37, 23, "7cb29ffc333dd9e8521394bcf7eef966"},
	{"anim.webp", 40, 30, "83b81e9ce6a9ac5cbebbb5746c8a5327"},
	{"bw_g3.tiff", 37, 23, "00549e86aa4bc61cb211e40e647dabb4"},
	{"bw_g4.tiff", 37, 23, "00549e86aa4bc61cb211e40e647dabb4"},
	{"cmyk.tiff", 37, 23, "acfd1ccb3195eeda224a97e2b6848fde"},
	{"lossy_even.webp", 38, 24, "3ac5e1534adc71eaeb8da5ee9e224a4e"},
	{"lossy_odd.webp", 37, 23, "ff2f42396369f99eae3bfaa455662abb"},
	{"p4.bmp", 37, 23, "57d2f79334a62cbed783cf8d48c7ff9a"},
	{"pal.tiff", 37, 23, "faf0e9b79574f5bc1c13cbb8afdc4b82"},
	{"rgb24.bmp", 37, 23, "acfd1ccb3195eeda224a97e2b6848fde"},
	{"rgb_pred.tiff", 37, 23, "acfd1ccb3195eeda224a97e2b6848fde"},
	{"rgb_tiff_lzw.tiff", 37, 23, "acfd1ccb3195eeda224a97e2b6848fde"},
	{"rgba32.bmp", 37, 23, "acfd1ccb3195eeda224a97e2b6848fde"},
	{"rgba_lzw.tiff", 37, 23, "5bb5ed12bde71ac22f6afa1fc6cb2878"},
	{"rle8.bmp", 37, 23, "e708e3c51cdd70b26cbd87d6540400e0"},
	{"tiled.tiff", 100, 70, "25b31d77639aa438bb35cb2338f7ac79"},
}

func TestDecodeFormats(t *testing.T) {
	for _, c := range decodeCases {
		f, err := os.Open("testdata/" + c.file)
		if err != nil {
			t.Fatal(err)
		}
		img, _, err := image.Decode(f)
		f.Close()
		if err != nil {
			t.Errorf("%s: %v", c.file, err)
			continue
		}
		b := img.Bounds()
		if b.Dx() != c.w || b.Dy() != c.h {
			t.Errorf("%s: size %v", c.file, b)
			continue
		}
		h := md5.New()
		for y := b.Min.Y; y < b.Max.Y; y++ {
			for x := b.Min.X; x < b.Max.X; x++ {
				p := color.NRGBAModel.Convert(img.At(x, y)).(color.NRGBA)
				h.Write([]byte{p.R, p.G, p.B, p.A})
			}
		}
		if got := hex.EncodeToString(h.Sum(nil)); got != c.md5 {
			t.Errorf("%s: pixels differ from Pillow's", c.file)
		}
	}
}

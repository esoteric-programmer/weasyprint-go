package images

import (
	"bytes"
	"compress/gzip"
	"crypto/md5"
	"encoding/hex"
	"image"
	"image/color"
	"os"
	"testing"
)

// The expected pixels are those decoded by Pillow (libtiff, libwebp).
type decodeCase struct {
	file string
	w, h int
	md5  string
}

var decodeCases = []decodeCase{
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

func TestSVGZ(t *testing.T) {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	zw.Write([]byte(`<svg xmlns="http://www.w3.org/2000/svg" width="20" height="10"><rect width="20" height="10"/></svg>`))
	zw.Close()
	im, err := Decode(buf.Bytes(), "image/svg+xml", "a.svgz")
	if err != nil || im.SVG == nil {
		t.Fatalf("SVGZ not decoded: %v", err)
	}
}

// formatCases were decoded like Pillow, except where Pillow has bugs (see
// testdata/pillow/README).
var formatCases = []decodeCase{
	{"fmt_a.qoi", 37, 23, "8fa9fa9a177f05c218c5be8e66648657"},
	{"fmt_p3.ppm", 37, 23, "8fa9fa9a177f05c218c5be8e66648657"},
	{"fmt_p5_16.pgm", 37, 23, "2aaa317e284b00feca604b3326d99f68"},
	{"fmt_pf.pfm", 37, 23, "802a0cc7669d4ab95bfd7d89bab89ac9"},
	{"fmt_a.xpm", 37, 23, "b2234652b16055576cfc8d851f0cea72"},
	{"fmt_a.xbm", 37, 23, "c9cf1a01c7fedbf6a6bd1c59f96f3858"},
	{"fmt_ega.pcx", 37, 23, "1bf552b31daba4d1c6889bf0efc6dfee"},
	{"fmt_a.dcx", 37, 23, "4d329628874b7812c334216f2a523876"},
	{"fmt_rle.sgi", 37, 23, "b7dc9aa6dc8eca234d06e2472034ebff"},
	{"fmt_rgb16rle.sgi", 37, 23, "8fa9fa9a177f05c218c5be8e66648657"},
	{"fmt_rle24.ras", 37, 23, "8fa9fa9a177f05c218c5be8e66648657"},
	{"fmt_rgba_rle.tga", 37, 23, "b7dc9aa6dc8eca234d06e2472034ebff"},
	{"fmt_p_rle.tga", 37, 23, "4d329628874b7812c334216f2a523876"},
	{"fmt_bmp32.ico", 32, 32, "10d30fa4b922ba1c536cffb724693094"},
	{"fmt_bmp8.ico", 32, 32, "d17e9639f071ac6cd2fc560e8a67277d"},
	{"fmt_a.cur", 32, 32, "89287df8ef67542e14489c007fc8d7bd"},
	{"fmt_rle.icns", 32, 32, "10d30fa4b922ba1c536cffb724693094"},
	{"fmt_rgb.im", 37, 23, "8fa9fa9a177f05c218c5be8e66648657"},
	{"fmt_p.im", 37, 23, "4d329628874b7812c334216f2a523876"},
	{"fmt_lins.msp", 37, 23, "e4f0611feabf6e2d56a09dfcc80e599b"},
	{"fmt_a.spider", 37, 23, "ac51d5c15ebf9ec47032ba27b8b9f331"},
	{"fmt_dxt5.dds", 37, 23, "3687a6167e6c314b43389c71b60dbc50"},
	{"fmt_bc4.dds", 37, 23, "e1718b874294809dfc96f21f68c5ae4b"},
	{"fmt_bc5s.dds", 37, 23, "f5de596b08d3c44fbe47c9273873dfd0"},
	{"fmt_bc7.dds", 37, 23, "4c9e3bf3596a1ec02ad0cecea7041b9d"},
	{"fmt_bc6h.dds", 37, 23, "a007e9933cce9ced31292a244d61ca34"},
	{"fmt_l.dds", 37, 23, "2aaa317e284b00feca604b3326d99f68"},
	{"fmt_rgb_rle.psd", 37, 23, "8fa9fa9a177f05c218c5be8e66648657"},
	{"fmt_cmyk.psd", 37, 23, "8fa9fa9a177f05c218c5be8e66648657"},
	{"fmt_lab.psd", 37, 23, "83aa17bb9f5ee84596d8696589c77d10"},
	{"fmt_a.fli", 37, 23, "0df4c13e922376391d1ae5599a90feed"},
	{"fmt_a16.fits", 37, 23, "ac51d5c15ebf9ec47032ba27b8b9f331"},
	{"fmt_rgba.gbr", 37, 23, "b7dc9aa6dc8eca234d06e2472034ebff"},
	{"fmt_blp1.blp", 37, 23, "4d329628874b7812c334216f2a523876"},
	{"fmt_p.blp", 37, 23, "4d329628874b7812c334216f2a523876"},
	{"fmt_dxt1.ftc", 37, 23, "babab38070e81c9b3fbfd35cecae49f0"},
	{"fmt_a.pxr", 37, 23, "8fa9fa9a177f05c218c5be8e66648657"},
	{"fmt_a.xv", 37, 23, "61abe22a24fc19b1e38bae8b8166a726"},
	{"fmt_a.mcidas", 37, 23, "2aaa317e284b00feca604b3326d99f68"},
	{"fmt_a.imt", 37, 23, "2aaa317e284b00feca604b3326d99f68"},
	{"fmt_a.iim", 37, 23, "2aaa317e284b00feca604b3326d99f68"},
	{"fmt_a.jp2", 37, 23, "8fa9fa9a177f05c218c5be8e66648657"},
	{"fmt_a.j2k", 37, 23, "8fa9fa9a177f05c218c5be8e66648657"},
	{"fmt_irr.jp2", 37, 23, "2947a72f0a51610b9525e9640be4c6b0"},
	{"fmt_tiles.jp2", 37, 23, "8fa9fa9a177f05c218c5be8e66648657"},
	{"fmt_rpcl.jp2", 37, 23, "8fa9fa9a177f05c218c5be8e66648657"},
	{"fmt_cprl.jp2", 37, 23, "8fa9fa9a177f05c218c5be8e66648657"},
	{"fmt_prec.jp2", 37, 23, "8fa9fa9a177f05c218c5be8e66648657"},
	{"fmt_layers.jp2", 37, 23, "4a13bdaf81b65ae9a387fe5c1dbaaa85"},
	{"fmt_rgba.jp2", 37, 23, "b7dc9aa6dc8eca234d06e2472034ebff"},
}

func TestRareFormats(t *testing.T) {
	for _, c := range formatCases {
		data, err := os.ReadFile("testdata/" + c.file)
		if err != nil {
			t.Fatal(err)
		}
		im, err := Decode(data, "", c.file)
		if err != nil {
			t.Errorf("%s: %v", c.file, err)
			continue
		}
		if im.Width != c.w || im.Height != c.h {
			t.Errorf("%s: size %dx%d", c.file, im.Width, im.Height)
			continue
		}
		h := md5.New()
		for i := 0; i < im.Width*im.Height; i++ {
			var px [4]byte
			switch im.Components {
			case 1:
				px = [4]byte{im.Pixels[i], im.Pixels[i], im.Pixels[i], 255}
			case 4:
				r, g, b := color.CMYKToRGB(im.Pixels[4*i], im.Pixels[4*i+1], im.Pixels[4*i+2], im.Pixels[4*i+3])
				px = [4]byte{r, g, b, 255}
			default:
				px = [4]byte{im.Pixels[3*i], im.Pixels[3*i+1], im.Pixels[3*i+2], 255}
			}
			if im.Alpha != nil {
				px[3] = im.Alpha[i]
			}
			h.Write(px[:])
		}
		if got := hex.EncodeToString(h.Sum(nil)); got != c.md5 {
			t.Errorf("%s: pixels differ (%s)", c.file, got)
		}
	}
}

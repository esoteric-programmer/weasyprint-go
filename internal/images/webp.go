package images

// WebP decoding. The VP8 (lossy) and VP8L (lossless) decoders in the vp8
// and vp8l packages, and readAlpha and unfilterAlpha below, come from
// golang.org/x/image (Copyright The Go Authors, BSD 3-Clause License, see
// vp8/LICENSE). Lossy images are converted to RGB with libwebp's fancy
// chroma upsampling and fixed-point conversion, like Pillow.

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"image/draw"
	"io"

	"github.com/esoteric-programmer/weasyprint-go/internal/images/vp8"
	"github.com/esoteric-programmer/weasyprint-go/internal/images/vp8l"
)

func init() {
	image.RegisterFormat("webp", "RIFF????WEBP", decodeWebP, decodeWebPConfig)
}

var errWebP = errors.New("webp: invalid format")

type webpChunk struct {
	id   string
	data []byte
}

func webpChunks(data []byte) ([]webpChunk, error) {
	if len(data) < 12 || string(data[:4]) != "RIFF" || string(data[8:12]) != "WEBP" {
		return nil, errWebP
	}
	end := min(len(data), 8+int(binary.LittleEndian.Uint32(data[4:])))
	var out []webpChunk
	for p := 12; p+8 <= end; {
		n := int(binary.LittleEndian.Uint32(data[p+4:]))
		if n < 0 || p+8+n > len(data) {
			return out, errWebP
		}
		out = append(out, webpChunk{string(data[p : p+4]), data[p+8 : p+8+n]})
		p += 8 + n + n%2
	}
	return out, nil
}

func decodeWebPConfig(r io.Reader) (image.Config, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return image.Config{}, err
	}
	chunks, err := webpChunks(data)
	if err != nil && len(chunks) == 0 {
		return image.Config{}, err
	}
	for _, c := range chunks {
		switch c.id {
		case "VP8X":
			if len(c.data) < 10 {
				return image.Config{}, errWebP
			}
			w := int(c.data[4]) | int(c.data[5])<<8 | int(c.data[6])<<16
			h := int(c.data[7]) | int(c.data[8])<<8 | int(c.data[9])<<16
			return image.Config{ColorModel: color.NRGBAModel, Width: w + 1, Height: h + 1}, nil
		case "VP8 ":
			d := vp8.NewDecoder()
			d.Init(bytes.NewReader(c.data), len(c.data))
			fh, err := d.DecodeFrameHeader()
			if err != nil {
				return image.Config{}, err
			}
			return image.Config{ColorModel: color.NRGBAModel, Width: fh.Width, Height: fh.Height}, nil
		case "VP8L":
			return vp8l.DecodeConfig(bytes.NewReader(c.data))
		}
	}
	return image.Config{}, errWebP
}

func decodeWebP(r io.Reader) (image.Image, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	chunks, err := webpChunks(data)
	if err != nil && len(chunks) == 0 {
		return nil, err
	}
	var canvasW, canvasH int
	for _, c := range chunks {
		switch c.id {
		case "VP8X":
			if len(c.data) >= 10 {
				canvasW = (int(c.data[4]) | int(c.data[5])<<8 | int(c.data[6])<<16) + 1
				canvasH = (int(c.data[7]) | int(c.data[8])<<8 | int(c.data[9])<<16) + 1
			}
		case "ANMF":
			// Animation: the first frame, on a transparent canvas.
			if len(c.data) < 16 || canvasW == 0 {
				return nil, errWebP
			}
			x := 2 * (int(c.data[0]) | int(c.data[1])<<8 | int(c.data[2])<<16)
			y := 2 * (int(c.data[3]) | int(c.data[4])<<8 | int(c.data[5])<<16)
			sub, err := decodeWebPFrame(frameChunks(c.data[16:]))
			if err != nil {
				return nil, err
			}
			canvas := image.NewNRGBA(image.Rect(0, 0, canvasW, canvasH))
			draw.Draw(canvas, sub.Bounds().Add(image.Pt(x, y)), sub, image.Point{}, draw.Src)
			return canvas, nil
		}
	}
	return decodeWebPFrame(chunks)
}

// frameChunks parses the chunks of an animation frame.
func frameChunks(data []byte) []webpChunk {
	var out []webpChunk
	for p := 0; p+8 <= len(data); {
		n := int(binary.LittleEndian.Uint32(data[p+4:]))
		if n < 0 || p+8+n > len(data) {
			break
		}
		out = append(out, webpChunk{string(data[p : p+4]), data[p+8 : p+8+n]})
		p += 8 + n + n%2
	}
	return out
}

// decodeWebPFrame decodes an image made of an optional ALPH chunk and a
// VP8 or VP8L chunk.
func decodeWebPFrame(chunks []webpChunk) (image.Image, error) {
	var alphaChunk []byte
	for _, c := range chunks {
		switch c.id {
		case "ALPH":
			alphaChunk = c.data
		case "VP8L":
			return vp8l.Decode(bytes.NewReader(c.data))
		case "VP8 ":
			d := vp8.NewDecoder()
			d.Init(bytes.NewReader(c.data), len(c.data))
			fh, err := d.DecodeFrameHeader()
			if err != nil {
				return nil, err
			}
			m, err := d.DecodeFrame()
			if err != nil {
				return nil, err
			}
			var alpha []byte
			if len(alphaChunk) > 0 {
				alpha, _, err = readAlpha(bytes.NewReader(alphaChunk[1:]), uint32(fh.Width-1), uint32(fh.Height-1), alphaChunk[0]&3)
				if err != nil {
					return nil, err
				}
				unfilterAlpha(alpha, fh.Width, (alphaChunk[0]>>2)&3)
			}
			return yuvToRGB(m, alpha), nil
		}
	}
	return nil, errWebP
}

// yuvClip8 and yuvToRGB follow libwebp's yuv.h (14-bit fixed point).
func yuvClip8(v int) uint8 {
	const mask = (256 << 6) - 1
	if v&^mask == 0 {
		return uint8(v >> 6)
	}
	if v < 0 {
		return 0
	}
	return 255
}

func multHi(v, coeff int) int { return (v * coeff) >> 8 }

func yuvPixel(y, u, v int, dst []byte) {
	dst[0] = yuvClip8(multHi(y, 19077) + multHi(v, 26149) - 14234)
	dst[1] = yuvClip8(multHi(y, 19077) - multHi(u, 6419) - multHi(v, 13320) + 8708)
	dst[2] = yuvClip8(multHi(y, 19077) + multHi(u, 33050) - 17685)
	dst[3] = 255
}

// upsampleRows converts one or two rows of luma with the "fancy"
// upsampling of libwebp: chroma samples are interpolated between the
// two nearest chroma rows and columns.
func upsampleRows(topY, bottomY, topU, topV, curU, curV []byte, topDst, bottomDst []byte, n int) {
	load := func(u, v byte) uint32 { return uint32(u) | uint32(v)<<16 }
	put := func(y byte, uv uint32, dst []byte) { yuvPixel(int(y), int(uv&0xff), int(uv>>16), dst) }
	last := (n - 1) >> 1
	tl := load(topU[0], topV[0])
	l := load(curU[0], curV[0])
	put(topY[0], (3*tl+l+0x00020002)>>2, topDst)
	if bottomY != nil {
		put(bottomY[0], (3*l+tl+0x00020002)>>2, bottomDst)
	}
	for x := 1; x <= last; x++ {
		t := load(topU[x], topV[x])
		uv := load(curU[x], curV[x])
		avg := tl + t + l + uv + 0x00080008
		diag12 := (avg + 2*(t+l)) >> 3
		diag03 := (avg + 2*(tl+uv)) >> 3
		put(topY[2*x-1], (diag12+tl)>>1, topDst[4*(2*x-1):])
		put(topY[2*x], (diag03+t)>>1, topDst[4*(2*x):])
		if bottomY != nil {
			put(bottomY[2*x-1], (diag03+l)>>1, bottomDst[4*(2*x-1):])
			put(bottomY[2*x], (diag12+uv)>>1, bottomDst[4*(2*x):])
		}
		tl, l = t, uv
	}
	if n&1 == 0 {
		put(topY[n-1], (3*tl+l+0x00020002)>>2, topDst[4*(n-1):])
		if bottomY != nil {
			put(bottomY[n-1], (3*l+tl+0x00020002)>>2, bottomDst[4*(n-1):])
		}
	}
}

func yuvToRGB(m *image.YCbCr, alpha []byte) *image.NRGBA {
	b := m.Bounds()
	w, h := b.Dx(), b.Dy()
	out := image.NewNRGBA(image.Rect(0, 0, w, h))
	yRow := func(y int) []byte { return m.Y[y*m.YStride : y*m.YStride+w] }
	uRow := func(y int) []byte { return m.Cb[y*m.CStride:] }
	vRow := func(y int) []byte { return m.Cr[y*m.CStride:] }
	dst := func(y int) []byte { return out.Pix[y*out.Stride:] }
	// First row, then pairs of rows between two chroma rows.
	upsampleRows(yRow(0), nil, uRow(0), vRow(0), uRow(0), vRow(0), dst(0), nil, w)
	for y := 1; y+1 < h; y += 2 {
		c := (y + 1) / 2
		upsampleRows(yRow(y), yRow(y+1), uRow(c-1), vRow(c-1), uRow(c), vRow(c), dst(y), dst(y+1), w)
	}
	if h > 1 && h%2 == 0 {
		c := (h - 1) / 2
		upsampleRows(yRow(h-1), nil, uRow(c), vRow(c), uRow(c), vRow(c), dst(h-1), nil, w)
	}
	if alpha != nil {
		for i := 0; i < w*h && i < len(alpha); i++ {
			out.Pix[4*i+3] = alpha[i]
		}
	}
	return out
}

func readAlpha(chunkData io.Reader, widthMinusOne, heightMinusOne uint32, compression byte) (
	alpha []byte, alphaStride int, err error) {

	switch compression {
	case 0:
		w := int(widthMinusOne) + 1
		h := int(heightMinusOne) + 1
		alpha = make([]byte, w*h)
		if _, err := io.ReadFull(chunkData, alpha); err != nil {
			return nil, 0, err
		}
		return alpha, w, nil

	case 1:
		// Read the VP8L-compressed alpha values. First, synthesize a 5-byte VP8L header:
		// a 1-byte magic number, a 14-bit widthMinusOne, a 14-bit heightMinusOne,
		// a 1-bit (ignored, zero) alphaIsUsed and a 3-bit (zero) version.
		if widthMinusOne > 0x3fff || heightMinusOne > 0x3fff {
			return nil, 0, errWebP
		}
		alphaImage, err := vp8l.Decode(io.MultiReader(
			bytes.NewReader([]byte{
				0x2f, // VP8L magic number.
				uint8(widthMinusOne),
				uint8(widthMinusOne>>8) | uint8(heightMinusOne<<6),
				uint8(heightMinusOne >> 2),
				uint8(heightMinusOne >> 10),
			}),
			chunkData,
		))
		if err != nil {
			return nil, 0, err
		}
		// The green values of the inner NRGBA image are the alpha values.
		pix := alphaImage.(*image.NRGBA).Pix
		alpha = make([]byte, len(pix)/4)
		for i := range alpha {
			alpha[i] = pix[4*i+1]
		}
		return alpha, int(widthMinusOne) + 1, nil
	}
	return nil, 0, errWebP
}

func unfilterAlpha(alpha []byte, alphaStride int, filter byte) {
	if len(alpha) == 0 || alphaStride == 0 {
		return
	}
	switch filter {
	case 1: // Horizontal filter.
		for i := 1; i < alphaStride; i++ {
			alpha[i] += alpha[i-1]
		}
		for i := alphaStride; i < len(alpha); i += alphaStride {
			// The first column is equivalent to the vertical filter.
			alpha[i] += alpha[i-alphaStride]

			for j := 1; j < alphaStride; j++ {
				alpha[i+j] += alpha[i+j-1]
			}
		}

	case 2: // Vertical filter.
		// The first row is equivalent to the horizontal filter.
		for i := 1; i < alphaStride; i++ {
			alpha[i] += alpha[i-1]
		}

		for i := alphaStride; i < len(alpha); i++ {
			alpha[i] += alpha[i-alphaStride]
		}

	case 3: // Gradient filter.
		// The first row is equivalent to the horizontal filter.
		for i := 1; i < alphaStride; i++ {
			alpha[i] += alpha[i-1]
		}

		for i := alphaStride; i < len(alpha); i += alphaStride {
			// The first column is equivalent to the vertical filter.
			alpha[i] += alpha[i-alphaStride]

			// The interior is predicted on the three top/left pixels.
			for j := 1; j < alphaStride; j++ {
				c := int(alpha[i+j-alphaStride-1])
				b := int(alpha[i+j-alphaStride])
				a := int(alpha[i+j-1])
				x := a + b - c
				if x < 0 {
					x = 0
				} else if x > 255 {
					x = 255
				}
				alpha[i+j] += uint8(x)
			}
		}
	}
}

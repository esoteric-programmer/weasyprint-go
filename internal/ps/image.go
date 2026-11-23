package ps

import (
	"io"
	"math"
)

// Image is a sampled image converted to device colors.
type Image struct {
	W, H int
	N    int    // 1 gray, 3 RGB, 4 CMYK (0 for stencil masks)
	Data []byte // N samples per pixel, or 1 coverage byte (0 or 255) for masks
	Mask bool   // stencil mask painted with Op.Paint
	// Alpha is the opacity of each pixel (color key masking), or nil.
	Alpha []byte
	// JPEG holds the original data of DCT-encoded device images.
	JPEG []byte
	// JPEGInverted: the JPEG is drawn with an inverted Decode array
	// (Adobe CMYK files).
	JPEGInverted bool
	Interpolate  bool
}

// imageSource reads image data from a string (reused), a file or a
// procedure.
func (in *Interp) imageSource(o Object) io.Reader {
	switch o.Kind {
	case KString:
		return &repeatReader{b: o.Bytes()}
	case KFile:
		return o.Ref.(*File)
	case KArray:
		if o.Exec {
			return &procReader{in: in, proc: o}
		}
	case KOperator, KName:
		if o.Exec {
			return &procReader{in: in, proc: o}
		}
	}
	typecheck("image")
	return nil
}

type repeatReader struct {
	b   []byte
	pos int
}

func (r *repeatReader) Read(p []byte) (int, error) {
	if len(r.b) == 0 {
		return 0, io.EOF
	}
	n := 0
	for n < len(p) {
		if r.pos >= len(r.b) {
			r.pos = 0
		}
		k := copy(p[n:], r.b[r.pos:])
		r.pos += k
		n += k
	}
	return n, nil
}

type imageParams struct {
	w, h, bpc int
	matrix    Matrix
	sources   []Object
	nComp     int
	decode    []float64
	cs        *colorSpace
	mask      bool
	interp    bool
	maskColor []float64
}

func (in *Interp) registerImages() {
	d := in.def
	d("image", func(in *Interp) {
		if t := in.top(); t.Kind == KDict {
			in.imageDict(in.popDict(), false)
			return
		}
		src := in.pop()
		_, m := in.popMatrix()
		bpc := in.popInt()
		h := in.popInt()
		w := in.popInt()
		in.drawImage(imageParams{w: w, h: h, bpc: bpc, matrix: m, sources: []Object{src}, nComp: 1, cs: deviceGray})
	})
	d("colorimage", func(in *Interp) {
		ncomp := in.popInt()
		multi := in.popBool()
		var srcs []Object
		if multi {
			srcs = make([]Object, ncomp)
			for i := ncomp - 1; i >= 0; i-- {
				srcs[i] = in.pop()
			}
		} else {
			srcs = []Object{in.pop()}
		}
		_, m := in.popMatrix()
		bpc := in.popInt()
		h := in.popInt()
		w := in.popInt()
		cs := deviceGray
		switch ncomp {
		case 3:
			cs = deviceRGB
		case 4:
			cs = deviceCMYK
		}
		in.drawImage(imageParams{w: w, h: h, bpc: bpc, matrix: m, sources: srcs, nComp: ncomp, cs: cs})
	})
	d("imagemask", func(in *Interp) {
		if t := in.top(); t.Kind == KDict {
			in.imageDict(in.popDict(), true)
			return
		}
		src := in.pop()
		_, m := in.popMatrix()
		polarity := in.popBool()
		h := in.popInt()
		w := in.popInt()
		dec := []float64{0, 1}
		if polarity {
			dec = []float64{1, 0}
		}
		in.drawImage(imageParams{w: w, h: h, bpc: 1, matrix: m, sources: []Object{src}, nComp: 1, mask: true, decode: dec})
	})
}

func (in *Interp) imageDict(d *Dict, mask bool) {
	geti := func(k string, def int) int {
		if v, ok := d.GetS(k); ok && v.IsNum() {
			return int(v.Num())
		}
		return def
	}
	typ := geti("ImageType", 1)
	if typ == 3 || typ == 4 {
		// Masked images: the image itself (the mask is dropped for
		// type 3; color key masking is kept for type 4).
		if typ == 3 {
			if v, ok := d.GetS("DataDict"); ok && v.Kind == KDict {
				in.warn("ps: image masks (ImageType 3) are ignored")
				d = v.DataDict()
			}
		}
	}
	p := imageParams{w: geti("Width", 0), h: geti("Height", 0), bpc: geti("BitsPerComponent", 1), mask: mask}
	if v, ok := d.GetS("ImageMatrix"); ok {
		p.matrix = matrixOf(v)
	} else {
		p.matrix = Matrix{float64(p.w), 0, 0, -float64(p.h), 0, float64(p.h)}
	}
	if v, ok := d.GetS("Decode"); ok {
		p.decode = numsOf(v)
	}
	if v, ok := d.GetS("Interpolate"); ok && v.Kind == KBool {
		p.interp = v.BoolV()
	}
	if typ == 4 {
		if v, ok := d.GetS("MaskColor"); ok {
			p.maskColor = numsOf(v)
		}
	}
	if mask {
		p.nComp = 1
		p.bpc = 1
		if p.decode == nil {
			p.decode = []float64{0, 1}
		}
	} else {
		p.cs = in.gs.Space
		if p.cs.family == "Pattern" && p.cs.base != nil {
			p.cs = p.cs.base
		}
		p.nComp = p.cs.n
	}
	src, _ := d.GetS("DataSource")
	multi := false
	if v, ok := d.GetS("MultipleDataSources"); ok && v.Kind == KBool {
		multi = v.BoolV()
	}
	if multi && src.Kind == KArray && !src.Exec {
		p.sources = src.Elems()
	} else {
		p.sources = []Object{src}
	}
	in.drawImage(p)
}

// DataDict returns the dictionary of an object (or nil).
func (o Object) DataDict() *Dict { return o.DictV() }

func (in *Interp) drawImage(p imageParams) {
	if p.w <= 0 || p.h <= 0 || p.w*p.h > 1<<28 {
		return
	}
	if p.bpc != 1 && p.bpc != 2 && p.bpc != 4 && p.bpc != 8 && p.bpc != 12 && p.bpc != 16 {
		rangecheck("image")
	}
	n := p.nComp
	// Default decode arrays.
	if p.decode == nil {
		if p.cs != nil && p.cs.family == "Indexed" {
			p.decode = []float64{0, float64(int(1)<<p.bpc - 1)}
		} else {
			for i := 0; i < n; i++ {
				lo, hi := 0.0, 1.0
				if p.cs != nil && 2*i+1 < len(p.cs.rng) {
					lo, hi = p.cs.rng[2*i], p.cs.rng[2*i+1]
				}
				p.decode = append(p.decode, lo, hi)
			}
		}
	}
	// Read the samples: one interleaved source or one per component.
	var raw [][]byte // per source
	var jpegData []byte
	var jpegN int
	comps := n
	if len(p.sources) > 1 {
		comps = 1
	}
	rowBytes := (p.w*p.bpc*comps + 7) / 8
	readers := make([]io.Reader, len(p.sources))
	for i, src := range p.sources {
		if src.Kind == KFile {
			if f := src.Ref.(*File); f.dctInit != nil && len(p.sources) == 1 {
				f.dctInit()
				if f.dct != nil {
					jpegData, jpegN = f.dct.jpeg, f.dct.comp
				}
			}
		}
		readers[i] = in.imageSource(src)
		raw = append(raw, make([]byte, rowBytes*p.h))
	}
	// Sources are read row by row in turn (procedures are called
	// alternately). Missing data is drawn as zeros.
	for y := 0; y < p.h; y++ {
		for i, r := range readers {
			io.ReadFull(r, raw[i][y*rowBytes:(y+1)*rowBytes])
		}
	}
	for _, src := range p.sources {
		if src.Kind == KFile {
			if f := src.Ref.(*File); f.skipEOD != nil && !f.unread {
				f.skipEOD()
			}
		}
	}
	img := &Image{W: p.w, H: p.h, Interpolate: p.interp}
	maxV := float64(int(1)<<p.bpc - 1)
	sample := func(buf []byte, row, idx, rowBytes int) int {
		bit := idx * p.bpc
		off := row*rowBytes + bit/8
		switch p.bpc {
		case 8:
			return int(buf[off])
		case 16:
			return int(buf[off])<<8 | int(buf[off+1])
		case 12:
			if bit%8 == 0 {
				return int(buf[off])<<4 | int(buf[off+1]>>4)
			}
			return int(buf[off]&15)<<8 | int(buf[off+1])
		}
		shift := 8 - p.bpc - bit%8
		return int(buf[off]>>uint(shift)) & (1<<p.bpc - 1)
	}
	if p.mask {
		img.Mask = true
		img.Data = make([]byte, p.w*p.h)
		rowBytes := (p.w + 7) / 8
		paint0 := p.decode[0] < p.decode[1] // 0 bits paint with [0 1]
		for y := 0; y < p.h; y++ {
			for x := 0; x < p.w; x++ {
				v := sample(raw[0], y, x, rowBytes)
				if (v == 0) == paint0 {
					img.Data[y*p.w+x] = 255
				}
			}
		}
	} else {
		dev := in.toDevice(p.cs, p.cs.initial())
		img.N = dev.N
		if img.N == 0 {
			return // Separation /None
		}
		img.Data = make([]byte, p.w*p.h*img.N)
		device := p.cs.family == "DeviceGray" || p.cs.family == "DeviceRGB" || p.cs.family == "DeviceCMYK"
		defaultDecode := true
		for i := 0; i < n; i++ {
			if p.decode[2*i] != 0 || p.decode[2*i+1] != 1 {
				defaultDecode = false
			}
		}
		cache := map[[4]int]Color{}
		vals := make([]float64, n)
		var key [4]int
		rowBytes := (p.w*p.bpc*n + 7) / 8
		srcRow := (p.w*p.bpc + 7) / 8
		for y := 0; y < p.h; y++ {
			for x := 0; x < p.w; x++ {
				for c := 0; c < n; c++ {
					var v int
					if len(raw) > 1 {
						v = sample(raw[c], y, x, srcRow)
					} else {
						v = sample(raw[0], y, x*n+c, rowBytes)
					}
					if c < 4 {
						key[c] = v
					}
					vals[c] = p.decode[2*c] + float64(v)*(p.decode[2*c+1]-p.decode[2*c])/maxV
				}
				o := (y*p.w + x) * img.N
				if device && defaultDecode && n == img.N {
					for c := 0; c < n; c++ {
						img.Data[o+c] = byte(math.Round(clamp01(vals[c]) * 255))
					}
				} else {
					col, ok := cache[key]
					if !ok || n > 4 {
						col = in.toDevice(p.cs, vals)
						if len(cache) < 1<<16 {
							cache[key] = col
						}
					}
					for c := 0; c < img.N; c++ {
						img.Data[o+c] = byte(math.Round(clamp01(col.V[c]) * 255))
					}
				}
				if p.maskColor != nil {
					masked := true
					for c := 0; c < n && c < 4; c++ {
						lo, hi := 0.0, 0.0
						if len(p.maskColor) == n {
							lo, hi = p.maskColor[c], p.maskColor[c]
						} else if 2*c+1 < len(p.maskColor) {
							lo, hi = p.maskColor[2*c], p.maskColor[2*c+1]
						}
						if float64(key[c]) < lo || float64(key[c]) > hi {
							masked = false
						}
					}
					if img.Alpha == nil {
						img.Alpha = make([]byte, p.w*p.h)
						for i := range img.Alpha {
							img.Alpha[i] = 255
						}
					}
					if masked {
						img.Alpha[y*p.w+x] = 0
					}
				}
			}
		}
		inverted := n == 4
		for i := 0; i < n; i++ {
			if p.decode[2*i] != 1 || p.decode[2*i+1] != 0 {
				inverted = false
			}
		}
		if jpegData != nil && device && (defaultDecode || inverted) && p.bpc == 8 && jpegN == img.N && n == img.N {
			img.JPEG = jpegData
			img.JPEGInverted = inverted
		}
	}
	inv, ok := p.matrix.Invert()
	if !ok {
		return
	}
	unit := Matrix{float64(p.w), 0, 0, -float64(p.h), 0, float64(p.h)}
	op := Op{Kind: OpImage, Image: img, CTM: unit.Mul(inv).Mul(in.gs.CTM)}
	if p.mask {
		op.Paint = in.gs.Paint
	}
	if b := in.currentBuild(); b != nil && b.capture != nil {
		return
	}
	in.emit(op)
}

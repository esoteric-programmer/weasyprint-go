package ps

import (
	"bytes"
	"math"
	"strconv"
	"strings"
)

func (in *Interp) registerOps() {
	in.registerCore()
	in.registerGraphics()
	in.registerFonts()
	in.registerImages()
}

func (in *Interp) registerCore() {
	d := in.def
	// Stack.
	d("pop", func(in *Interp) { in.pop() })
	d("exch", func(in *Interp) {
		in.need(2)
		n := len(in.ostack)
		in.ostack[n-1], in.ostack[n-2] = in.ostack[n-2], in.ostack[n-1]
	})
	d("dup", func(in *Interp) { in.push(in.top()) })
	d("copy", opCopy)
	d("index", func(in *Interp) {
		n := in.popInt()
		if n < 0 || n >= len(in.ostack) {
			rangecheck("index")
		}
		in.push(in.ostack[len(in.ostack)-1-n])
	})
	d("roll", func(in *Interp) {
		j := in.popInt()
		n := in.popInt()
		if n < 0 || n > len(in.ostack) {
			rangecheck("roll")
		}
		if n == 0 {
			return
		}
		s := in.ostack[len(in.ostack)-n:]
		j = ((j % n) + n) % n
		tmp := append([]Object{}, s...)
		for i := range s {
			s[(i+j)%n] = tmp[i]
		}
	})
	d("clear", func(in *Interp) { in.ostack = in.ostack[:0] })
	d("count", func(in *Interp) { in.push(Int(len(in.ostack))) })
	d("mark", func(in *Interp) { in.push(mark) })
	d("[", func(in *Interp) { in.push(mark) })
	d("<<", func(in *Interp) { in.push(mark) })
	d("cleartomark", func(in *Interp) {
		k := in.markIndex()
		in.ostack = in.ostack[:k]
	})
	d("counttomark", func(in *Interp) {
		k := in.markIndex()
		in.push(Int(len(in.ostack) - k - 1))
	})
	d("]", func(in *Interp) {
		k := in.markIndex()
		elems := append([]Object{}, in.ostack[k+1:]...)
		in.ostack = in.ostack[:k]
		in.push(Array(elems))
	})
	d(">>", func(in *Interp) {
		k := in.markIndex()
		elems := in.ostack[k+1:]
		if len(elems)%2 != 0 {
			rangecheck(">>")
		}
		dd := NewDict(len(elems) / 2)
		for i := 0; i < len(elems); i += 2 {
			dd.Put(elems[i], elems[i+1])
		}
		in.ostack = in.ostack[:k]
		in.push(DictObj(dd))
	})

	// Arithmetic.
	d("add", func(in *Interp) {
		b, a := in.pop(), in.pop()
		if a.Kind == KInt && b.Kind == KInt {
			r := int64(a.Int) + int64(b.Int)
			if r == int64(int32(r)) {
				in.push(Int(int(r)))
				return
			}
		}
		in.push(Real(num(a) + num(b)))
	})
	d("sub", func(in *Interp) {
		b, a := in.pop(), in.pop()
		if a.Kind == KInt && b.Kind == KInt {
			r := int64(a.Int) - int64(b.Int)
			if r == int64(int32(r)) {
				in.push(Int(int(r)))
				return
			}
		}
		in.push(Real(num(a) - num(b)))
	})
	d("mul", func(in *Interp) {
		b, a := in.pop(), in.pop()
		if a.Kind == KInt && b.Kind == KInt {
			r := int64(a.Int) * int64(b.Int)
			if r == int64(int32(r)) {
				in.push(Int(int(r)))
				return
			}
		}
		in.push(Real(num(a) * num(b)))
	})
	d("div", func(in *Interp) {
		b, a := in.popNum(), in.popNum()
		if b == 0 {
			panic(psError{"undefinedresult", "div"})
		}
		in.push(Real(a / b))
	})
	d("idiv", func(in *Interp) {
		b, a := in.popInt(), in.popInt()
		if b == 0 {
			panic(psError{"undefinedresult", "idiv"})
		}
		in.push(Int(a / b))
	})
	d("mod", func(in *Interp) {
		b, a := in.popInt(), in.popInt()
		if b == 0 {
			panic(psError{"undefinedresult", "mod"})
		}
		in.push(Int(a % b))
	})
	d("abs", func(in *Interp) {
		a := in.pop()
		if a.Kind == KInt {
			if a.Int < 0 {
				a.Int = -a.Int
			}
			in.push(a)
			return
		}
		in.push(Real(math.Abs(num(a))))
	})
	d("neg", func(in *Interp) {
		a := in.pop()
		if a.Kind == KInt {
			in.push(Int(-a.Int))
			return
		}
		in.push(Real(-num(a)))
	})
	round := func(f func(float64) float64) func(*Interp) {
		return func(in *Interp) {
			a := in.pop()
			if a.Kind == KInt {
				in.push(a)
				return
			}
			in.push(Real(f(num(a))))
		}
	}
	d("ceiling", round(math.Ceil))
	d("floor", round(math.Floor))
	d("round", round(func(x float64) float64 { return math.Floor(x + 0.5) }))
	d("truncate", round(math.Trunc))
	d("sqrt", func(in *Interp) {
		a := in.popNum()
		if a < 0 {
			rangecheck("sqrt")
		}
		in.push(Real(math.Sqrt(a)))
	})
	d("atan", func(in *Interp) {
		den, numr := in.popNum(), in.popNum()
		if den == 0 && numr == 0 {
			panic(psError{"undefinedresult", "atan"})
		}
		a := math.Atan2(numr, den) * 180 / math.Pi
		if a < 0 {
			a += 360
		}
		in.push(Real(a))
	})
	d("cos", func(in *Interp) { in.push(Real(math.Cos(in.popNum() * math.Pi / 180))) })
	d("sin", func(in *Interp) { in.push(Real(math.Sin(in.popNum() * math.Pi / 180))) })
	d("exp", func(in *Interp) {
		e, b := in.popNum(), in.popNum()
		in.push(Real(math.Pow(b, e)))
	})
	d("ln", func(in *Interp) { in.push(Real(math.Log(in.popNum()))) })
	d("log", func(in *Interp) { in.push(Real(math.Log10(in.popNum()))) })
	seed := uint32(0)
	d("rand", func(in *Interp) {
		seed = seed*1103515245 + 12345
		in.push(Int(int(seed>>1) & 0x7fffffff))
	})
	d("srand", func(in *Interp) { seed = uint32(in.popInt()) })
	d("rrand", func(in *Interp) { in.push(Int(int(seed))) })
	d("cvi", func(in *Interp) {
		o := in.pop()
		switch o.Kind {
		case KInt:
			in.push(o)
		case KReal:
			in.push(Int(int(math.Trunc(o.Real))))
		case KString:
			v, ok := parseNumber(strings.TrimSpace(o.Text()))
			if !ok {
				typecheck("cvi")
			}
			in.push(Int(int(math.Trunc(v.Num()))))
		default:
			typecheck("cvi")
		}
	})
	d("cvr", func(in *Interp) {
		o := in.pop()
		switch o.Kind {
		case KInt, KReal:
			in.push(Real(o.Num()))
		case KString:
			v, ok := parseNumber(strings.TrimSpace(o.Text()))
			if !ok {
				typecheck("cvr")
			}
			in.push(Real(v.Num()))
		default:
			typecheck("cvr")
		}
	})

	// Relational and boolean.
	d("eq", func(in *Interp) { b, a := in.pop(), in.pop(); in.push(Bool(equal(a, b))) })
	d("ne", func(in *Interp) { b, a := in.pop(), in.pop(); in.push(Bool(!equal(a, b))) })
	cmp := func(name string, f func(c int) bool) {
		d(name, func(in *Interp) {
			b, a := in.pop(), in.pop()
			var c int
			switch {
			case a.IsNum() && b.IsNum():
				x, y := a.Num(), b.Num()
				switch {
				case x < y:
					c = -1
				case x > y:
					c = 1
				}
			case a.Kind == KString && b.Kind == KString:
				c = bytes.Compare(a.Bytes(), b.Bytes())
			default:
				typecheck(name)
			}
			in.push(Bool(f(c)))
		})
	}
	cmp("ge", func(c int) bool { return c >= 0 })
	cmp("gt", func(c int) bool { return c > 0 })
	cmp("le", func(c int) bool { return c <= 0 })
	cmp("lt", func(c int) bool { return c < 0 })
	logic := func(name string, fb func(a, b bool) bool, fi func(a, b int) int) {
		d(name, func(in *Interp) {
			b, a := in.pop(), in.pop()
			switch {
			case a.Kind == KBool && b.Kind == KBool:
				in.push(Bool(fb(a.BoolV(), b.BoolV())))
			case a.Kind == KInt && b.Kind == KInt:
				in.push(Int(fi(a.Int, b.Int)))
			default:
				typecheck(name)
			}
		})
	}
	logic("and", func(a, b bool) bool { return a && b }, func(a, b int) int { return a & b })
	logic("or", func(a, b bool) bool { return a || b }, func(a, b int) int { return a | b })
	logic("xor", func(a, b bool) bool { return a != b }, func(a, b int) int { return a ^ b })
	d("not", func(in *Interp) {
		a := in.pop()
		switch a.Kind {
		case KBool:
			in.push(Bool(!a.BoolV()))
		case KInt:
			in.push(Int(^a.Int))
		default:
			typecheck("not")
		}
	})
	d("bitshift", func(in *Interp) {
		s, a := in.popInt(), in.popInt()
		v := uint32(a)
		if s >= 0 {
			v <<= uint(s)
		} else {
			v >>= uint(-s)
		}
		in.push(Int(int(int32(v))))
	})

	// Control.
	d("exec", func(in *Interp) { in.execObj(in.pop()) })
	d("if", func(in *Interp) {
		p := in.popProc()
		if in.popBool() {
			in.execObj(p)
		}
	})
	d("ifelse", func(in *Interp) {
		p2, p1 := in.popProc(), in.popProc()
		if in.popBool() {
			in.execObj(p1)
		} else {
			in.execObj(p2)
		}
	})
	d("for", func(in *Interp) {
		p := in.popProc()
		limit, incr, init := in.pop(), in.pop(), in.pop()
		if !limit.IsNum() || !incr.IsNum() || !init.IsNum() {
			typecheck("for")
		}
		if init.Kind == KInt && incr.Kind == KInt {
			i, inc, lim := init.Int, incr.Int, limit.Num()
			in.loopBody(func() bool {
				if inc >= 0 && float64(i) > lim || inc < 0 && float64(i) < lim {
					return false
				}
				in.push(Int(i))
				in.execObj(p)
				i += inc
				return true
			})
			return
		}
		x, inc, lim := init.Num(), incr.Num(), limit.Num()
		in.loopBody(func() bool {
			if inc >= 0 && x > lim || inc < 0 && x < lim {
				return false
			}
			in.push(Real(x))
			in.execObj(p)
			x += inc
			return true
		})
	})
	d("repeat", func(in *Interp) {
		p := in.popProc()
		n := in.popInt()
		if n < 0 {
			rangecheck("repeat")
		}
		in.loopBody(func() bool {
			if n <= 0 {
				return false
			}
			n--
			in.execObj(p)
			return true
		})
	})
	d("loop", func(in *Interp) {
		p := in.popProc()
		in.loopBody(func() bool { in.execObj(p); return true })
	})
	d("exit", func(in *Interp) { panic(exitSignal{}) })
	d("forall", func(in *Interp) {
		p := in.popProc()
		o := in.pop()
		switch o.Kind {
		case KArray:
			elems := o.Elems()
			i := 0
			in.loopBody(func() bool {
				if i >= len(elems) {
					return false
				}
				in.push(elems[i])
				i++
				in.execObj(p)
				return true
			})
		case KString:
			b := o.Bytes()
			i := 0
			in.loopBody(func() bool {
				if i >= len(b) {
					return false
				}
				in.push(Int(int(b[i])))
				i++
				in.execObj(p)
				return true
			})
		case KDict:
			dd := o.DictV()
			var ks, vs []Object
			dd.Each(func(k, v Object) bool { ks = append(ks, k); vs = append(vs, v); return true })
			i := 0
			in.loopBody(func() bool {
				if i >= len(ks) {
					return false
				}
				in.push(ks[i])
				in.push(vs[i])
				i++
				in.execObj(p)
				return true
			})
		default:
			typecheck("forall")
		}
	})
	d("stop", func(in *Interp) { panic(stopSignal{}) })
	d("stopped", func(in *Interp) {
		p := in.pop()
		st, _ := in.stopped(func() { in.execObj(p) })
		in.push(Bool(st))
	})
	d("countexecstack", func(in *Interp) { in.push(Int(in.depth + 1)) })
	d("execstack", func(in *Interp) { a := in.popArray(); in.push(Object{Kind: KArray, Ref: a.Ref, Int: a.Int, Len: 0}) })
	d("quit", func(in *Interp) { panic(quitSignal{}) })
	d("start", func(in *Interp) {})
	d("handleerror", func(in *Interp) {})
	d("superexec", func(in *Interp) { in.execObj(in.pop()) })

	// Types and attributes.
	d("type", func(in *Interp) {
		o := in.pop()
		in.push(XName(kindNames[o.Kind]))
	})
	d("cvlit", func(in *Interp) { o := in.pop(); o.Exec = false; in.push(o) })
	d("cvx", func(in *Interp) { o := in.pop(); o.Exec = true; in.push(o) })
	d("xcheck", func(in *Interp) { in.push(Bool(in.pop().Exec)) })
	for _, n := range []string{"executeonly", "noaccess", "readonly"} {
		d(n, func(in *Interp) { in.need(1) })
	}
	d("rcheck", func(in *Interp) { in.pop(); in.push(Bool(true)) })
	d("wcheck", func(in *Interp) { in.pop(); in.push(Bool(true)) })
	d("gcheck", func(in *Interp) { in.pop(); in.push(Bool(false)) })
	d("cvn", func(in *Interp) {
		o := in.popString()
		in.push(Object{Kind: KName, Ref: o.Text(), Exec: o.Exec})
	})
	d("cvs", func(in *Interp) {
		s := in.popString()
		o := in.pop()
		in.push(putText(s, cvs(o)))
	})
	d("cvrs", func(in *Interp) {
		s := in.popString()
		radix := in.popInt()
		o := in.pop()
		var t string
		if radix == 10 {
			t = cvs(o)
		} else {
			v := int64(o.Num())
			if o.Kind == KInt {
				v = int64(uint32(o.Int))
			}
			t = strings.ToUpper(strconv.FormatInt(v, radix))
		}
		in.push(putText(s, t))
	})

	// Dictionaries.
	d("dict", func(in *Interp) { in.push(DictObj(NewDict(in.popInt()))) })
	d("maxlength", func(in *Interp) {
		dd := in.popDict()
		m := dd.Max
		if m < dd.Len() {
			m = dd.Len() + 1
		}
		in.push(Int(m))
	})
	d("begin", func(in *Interp) {
		if len(in.dstack) > 250 {
			panic(psError{"dictstackoverflow", "begin"})
		}
		in.dstack = append(in.dstack, in.popDict())
	})
	d("end", func(in *Interp) {
		if len(in.dstack) <= 3 {
			panic(psError{"dictstackunderflow", "end"})
		}
		in.dstack = in.dstack[:len(in.dstack)-1]
	})
	d("def", func(in *Interp) {
		v, k := in.pop(), in.pop()
		in.currentDict().Put(k, v)
	})
	d("load", func(in *Interp) {
		k := in.pop()
		dd := in.where(k)
		if dd == nil {
			panic(psError{"undefined", k.Text()})
		}
		v, _ := dd.Get(k)
		in.push(v)
	})
	d("store", func(in *Interp) {
		v, k := in.pop(), in.pop()
		dd := in.where(k)
		if dd == nil {
			dd = in.currentDict()
		}
		dd.Put(k, v)
	})
	d("known", func(in *Interp) {
		k := in.pop()
		_, ok := in.popDict().Get(k)
		in.push(Bool(ok))
	})
	d("where", func(in *Interp) {
		k := in.pop()
		if dd := in.where(k); dd != nil {
			in.push(DictObj(dd))
			in.push(Bool(true))
		} else {
			in.push(Bool(false))
		}
	})
	d("undef", func(in *Interp) {
		k := in.pop()
		in.popDict().Delete(k)
	})
	d("currentdict", func(in *Interp) { in.push(DictObj(in.currentDict())) })
	d("countdictstack", func(in *Interp) { in.push(Int(len(in.dstack))) })
	d("dictstack", func(in *Interp) {
		a := in.popArray()
		el := a.Elems()
		for i, dd := range in.dstack {
			if i < len(el) {
				el[i] = DictObj(dd)
			}
		}
		a.Len = min(len(in.dstack), a.Len)
		in.push(a)
	})
	d("cleardictstack", func(in *Interp) { in.dstack = in.dstack[:3] })
	d("get", func(in *Interp) {
		k := in.pop()
		o := in.pop()
		switch o.Kind {
		case KDict:
			v, ok := o.DictV().Get(k)
			if !ok {
				panic(psError{"undefined", k.Text()})
			}
			in.push(v)
		case KArray:
			i := intOf(k)
			if i < 0 || i >= o.Len {
				rangecheck("get")
			}
			in.push(o.Elems()[i])
		case KString:
			i := intOf(k)
			if i < 0 || i >= o.Len {
				rangecheck("get")
			}
			in.push(Int(int(o.Bytes()[i])))
		default:
			typecheck("get")
		}
	})
	d("put", func(in *Interp) {
		v, k, o := in.pop(), in.pop(), in.pop()
		switch o.Kind {
		case KDict:
			o.DictV().Put(k, v)
		case KArray:
			i := intOf(k)
			if i < 0 || i >= o.Len {
				rangecheck("put")
			}
			o.Elems()[i] = v
		case KString:
			i := intOf(k)
			if i < 0 || i >= o.Len {
				rangecheck("put")
			}
			o.Bytes()[i] = byte(intOf(v))
		default:
			typecheck("put")
		}
	})
	d("length", func(in *Interp) {
		o := in.pop()
		switch o.Kind {
		case KDict:
			in.push(Int(o.DictV().Len()))
		case KArray, KString:
			in.push(Int(o.Len))
		case KName:
			in.push(Int(len(o.NameV())))
		default:
			typecheck("length")
		}
	})
	d("getinterval", func(in *Interp) {
		n, i := in.popInt(), in.popInt()
		o := in.pop()
		if o.Kind != KArray && o.Kind != KString {
			typecheck("getinterval")
		}
		if i < 0 || n < 0 || i+n > o.Len {
			rangecheck("getinterval")
		}
		o.Int += i
		o.Len = n
		in.push(o)
	})
	d("putinterval", func(in *Interp) {
		src := in.pop()
		i := in.popInt()
		dst := in.pop()
		switch {
		case dst.Kind == KArray && src.Kind == KArray:
			if i < 0 || i+src.Len > dst.Len {
				rangecheck("putinterval")
			}
			copy(dst.Elems()[i:], src.Elems())
		case dst.Kind == KString && src.Kind == KString:
			if i < 0 || i+src.Len > dst.Len {
				rangecheck("putinterval")
			}
			copy(dst.Bytes()[i:], src.Bytes())
		default:
			typecheck("putinterval")
		}
	})
	d("array", func(in *Interp) {
		n := in.popInt()
		if n < 0 {
			rangecheck("array")
		}
		a := make([]Object, n)
		in.push(Array(a))
	})
	d("packedarray", func(in *Interp) {
		n := in.popInt()
		if n < 0 || n > len(in.ostack) {
			rangecheck("packedarray")
		}
		elems := append([]Object{}, in.ostack[len(in.ostack)-n:]...)
		in.ostack = in.ostack[:len(in.ostack)-n]
		in.push(Array(elems))
	})
	packing := false
	d("setpacking", func(in *Interp) { packing = in.popBool() })
	d("currentpacking", func(in *Interp) { in.push(Bool(packing)) })
	d("aload", func(in *Interp) {
		a := in.popArray()
		for _, e := range a.Elems() {
			in.push(e)
		}
		in.push(a)
	})
	d("astore", func(in *Interp) {
		a := in.popArray()
		in.need(a.Len)
		copy(a.Elems(), in.ostack[len(in.ostack)-a.Len:])
		in.ostack = in.ostack[:len(in.ostack)-a.Len]
		in.push(a)
	})
	d("string", func(in *Interp) {
		n := in.popInt()
		if n < 0 || n > 1<<26 {
			rangecheck("string")
		}
		in.push(Object{Kind: KString, Ref: &strStore{make([]byte, n)}, Len: n})
	})
	d("search", func(in *Interp) {
		seek := in.popString()
		s := in.popString()
		k := bytes.Index(s.Bytes(), seek.Bytes())
		if k < 0 {
			in.push(s)
			in.push(Bool(false))
			return
		}
		post, match, pre := s, s, s
		post.Int, post.Len = s.Int+k+seek.Len, s.Len-k-seek.Len
		match.Int, match.Len = s.Int+k, seek.Len
		pre.Len = k
		in.push(post)
		in.push(match)
		in.push(pre)
		in.push(Bool(true))
	})
	d("anchorsearch", func(in *Interp) {
		seek := in.popString()
		s := in.popString()
		if !bytes.HasPrefix(s.Bytes(), seek.Bytes()) {
			in.push(s)
			in.push(Bool(false))
			return
		}
		post, match := s, s
		post.Int, post.Len = s.Int+seek.Len, s.Len-seek.Len
		match.Len = seek.Len
		in.push(post)
		in.push(match)
		in.push(Bool(true))
	})
	d("token", func(in *Interp) {
		o := in.pop()
		switch o.Kind {
		case KString:
			f := memFile(o.Bytes())
			t, err := in.token(f)
			if err != nil {
				in.push(Bool(false))
				return
			}
			rest := o
			rest.Int += f.pos
			rest.Len -= f.pos
			in.push(rest)
			in.push(t)
			in.push(Bool(true))
		case KFile:
			t, err := in.token(o.Ref.(*File))
			if err != nil {
				in.push(Bool(false))
				return
			}
			in.push(t)
			in.push(Bool(true))
		default:
			typecheck("token")
		}
	})
	d("bind", func(in *Interp) {
		p := in.top()
		if p.Kind == KArray {
			in.bind(p, 0)
		}
	})
	d("null", func(in *Interp) { in.push(null) })

	// VM, environment.
	d("save", func(in *Interp) {
		in.saves++
		in.gsave()
		in.push(Object{Kind: KSave, Int: len(in.gstack)})
	})
	d("restore", func(in *Interp) {
		s := in.pop()
		if s.Kind != KSave {
			typecheck("restore")
		}
		for len(in.gstack) >= s.Int && len(in.gstack) > 0 {
			in.grestore()
		}
	})
	d("vmstatus", func(in *Interp) { in.push(Int(in.saves)); in.push(Int(100000)); in.push(Int(10000000)) })
	d("realtime", func(in *Interp) { in.push(Int(int(in.steps / 1000))) })
	d("usertime", func(in *Interp) { in.push(Int(int(in.steps / 1000))) })
	// An integer, like in Ghostscript (prologs use
	// "systemdict /languagelevel get 2 ge").
	in.systemdict.PutS("languagelevel", Int(3))
	in.systemdict.PutS("version", String([]byte("3010")))
	in.systemdict.PutS("product", String([]byte("weasyprint-go")))
	in.systemdict.PutS("revision", Int(1))
	in.systemdict.PutS("serialnumber", Int(0))
	global := false
	d("setglobal", func(in *Interp) { global = in.popBool() })
	d("currentglobal", func(in *Interp) { in.push(Bool(global)) })
	d("setshared", func(in *Interp) { in.pop() })
	d("currentshared", func(in *Interp) { in.push(Bool(false)) })
	for _, n := range []string{"print", "=", "==", "setobjectformat", "setvmthreshold", "vmreclaim", "echo", "setcachelimit",
		"setuserparams", "setsystemparams", "setucacheparams", "setpagedevice", "setstrokeadjust", "setoverprint",
		"setsmoothness", "setflat", "sethalftone", "settransfer", "setblackgeneration", "setundercolorremoval",
		"setcolorrendering", "setrenderingintent", "settrapparams", "setdistillerparams", "setfileposition"} {
		d(n, func(in *Interp) { in.pop() })
	}
	d("setfileposition", func(in *Interp) { in.pop(); in.pop() })
	d("setdevparams", func(in *Interp) { in.pop(); in.pop() })
	d("setscreen", func(in *Interp) { in.pop(); in.pop(); in.pop() })
	d("setcolorscreen", func(in *Interp) {
		for i := 0; i < 12; i++ {
			in.pop()
		}
	})
	d("setcolortransfer", func(in *Interp) {
		for i := 0; i < 4; i++ {
			in.pop()
		}
	})
	d("sethalftonephase", func(in *Interp) { in.pop(); in.pop() })
	d("setcacheparams", func(in *Interp) { k := in.markIndex(); in.ostack = in.ostack[:k] })
	for _, n := range []string{"flush", "showpage", "copypage", "erasepage", "prompt", "executive", "ucache", "nulldevice"} {
		name := n
		d(n, func(in *Interp) {
			if name == "showpage" || name == "copypage" {
				in.list.showpages++
			}
		})
	}
	d("stack", func(in *Interp) {})
	d("pstack", func(in *Interp) {})
	d("flushfile", func(in *Interp) { in.pop() })
	d("currentscreen", func(in *Interp) { in.push(Real(60)); in.push(Real(45)); in.push(procObj()) })
	d("currenttransfer", func(in *Interp) { in.push(procObj()) })
	d("currentblackgeneration", func(in *Interp) { in.push(procObj()) })
	d("currentundercolorremoval", func(in *Interp) { in.push(procObj()) })
	d("currentcolortransfer", func(in *Interp) {
		for i := 0; i < 4; i++ {
			in.push(procObj())
		}
	})
	d("currentflat", func(in *Interp) { in.push(Real(1)) })
	d("currentsmoothness", func(in *Interp) { in.push(Real(0.02)) })
	d("currentoverprint", func(in *Interp) { in.push(Bool(false)) })
	d("currentstrokeadjust", func(in *Interp) { in.push(Bool(false)) })
	d("currenthalftone", func(in *Interp) {
		h := NewDict(4)
		h.PutS("HalftoneType", Int(1))
		h.PutS("Frequency", Real(60))
		h.PutS("Angle", Real(45))
		h.PutS("SpotFunction", procObj())
		in.push(DictObj(h))
	})
	d("currentcolorrendering", func(in *Interp) { in.push(DictObj(NewDict(1))) })
	d("currentuserparams", func(in *Interp) { in.push(DictObj(NewDict(1))) })
	d("currentsystemparams", func(in *Interp) { in.push(DictObj(NewDict(1))) })
	d("currentdevparams", func(in *Interp) { in.pop(); in.push(DictObj(NewDict(1))) })
	d("currentpagedevice", func(in *Interp) {
		p := NewDict(8)
		p.PutS("PageSize", Array([]Object{Real(612), Real(792)}))
		p.PutS("HWResolution", Array([]Object{Real(72), Real(72)}))
		p.PutS("ImagingBBox", null)
		p.PutS("Policies", DictObj(NewDict(1)))
		p.PutS("ProcessColorModel", Name("DeviceCMYK"))
		in.push(DictObj(p))
	})
	d("cachestatus", func(in *Interp) {
		for i := 0; i < 7; i++ {
			in.push(Int(0))
		}
	})

	// Files.
	d("currentfile", func(in *Interp) {
		var f *File
		if len(in.files) > 0 {
			f = in.files[len(in.files)-1]
		} else {
			f = memFile(nil)
		}
		in.push(Object{Kind: KFile, Ref: f, Exec: false})
	})
	d("file", func(in *Interp) {
		in.pop()
		name := in.pop()
		if t := name.Text(); t == "%stdin" || t == "%stdout" || t == "%stderr" || t == "%lineedit" || t == "%statementedit" {
			in.push(Object{Kind: KFile, Ref: memFile(nil)})
			return
		}
		panic(psError{"undefinedfilename", "file"})
	})
	d("run", func(in *Interp) { in.pop(); panic(psError{"undefinedfilename", "run"}) })
	d("deletefile", func(in *Interp) { in.pop(); panic(psError{"undefinedfilename", "deletefile"}) })
	d("closefile", func(in *Interp) { in.popFile().Close() })
	d("resetfile", func(in *Interp) { in.pop() })
	d("bytesavailable", func(in *Interp) { in.pop(); in.push(Int(-1)) })
	d("status", func(in *Interp) {
		o := in.pop()
		if o.Kind == KFile {
			in.push(Bool(!o.Ref.(*File).closed))
			return
		}
		in.push(Bool(false))
	})
	d("fileposition", func(in *Interp) { f := in.popFile(); in.push(Int(f.pos)) })
	d("read", func(in *Interp) {
		f := in.popFile()
		c, err := f.ReadByte()
		if err != nil {
			in.push(Bool(false))
			return
		}
		in.push(Int(int(c)))
		in.push(Bool(true))
	})
	d("write", func(in *Interp) { in.pop(); in.pop() })
	d("writestring", func(in *Interp) { in.pop(); in.pop() })
	d("writehexstring", func(in *Interp) { in.pop(); in.pop() })
	d("readhexstring", func(in *Interp) {
		s := in.popString()
		f := in.popFile()
		b := s.Bytes()
		n := 0
		h := -1
		for n < len(b) {
			c, err := f.ReadByte()
			if err != nil {
				break
			}
			v := hexVal(c)
			if v < 0 {
				continue
			}
			if h < 0 {
				h = v
				continue
			}
			b[n] = byte(h<<4 | v)
			n++
			h = -1
		}
		s.Len = n
		in.push(s)
		in.push(Bool(n == len(b)))
	})
	d("readstring", func(in *Interp) {
		s := in.popString()
		f := in.popFile()
		n := f.readFull(s.Bytes())
		full := n == s.Len
		s.Len = n
		in.push(s)
		in.push(Bool(full))
	})
	d("readline", func(in *Interp) {
		s := in.popString()
		f := in.popFile()
		b := s.Bytes()
		n := 0
		ok := false
		for {
			c, err := f.ReadByte()
			if err != nil {
				break
			}
			if c == '\n' {
				ok = true
				break
			}
			if c == '\r' {
				if c2, err := f.ReadByte(); err == nil && c2 != '\n' {
					f.UnreadByte()
				}
				ok = true
				break
			}
			if n >= len(b) {
				rangecheck("readline")
			}
			b[n] = c
			n++
		}
		s.Len = n
		in.push(s)
		in.push(Bool(ok))
	})
	d("filter", func(in *Interp) {
		name := in.pop().Text()
		var parms *Dict
		if name == "SubFileDecode" && in.top().Kind == KString {
			// Level 2 form: source count string.
			eod := in.popString()
			cnt := in.popInt()
			parms = NewDict(2)
			parms.PutS("EODString", eod)
			parms.PutS("EODCount", Int(cnt))
		} else if t := in.top(); t.Kind == KDict {
			parms = in.popDict()
		}
		src := in.pop()
		// Encode filters take a target; only decoding is supported.
		var rd interface{ ReadByte() (byte, error) }
		switch src.Kind {
		case KFile:
			rd = src.Ref.(*File)
		case KString:
			rd = memFile(src.Bytes())
		case KArray:
			if src.Exec {
				rd = readerFile(&procReader{in: in, proc: src})
			} else {
				typecheck("filter")
			}
		default:
			typecheck("filter")
		}
		f := in.makeFilter(name, rd, parms)
		in.push(Object{Kind: KFile, Ref: f})
	})
	d("eexec", func(in *Interp) {
		o := in.pop()
		var src *File
		switch o.Kind {
		case KFile:
			src = o.Ref.(*File)
		case KString:
			src = memFile(o.Bytes())
		default:
			typecheck("eexec")
		}
		f := byteFile(newEexec(src))
		in.dstack = append(in.dstack, in.systemdict)
		n := len(in.dstack)
		in.runFile(f, false)
		if len(in.dstack) >= n {
			in.dstack = append(in.dstack[:n-1], in.dstack[n:]...)
		}
	})

	// Resources.
	d("findresource", func(in *Interp) {
		cat := in.pop().Text()
		key := in.pop()
		v, ok := in.findResource(cat, key)
		if !ok {
			panic(psError{"undefinedresource", key.Text()})
		}
		in.push(v)
	})
	d("defineresource", func(in *Interp) {
		cat := in.pop().Text()
		v := in.pop()
		key := in.pop()
		if cat == "Font" || cat == "CIDFont" {
			v = in.defineFont(key, v)
		}
		in.resourceDict(cat).Put(key, v)
		in.push(v)
	})
	d("undefineresource", func(in *Interp) {
		cat := in.pop().Text()
		key := in.pop()
		in.resourceDict(cat).Delete(key)
		if cat == "Font" {
			in.fontDir.Delete(key)
		}
	})
	d("resourcestatus", func(in *Interp) {
		cat := in.pop().Text()
		key := in.pop()
		if _, ok := in.findResourceNoLoad(cat, key); ok {
			in.push(Int(1))
			in.push(Int(0))
			in.push(Bool(true))
			return
		}
		in.push(Bool(false))
	})
	d("resourceforall", func(in *Interp) {
		in.pop() // category
		in.pop() // scratch
		in.pop() // proc
		in.pop() // template
	})
	d("findencoding", func(in *Interp) {
		key := in.pop()
		v, ok := in.findResource("Encoding", key)
		if !ok {
			panic(psError{"undefinedresource", key.Text()})
		}
		in.push(v)
	})
}

func procObj() Object { a := Array(nil); a.Exec = true; return a }

func (in *Interp) popFile() *File {
	o := in.pop()
	if o.Kind != KFile {
		typecheck("file")
	}
	return o.Ref.(*File)
}

func (in *Interp) markIndex() int {
	for i := len(in.ostack) - 1; i >= 0; i-- {
		if in.ostack[i].Kind == KMark {
			return i
		}
	}
	panic(psError{"unmatchedmark", ""})
}

func (in *Interp) resourceDict(cat string) *Dict {
	d, ok := in.resources[cat]
	if !ok {
		d = NewDict(8)
		in.resources[cat] = d
	}
	return d
}

func (in *Interp) findResourceNoLoad(cat string, key Object) (Object, bool) {
	if v, ok := in.resourceDict(cat).Get(key); ok {
		return v, true
	}
	if cat == "Font" {
		if v, ok := in.fontDir.Get(key); ok {
			return v, true
		}
	}
	if cat == "Category" {
		if d, ok := in.resources[key.Text()]; ok {
			return DictObj(d), true
		}
	}
	return null, false
}

func (in *Interp) findResource(cat string, key Object) (Object, bool) {
	if v, ok := in.findResourceNoLoad(cat, key); ok {
		return v, true
	}
	switch cat {
	case "Font":
		return in.findFont(key), true
	case "ProcSet":
		// Unknown procsets are empty dictionaries.
		return null, false
	case "CMap":
		if c := identityCMap(key.Text()); c != nil {
			return DictObj(c), true
		}
	}
	return null, false
}

func (in *Interp) bind(p Object, depth int) {
	if depth > 50 {
		return
	}
	elems := p.Elems()
	for i, e := range elems {
		switch {
		case e.Kind == KName && e.Exec:
			if v, ok := in.lookup(e.NameV()); ok && v.Kind == KOperator {
				elems[i] = v
			}
		case e.Kind == KArray && e.Exec:
			in.bind(e, depth+1)
		}
	}
}

func opCopy(in *Interp) {
	o := in.pop()
	switch o.Kind {
	case KInt:
		n := o.Int
		if n < 0 || n > len(in.ostack) {
			rangecheck("copy")
		}
		in.ostack = append(in.ostack, in.ostack[len(in.ostack)-n:]...)
	case KArray:
		src := in.popArray()
		if src.Len > o.Len {
			rangecheck("copy")
		}
		copy(o.Elems(), src.Elems())
		o.Len = src.Len
		in.push(o)
	case KString:
		src := in.popString()
		if src.Len > o.Len {
			rangecheck("copy")
		}
		copy(o.Bytes(), src.Bytes())
		o.Len = src.Len
		in.push(o)
	case KDict:
		src := in.popDict()
		dst := o.DictV()
		src.Each(func(k, v Object) bool { dst.Put(k, v); return true })
		in.push(o)
	case KGState:
		src := in.pop()
		if src.Kind != KGState {
			typecheck("copy")
		}
		*o.Ref.(*GState) = *src.Ref.(*GState).clone()
		in.push(o)
	default:
		typecheck("copy")
	}
}

func num(o Object) float64 {
	if !o.IsNum() {
		typecheck("number")
	}
	return o.Num()
}

func intOf(o Object) int {
	switch o.Kind {
	case KInt:
		return o.Int
	case KReal:
		return int(o.Real)
	}
	typecheck("integer")
	return 0
}

func equal(a, b Object) bool {
	switch {
	case a.IsNum() && b.IsNum():
		return a.Num() == b.Num()
	case (a.Kind == KName || a.Kind == KString) && (b.Kind == KName || b.Kind == KString):
		return a.Text() == b.Text()
	case a.Kind != b.Kind:
		return false
	}
	switch a.Kind {
	case KBool:
		return a.Int == b.Int
	case KNull, KMark:
		return true
	case KArray:
		return a.Ref == b.Ref && a.Int == b.Int && a.Len == b.Len
	}
	return a.Ref == b.Ref
}

// putText writes t into the string s and returns the substring.
func putText(s Object, t string) Object {
	if len(t) > s.Len {
		rangecheck("cvs")
	}
	copy(s.Bytes(), t)
	s.Len = len(t)
	return s
}

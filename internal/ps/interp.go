package ps

import (
	"fmt"
	"time"
)

// psError is a PostScript error (raised with panic, caught by stopped).
type psError struct {
	name, cmd string
}

func (e psError) Error() string { return fmt.Sprintf("%s in %s", e.name, e.cmd) }

type exitSignal struct{}
type stopSignal struct{}
type quitSignal struct{}
type limitSignal struct{}

// Options configures a run.
type Options struct {
	// Font returns the outlines of a font that the program uses without
	// embedding it (e.g. "Helvetica-Bold"), or nil.
	Font func(psName string) SystemFont
	// Warn receives warnings (nil to ignore).
	Warn func(string)
	// MaxSteps limits the number of executed objects (0: 50 millions).
	MaxSteps int
	// Timeout limits the run time (0: 20 seconds).
	Timeout time.Duration
}

// Interp is a PostScript interpreter.
type Interp struct {
	opts   Options
	ostack []Object
	dstack []*Dict
	// files is the stack of files being executed (for currentfile).
	files []*File

	systemdict, globaldict, userdict *Dict
	errordict, dollarError           *Dict
	statusdict, fontDir              *Dict
	resources                        map[string]*Dict // resource categories

	gs     *GState
	gstack []*GState
	saves  int

	list   *List
	steps  int
	depth  int
	start  time.Time
	nextID int
	// fonts caches the decoded fonts by font dictionary.
	fonts map[*Dict]*fontInfo
	// cacheDevice is set while a Type 3 glyph is built.
	building []*buildState
	errs     int
}

func (in *Interp) warn(f string, a ...any) {
	if in.opts.Warn != nil {
		in.opts.Warn(fmt.Sprintf(f, a...))
	}
}

// ---------------------------------------------------------------- stacks

func (in *Interp) push(o Object) {
	if len(in.ostack) > 100000 {
		panic(psError{"stackoverflow", ""})
	}
	in.ostack = append(in.ostack, o)
}

func (in *Interp) pop() Object {
	n := len(in.ostack)
	if n == 0 {
		panic(psError{"stackunderflow", ""})
	}
	o := in.ostack[n-1]
	in.ostack = in.ostack[:n-1]
	return o
}

func (in *Interp) need(n int) {
	if len(in.ostack) < n {
		panic(psError{"stackunderflow", ""})
	}
}

func (in *Interp) top() Object {
	in.need(1)
	return in.ostack[len(in.ostack)-1]
}

func typecheck(cmd string)  { panic(psError{"typecheck", cmd}) }
func rangecheck(cmd string) { panic(psError{"rangecheck", cmd}) }

func (in *Interp) popNum() float64 {
	o := in.pop()
	if !o.IsNum() {
		typecheck("number")
	}
	return o.Num()
}

func (in *Interp) popInt() int {
	o := in.pop()
	switch o.Kind {
	case KInt:
		return o.Int
	case KReal:
		if o.Real == float64(int(o.Real)) {
			return int(o.Real)
		}
	}
	typecheck("integer")
	return 0
}

func (in *Interp) popBool() bool {
	o := in.pop()
	if o.Kind != KBool {
		typecheck("boolean")
	}
	return o.BoolV()
}

func (in *Interp) popDict() *Dict {
	o := in.pop()
	if o.Kind != KDict {
		typecheck("dict")
	}
	return o.DictV()
}

func (in *Interp) popProc() Object {
	o := in.pop()
	if !isProc(o) {
		typecheck("proc")
	}
	return o
}

func isProc(o Object) bool {
	return o.Exec && o.Kind == KArray || o.Kind == KOperator || o.Exec && (o.Kind == KName || o.Kind == KString || o.Kind == KFile)
}

func (in *Interp) popString() Object {
	o := in.pop()
	if o.Kind != KString {
		typecheck("string")
	}
	return o
}

func (in *Interp) popArray() Object {
	o := in.pop()
	if o.Kind != KArray {
		typecheck("array")
	}
	return o
}

// ---------------------------------------------------------------- names

func (in *Interp) lookup(name string) (Object, bool) {
	for i := len(in.dstack) - 1; i >= 0; i-- {
		if v, ok := in.dstack[i].GetS(name); ok {
			return v, true
		}
	}
	return null, false
}

func (in *Interp) where(k Object) *Dict {
	for i := len(in.dstack) - 1; i >= 0; i-- {
		if _, ok := in.dstack[i].Get(k); ok {
			return in.dstack[i]
		}
	}
	return nil
}

func (in *Interp) currentDict() *Dict { return in.dstack[len(in.dstack)-1] }

// ---------------------------------------------------------------- execution

func (in *Interp) step() {
	in.steps++
	if in.steps&0xfff == 0 {
		max := in.opts.MaxSteps
		if max == 0 {
			max = 50_000_000
		}
		to := in.opts.Timeout
		if to == 0 {
			to = 20 * time.Second
		}
		if in.steps > max || time.Since(in.start) > to {
			panic(limitSignal{})
		}
	}
}

// execObj executes an object (the value of a name, or the operand of
// exec, if...).
func (in *Interp) execObj(o Object) {
	in.step()
	if !o.Exec {
		in.push(o)
		return
	}
	switch o.Kind {
	case KOperator:
		o.Ref.(*Operator).Fn(in)
	case KName:
		v, ok := in.lookup(o.NameV())
		if !ok {
			panic(psError{"undefined", o.NameV()})
		}
		if v.Kind == KArray && v.Exec {
			in.runProc(v)
		} else {
			in.execObj(v)
		}
	case KArray:
		in.runProc(o)
	case KString:
		f := memFile(o.Bytes())
		in.runFile(f, false)
	case KFile:
		in.runFile(o.Ref.(*File), false)
	case KNull:
	default:
		in.push(o)
	}
}

// execDirect executes an object found in a procedure or a file:
// procedures are pushed.
func (in *Interp) execDirect(o Object) {
	if o.Exec && (o.Kind == KName || o.Kind == KOperator) {
		in.execObj(o)
		return
	}
	in.push(o)
}

func (in *Interp) runProc(p Object) {
	in.depth++
	if in.depth > 400 {
		in.depth = 0
		panic(psError{"execstackoverflow", ""})
	}
	defer func() { in.depth-- }()
	for _, e := range p.Elems() {
		in.execDirect(e)
	}
}

// runFile executes the tokens of a file.
func (in *Interp) runFile(f *File, top bool) {
	in.files = append(in.files, f)
	defer func() { in.files = in.files[:len(in.files)-1] }()
	for !f.closed {
		o, err := in.token(f)
		if err != nil {
			return
		}
		if o.Kind == KName && o.Exec && o.NameV() == "}" {
			panic(psError{"syntaxerror", "}"})
		}
		if top {
			in.topLevel(o)
		} else {
			in.execDirect(o)
		}
	}
}

// topLevel executes an object of the program, handling errors like a
// job server: the error is reported and the job ends.
func (in *Interp) topLevel(o Object) {
	in.execDirect(o)
}

// call runs a procedure and recovers errors, returning them.
func (in *Interp) stopped(f func()) (stopped bool, err *psError) {
	n := len(in.files)
	depth := in.depth
	nb := len(in.building)
	defer func() {
		if r := recover(); r != nil {
			switch e := r.(type) {
			case psError:
				stopped, err = true, &e
				in.recordError(e)
			case stopSignal:
				stopped = true
			default:
				panic(r)
			}
			in.files = in.files[:n]
			in.depth = depth
			in.building = in.building[:nb]
		}
	}()
	f()
	return false, nil
}

func (in *Interp) recordError(e psError) {
	d := in.dollarError
	d.PutS("newerror", Bool(true))
	d.PutS("errorname", Name(e.name))
	d.PutS("command", Name(e.cmd))
}

// loop runs body until exit.
func (in *Interp) loopBody(f func() bool) {
	defer func() {
		if r := recover(); r != nil {
			if _, ok := r.(exitSignal); !ok {
				panic(r)
			}
		}
	}()
	for f() {
	}
}

// ---------------------------------------------------------------- setup

// New returns an interpreter.
func New(opts Options) *Interp {
	in := &Interp{opts: opts, start: time.Now(), fonts: map[*Dict]*fontInfo{}}
	in.systemdict = NewDict(512)
	in.globaldict = NewDict(64)
	in.userdict = NewDict(256)
	in.errordict = NewDict(32)
	in.dollarError = NewDict(16)
	in.statusdict = NewDict(32)
	in.fontDir = NewDict(64)
	in.resources = map[string]*Dict{}
	for _, c := range []string{"Font", "Encoding", "ProcSet", "Category", "CIDFont", "CMap", "ColorSpace",
		"Pattern", "Form", "Halftone", "ColorRendering", "Generic", "FontSet", "IdiomSet", "InkParams", "TrapParams",
		"ColorSpaceFamily", "Emulator", "Filter", "FMapType", "FontType", "ImageType", "PatternType", "ShadingType", "FunctionType", "HalftoneType"} {
		in.resources[c] = NewDict(8)
	}
	in.registerOps()
	// The CIDInit procset: the CMap operators are in systemdict.
	cidInit := NewDict(4)
	in.resources["ProcSet"].PutS("CIDInit", DictObj(cidInit))
	sd := in.systemdict
	sd.PutS("systemdict", DictObj(sd))
	sd.PutS("userdict", DictObj(in.userdict))
	sd.PutS("globaldict", DictObj(in.globaldict))
	sd.PutS("errordict", DictObj(in.errordict))
	sd.PutS("$error", DictObj(in.dollarError))
	sd.PutS("statusdict", DictObj(in.statusdict))
	sd.PutS("FontDirectory", DictObj(in.fontDir))
	sd.PutS("GlobalFontDirectory", DictObj(in.fontDir))
	sd.PutS("SharedFontDirectory", DictObj(in.fontDir))
	sd.PutS("shareddict", DictObj(in.globaldict))
	sd.PutS("true", Bool(true))
	sd.PutS("false", Bool(false))
	sd.PutS("null", null)
	sd.PutS("StandardEncoding", encodingArray(standardEncoding[:]))
	sd.PutS("ISOLatin1Encoding", encodingArray(isoLatin1Encoding[:]))
	in.resources["Encoding"].PutS("StandardEncoding", encodingArray(standardEncoding[:]))
	in.resources["Encoding"].PutS("ISOLatin1Encoding", encodingArray(isoLatin1Encoding[:]))
	in.resources["Encoding"].PutS("SymbolEncoding", encodingArray(symbolEncoding[:]))
	in.dollarError.PutS("newerror", Bool(false))
	in.dollarError.PutS("errorname", null)
	in.dollarError.PutS("command", null)
	in.dollarError.PutS("ostack", Array(nil))
	in.dollarError.PutS("estack", Array(nil))
	in.dollarError.PutS("dstack", Array(nil))
	in.dollarError.PutS("recordstacks", Bool(false))
	in.dollarError.PutS("binary", Bool(false))
	for _, e := range []string{"dictfull", "dictstackoverflow", "dictstackunderflow", "execstackoverflow", "handleerror",
		"interrupt", "invalidaccess", "invalidexit", "invalidfileaccess", "invalidfont", "invalidrestore", "ioerror",
		"limitcheck", "nocurrentpoint", "rangecheck", "stackoverflow", "stackunderflow", "syntaxerror", "timeout",
		"typecheck", "undefined", "undefinedfilename", "undefinedresource", "undefinedresult", "unmatchedmark",
		"unregistered", "VMerror", "configurationerror", "undefinedresource"} {
		name := e
		in.errordict.PutS(e, opObj(name, func(in *Interp) { panic(psError{name, ""}) }))
	}
	in.statusdict.PutS("product", String([]byte("weasyprint-go")))
	in.statusdict.PutS("processcolors", Int(4))
	in.dstack = []*Dict{in.systemdict, in.globaldict, in.userdict}
	in.gs = newGState()
	in.list = &List{}
	return in
}

func opObj(name string, fn func(*Interp)) Object {
	return Object{Kind: KOperator, Exec: true, Ref: &Operator{Name: name, Fn: fn}}
}

func (in *Interp) def(name string, fn func(*Interp)) {
	in.systemdict.PutS(name, opObj(name, fn))
}

// Run executes a program and returns the display list. Errors end the
// program; what was painted before is kept.
func (in *Interp) Run(program []byte) (l *List, err error) {
	defer func() {
		if r := recover(); r != nil {
			switch e := r.(type) {
			case psError:
				err = e
			case quitSignal, stopSignal:
			case limitSignal:
				err = fmt.Errorf("ps: execution limit reached")
			case exitSignal:
				err = psError{"invalidexit", "exit"}
			default:
				err = fmt.Errorf("ps: internal error: %v", r)
			}
		}
		l = in.list
	}()
	in.runFile(memFile(program), true)
	return in.list, nil
}

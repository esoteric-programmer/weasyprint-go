// Package ps is a PostScript interpreter for EPS images: it runs the
// program and records what it paints (paths, images, shadings) in a
// display list, in the default user space (points, y up).
package ps

import (
	"math"
	"strconv"
)

// Kind is the type of an object.
type Kind uint8

const (
	KNull Kind = iota
	KInt
	KReal
	KBool
	KName
	KString
	KArray
	KDict
	KOperator
	KMark
	KFile
	KSave
	KFontID
	KGState
	KInternal // opaque Go value (system fonts...)
)

var kindNames = [...]string{"nulltype", "integertype", "realtype", "booleantype", "nametype", "stringtype",
	"arraytype", "dicttype", "operatortype", "marktype", "filetype", "savetype", "fonttype", "gstatetype", "nulltype"}

// Object is a PostScript object. Strings and arrays are views (Int is
// the offset, Len the length) on shared storage.
type Object struct {
	Kind Kind
	Exec bool
	Int  int
	Len  int
	Real float64
	Ref  any
}

type strStore struct{ b []byte }
type arrStore struct{ a []Object }

// Operator is a built-in operator.
type Operator struct {
	Name string
	Fn   func(*Interp)
}

var (
	null = Object{Kind: KNull}
	mark = Object{Kind: KMark}
)

func Int(i int) Object      { return Object{Kind: KInt, Int: i} }
func Real(f float64) Object { return Object{Kind: KReal, Real: f} }
func Bool(b bool) Object {
	o := Object{Kind: KBool}
	if b {
		o.Int = 1
	}
	return o
}
func Name(s string) Object     { return Object{Kind: KName, Ref: s} }
func XName(s string) Object    { return Object{Kind: KName, Ref: s, Exec: true} }
func DictObj(d *Dict) Object   { return Object{Kind: KDict, Ref: d} }
func internal(v any) Object    { return Object{Kind: KInternal, Ref: v} }
func numObj(f float64) Object  { return Real(f) }
func (o Object) IsNum() bool   { return o.Kind == KInt || o.Kind == KReal }
func (o Object) BoolV() bool   { return o.Int != 0 }
func (o Object) NameV() string { s, _ := o.Ref.(string); return s }
func (o Object) DictV() *Dict  { d, _ := o.Ref.(*Dict); return d }

// Num returns the value of a number.
func (o Object) Num() float64 {
	if o.Kind == KInt {
		return float64(o.Int)
	}
	return o.Real
}

// String makes a string object holding a copy of b.
func String(b []byte) Object {
	c := make([]byte, len(b))
	copy(c, b)
	return Object{Kind: KString, Ref: &strStore{c}, Len: len(c)}
}

// Bytes returns the bytes of a string (shared).
func (o Object) Bytes() []byte {
	s, ok := o.Ref.(*strStore)
	if !ok {
		return nil
	}
	return s.b[o.Int : o.Int+o.Len]
}

// Array makes an array object.
func Array(a []Object) Object {
	return Object{Kind: KArray, Ref: &arrStore{a}, Len: len(a)}
}

// Elems returns the elements of an array (shared).
func (o Object) Elems() []Object {
	s, ok := o.Ref.(*arrStore)
	if !ok {
		return nil
	}
	return s.a[o.Int : o.Int+o.Len]
}

// Text returns the bytes of a string or the name.
func (o Object) Text() string {
	switch o.Kind {
	case KName:
		return o.NameV()
	case KString:
		return string(o.Bytes())
	}
	return ""
}

// Dict is a dictionary, keeping the insertion order.
type Dict struct {
	idx  map[any]int
	keys []Object
	vals []Object
	dead int
	Max  int
}

// NewDict returns an empty dictionary.
func NewDict(n int) *Dict { return &Dict{idx: make(map[any]int, n), Max: n} }

// dictKey normalizes a key: strings and names are the same, integral
// reals equal integers.
func dictKey(k Object) any {
	switch k.Kind {
	case KName, KString:
		return k.Text()
	case KInt:
		return int64(k.Int)
	case KReal:
		if k.Real == math.Trunc(k.Real) && math.Abs(k.Real) < 1e15 {
			return int64(k.Real)
		}
		return k.Real
	case KBool:
		return k.Int != 0
	case KNull:
		return nil
	case KMark:
		return mark.Kind
	}
	if k.Ref != nil {
		type refKey struct {
			r   any
			off int
		}
		return refKey{k.Ref, k.Int}
	}
	return k.Kind
}

// Get returns the value of a key.
func (d *Dict) Get(k Object) (Object, bool) {
	i, ok := d.idx[dictKey(k)]
	if !ok {
		return null, false
	}
	return d.vals[i], true
}

// GetS returns the value of a name key.
func (d *Dict) GetS(k string) (Object, bool) {
	i, ok := d.idx[k]
	if !ok {
		return null, false
	}
	return d.vals[i], true
}

// Put sets a key.
func (d *Dict) Put(k, v Object) {
	if k.Kind == KString {
		k = Name(k.Text())
	}
	key := dictKey(k)
	if i, ok := d.idx[key]; ok {
		d.vals[i] = v
		return
	}
	d.idx[key] = len(d.keys)
	d.keys = append(d.keys, k)
	d.vals = append(d.vals, v)
}

// PutS sets a name key.
func (d *Dict) PutS(k string, v Object) { d.Put(Name(k), v) }

// Delete removes a key.
func (d *Dict) Delete(k Object) {
	key := dictKey(k)
	i, ok := d.idx[key]
	if !ok {
		return
	}
	delete(d.idx, key)
	d.keys[i] = Object{Kind: KMark, Ref: deadKey{}}
	d.dead++
}

type deadKey struct{}

// Len returns the number of entries.
func (d *Dict) Len() int { return len(d.keys) - d.dead }

// Each calls f for the entries in insertion order.
func (d *Dict) Each(f func(k, v Object) bool) {
	n := len(d.keys)
	for i := 0; i < n && i < len(d.keys); i++ {
		if _, dead := d.keys[i].Ref.(deadKey); dead {
			continue
		}
		if !f(d.keys[i], d.vals[i]) {
			return
		}
	}
}

// Copy returns a shallow copy.
func (d *Dict) Copy() *Dict {
	c := NewDict(d.Len())
	d.Each(func(k, v Object) bool { c.Put(k, v); return true })
	c.Max = d.Max
	return c
}

// cvs returns the text of an object for cvs and =.
func cvs(o Object) string {
	switch o.Kind {
	case KInt:
		return strconv.Itoa(o.Int)
	case KReal:
		return formatReal(o.Real)
	case KBool:
		if o.BoolV() {
			return "true"
		}
		return "false"
	case KName:
		return o.NameV()
	case KString:
		return string(o.Bytes())
	case KOperator:
		return o.Ref.(*Operator).Name
	}
	return "--nostringval--"
}

func formatReal(f float64) string {
	if f == math.Trunc(f) && math.Abs(f) < 1e15 {
		return strconv.FormatFloat(f, 'f', 1, 64)
	}
	return strconv.FormatFloat(f, 'g', 6, 32)
}

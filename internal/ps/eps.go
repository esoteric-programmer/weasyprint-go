package ps

// RenderEPS runs an EPS program and returns its display list (in the
// default user space; the caller clips to the bounding box).
func RenderEPS(program []byte, bbox [4]float64, opts Options) (*List, error) {
	in := New(opts)
	in.list.box = bbox
	return in.Run(program)
}

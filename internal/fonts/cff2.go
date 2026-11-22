package fonts

import "errors"

// cff2ToCFF converts a CFF2 table to a CFF table for an instance.
func cff2ToCFF(f *Font, cff2 []byte, coords []float64) ([]byte, error) {
	return nil, errors.New("fonts: CFF2 instances are not supported")
}

// Package vec provides component-wise arithmetic on resource vectors.
package vec

import "errors"

// ErrDim is returned when two vectors have different lengths.
var ErrDim = errors.New("vec: dimension mismatch")

// Vec is a resource vector, one component per resource class.
type Vec []int

func dim(a, b Vec) error {
	if len(a) != len(b) {
		return ErrDim
	}
	return nil
}

// LE reports whether a <= b holds component-wise.
func LE(a, b Vec) (bool, error) {
	if err := dim(a, b); err != nil {
		return false, err
	}
	for i := range a {
		if a[i] > b[i] {
			return false, nil
		}
	}
	return true, nil
}

// Add returns a+b as a new vector.
func Add(a, b Vec) (Vec, error) {
	if err := dim(a, b); err != nil {
		return nil, err
	}
	r := make(Vec, len(a))
	for i := range a {
		r[i] = a[i] + b[i]
	}
	return r, nil
}

// Sub returns a-b as a new vector.
func Sub(a, b Vec) (Vec, error) {
	if err := dim(a, b); err != nil {
		return nil, err
	}
	r := make(Vec, len(a))
	for i := range a {
		r[i] = a[i] - b[i]
	}
	return r, nil
}

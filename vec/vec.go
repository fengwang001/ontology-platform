package vec

import "errors"

var ErrDim = errors.New("vec: length mismatch")

type V []int

func check(a, b V) error {
	if len(a) != len(b) {
		return ErrDim
	}
	return nil
}

func LE(a, b V) (bool, error) {
	if err := check(a, b); err != nil {
		return false, err
	}
	for i := range a {
		if a[i] > b[i] {
			return false, nil
		}
	}
	return true, nil
}

func Add(a, b V) (V, error) {
	if err := check(a, b); err != nil {
		return nil, err
	}
	out := make(V, len(a))
	for i := range a {
		out[i] = a[i] + b[i]
	}
	return out, nil
}

func Sub(a, b V) (V, error) {
	if err := check(a, b); err != nil {
		return nil, err
	}
	out := make(V, len(a))
	for i := range a {
		out[i] = a[i] - b[i]
	}
	return out, nil
}

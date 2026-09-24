package vec

import "errors"

var ErrLength = errors.New("vec: length mismatch")

type Vec []int

func Clone(v Vec) Vec { return append(Vec(nil), v...) }

func SameLen(a, b Vec) bool { return len(a) == len(b) }

func Add(dst, a, b Vec) (Vec, error) {
	if !SameLen(a, b) {
		return nil, ErrLength
	}
	out := resize(dst, len(a))
	for i := range a {
		out[i] = a[i] + b[i]
	}
	return out, nil
}

func Sub(dst, a, b Vec) (Vec, error) {
	if !SameLen(a, b) {
		return nil, ErrLength
	}
	out := resize(dst, len(a))
	for i := range a {
		out[i] = a[i] - b[i]
	}
	return out, nil
}

func LE(a, b Vec) (bool, error) {
	if !SameLen(a, b) {
		return false, ErrLength
	}
	for i := range a {
		if a[i] > b[i] {
			return false, nil
		}
	}
	return true, nil
}

func resize(dst Vec, n int) Vec {
	if cap(dst) < n {
		return make(Vec, n)
	}
	return dst[:n]
}

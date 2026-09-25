package hash

import "errors"

var (
	ErrInvalidBase = errors.New("hash: base must be greater than one")
	ErrInvalidMod  = errors.New("hash: mod must be greater than one")
)

type Rolling struct {
	base  int64
	mod   int64
	power int64
	size  int
	value int64
}

func New(base, mod int64, windowSize int) (*Rolling, error) {
	if base <= 1 {
		return nil, ErrInvalidBase
	}
	if mod <= 1 {
		return nil, ErrInvalidMod
	}
	if windowSize < 0 {
		windowSize = 0
	}

	power := int64(1)
	for i := 1; i < windowSize; i++ {
		power = power * base % mod
	}

	return &Rolling{base: base, mod: mod, power: power, size: windowSize}, nil
}

func (r *Rolling) Append(c byte) {
	r.value = (r.value*r.base + int64(c)) % r.mod
}

func (r *Rolling) Remove(old, next byte) {
	updated := r.value - int64(old)*r.power
	updated = updated*r.base + int64(next)
	r.value = (updated%r.mod + r.mod) % r.mod
}

func (r *Rolling) Value() int64 {
	return r.value
}

// Package hash implements a polynomial rolling hash over a sliding
// byte window: h = (h*base + c) % mod.
package hash

import "errors"

var (
	// ErrEmptyWindow is returned by Remove on an empty window.
	ErrEmptyWindow = errors.New("hash: remove from empty window")
	// ErrInvalidParam is returned by New when base or mod is < 2.
	ErrInvalidParam = errors.New("hash: base and mod must be >= 2")
)

// Rolling is a rolling hash of a FIFO byte window.
type Rolling struct {
	base uint64
	mod  uint64
	h    uint64
	pows []uint64 // pows[k] = base^k % mod, extended lazily
	n    int
}

// New creates a Rolling hash with the given base and modulus.
func New(base, mod uint64) (*Rolling, error) {
	if base < 2 || mod < 2 {
		return nil, ErrInvalidParam
	}
	return &Rolling{base: base, mod: mod, pows: []uint64{1}}, nil
}

// Append adds c at the right end of the window.
func (r *Rolling) Append(c byte) {
	r.h = (r.h*r.base + uint64(c)) % r.mod
	for len(r.pows) <= r.n {
		r.pows = append(r.pows, r.pows[len(r.pows)-1]*r.base%r.mod)
	}
	r.n++
}

// Remove drops the leftmost byte c from the window.
func (r *Rolling) Remove(c byte) error {
	if r.n == 0 {
		return ErrEmptyWindow
	}
	sub := uint64(c) * r.pows[r.n-1] % r.mod
	// Add mod before subtracting so the result never goes negative.
	r.h = (r.h + r.mod - sub) % r.mod
	r.n--
	return nil
}

// Value returns the hash of the current window.
func (r *Rolling) Value() uint64 { return r.h }

// Len returns the current window length.
func (r *Rolling) Len() int { return r.n }

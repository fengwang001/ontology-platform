// Package mhash holds the stateless hashing rules and shape parameters for
// the Merkle range-hash anti-entropy exercise. It depends on no other package.
package mhash

import "errors"

const (
	// M is the hash modulus; Q is the mixing constant. Both are fixed by spec.
	M = int64(1000000007)
	Q = int64(1000003)
	// MaxN caps the key space: F^D <= 2^20.
	MaxN = int64(1) << 20
)

// ErrInvalidParams is returned when F<2, D<1 or F^D exceeds 2^20.
var ErrInvalidParams = errors.New("mhash: invalid parameters")

// Params is the immutable shape of a key space [0, N) with N = F^D.
type Params struct {
	F int
	D int
	N int64
}

// NewParams validates F, D and the F^D bound (overflow safe) and returns
// the shape. Construction failure leaves no state behind.
func NewParams(F, D int) (Params, error) {
	if F < 2 || D < 1 {
		return Params{}, ErrInvalidParams
	}
	n := int64(1)
	for d := 0; d < D; d++ {
		n *= int64(F)
		if n > MaxN {
			return Params{}, ErrInvalidParams
		}
	}
	return Params{F: F, D: D, N: n}, nil
}

// Mod returns the non-negative residue of x modulo M.
func Mod(x int64) int64 {
	r := x % M
	if r < 0 {
		r += M
	}
	return r
}

// LeafHash is rule 2: hash of leaf [k, k+1) whose key k exists with value v.
// v is reduced to its non-negative residue before multiplication.
func LeafHash(k, v int64) int64 {
	return Mod(k*Q + Mod(v)*31 + 7)
}

// Combine is rule 3: fold F child hashes as acc=(acc*Q+h_i) mod M, acc:=17.
// A non-empty internal node always calls this even when some children hash 0.
func Combine(children []int64) int64 {
	acc := int64(17)
	for _, h := range children {
		acc = Mod(acc*Q + h)
	}
	return acc
}

// CombineNode hashes an internal node: rule 1 gives 0 when the subtree holds
// no key (every child zero), otherwise rule 3 combines the children.
func CombineNode(children []int64) int64 {
	for _, h := range children {
		if h != 0 {
			return Combine(children)
		}
	}
	return 0
}

// Child returns the i-th child range [clo, chi) of [lo, hi), split into F
// equal left-closed/right-open pieces: i=0..F-1.
// [lo+i*w/F, lo+(i+1)*w/F), w = hi-lo.
func (p Params) Child(lo, hi int64, i int) (clo, chi int64) {
	w := hi - lo
	clo = lo + int64(i)*w/int64(p.F)
	chi = lo + int64(i+1)*w/int64(p.F)
	return
}

// DepthOf returns the tree depth (D for the root, 0 for a leaf) of a range
// whose width is w. Key space width is F^D so widths are powers of F.
func (p Params) DepthOf(w int64) int {
	d := 0
	for w > 1 {
		w /= int64(p.F)
		d++
	}
	return d
}

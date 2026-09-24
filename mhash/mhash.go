// Package mhash holds the fixed hash rules for Merkle range anti-entropy:
// nonnegative modular arithmetic, leaf/combine hashing and interval splitting.
package mhash

import "errors"

// M is the hash modulus; Q is the rolling multiplier.
const (
	M int64 = 1000000007
	Q int64 = 1000003
)

// MaxN caps the key space size F^D.
const MaxN = 1 << 20

// ErrParams is the sentinel for illegal constructor parameters.
var ErrParams = errors.New("mhash: invalid parameters")

// Mod returns the nonnegative remainder of x modulo M (result in [0, M)).
func Mod(x int64) int64 {
	r := x % M
	if r < 0 {
		r += M
	}
	return r
}

// LeafHash is rule 2: hash of leaf [k,k+1) holding value v.
// v is reduced before multiplication to avoid overflow; the result is
// provably nonzero for every (k, 0) with k in [0, 2^20), so a value-0 key
// stays distinguishable from an absent key.
func LeafHash(k, v int64) int64 {
	return Mod(k*Q + Mod(v)*31 + 7)
}

// Combine is rule 3 for a NONEMPTY non-leaf interval: acc starts at 17 and
// folds the F child hashes (empty children already hash to 0) in ascending i.
// Callers must apply rule 1 (empty interval => 0) themselves.
func Combine(children []int64) int64 {
	acc := int64(17)
	for _, h := range children {
		acc = Mod(acc*Q + h)
	}
	return acc
}

// ChildRange returns the i-th left-closed, right-open child [clo, chi) of an
// interval of width w > 1 with fanout f: clo = lo + i*w/f, chi = lo +(i+1)*w/f.
func ChildRange(lo, w int64, f, i int) (clo, chi int64) {
	step := w / int64(f)
	clo = lo + int64(i)*step
	chi = lo + int64(i+1)*step
	return
}

// Validate checks F >= 2, D >= 1, F^D <= 2^20 (computed without overflow) and
// maxKeys > 0.
func Validate(f, d, maxKeys int) error {
	if f < 2 || d < 1 || maxKeys <= 0 {
		return ErrParams
	}
	n := 1
	for l := 0; l < d; l++ {
		n *= f
		if n > MaxN {
			return ErrParams
		}
	}
	return nil
}

// SpaceSize returns F^D; Validate must have passed.
func SpaceSize(f, d int) int {
	n := 1
	for l := 0; l < d; l++ {
		n *= f
	}
	return n
}

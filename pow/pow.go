// Package pow implements modular exponentiation via square-and-multiply.
package pow

import (
	"errors"
	"sync/atomic"

	"ontology/mod"
)

// Sentinel errors, mutually distinguishable via errors.Is.
var (
	ErrModZero     = errors.New("pow: modulus is zero")
	ErrModNegative = errors.New("pow: modulus is negative")
	ErrExpNegative = errors.New("pow: exponent is negative")
)

// lastMulmods records the mulmod count of the most recent PowMod call.
// Unexported: not reachable through any exported function or method.
var lastMulmods atomic.Int64

// PowMod returns base^exp mod mod, always in [0, mod).
func PowMod(base, exp, m int64) (int64, error) {
	if m == 0 {
		return 0, ErrModZero
	}
	if m < 0 {
		return 0, ErrModNegative
	}
	if exp < 0 {
		return 0, ErrExpNegative
	}
	var n int64
	b := mod.Normalize(base, m)
	r := int64(1) % m // m==1 forces every result to 0, including exp==0
	for e := exp; e > 0; e >>= 1 {
		if e&1 == 1 {
			r = mod.Mulmod(r, b, m)
			n++
		}
		if e > 1 { // skip the useless final squaring
			b = mod.Mulmod(b, b, m)
			n++
		}
	}
	lastMulmods.Store(n)
	return r, nil
}

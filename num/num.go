// Package num provides integer helpers built on egcd and the sentinel
// errors used across the module. State lives in process memory only.
package num

import (
	"errors"

	"ontology/egcd"
)

// Sentinel errors, distinguishable with errors.Is.
var (
	// ErrBadArg reports illegal input such as a < 0 or m <= 0.
	ErrBadArg = errors.New("num: bad argument")
	// ErrNoInverse reports that a has no inverse mod m (gcd(a, m) != 1).
	ErrNoInverse = errors.New("num: no modular inverse")
)

// ModInverse returns the unique v in [0, m) with a*v ≡ 1 (mod m).
// It requires a >= 0 and m > 0, otherwise ErrBadArg; when gcd(a, m) != 1
// no inverse exists and it returns ErrNoInverse. The inverse comes from
// the Bézout coefficient x of a*x + m*y = 1, reduced into [0, m).
func ModInverse(a, m int) (int, error) {
	if a < 0 || m <= 0 {
		return 0, ErrBadArg
	}
	g, x, _, _ := egcd.ExtendedGCD(a, m)
	if g != 1 {
		return 0, ErrNoInverse
	}
	v := x % m
	if v < 0 {
		v += m
	}
	return v, nil
}

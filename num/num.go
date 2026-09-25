// Package num defines the integer type used by the ontology ID
// generator, its sentinel errors, and modular arithmetic helpers
// built on egcd.
package num

import (
	"errors"

	"ontology/egcd"
)

// Int is the integer type used for IDs and modular arithmetic.
type Int = int

// Sentinel errors, distinguishable with errors.Is.
var (
	// ErrBadArg reports illegal input such as a < 0 or m <= 0.
	ErrBadArg = errors.New("num: bad argument")
	// ErrNoInverse reports that a has no inverse mod m (gcd(a, m) != 1).
	ErrNoInverse = errors.New("num: inverse does not exist")
)

// ModInverse returns the inverse of a modulo m, normalized to [0, m).
// It requires a >= 0 and m > 0, and that gcd(a, m) == 1.
func ModInverse(a, m Int) (Int, error) {
	if a < 0 || m <= 0 {
		return 0, ErrBadArg
	}
	g, x, _, err := egcd.ExtendedGCD(a, m)
	if err != nil {
		return 0, err
	}
	if g != 1 {
		return 0, ErrNoInverse
	}
	// a*x + m*y == 1, so x is the inverse; normalize into [0, m).
	r := x % m
	if r < 0 {
		r += m
	}
	return r, nil
}

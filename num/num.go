// Package num defines the integer type of the ontology ID space and the
// sentinel errors for modular arithmetic built on egcd.
package num

import (
	"errors"

	"ontology/egcd"
)

// Int is the integer type of the ID space (64-bit on supported platforms).
type Int = int

var (
	// ErrBadArg marks illegal arguments (a < 0 or m <= 0).
	ErrBadArg = errors.New("num: bad argument")
	// ErrNoInverse marks a non-invertible element (gcd(a, m) != 1).
	ErrNoInverse = errors.New("num: inverse does not exist")
)

// ModInverse returns the inverse of a modulo m, normalized to [0, m).
// It exists iff gcd(a, m) == 1; then a*x + m*y = 1 makes x the inverse.
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
	r := x % m
	if r < 0 {
		r += m
	}
	return r, nil
}

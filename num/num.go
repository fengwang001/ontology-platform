// Package num provides integer helpers built on egcd.
package num

import (
	"errors"

	"ontology/egcd"
)

// Sentinel errors, distinguishable via errors.Is.
var (
	ErrBadArg    = errors.New("num: bad argument")
	ErrNoInverse = errors.New("num: inverse does not exist")
)

// ModInverse returns the inverse of a modulo m: the unique r in
// [0, m) with a*r == 1 (mod m). It reports ErrBadArg for a < 0 or
// m <= 0, and ErrNoInverse when gcd(a, m) != 1.
func ModInverse(a, m int) (int, error) {
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

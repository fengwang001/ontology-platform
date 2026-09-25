// Package check holds naive reference implementations (enumeration and
// math/big cross-checks) plus all tests of the module.
package check

import (
	"math/big"

	"ontology/egcd"
)

// NaiveGCD computes gcd(a, b) >= 0 by enumerating candidate divisors
// downward. Only suitable for small inputs; gcd(0, 0) = 0.
func NaiveGCD(a, b int) int {
	a, b = abs(a), abs(b)
	if a < b {
		a, b = b, a
	}
	for d := b; d >= 1; d-- {
		if a%d == 0 && b%d == 0 {
			return d
		}
	}
	return a // b == 0: gcd is a (0 when a == 0 as well)
}

// BigGCD cross-checks gcd(a, b) via math/big (always non-negative).
func BigGCD(a, b int) int {
	g := new(big.Int).GCD(nil, nil, big.NewInt(int64(a)), big.NewInt(int64(b)))
	return int(g.Int64())
}

// VerifyBezout runs egcd.ExtendedGCD on (a, b) and reports whether the
// returned g, x, y satisfy a*x + b*y = g with g = gcd(a, b) >= 0.
func VerifyBezout(a, b int) bool {
	g, x, y, err := egcd.ExtendedGCD(a, b)
	return err == nil && g >= 0 && a*x+b*y == g && g == BigGCD(a, b)
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

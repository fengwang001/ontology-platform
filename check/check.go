// Package check holds naive references for cross-checking egcd and num.
package check

import "math/big"

// NaiveGCD computes gcd(a, b) >= 0 by enumerating divisors downward.
func NaiveGCD(a, b int) int {
	if a < 0 {
		a = -a
	}
	if b < 0 {
		b = -b
	}
	if a == 0 {
		return b
	}
	if b == 0 {
		return a
	}
	lo := a
	if b < lo {
		lo = b
	}
	for d := lo; d > 1; d-- {
		if a%d == 0 && b%d == 0 {
			return d
		}
	}
	return 1
}

// BigBezout cross-checks via math/big: g = gcd(a, b) >= 0 with
// a*x + b*y == g. Inputs must be non-negative.
func BigBezout(a, b int) (g, x, y int) {
	bigG, bigX, bigY := new(big.Int), new(big.Int), new(big.Int)
	bigG.GCD(bigX, bigY, big.NewInt(int64(a)), big.NewInt(int64(b)))
	return int(bigG.Int64()), int(bigX.Int64()), int(bigY.Int64())
}

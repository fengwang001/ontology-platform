// Package check holds naive references (math/big cross-checks) used by
// the tests and the demo to validate the egcd and num packages.
package check

import "math/big"

// RefGCD is the reference gcd via math/big. It is always >= 0 and
// RefGCD(0, 0) == 0, matching the contract of egcd.ExtendedGCD.
func RefGCD(a, b int) int {
	z := new(big.Int).GCD(nil, nil, big.NewInt(int64(a)), big.NewInt(int64(b)))
	return int(z.Int64())
}

// BezoutOK reports whether a*x + b*y == g. The arithmetic uses big.Int so
// the check itself cannot overflow for inputs up to 1e18.
func BezoutOK(a, b, x, y, g int) bool {
	lhs := new(big.Int).Add(
		new(big.Int).Mul(big.NewInt(int64(a)), big.NewInt(int64(x))),
		new(big.Int).Mul(big.NewInt(int64(b)), big.NewInt(int64(y))),
	)
	return lhs.Cmp(big.NewInt(int64(g))) == 0
}

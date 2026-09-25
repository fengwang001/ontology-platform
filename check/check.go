// Package check holds naive reference implementations (via math/big)
// used to cross-verify egcd and num in tests.
package check

import "math/big"

// RefGCD is the reference: gcd and Bezout coefficients from math/big.
func RefGCD(a, b int) (g, x, y int) {
	bg, bx, by := new(big.Int), new(big.Int), new(big.Int)
	bg.GCD(bx, by, big.NewInt(int64(a)), big.NewInt(int64(b)))
	return int(bg.Int64()), int(bx.Int64()), int(by.Int64())
}

// BezoutOK reports whether a*x + b*y == g, computed with big.Int so
// that 1e18-scale inputs cannot overflow int64 intermediate products.
func BezoutOK(a, b, x, y, g int) bool {
	ba, bb := big.NewInt(int64(a)), big.NewInt(int64(b))
	lhs := new(big.Int).Add(
		new(big.Int).Mul(ba, big.NewInt(int64(x))),
		new(big.Int).Mul(bb, big.NewInt(int64(y))),
	)
	return lhs.Cmp(big.NewInt(int64(g))) == 0
}

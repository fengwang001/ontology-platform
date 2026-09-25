// Package egcd implements the extended Euclidean algorithm: given a, b it
// finds g = gcd(a, b) >= 0 and coefficients x, y with a*x + b*y = g.
package egcd

import (
	"errors"
	"math/bits"
)

// errDepth reports an internal bug: the recursion depth counter exceeded
// the proven logarithmic upper bound 2*log2(max(a,b))+2.
var errDepth = errors.New("egcd: recursion depth above logarithmic bound")

// ExtendedGCD returns g = gcd(a, b) (non-negative) and a deterministic
// pair x, y satisfying a*x + b*y = g. It is a pure function and safe for
// concurrent use. Inputs are assumed within int64 range with |a|,|b| <= 1e18
// so the back-substitution products cannot overflow.
func ExtendedGCD(a, b int) (g, x, y int, err error) {
	depth := 0
	g, x, y = extended(abs(a), abs(b), &depth)
	if a < 0 {
		x = -x
	}
	if b < 0 {
		y = -y
	}
	if limit := 2*bits.Len(uint(max(abs(a), abs(b)))) + 2; depth > limit {
		return 0, 0, 0, errDepth
	}
	return g, x, y, nil
}

// extended solves a*x + b*y = gcd(a, b) for a, b >= 0, counting recursion
// levels in *depth. Back-substitution: from b*x' + (a mod b)*y' = g and
// a mod b = a - (a/b)*b follows x = y', y = x' - (a/b)*y'.
func extended(a, b int, depth *int) (g, x, y int) {
	*depth++
	if b == 0 {
		return a, 1, 0
	}
	g, x1, y1 := extended(b, a%b, depth)
	return g, y1, x1 - (a/b)*y1
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

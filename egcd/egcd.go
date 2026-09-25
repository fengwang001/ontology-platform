// Package egcd implements the extended Euclidean algorithm: given a, b it
// computes g = gcd(a, b) (non-negative) and Bézout coefficients x, y with
// a*x + b*y = g. All state lives in process memory; functions are pure.
package egcd

// ExtendedGCD returns g = gcd(a, b) >= 0 and x, y with a*x + b*y = g.
// err is always nil today and reserved for future input validation.
// Deterministic: the same (a, b) always yields the same (x, y).
func ExtendedGCD(a, b int) (g, x, y int, err error) {
	var depth int
	g, x, y = egcd(abs(a), abs(b), &depth)
	if a < 0 {
		x = -x
	}
	if b < 0 {
		y = -y
	}
	return g, x, y, nil
}

// RecursionDepth reports how many recursion levels egcd needs for (a, b).
// It exists so tests can assert the logarithmic bound 2*log2(max(a,b))+2.
func RecursionDepth(a, b int) int {
	var depth int
	egcd(abs(a), abs(b), &depth)
	return depth
}

// egcd recurses on non-negative a, b. depth is a non-exported counter of
// recursion levels, threaded by pointer so ExtendedGCD stays a pure
// function with no shared state (safe under -race and concurrency).
//
// Back-substitution: with q = a/b and r = a%b the recursive call returns
// x1, y1 with b*x1 + r*y1 = g. Since r = a - b*q,
//
//	g = b*x1 + (a-b*q)*y1 = a*y1 + b*(x1-q*y1)
//
// hence x = y1 and y = x1 - (a/b)*y1. Swapping x/y or dropping the
// (a/b) term breaks the Bézout identity (see NOTES.md).
func egcd(a, b int, depth *int) (g, x, y int) {
	*depth++
	if b == 0 {
		return a, 1, 0
	}
	g, x1, y1 := egcd(b, a%b, depth)
	return g, y1, x1-(a/b)*y1
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

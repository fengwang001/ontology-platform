// Package egcd implements the extended Euclidean algorithm.
package egcd

// ExtendedGCD returns g = gcd(a, b) (non-negative) and Bezout
// coefficients x, y with a*x + b*y == g. It is a pure function:
// no shared state, safe for concurrent use.
func ExtendedGCD(a, b int) (g, x, y int, err error) {
	sa, sb := 1, 1
	if a < 0 {
		a, sa = -a, -1
	}
	if b < 0 {
		b, sb = -b, -1
	}
	g, x, y, _ = solve(a, b)
	return g, sa * x, sb * y, nil
}

// Depth reports the recursion levels ExtendedGCD needs for (a, b).
func Depth(a, b int) int {
	if a < 0 {
		a = -a
	}
	if b < 0 {
		b = -b
	}
	_, _, _, d := solve(a, b)
	return d
}

// solve runs the recursion on non-negative a, b. Back-substitution:
// from b*x' + (a mod b)*y' = g and a mod b = a - (a/b)*b we get
// x = y', y = x' - (a/b)*y'. depth is the recursion-level counter.
func solve(a, b int) (g, x, y, depth int) {
	if b == 0 {
		return a, 1, 0, 1
	}
	g, x1, y1, d := solve(b, a%b)
	return g, y1, x1 - (a/b)*y1, d + 1
}

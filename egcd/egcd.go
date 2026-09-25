// Package egcd implements the extended Euclidean algorithm:
// given a, b it returns g = gcd(a, b) (non-negative) and
// Bezout coefficients x, y such that a*x + b*y == g.
package egcd

import "sync/atomic"

// maxDepth is the unexported recursion-depth counter: it records the
// deepest recursion observed so far. For |a|, |b| <= 1e18 it stays
// below 2*log2(max(|a|,|b|))+2, matching the logarithmic convergence
// of the Euclidean algorithm. Atomic, so concurrent calls stay
// race-free and never affect each other's results.
var maxDepth atomic.Int64

// MaxDepth reports the deepest recursion observed so far.
func MaxDepth() int64 { return maxDepth.Load() }

// ExtendedGCD returns g = gcd(a, b) (non-negative) and coefficients
// x, y with a*x + b*y == g. gcd(0, 0) = 0. err is always nil; the
// signature keeps room for callers that chain error returns.
func ExtendedGCD(a, b int) (g, x, y int, err error) {
	ua, sa := mag(a)
	ub, sb := mag(b)
	g, x, y = extend(ua, ub, 1)
	return g, sa * x, sb * y, nil
}

// mag returns the absolute value of v and its sign (+-1, 0 for 0).
func mag(v int) (abs, sign int) {
	switch {
	case v < 0:
		return -v, -1
	case v > 0:
		return v, 1
	}
	return 0, 0
}

// extend runs the recursion on non-negative inputs. Back-substitution:
// from b*x' + (a mod b)*y' = g and a mod b = a - (a/b)*b we get
// x = y' and y = x' - (a/b)*y'. Swapping x'/y' or dropping the (a/b)
// term breaks the identity (see NOTES.md).
func extend(a, b, depth int) (g, x, y int) {
	track(depth)
	if b == 0 {
		return a, 1, 0
	}
	g, x1, y1 := extend(b, a%b, depth+1)
	return g, y1, x1 - (a/b)*y1
}

// track records the deepest recursion level seen so far.
func track(depth int) {
	for {
		cur := maxDepth.Load()
		if int64(depth) <= cur || maxDepth.CompareAndSwap(cur, int64(depth)) {
			return
		}
	}
}

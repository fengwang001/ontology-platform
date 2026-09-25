// Package mod provides overflow-safe modular arithmetic primitives.
package mod

// Normalize maps v into [0, m). m must be positive.
// Written as a conditional add (not ((v%m)+m)%m) so that
// v%m close to m cannot overflow int64.
func Normalize(v, m int64) int64 {
	r := v % m
	if r < 0 {
		r += m
	}
	return r
}

// Mulmod returns a*b mod m without overflowing int64, using
// double-and-add on uint64. a, b must already lie in [0, m), m > 0.
// Since a, b < m <= 2^63-1, res+a and 2*a stay below 2^64-2 in uint64.
func Mulmod(a, b, m int64) int64 {
	ua, ub, um := uint64(a), uint64(b), uint64(m)
	var res uint64
	for ub > 0 {
		if ub&1 == 1 {
			res = (res + ua) % um
		}
		ua = (ua * 2) % um
		ub >>= 1
	}
	return int64(res)
}

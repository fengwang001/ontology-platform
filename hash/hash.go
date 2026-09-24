// Package hash computes double-hashing probe positions for the counting
// bloom filter and validates the constructor parameters.
package hash

import "errors"

// ErrInvalidParams reports an illegal (m, k, maxCount) combination:
// m not prime, m <= k, k < 1, or maxCount outside 1..255.
var ErrInvalidParams = errors.New("hash: invalid parameters")

// IsPrime reports whether n is a prime number.
func IsPrime(n int) bool {
	if n < 2 {
		return false
	}
	if n%2 == 0 {
		return n == 2
	}
	for d := 3; d*d <= n; d += 2 {
		if n%d == 0 {
			return false
		}
	}
	return true
}

// Validate checks the constructor contract: m prime, m > k, k >= 1,
// maxCount in 1..255 (uint8 already bounds it above).
func Validate(m, k int, maxCount uint8) error {
	if k < 1 || m <= k || !IsPrime(m) || maxCount < 1 {
		return ErrInvalidParams
	}
	return nil
}

// Positions returns the k distinct probe positions for key x:
// h1 = x mod m, h2 = 1 + (x mod (m-1)), p_i = (h1 + i*h2) mod m.
// m is prime and h2 is coprime with m, so the positions are pairwise
// distinct. Negative x is normalized into [0, m).
func Positions(x int64, m, k int) []int {
	mm := int64(m)
	h1 := x % mm
	if h1 < 0 {
		h1 += mm
	}
	r := x % (mm - 1)
	if r < 0 {
		r += mm - 1
	}
	h2 := 1 + r
	pos := make([]int, k)
	p := h1
	for i := range pos {
		pos[i] = int(p)
		p = (p + h2) % mm
	}
	return pos
}

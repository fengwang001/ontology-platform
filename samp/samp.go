// Package samp computes systematic-sampling indices: given a sorted
// population of N elements, a sample size s, and an offset r, it yields
// d = N/s (real, not rounded) and index_i = floor(r + i*d) for i = 0..s-1.
// It depends on no other package in this module.
package samp

import "math"

// Spacing returns the real-valued sampling interval d = N/s.
func Spacing(n, s int) float64 {
	return float64(n) / float64(s)
}

// Indices returns exactly the s indices floor(r + i*d), i = 0..s-1,
// computed term by term exactly like the naive reference.
// Precondition (enforced by the api package): 1 <= s <= n and 0 <= r < d.
func Indices(n, s int, r float64) []int {
	d := Spacing(n, s)
	out := make([]int, 0, s)
	for i := 0; i < s; i++ {
		out = append(out, int(math.Floor(r+float64(i)*d)))
	}
	return out
}

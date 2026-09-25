// Package check holds a naive reference implementation used by tests.
package check

// Naive is a plain-array reference with the same 0-based semantics as bit.
type Naive struct {
	v []int64
}

// NewNaive creates a reference of n zero-valued slots.
func NewNaive(n int) *Naive { return &Naive{v: make([]int64, n)} }

// Add adds delta to slot i.
func (s *Naive) Add(i int, delta int64) { s.v[i] += delta }

// PrefixSum returns the sum of slots 0..i; PrefixSum(-1) returns 0.
func (s *Naive) PrefixSum(i int) int64 {
	var sum int64
	for k := 0; k <= i; k++ {
		sum += s.v[k]
	}
	return sum
}

// RangeSum returns the closed-interval sum [l, r].
func (s *Naive) RangeSum(l, r int) int64 {
	var sum int64
	for k := l; k <= r; k++ {
		sum += s.v[k]
	}
	return sum
}

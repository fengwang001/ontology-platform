// Package check holds the naive array-based reference implementation
// that the Fenwick tree is tested against.
package check

import "ontology/bit"

// Naive is a plain array; every query is an O(n) accumulation.
type Naive struct {
	a []int64
}

// NewNaive creates a reference counter with n slots.
func NewNaive(n int) *Naive { return &Naive{a: make([]int64, n)} }

// Add adds delta at position i.
func (s *Naive) Add(i int, delta int64) error {
	if i < 0 || i >= len(s.a) {
		return bit.ErrBadIndex
	}
	s.a[i] += delta
	return nil
}

// PrefixSum returns the sum over [0, i]; PrefixSum(-1) returns 0.
func (s *Naive) PrefixSum(i int) (int64, error) {
	if i < -1 || i >= len(s.a) {
		return 0, bit.ErrBadIndex
	}
	var sum int64
	for j := 0; j <= i; j++ {
		sum += s.a[j]
	}
	return sum, nil
}

// RangeSum returns the closed-interval sum over [l, r].
func (s *Naive) RangeSum(l, r int) (int64, error) {
	if l > r {
		return 0, bit.ErrBadRange
	}
	var sum int64
	for j := l; j <= r; j++ {
		sum += s.a[j]
	}
	return sum, nil
}

// Package fen implements a Fenwick tree (binary indexed tree) over a
// fixed-size array indexed [0, n): point updates and prefix queries,
// each in O(log n). Every mutating/querying method reports how many
// tree nodes it touched, so callers can bound incremental maintenance
// cost without scanning.
package fen

// Tree is a Fenwick tree over n int64 cells, all zero at construction.
type Tree struct {
	n   int
	bit []int64 // 1-indexed internal storage
}

// New returns a zeroed tree of size n. n must be positive.
func New(n int) *Tree {
	if n < 1 {
		panic("fen: non-positive size")
	}
	return &Tree{n: n, bit: make([]int64, n+1)}
}

// Add adds d to the cell at index i (0 <= i < n) and returns the
// number of tree nodes visited.
func (t *Tree) Add(i int, d int64) int {
	visits := 0
	for x := i + 1; x <= t.n; x += x & -x {
		t.bit[x] += d
		visits++
	}
	return visits
}

// Sum returns the cumulative sum over indices [0, i]. An i below 0
// yields 0; an i beyond n-1 is clamped to n-1. It also returns the
// number of tree nodes visited.
func (t *Tree) Sum(i int) (int64, int) {
	if i < 0 {
		return 0, 0
	}
	if i >= t.n {
		i = t.n - 1
	}
	var s int64
	visits := 0
	for x := i + 1; x > 0; x -= x & -x {
		s += t.bit[x]
		visits++
	}
	return s, visits
}

// Package bit implements a Fenwick tree (binary indexed tree).
// The public API is 0-based; the internal tree is 1-based so that
// lowbit jumps never land on index 0 (see NOTES.md).
package bit

import (
	"errors"
	"sync"
	"sync/atomic"
)

var (
	ErrBadIndex = errors.New("bit: index out of range")
	ErrBadSize  = errors.New("bit: negative size")
	ErrBadRange = errors.New("bit: invalid range l > r")
)

// Tree is a Fenwick tree of length n slots indexed 0..n-1.
type Tree struct {
	tree []int64
	mu   sync.RWMutex
	// visited counts nodes touched by the most recent Add/PrefixSum.
	visited atomic.Int64
}

// New creates a tree with n zero-valued slots. n == 0 is valid.
func New(n int) (*Tree, error) {
	if n < 0 {
		return nil, ErrBadSize
	}
	return &Tree{tree: make([]int64, n+1)}, nil
}

// Visited reports nodes touched by the last Add or PrefixSum.
func (t *Tree) Visited() int64 { return t.visited.Load() }

// Add adds delta to slot i.
func (t *Tree) Add(i int, delta int64) error {
	if i < 0 || i >= len(t.tree)-1 {
		return ErrBadIndex
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	var n int64
	for j := int64(i + 1); j < int64(len(t.tree)); j += j & -j {
		t.tree[j] += delta
		n++
	}
	t.visited.Store(n)
	return nil
}

// PrefixSum returns the sum of slots 0..i. PrefixSum(-1) returns 0.
func (t *Tree) PrefixSum(i int) (int64, error) {
	if i < -1 || i >= len(t.tree)-1 {
		return 0, ErrBadIndex
	}
	if i == -1 {
		t.visited.Store(0)
		return 0, nil
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	var sum, n int64
	for j := int64(i + 1); j > 0; j -= j & -j {
		sum += t.tree[j]
		n++
	}
	t.visited.Store(n)
	return sum, nil
}

// RangeSum returns the closed-interval sum [l, r].
func (t *Tree) RangeSum(l, r int) (int64, error) {
	if l < 0 || r < 0 || l >= len(t.tree)-1 || r >= len(t.tree)-1 {
		return 0, ErrBadIndex
	}
	if l > r {
		return 0, ErrBadRange
	}
	t.mu.RLock()
	sum := func(i int) int64 {
		var s int64
		for j := int64(i + 1); j > 0; j -= j & -j {
			s += t.tree[j]
		}
		return s
	}
	v := sum(r) - sum(l-1)
	t.mu.RUnlock()
	return v, nil
}

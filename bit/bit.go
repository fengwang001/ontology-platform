// Package bit implements a Fenwick tree (binary indexed tree).
//
// The public API is zero-based; internally every index is shifted by +1 so
// the Fenwick loops run over one-based positions. See NOTES.md for why the
// shift is mandatory (lowbit(0) == 0 never advances the update loop).
package bit

import (
	"errors"
	"sync"
	"sync/atomic"
)

var (
	// ErrBadIndex is returned for an index outside [0, n-1].
	ErrBadIndex = errors.New("bit: index out of range")
	// ErrBadSize is returned when New receives a negative size.
	ErrBadSize = errors.New("bit: negative size")
	// ErrBadRange is returned by RangeSum when l > r.
	ErrBadRange = errors.New("bit: l > r")
)

// Tree is a zero-based Fenwick tree of length n.
type Tree struct {
	n     int
	tree  []int64
	mu    sync.RWMutex
	nodes atomic.Int64 // nodes touched by the last Add/PrefixSum call
}

// New creates a tree holding n slots. n == 0 is valid.
func New(n int) (*Tree, error) {
	if n < 0 {
		return nil, ErrBadSize
	}
	return &Tree{n: n, tree: make([]int64, n+1)}, nil
}

// Add adds delta to position i (zero-based).
func (t *Tree) Add(i int, delta int64) error {
	if i < 0 || i >= t.n {
		return ErrBadIndex
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	visited := 0
	for j := i + 1; j <= t.n; j += j & -j {
		t.tree[j] += delta
		visited++
	}
	t.nodes.Store(int64(visited))
	return nil
}

// PrefixSum returns the sum over positions [0, i].
// PrefixSum(-1) returns 0 (the empty prefix); i < -1 reports ErrBadIndex.
func (t *Tree) PrefixSum(i int) (int64, error) {
	if i < -1 || i >= t.n {
		return 0, ErrBadIndex
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	var sum int64
	visited := 0
	for j := i + 1; j > 0; j -= j & -j {
		sum += t.tree[j]
		visited++
	}
	t.nodes.Store(int64(visited))
	return sum, nil
}

// RangeSum returns the closed-interval sum over [l, r].
func (t *Tree) RangeSum(l, r int) (int64, error) {
	if l > r {
		return 0, ErrBadRange
	}
	hi, err := t.PrefixSum(r)
	if err != nil {
		return 0, err
	}
	lo, err := t.PrefixSum(l - 1)
	if err != nil {
		return 0, err
	}
	return hi - lo, nil
}

// Len returns the number of slots.
func (t *Tree) Len() int { return t.n }

// LastNodes returns the number of nodes touched by the last Add/PrefixSum.
func (t *Tree) LastNodes() int64 { return t.nodes.Load() }

// Package segtree implements a lazy-propagation segment tree (range add, range sum).
package segtree

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
)

// Sentinel errors, distinguishable with errors.Is; the latter two wrap ErrBadRange.
var (
	ErrBadRange    = errors.New("segtree: bad range")
	ErrOutOfBounds = fmt.Errorf("%w: index out of bounds", ErrBadRange)
	ErrReversed    = fmt.Errorf("%w: l > r", ErrBadRange)
)

// Tree is a lazy segment tree over [0, n); query pushdown mutates, so ops take mu.
type Tree struct {
	mu        sync.Mutex
	n         int
	sum, lazy []int64
	last      atomic.Int64 // nodes visited by the most recent operation
}

func New(n int) *Tree {
	if n <= 0 {
		panic("segtree: New requires n > 0")
	}
	return &Tree{n: n, sum: make([]int64, 4*n), lazy: make([]int64, 4*n)}
}
func (t *Tree) LastVisited() int64 { return t.last.Load() }
func (t *Tree) run(l, r int, apply func(*int64) int64) (int64, error) {
	if l > r {
		return 0, ErrReversed
	}
	if l < 0 || r >= t.n {
		return 0, ErrOutOfBounds
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	var cnt int64
	v := apply(&cnt)
	t.last.Store(cnt)
	return v, nil
}
func (t *Tree) AddRange(l, r int, delta int64) error {
	_, err := t.run(l, r, func(c *int64) int64 { t.add(1, 0, t.n-1, l, r, delta, c); return 0 })
	return err
}
func (t *Tree) SumRange(l, r int) (int64, error) {
	return t.run(l, r, func(c *int64) int64 { return t.query(1, 0, t.n-1, l, r, c) })
}
func (t *Tree) pushdown(i, nl, nr int) {
	d := t.lazy[i]
	if d == 0 {
		return
	}
	m := (nl + nr) / 2
	t.sum[2*i] += int64(m-nl+1) * d
	t.lazy[2*i] += d
	t.sum[2*i+1] += int64(nr-m) * d
	t.lazy[2*i+1] += d
	t.lazy[i] = 0
}
func (t *Tree) add(i, nl, nr, l, r int, d int64, cnt *int64) {
	*cnt++
	if l <= nl && nr <= r {
		t.sum[i] += int64(nr-nl+1) * d
		t.lazy[i] += d
		return
	}
	t.pushdown(i, nl, nr)
	m := (nl + nr) / 2
	if l <= m {
		t.add(2*i, nl, m, l, r, d, cnt)
	}
	if r > m {
		t.add(2*i+1, m+1, nr, l, r, d, cnt)
	}
	t.sum[i] = t.sum[2*i] + t.sum[2*i+1]
}

// query pushdowns before descending so children never serve stale sums.
func (t *Tree) query(i, nl, nr, l, r int, cnt *int64) int64 {
	*cnt++
	if l <= nl && nr <= r {
		return t.sum[i]
	}
	t.pushdown(i, nl, nr)
	m := (nl + nr) / 2
	var s int64
	if l <= m {
		s += t.query(2*i, nl, m, l, r, cnt)
	}
	if r > m {
		s += t.query(2*i+1, m+1, nr, l, r, cnt)
	}
	return s
}

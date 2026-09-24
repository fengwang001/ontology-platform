// Package qnt is a multiset quantile engine backed by an order-statistic
// tree. It keeps an unexported counter of tree nodes visited by the most
// recent Median/QuantileP90, readable only from same-package tests.
package qnt

import (
	"errors"
	"sync/atomic"

	"ontology/ost"
)

// ErrEmpty reports a median/quantile query on an empty multiset.
var ErrEmpty = errors.New("qnt: empty multiset")

// Engine maintains a multiset of int64 and serves exact quantiles.
// The zero value is ready to use. It is not goroutine-safe; callers
// needing concurrency must serialize access (see package api).
type Engine struct {
	t          ost.Tree
	lastVisits atomic.Int64 // nodes touched by the latest Median/QuantileP90
}

// Insert adds one occurrence of v.
func (e *Engine) Insert(v int64) { e.t.Insert(v) }

// Delete removes exactly one occurrence of v.
func (e *Engine) Delete(v int64) error { return e.t.Delete(v) }

// Count returns the number of elements (with multiplicity).
func (e *Engine) Count() int { return e.t.Count() }

// Kth returns the k-th smallest value (1-based).
func (e *Engine) Kth(k int) (int64, error) {
	v, _, err := e.t.Kth(k)
	return v, err
}

func (e *Engine) kthCounting(k int) (int64, error) {
	v, vis, err := e.t.Kth(k)
	e.lastVisits.Add(int64(vis))
	return v, err
}

// Median returns the exact p50: the middle value for odd n, the
// arithmetic mean of the two middle values for even n.
func (e *Engine) Median() (float64, error) {
	n := e.t.Count()
	e.lastVisits.Store(0)
	if n == 0 {
		return 0, ErrEmpty
	}
	if n%2 == 1 {
		v, err := e.kthCounting((n + 1) / 2)
		return float64(v), err
	}
	a, err := e.kthCounting(n / 2)
	if err != nil {
		return 0, err
	}
	b, err := e.kthCounting(n/2 + 1)
	if err != nil {
		return 0, err
	}
	return float64(a)/2 + float64(b)/2, nil
}

// QuantileP90 returns the exact p90 by the nearest-rank method:
// rank = ceil(90*n/100) = (90*n+99)/100.
func (e *Engine) QuantileP90() (int64, error) {
	n := e.t.Count()
	e.lastVisits.Store(0)
	if n == 0 {
		return 0, ErrEmpty
	}
	return e.kthCounting((90*n + 99) / 100)
}

// VisitBoundOK builds fresh engines of several sizes, runs one Median on
// each, and reports whether the visited-node count stays within
// 2*ceil(log2 m)+2 (and below 64) — i.e. a height-bounded descent, not a
// full scan. It exposes only a verdict, never the counter itself.
func VisitBoundOK() bool {
	for _, m := range []int{101, 1001, 9999} {
		e := &Engine{}
		for i := 0; i < m; i++ {
			e.Insert(int64(i)*2 + 1)
		}
		if _, err := e.Median(); err != nil {
			return false
		}
		ceilLog2 := 0
		for x := 1; x < m; x <<= 1 {
			ceilLog2++
		}
		v := e.lastVisits.Load()
		if v > int64(2*ceilLog2+2) || v >= 64 {
			return false
		}
	}
	return true
}

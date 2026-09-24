// Package qnt is the multiset quantile engine: exact median (p50) and p90
// over an int64 multiset, backed by package ost. It depends only on ost.
package qnt

import (
	"errors"
	"sync/atomic"

	"ontology/ost"
)

// ErrEmpty is returned by Median/QuantileP90 when the multiset is empty.
// It is distinct from ErrAbsent and from the api-layer sentinels.
var ErrEmpty = errors.New("qnt: multiset is empty")

// ErrAbsent is the delete-missing-value sentinel (re-exported from ost so
// callers of qnt never need to import ost).
var ErrAbsent = ost.ErrAbsent

// Engine keeps an int64 multiset and answers exact order statistics.
// The zero value is ready to use.
type Engine struct {
	tree ost.Tree

	// visits counts tree nodes visited by the most recent median/p90
	// descent. It is atomic so that concurrent read-only queries (which hold
	// only a shared lock at the api layer) never race on it. Unexported on
	// purpose: read only by in-package tests, never in a public signature.
	visits atomic.Int64
}

// New returns an empty engine.
func New() *Engine { return &Engine{} }

// kth performs one descent, counting the visited nodes in a local (so the
// descent itself touches no shared state), then publishes the count once.
func (e *Engine) kth(k int) (int64, error) {
	n := 0
	v, err := e.tree.KthInto(k, func() { n++ })
	e.visits.Store(int64(n))
	return v, err
}

// Insert adds one occurrence of v.
func (e *Engine) Insert(v int64) { e.tree.Insert(v) }

// Delete removes exactly one occurrence of v. Deleting a value with no
// occurrence is ost.ErrAbsent and leaves the multiset unchanged.
func (e *Engine) Delete(v int64) error { return e.tree.Delete(v) }

// Count is the multiset size: number of inserts minus number of deletes.
func (e *Engine) Count() int { return e.tree.Count() }

// Kth returns the k-th smallest value (1-based, duplicates counted).
func (e *Engine) Kth(k int) (int64, error) { return e.tree.Kth(k) }

// Median is the exact p50: for odd n the middle value; for even n the
// arithmetic mean of the two middle values (so it may end in .5).
func (e *Engine) Median() (float64, error) {
	n := e.tree.Count()
	if n == 0 {
		return 0, ErrEmpty
	}
	if n%2 == 1 {
		v, err := e.kth((n + 1) / 2)
		return float64(v), err
	}
	lo, err := e.kth(n / 2)
	if err != nil {
		return 0, err
	}
	hi, err := e.kth(n/2 + 1)
	if err != nil {
		return 0, err
	}
	// Convert before adding: lo+hi as int64 could overflow the value range.
	return (float64(lo) + float64(hi)) / 2, nil
}

// QuantileP90 uses the nearest-rank rule: rank = ceil(90*n/100).
func (e *Engine) QuantileP90() (int64, error) {
	n := e.tree.Count()
	if n == 0 {
		return 0, ErrEmpty
	}
	rank := (90*n + 99) / 100
	return e.kth(rank)
}

// CheckLogDescent reports whether a single Median descent over m distinct
// values stays within 2*ceil(log2 m)+2 visited nodes (and below 64) at every
// tested size. It exposes only pass/fail: the unexported visit counter never
// appears in any public signature or return value.
func CheckLogDescent() error {
	for _, m := range []int{100, 1000, 10000} {
		e := New()
		for v := 0; v < m; v++ {
			e.Insert(int64(v))
		}
		if _, err := e.Median(); err != nil {
			return err
		}
		bound := 2*ceilLog2(m) + 2
		if v := int(e.visits.Load()); v > bound || v >= 64 {
			return errors.New("qnt: median descent is not logarithmic")
		}
	}
	return nil
}

func ceilLog2(m int) int {
	k, p := 0, 1
	for p < m {
		p, k = p<<1, k+1
	}
	return k
}

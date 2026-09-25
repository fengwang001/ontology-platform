// Package inj implements the index nested-loop join: it scans the outer
// relation R in order and probes the idx index independently for every
// tuple, expanding the equality interval into pairs. Depends on idx only.
package inj

import (
	"errors"
	"sync/atomic"

	"ontology/idx"
)

// ErrNoIndex reports a Probe before any successful index build.
var ErrNoIndex = errors.New("inj: probe before BuildIndex")

// Pair is one join result tuple (r, s) with r.Key == s.Key.
type Pair struct{ R, S int }

// Joiner holds the inner-relation index. The index pointer is atomic so
// concurrent read-only Probes are race-free; cmps is a non-exported
// counter of index entries compared while locating the most recent key.
type Joiner struct {
	index atomic.Pointer[idx.Index]
	cmps  atomic.Int64
}

// NewJoiner returns an empty joiner (no index yet).
func NewJoiner() *Joiner { return &Joiner{} }

// SetIndex installs a successfully built index.
func (j *Joiner) SetIndex(x *idx.Index) { j.index.Store(x) }

// Probe validates the whole batch first (nil slice, negative keys), then
// probes the index independently for every tuple of R — each key is
// located by a fresh binary search from the index origin, never by
// reusing a cursor — and expands each match interval [lo, hi).
func (j *Joiner) Probe(r []int) ([]Pair, error) {
	if r == nil {
		return nil, idx.ErrNilInput
	}
	for _, k := range r {
		if k < 0 {
			return nil, idx.ErrNegativeKey
		}
	}
	x := j.index.Load()
	if x == nil {
		return nil, ErrNoIndex
	}
	var out []Pair
	for _, k := range r {
		j.cmps.Store(0)
		lo, hi := x.Bounds(k, func(int) { j.cmps.Add(1) })
		for i := lo; i < hi; i++ {
			out = append(out, Pair{R: k, S: x.At(i)})
		}
	}
	return out, nil
}

// CheckCost builds fresh indexes of several sizes and verifies that the
// locating comparisons per probe stay within ceil(log2 m)+1, i.e. binary
// search rather than a linear scan. It reports pass/fail only; the
// counter value itself is never exposed.
func CheckCost() error {
	for _, m := range []int{100, 1000, 10000} {
		keys := make([]int, m)
		for i := range keys {
			keys[i] = i
		}
		x, err := idx.Build(keys)
		if err != nil {
			return err
		}
		j := NewJoiner()
		j.SetIndex(x)
		if _, err := j.Probe([]int{m / 2}); err != nil {
			return err
		}
		bound := int64(1)
		for n := m; n > 1; n = (n + 1) / 2 {
			bound++
		}
		if j.cmps.Load() > bound {
			return errors.New("inj: probe cost exceeds binary-search bound")
		}
	}
	return nil
}

// Package api is the public entry point of the systematic sampler. It wires
// the index math (samp) and value fetching (select) together behind one
// type. Dependency direction is one-way: api -> select -> samp.
package api

import (
	"math"
	"sync"

	"ontology/samp"
	selectx "ontology/select"
)

// Judgeable sentinel errors, re-exported from the lower layers. The three
// failures are deliberately distinct values so callers can branch with
// errors.Is.
var (
	ErrInvalidSize              = samp.ErrInvalidSize
	ErrOffsetOutOfRange         = samp.ErrOffsetOutOfRange
	ErrPopulationLengthMismatch = selectx.ErrPopulationLengthMismatch
)

// Sampler is the reusable, concurrency-safe public object.
type Sampler struct {
	mu   sync.RWMutex
	plan *samp.Plan
	sel  *selectx.Selector
}

// New constructs a Sampler. Illegal s or r fail before any state exists, so
// a rejected construction returns a nil sampler and leaves nothing behind.
func New(n, s int, r float64) (*Sampler, error) {
	plan, err := samp.New(n, s, r)
	if err != nil {
		return nil, err
	}
	return &Sampler{plan: plan, sel: selectx.New(plan)}, nil
}

// Indices returns the s computed indices. It only reads immutable state and
// is safe for unlimited concurrent use.
func (z *Sampler) Indices() []int {
	z.mu.RLock()
	defer z.mu.RUnlock()
	return z.plan.Indices()
}

// Sample returns the population values at the sampled indices. A length
// mismatch is rejected before the selector's counter is touched, so a failed
// call changes no state and the sampler stays usable.
func (z *Sampler) Sample(vals []int64) ([]int64, error) {
	z.mu.RLock()
	sel := z.sel
	z.mu.RUnlock()
	return sel.Sample(vals)
}

// SelfCheck verifies the four invariants on a fixed built-in case
// (N=10, s=4, r=1.0 -> indices [1 3 6 8]). It mutates no receiver state and
// is safe to call concurrently.
func (z *Sampler) SelfCheck() bool {
	const n, s = 10, 4
	const r = 1.0
	c, err := New(n, s, r)
	if err != nil {
		return false
	}
	idx := c.Indices()

	// (1) exactly s; (2) in-bounds and strictly increasing.
	if len(idx) != s || idx[0] < 0 || idx[len(idx)-1] >= n {
		return false
	}
	for i := 1; i < s; i++ {
		if idx[i] <= idx[i-1] {
			return false
		}
	}
	// (3) identical to the naive batch recomputation.
	d := float64(n) / s
	for i := 0; i < s; i++ {
		if idx[i] != int(math.Floor(r+float64(i)*d)) {
			return false
		}
	}
	// (4) rejected operations leave no trace: the three errors are distinct,
	// and after a rejected Sample the sampler still returns the right values.
	if _, e := New(n, 0, 0); e != ErrInvalidSize {
		return false
	}
	if _, e := New(n, s, d); e != ErrOffsetOutOfRange {
		return false
	}
	if _, e := c.Sample(make([]int64, n-1)); e != ErrPopulationLengthMismatch {
		return false
	}
	vals := make([]int64, n)
	for i := range vals {
		vals[i] = int64(i)
	}
	got, e := c.Sample(vals)
	if e != nil || len(got) != s {
		return false
	}
	for i, at := range idx {
		if got[i] != int64(at) {
			return false
		}
	}
	return true
}

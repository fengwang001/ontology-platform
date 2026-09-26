// Package api is the outward face of the systematic sampler: it
// validates parameters, exposes indices and sampled values, and
// provides a self-check of the four invariants.
package api

import (
	"errors"
	"fmt"
	"math"
	"reflect"

	"ontology/samp"
	sel "ontology/select"
)

// Sentinel errors, mutually distinct, decidable with errors.Is.
var (
	ErrSampleSize       = errors.New("api: sample size s out of range")
	ErrOffset           = errors.New("api: offset r out of range [0, d)")
	ErrPopulationLength = sel.ErrPopulationLength
)

// Sampler draws exactly s elements out of a sorted population of n
// elements by systematic (equal-spacing) sampling with offset r.
type Sampler struct {
	n     int
	inner *sel.Selector
}

// New validates the parameters before constructing anything; a rejected
// call changes no state. Requires 1 <= s <= n and 0 <= r < d = n/s.
func New(n, s int, r float64) (*Sampler, error) {
	if s < 1 || s > n {
		return nil, ErrSampleSize
	}
	if d := samp.Spacing(n, s); r < 0 || r >= d {
		return nil, ErrOffset
	}
	return &Sampler{n: n, inner: sel.New(n, s, r)}, nil
}

// Indices returns the s sample indices. Safe for concurrent use.
func (sm *Sampler) Indices() []int { return sm.inner.Indices() }

// Sample returns the sampled values. len(vals) != n fails wholesale
// with ErrPopulationLength before any state is touched.
func (sm *Sampler) Sample(vals []int64) ([]int64, error) {
	if len(vals) != sm.n {
		return nil, ErrPopulationLength
	}
	return sm.inner.Sample(vals)
}

// SelfCheck verifies the four invariants on built-in parameter sets.
// It is read-only and safe for concurrent use.
func (sm *Sampler) SelfCheck() error {
	cases := []struct {
		n, s int
		r    float64
	}{{10, 4, 1.0}, {1, 1, 0}, {1000, 7, 0.5}, {97, 97, 0}, {8, 3, 0.999}}
	for _, c := range cases {
		idx := samp.Indices(c.n, c.s, c.r)
		if len(idx) != c.s { // invariant 1: exactly s
			return fmt.Errorf("selfcheck: got %d indices, want %d", len(idx), c.s)
		}
		d := samp.Spacing(c.n, c.s)
		for i, ix := range idx {
			if ix < 0 || ix >= c.n { // invariant 2: in bounds
				return fmt.Errorf("selfcheck: index %d out of [0,%d)", ix, c.n)
			}
			if i > 0 && ix <= idx[i-1] { // invariant 2: strictly increasing
				return fmt.Errorf("selfcheck: not increasing at %d", i)
			}
			if want := int(math.Floor(c.r + float64(i)*d)); ix != want { // invariant 3
				return fmt.Errorf("selfcheck: index %d = %d, naive %d", i, ix, want)
			}
		}
	}
	// Invariant 4: rejected operations are decidable and leave no trace.
	for _, bad := range []struct{ n, s int }{{10, 0}, {10, -1}, {10, 11}} {
		if _, err := New(bad.n, bad.s, 0); !errors.Is(err, ErrSampleSize) {
			return fmt.Errorf("selfcheck: s=%d: %v", bad.s, err)
		}
	}
	if _, err := New(10, 4, -0.5); !errors.Is(err, ErrOffset) {
		return fmt.Errorf("selfcheck: negative r: %v", err)
	}
	if _, err := New(10, 4, 2.5); !errors.Is(err, ErrOffset) { // r == d
		return fmt.Errorf("selfcheck: r == d: %v", err)
	}
	chk, err := New(10, 4, 1.0)
	if err != nil {
		return fmt.Errorf("selfcheck: build: %v", err)
	}
	before := chk.Indices()
	if _, err = chk.Sample(make([]int64, 9)); !errors.Is(err, ErrPopulationLength) {
		return fmt.Errorf("selfcheck: short population: %v", err)
	}
	if !reflect.DeepEqual(chk.Indices(), before) {
		return errors.New("selfcheck: rejected Sample mutated state")
	}
	if _, err = chk.Sample(make([]int64, 10)); err != nil { // still usable
		return fmt.Errorf("selfcheck: unusable after rejection: %v", err)
	}
	return nil
}

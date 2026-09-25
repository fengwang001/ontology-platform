// Package api is the public face of the HLL sketch: construction via New,
// Add, Estimate, Merge and a SelfCheck verifying the four invariants.
package api

import (
	"fmt"
	"math"

	"ontology/hll"
)

// Distinguishable sentinel errors, re-exported from hll.
var (
	ErrPrecision = hll.ErrPrecision
	ErrMismatch  = hll.ErrMismatch
	ErrNotInit   = hll.ErrNotInit
)

type Sketch struct {
	inner *hll.Sketch
}

func New(p int) (*Sketch, error) {
	s, err := hll.New(uint(p))
	if err != nil {
		return nil, err
	}
	return &Sketch{inner: s}, nil
}

func (s *Sketch) Add(key string) error {
	if s.inner == nil {
		return ErrNotInit
	}
	return s.inner.Add(key)
}

func (s *Sketch) Estimate() (float64, error) {
	if s.inner == nil {
		return 0, ErrNotInit
	}
	return s.inner.Estimate()
}

func (s *Sketch) Merge(o *Sketch) error {
	if s.inner == nil || o == nil || o.inner == nil {
		return ErrNotInit
	}
	return s.inner.Merge(o.inner)
}

func (s *Sketch) SelfCheck() error {
	if s.inner == nil {
		return ErrNotInit
	}
	// Invariant 1: merge of disjoint sets == sequential adds; commutative; idempotent.
	a, _ := New(10)
	b, _ := New(10)
	all, _ := New(10)
	for i := 0; i < 2000; i++ {
		k := fmt.Sprintf("selfcheck-%d", i)
		all.Add(k)
		if i%2 == 0 {
			a.Add(k)
		} else {
			b.Add(k)
		}
	}
	m1, _ := New(10)
	m1.Merge(a)
	m1.Merge(b)
	m2, _ := New(10)
	m2.Merge(b)
	m2.Merge(a)
	if !m1.inner.Equal(all.inner) || !m2.inner.Equal(all.inner) {
		return fmt.Errorf("selfcheck: merge != sequential adds")
	}
	cp, _ := New(10)
	cp.Merge(a)
	a.Merge(a)
	if !a.inner.Equal(cp.inner) {
		return fmt.Errorf("selfcheck: merge not idempotent")
	}
	// Invariant 2: estimate within 3 sigma of the true distinct count.
	est, _ := all.Estimate()
	if d := math.Abs(est-2000) / 2000; d > 3*1.04/math.Sqrt(1024) {
		return fmt.Errorf("selfcheck: estimate %f off by %f", est, d)
	}
	// Invariant 3: estimate never decreases as keys are added.
	mono, _ := New(8)
	prev := 0.0
	for i := 0; i < 500; i++ {
		mono.Add(fmt.Sprintf("mono-%d", i))
		e, _ := mono.Estimate()
		if e < prev {
			return fmt.Errorf("selfcheck: estimate decreased %f -> %f", prev, e)
		}
		prev = e
	}
	// Invariant 4: rejected operations change nothing and are distinguishable.
	if _, err := New(3); err != ErrPrecision {
		return fmt.Errorf("selfcheck: bad p not rejected: %v", err)
	}
	other, _ := New(9)
	before, _ := a.Estimate()
	if err := a.Merge(other); err != ErrMismatch {
		return fmt.Errorf("selfcheck: p mismatch not rejected: %v", err)
	}
	zero := &Sketch{}
	if err := zero.Add("x"); err != ErrNotInit {
		return fmt.Errorf("selfcheck: zero value not rejected: %v", err)
	}
	after, _ := a.Estimate()
	if before != after {
		return fmt.Errorf("selfcheck: rejected merge changed state")
	}
	a.Add("selfcheck-extra") // still usable after rejections
	return nil
}

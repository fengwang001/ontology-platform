// Package api is the outward-facing facade over a cluster of OR-set replicas.
package api

import (
	"errors"
	"fmt"

	"ontology/cluster"
	"ontology/orset"
)

// Set is a replicated add-wins observed-remove set.
type Set struct{ c *cluster.Cluster }

func New(n, maxTags int) (*Set, error) {
	c, err := cluster.New(n, maxTags)
	if err != nil {
		return nil, err
	}
	return &Set{c: c}, nil
}

func (s *Set) Add(r int, e string) error                      { return s.c.Add(r, e) }
func (s *Set) Remove(r int, e string) error                   { return s.c.Remove(r, e) }
func (s *Set) Merge(dst, src int) error                       { return s.c.Merge(dst, src) }
func (s *Set) SyncAll() error                                 { return s.c.SyncAll() }
func (s *Set) Contains(r int, e string) (bool, error)         { return s.c.Contains(r, e) }
func (s *Set) Elements(r int) (map[string][]orset.Tag, error) { return s.c.Elements(r) }

// SelfCheck verifies the four invariants on built-in operation sequences.
func (s *Set) SelfCheck() error {
	// Invariants 1 & 3: the canonical 11-step scenario converges to the
	// naive add-wins result {y: [B1]} on both replicas.
	a, err := New(2, 1000)
	if err != nil {
		return err
	}
	steps := []func() error{
		func() error { return a.Add(0, "x") }, func() error { return a.Merge(1, 0) },
		func() error { return a.Remove(1, "x") }, func() error { return a.Add(0, "x") },
		func() error { return a.Add(0, "y") }, func() error { return a.Add(1, "y") },
		func() error { return a.Remove(0, "y") }, func() error { return a.Merge(0, 1) },
		func() error { return a.Merge(1, 0) }, func() error { return a.Remove(1, "x") },
		func() error { return a.Merge(0, 1) },
	}
	for i, st := range steps {
		if err := st(); err != nil {
			return fmt.Errorf("selfcheck step %d: %w", i+1, err)
		}
	}
	for r := 0; r < 2; r++ {
		el, _ := a.Elements(r)
		if fmt.Sprint(el) != "map[y:[{1 1}]]" {
			return fmt.Errorf("selfcheck: replica %d = %v", r, el)
		}
	}
	// Invariant 2: merge is commutative, associative and idempotent.
	build := func() *Set {
		b, _ := New(3, 1000)
		b.Add(0, "a")
		b.Add(0, "b")
		b.Add(1, "b")
		b.Add(1, "c")
		b.Add(2, "a")
		b.Remove(2, "a")
		return b
	}
	eq := func(x *Set, xr int, y *Set, yr int) bool {
		xe, _ := x.Elements(xr)
		ye, _ := y.Elements(yr)
		return fmt.Sprint(xe) == fmt.Sprint(ye)
	}
	p, q := build(), build()
	p.Merge(0, 1)
	p.Merge(0, 1) // idempotent
	q.Merge(0, 1)
	p2, q2 := build(), build()
	p2.Merge(0, 1)
	q2.Merge(1, 0) // commutative
	p3, q3 := build(), build()
	p3.Merge(0, 1)
	p3.Merge(0, 2) // (x⊔y)⊔z
	q3.Merge(1, 2)
	q3.Merge(0, 1) // x⊔(y⊔z)
	if !eq(p, 0, q, 0) || !eq(p2, 0, q2, 1) || !eq(p3, 0, q3, 0) {
		return errors.New("selfcheck: merge laws violated")
	}
	// Invariant 4: rejected operations leave no trace, tags do not skip.
	f, _ := New(2, 4)
	f.Add(0, "a") // tag {0,1}
	if err := f.Add(0, ""); !errors.Is(err, orset.ErrEmptyElement) {
		return errors.New("selfcheck: empty element not rejected")
	}
	if err := f.Add(9, "b"); !errors.Is(err, orset.ErrInvalidArgument) {
		return errors.New("selfcheck: bad replica not rejected")
	}
	if err := f.Remove(0, "zz"); !errors.Is(err, orset.ErrNotFound) {
		return errors.New("selfcheck: missing remove not rejected")
	}
	g, _ := New(1, 1)
	g.Add(0, "x")
	if err := g.Add(0, "y"); !errors.Is(err, orset.ErrCapacity) {
		return errors.New("selfcheck: capacity not enforced")
	}
	f.Add(0, "b") // must be tag {0,2}: rejected ops did not skip a sequence number
	el, _ := f.Elements(0)
	if fmt.Sprint(el) != "map[a:[{0 1}] b:[{0 2}]]" {
		return fmt.Errorf("selfcheck: state changed by rejected ops: %v", el)
	}
	return nil
}

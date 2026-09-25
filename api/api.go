// Package api is the public facade over reg.
package api

import (
	"errors"
	"fmt"

	"ontology/reg"
)

// Sentinel errors, re-exported so callers can compare directly.
var (
	ErrEmpty    = reg.ErrEmpty
	ErrExists   = reg.ErrExists
	ErrNotFound = reg.ErrNotFound
	ErrNotHeld  = reg.ErrNotHeld
)

// Service is the public handle.
type Service struct{ r *reg.Registry }

// New returns a ready-to-use Service.
func New() *Service { return &Service{r: reg.New()} }

func (s *Service) Create(id string) error              { return s.r.Create(id) }
func (s *Service) Acquire(ref, id string) error        { return s.r.Acquire(ref, id) }
func (s *Service) Release(ref, id string) error        { return s.r.Release(ref, id) }
func (s *Service) RefCount(id string) (int, error)     { return s.r.RefCount(id) }
func (s *Service) Alive(id string) bool                { return s.r.Alive(id) }
func (s *Service) Holders(id string) ([]string, error) { return s.r.Holders(id) }

// SelfCheck runs built-in sequences verifying the four invariants.
func (s *Service) SelfCheck() error {
	checks := []func() error{checkConsistency, checkReclaimTiming, checkIdemShared, checkFailureAtomic}
	for i, c := range checks {
		if err := c(); err != nil {
			return fmt.Errorf("invariant %d violated: %w", i+1, err)
		}
	}
	return nil
}

// run executes ops in order, stopping at the first error.
func run(ops ...func() error) error {
	for _, op := range ops {
		if err := op(); err != nil {
			return err
		}
	}
	return nil
}

// Invariant 1: RefCount == number of distinct current holders.
func checkConsistency() error {
	x := New()
	err := run(
		func() error { return x.Create("a") },
		func() error { return x.Acquire("r1", "a") },
		func() error { return x.Acquire("r2", "a") },
		func() error { return x.Acquire("r3", "a") },
		func() error { return x.Release("r2", "a") },
	)
	if err != nil {
		return err
	}
	if n, _ := x.RefCount("a"); n != 2 {
		return fmt.Errorf("count(a)=%d want 2", n)
	}
	return nil
}

// Invariant 2: reclaim exactly at the 1->0 moment, never before.
func checkReclaimTiming() error {
	x := New()
	return run(
		func() error { return x.Create("o") },
		func() error { return x.Acquire("u", "o") },
		func() error { return x.Acquire("v", "o") },
		func() error { return x.Release("u", "o") },
		func() error {
			if !x.Alive("o") {
				return errors.New("reclaimed while count>0")
			}
			return nil
		},
		func() error { return x.Release("v", "o") },
		func() error {
			if x.Alive("o") {
				return errors.New("not reclaimed at count 0")
			}
			return nil
		},
		func() error {
			if _, err := x.RefCount("o"); err != ErrNotFound {
				return errors.New("reclaimed object must report ErrNotFound")
			}
			return nil
		},
	)
}

// Invariant 3: idempotent acquire; shared object reclaimed only by last release.
func checkIdemShared() error {
	x := New()
	err := run(
		func() error { return x.Create("s") },
		func() error { return x.Acquire("r", "s") },
		func() error { return x.Acquire("r", "s") },
		func() error { return x.Acquire("r", "s") },
		func() error { return x.Acquire("q", "s") },
	)
	if err != nil {
		return err
	}
	if n, _ := x.RefCount("s"); n != 2 {
		return fmt.Errorf("count=%d want 2 (idempotent acquire)", n)
	}
	if err := x.Release("r", "s"); err != nil || !x.Alive("s") {
		return errors.New("shared object reclaimed before last release")
	}
	if err := x.Release("q", "s"); err != nil || x.Alive("s") {
		return errors.New("not reclaimed after last release")
	}
	return nil
}

// Invariant 4: rejected ops change nothing; service stays usable.
func checkFailureAtomic() error {
	x := New()
	if err := run(
		func() error { return x.Create("k") },
		func() error { return x.Acquire("r", "k") },
	); err != nil {
		return err
	}
	bads := []error{
		x.Create(""), x.Acquire("", "k"), x.Acquire("r", ""),
		x.Create("k"), x.Acquire("r", "ghost"), x.Release("r", "ghost"),
		x.Release("nobody", "k"),
	}
	for _, err := range bads {
		if err == nil {
			return errors.New("rejected op returned nil error")
		}
	}
	if n, _ := x.RefCount("k"); n != 1 || !x.Alive("k") {
		return errors.New("rejected op changed state")
	}
	return x.Release("r", "k")
}

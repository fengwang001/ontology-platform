// Package api is the public face of the clock-synchronization service.
// Depends on csync.
package api

import (
	"errors"
	"fmt"

	"ontology/csync"
	"ontology/marz"
)

var (
	// ErrInvalidF: f < 0, or f >= current clock count (K-f <= 0).
	ErrInvalidF = errors.New("api: invalid fault tolerance f")
	// ErrTooFewClocks: fewer than 2 clocks, consensus impossible.
	ErrTooFewClocks = errors.New("api: fewer than 2 clocks")
)

// System is a clock-synchronization service tolerating f faulty clocks.
type System struct {
	set *csync.Set
	f   int
}

// New validates f and returns an empty System. f must be >= 0; the
// f < K half of the rule is enforced at Consensus time as K grows.
func New(f int) (*System, error) {
	if f < 0 {
		return nil, ErrInvalidF
	}
	return &System{set: csync.NewSet(), f: f}, nil
}

// Add registers one clock. Rejections (negative error, duplicate ID)
// change no state.
func (s *System) Add(id string, offset, err int64) error {
	return s.set.Add(id, offset, err)
}

// Consensus returns the shortest closed interval [lo, hi] covered by at
// least K-f clocks.
func (s *System) Consensus() (lo, hi int64, err error) {
	k := s.set.Len()
	if k < 2 {
		return 0, 0, ErrTooFewClocks
	}
	if s.f >= k {
		return 0, 0, ErrInvalidF
	}
	return s.set.Consensus(k - s.f)
}

// CountAt returns how many clocks' closed error intervals contain t.
func (s *System) CountAt(t int64) int {
	return s.set.CountAt(t)
}

// SelfCheck verifies the four invariants on a built-in clock sequence.
func (s *System) SelfCheck() error {
	seq := []struct {
		id          string
		offset, err int64
	}{{"A", 10, 2}, {"B", 11, 1}, {"C", 20, 1}, {"D", 11, 2}, {"E", 11, 0}}
	const f = 1
	sys, err := New(f)
	if err != nil {
		return err
	}
	for _, c := range seq {
		if err := sys.Add(c.id, c.offset, c.err); err != nil {
			return fmt.Errorf("selfcheck add %s: %w", c.id, err)
		}
	}
	need := len(seq) - f
	lo, hi, err := sys.Consensus()
	if err != nil {
		return fmt.Errorf("selfcheck consensus: %w", err)
	}
	// Invariant 1: equals a naive recompute from the raw sequence.
	eps := []marz.Endpoint{}
	for _, c := range seq {
		l, r := marz.Endpoints(c.offset, c.err)
		eps = append(eps, l, r)
	}
	marz.SortEndpoints(eps)
	if nlo, nhi, ok := marz.Sweep(eps, need); !ok || nlo != lo || nhi != hi {
		return fmt.Errorf("selfcheck: consensus [%d,%d] != naive [%d,%d]", lo, hi, nlo, nhi)
	}
	// Invariant 2: both ends are clock boundaries, covered by >= need clocks.
	for _, p := range []int64{lo, hi} {
		boundary := false
		for _, c := range seq {
			if p == c.offset-c.err || p == c.offset+c.err {
				boundary = true
			}
		}
		if !boundary || sys.CountAt(p) < need {
			return fmt.Errorf("selfcheck: boundary closure violated at %d", p)
		}
	}
	// Invariant 3: CountAt equals naive per-clock counting.
	for t := int64(7); t <= 41; t++ {
		want := 0
		for _, c := range seq {
			if c.offset-c.err <= t && t <= c.offset+c.err {
				want++
			}
		}
		if got := sys.CountAt(t); got != want {
			return fmt.Errorf("selfcheck: CountAt(%d)=%d want %d", t, got, want)
		}
	}
	// Invariant 4: rejected operations change no state.
	if err := sys.Add("NEG", 0, -1); !errors.Is(err, csync.ErrNegativeError) {
		return fmt.Errorf("selfcheck: negative error: %v", err)
	}
	if err := sys.Add("A", 0, 0); !errors.Is(err, csync.ErrDuplicateID) {
		return fmt.Errorf("selfcheck: duplicate id: %v", err)
	}
	if _, err := New(-1); !errors.Is(err, ErrInvalidF) {
		return fmt.Errorf("selfcheck: negative f: %v", err)
	}
	if empty, _ := New(0); true {
		if _, _, err := empty.Consensus(); !errors.Is(err, ErrTooFewClocks) {
			return fmt.Errorf("selfcheck: too few clocks: %v", err)
		}
	}
	if lo2, hi2, err := sys.Consensus(); err != nil || lo2 != lo || hi2 != hi {
		return errors.New("selfcheck: rejected operation mutated state")
	}
	return nil
}

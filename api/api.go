// Package api is the public surface of the dual-timestamp version store.
package api

import (
	"errors"
	"fmt"

	"ontology/hist"
	"ontology/ver"
)

// Version is one {Value, Ev, In} triple in a key's history.
type Version = ver.Version

// Sentinel errors, re-exported so callers can errors.Is against api alone.
var (
	ErrBadConfig          = hist.ErrBadConfig
	ErrEmptyKey           = hist.ErrEmptyKey
	ErrNonMonotonicIngest = hist.ErrNonMonotonicIngest
	ErrCapacity           = hist.ErrCapacity
	ErrNotFound           = ver.ErrNotFound
)

// System is the version store; the zero value is unusable, call New.
type System struct{ s *hist.Store }

// New validates maxVersions (>= 1) and returns an empty store.
func New(maxVersions int) (*System, error) {
	s, err := hist.NewStore(maxVersions)
	if err != nil {
		return nil, err
	}
	return &System{s: s}, nil
}

// Apply appends a version; any rejection leaves all state untouched.
func (y *System) Apply(key, value string, ev, in int64) error { return y.s.Apply(key, value, ev, in) }

// LatestEvent returns the version with max Ev (ties: max In).
func (y *System) LatestEvent(key string) (Version, error) {
	return y.s.Query(key, (*ver.History).LatestEvent)
}

// LatestIngest returns the last-arrived version (max In).
func (y *System) LatestIngest(key string) (Version, error) {
	return y.s.Query(key, (*ver.History).LatestIngest)
}

// AtEvent returns the winner among versions with Ev <= T.
func (y *System) AtEvent(key string, T int64) (Version, error) {
	return y.s.Query(key, func(h *ver.History) (ver.Version, error) { return h.AtEvent(T) })
}

// AtIngest returns the version with max In that is <= T.
func (y *System) AtIngest(key string, T int64) (Version, error) {
	return y.s.Query(key, func(h *ver.History) (ver.Version, error) { return h.AtIngest(T) })
}

// SelfCheck runs a built-in apply/query sequence on a private store and
// verifies the four invariants; receiver state is untouched, safe concurrently.
func (y *System) SelfCheck() error {
	s, err := New(100)
	if err != nil {
		return err
	}
	type rec struct {
		v      string
		ev, in int64
	}
	var got []rec                 // naive shadow of accepted versions, in arrival order
	bestEv := func(t int64) rec { // naive scan: max Ev <= t, ties max In
		var b rec
		ok := false
		for _, r := range got {
			if r.ev <= t && (!ok || r.ev > b.ev || (r.ev == b.ev && r.in > b.in)) {
				b, ok = r, true
			}
		}
		return b
	}
	seed, prevEv := int64(7), int64(0)
	for i := 0; i < 64; i++ {
		seed = seed*6364136223846793005 + 1442695040888963407
		r := rec{fmt.Sprintf("v%d", i), (seed >> 33) % 40, int64(i + 1)}
		if err := s.Apply("k", r.v, r.ev, r.in); err != nil {
			return fmt.Errorf("selfcheck: apply rejected: %w", err)
		}
		got = append(got, r)
		le, _ := s.LatestEvent("k")
		li, _ := s.LatestIngest("k")
		ae, _ := s.AtEvent("k", r.ev)
		ai, _ := s.AtIngest("k", r.in)
		if le.Value != bestEv(1<<62).v || ae.Value != bestEv(r.ev).v ||
			li.Value != r.v || ai.Value != got[r.in-1].v {
			return fmt.Errorf("selfcheck: invariant 1 (naive-scan consistency) broken at step %d", i)
		}
		if i > 0 && le.Ev < prevEv {
			return fmt.Errorf("selfcheck: invariant 2 (event-latest Ev monotone) broken at step %d", i)
		}
		prevEv = le.Ev
		if li.In != r.in { // arrivals are 1..i+1, strictly increasing
			return fmt.Errorf("selfcheck: invariant 3 (ingest strictly increasing) broken at step %d", i)
		}
	}
	return checkRejections(s)
}

// checkRejections verifies invariant 4: rejections carry the right mutually
// distinct sentinels, leave state untouched, and the store stays usable.
func checkRejections(s *System) error {
	beforeE, _ := s.LatestEvent("k")
	beforeI, _ := s.LatestIngest("k")
	c, _ := New(1)
	if err := c.Apply("a", "v", 1, 1); err != nil {
		return err
	}
	if _, err := New(0); !errors.Is(err, ErrBadConfig) {
		return fmt.Errorf("selfcheck: New(0) = %v, want ErrBadConfig", err)
	}
	cases := []struct {
		err  error
		want error
	}{
		{s.Apply("", "x", 1, 1000), ErrEmptyKey},
		{s.Apply("k", "x", 1, 5), ErrNonMonotonicIngest},
		{c.Apply("a", "x", 2, 2), ErrCapacity},
	}
	for i, tc := range cases {
		if !errors.Is(tc.err, tc.want) {
			return fmt.Errorf("selfcheck: rejection %d = %v, want %v", i, tc.err, tc.want)
		}
		for j, o := range cases {
			if i != j && errors.Is(tc.err, o.want) {
				return fmt.Errorf("selfcheck: sentinels %d and %d not distinct", i, j)
			}
		}
	}
	afterE, _ := s.LatestEvent("k")
	afterI, _ := s.LatestIngest("k")
	ca, _ := c.LatestEvent("a")
	if afterE != beforeE || afterI != beforeI || ca.Value != "v" {
		return errors.New("selfcheck: invariant 4 (rejection leaves no trace) broken")
	}
	if err := s.Apply("k2", "ok", 1, 1); err != nil {
		return fmt.Errorf("selfcheck: store unusable after rejections: %w", err)
	}
	return nil
}

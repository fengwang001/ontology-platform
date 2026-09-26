// Package api is the public face of the weighted random sampler.
package api

import (
	"fmt"

	"ontology/wrs"
	"ontology/wsampler"
)

// Item is one upstream element: a value and a positive weight.
type Item = wrs.Item

// Sentinel errors, re-exported so callers can match with errors.Is.
var (
	ErrBadCapacity = wsampler.ErrBadCapacity
	ErrNilRNG      = wsampler.ErrNilRNG
	ErrEmptyVal    = wsampler.ErrEmptyVal
	ErrBadWeight   = wsampler.ErrBadWeight
	ErrBadU        = wsampler.ErrBadU
)

// Sampler is a concurrency-safe weighted random sampler.
type Sampler struct {
	s *wsampler.Sampler
}

// New returns a sampler of capacity k drawing U_i from rng (i starts at 1).
func New(k int, rng func(i int) float64) (*Sampler, error) {
	s, err := wsampler.New(k, rng)
	if err != nil {
		return nil, err
	}
	return &Sampler{s: s}, nil
}

// Feed offers a batch; any rejection rejects the whole batch atomically.
func (s *Sampler) Feed(items []Item) error { return s.s.Feed(items) }

// Sample returns the retained items, highest key first.
func (s *Sampler) Sample() []Item { return s.s.Sample() }

// Size reports how many elements are currently retained.
func (s *Sampler) Size() int { return s.s.Size() }

// SelfCheck verifies the four invariants on built-in sequences and
// returns a descriptive error for the first one that fails.
func SelfCheck() error {
	rng := func(i int) float64 { return float64((i*37)%101+1) / 102.0 }
	for _, c := range []struct{ k, n int }{{1, 1}, {3, 2}, {2, 5}, {5, 40}} {
		s, err := New(c.k, rng)
		if err != nil {
			return err
		}
		items := make([]Item, c.n)
		for i := range items {
			items[i] = Item{Val: fmt.Sprintf("v%d", i), Weight: float64(i%4 + 1)}
		}
		if err := s.Feed(items); err != nil {
			return err
		}
		if s.Size() != min(c.k, c.n) { // invariant 1
			return fmt.Errorf("selfcheck: size %d != min(%d,%d)", s.Size(), c.k, c.n)
		}
		if c.n <= c.k && !containsAll(s.Sample(), items) { // invariant 2
			return fmt.Errorf("selfcheck: n<=k but not all retained")
		}
		if !matchesOffline(s.Sample(), items, c.k, rng) { // invariant 3
			return fmt.Errorf("selfcheck: online != offline reference")
		}
		before := s.Sample()
		if s.Feed([]Item{{Val: "", Weight: 1}}) == nil || s.Feed([]Item{{Val: "x", Weight: -1}}) == nil {
			return fmt.Errorf("selfcheck: invalid batch accepted")
		}
		if !equalSample(s.Sample(), before) { // invariant 4
			return fmt.Errorf("selfcheck: rejected feed changed state")
		}
	}
	return nil
}

func containsAll(got, all []Item) bool {
	if len(got) != len(all) {
		return false
	}
	seen := map[string]int{}
	for _, g := range got {
		seen[g.Val]++
	}
	for _, a := range all {
		if seen[a.Val] == 0 {
			return false
		}
		seen[a.Val]--
	}
	return true
}

// matchesOffline recomputes the naive offline top-k and compares in order.
func matchesOffline(got, items []Item, k int, rng func(int) float64) bool {
	type keyed struct {
		val string
		key float64
	}
	ks := make([]keyed, len(items))
	for i, it := range items {
		ks[i] = keyed{it.Val, wrs.Key(rng(i+1), it.Weight)}
	}
	for i := 0; i < len(ks); i++ { // selection sort, key descending
		for j := i + 1; j < len(ks); j++ {
			if ks[j].key > ks[i].key {
				ks[i], ks[j] = ks[j], ks[i]
			}
		}
	}
	if len(got) != min(k, len(items)) {
		return false
	}
	for i := range got {
		if got[i].Val != ks[i].val {
			return false
		}
	}
	return true
}

func equalSample(a, b []Item) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

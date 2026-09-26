// Package api is the public entry point for online weighted random sampling
// (Efraimidis–Spirakis A-Res, without replacement).
package api

import (
	"fmt"
	"sort"

	"ontology/wrs"
	"ontology/wsampler"
)

// Item is one arriving element: a string value with a positive weight.
type Item struct {
	Val    string
	Weight float64
}

// The four rejection causes are distinct, distinguishable sentinel errors.
// They alias the driver package's errors so errors.Is works across layers.
var (
	ErrInvalidK          = wsampler.ErrInvalidK
	ErrNilRNG            = wsampler.ErrNilRNG
	ErrUniformOutOfRange = wsampler.ErrUniformOutOfRange
	ErrInvalidItem       = wsampler.ErrInvalidItem
)

// Sampler keeps, among the N elements fed so far, the k with the largest
// A-Res keys, using only O(k) memory.
type Sampler struct{ s *wsampler.Sampler }

// New constructs a sampler of positive capacity k. rng maps the 1-based
// arrival step i to U_i drawn from the open interval (0,1).
func New(k int, rng func(i int) float64) (*Sampler, error) {
	s, err := wsampler.New(k, rng)
	if err != nil {
		return nil, err
	}
	return &Sampler{s: s}, nil
}

// Feed offers one batch. Every item is validated and every U_i is range
// checked before any state changes; one rejection rejects the whole batch.
func (s *Sampler) Feed(items []Item) error {
	in := make([]wsampler.Input, len(items))
	for i, it := range items {
		in[i] = wsampler.Input{Val: it.Val, Weight: it.Weight}
	}
	return s.s.Feed(in)
}

// Sample returns the retained elements ordered by descending key (ties broken
// by ascending value). Concurrent callers receive identical slices.
func (s *Sampler) Sample() []Item {
	slots := s.s.Sample()
	out := make([]Item, len(slots))
	for i, sl := range slots {
		out[i] = Item{Val: sl.Val, Weight: sl.Weight}
	}
	return out
}

// Size reports the number of retained elements: min(k, N).
func (s *Sampler) Size() int { return s.s.Size() }

// SelfCheck verifies the four invariants on a built-in deterministic sequence:
// size is always min(k,N); the first k elements are always kept; the online
// result matches the naive offline reference; a rejected batch leaves no
// trace. It never touches the receiver's state, so it is concurrency-safe.
func (s *Sampler) SelfCheck() error {
	const k = 3
	us := []float64{0.5, 0.8, 0.2, 0.9, 0.7, 0.1, 0.95}
	items := []Item{{"a", 1}, {"b", 2}, {"c", 1}, {"d", 3}, {"e", 1}, {"f", 2}, {"g", 1}}
	chk, err := New(k, func(i int) float64 {
		if i-1 < len(us) {
			return us[i-1]
		}
		return 0.5 // in-range U for the later rejected batch
	})
	if err != nil {
		return err
	}
	for step, it := range items {
		if err := chk.Feed([]Item{it}); err != nil {
			return fmt.Errorf("selfcheck step %d: %w", step+1, err)
		}
		n := step + 1
		got := vals(chk.Sample())
		if chk.Size() != min(k, n) {
			return fmt.Errorf("selfcheck invariant 1: size=%d want %d", chk.Size(), min(k, n))
		}
		if n <= k && len(got) != n {
			return fmt.Errorf("selfcheck invariant 2: kept %d of first %d", len(got), n)
		}
		if want := offline(us[:n], items[:n], k); !equal(got, want) {
			return fmt.Errorf("selfcheck invariant 3: online=%v offline=%v", got, want)
		}
	}
	before := vals(chk.Sample())
	beforeSize := chk.Size()
	if err := chk.Feed([]Item{{"ok", 1}, {"", 1}}); err == nil {
		return fmt.Errorf("selfcheck invariant 4: invalid batch was accepted")
	}
	if after := vals(chk.Sample()); !equal(after, before) || chk.Size() != beforeSize {
		return fmt.Errorf("selfcheck invariant 4: rejected batch changed state")
	}
	return nil
}

// offline is the naive reference: collect all N, compute U^(1/w), take top k.
func offline(us []float64, items []Item, k int) []string {
	type kv struct {
		v string
		x float64
	}
	all := make([]kv, len(items))
	for i, it := range items {
		all[i] = kv{it.Val, wrs.Key(us[i], it.Weight)}
	}
	sort.SliceStable(all, func(i, j int) bool {
		return all[i].x > all[j].x || (all[i].x == all[j].x && all[i].v < all[j].v)
	})
	out := make([]string, 0, min(k, len(all)))
	for _, q := range all[:min(k, len(all))] {
		out = append(out, q.v)
	}
	return out
}

func vals(items []Item) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.Val
	}
	return out
}

func equal(a, b []string) bool {
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

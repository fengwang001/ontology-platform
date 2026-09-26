// Package wsampler drives a wrs reservoir: it feeds elements one by one,
// draws the per-step random number U_i from the injected source, and
// tracks how many elements were seen and how many are retained.
package wsampler

import (
	"errors"
	"sync"

	"ontology/wrs"
)

// Sentinel errors, each identifying exactly one failure class.
var (
	ErrBadCapacity = errors.New("wsampler: capacity k must be > 0")
	ErrNilRNG      = errors.New("wsampler: rng must not be nil")
	ErrEmptyVal    = errors.New("wsampler: item value must not be empty")
	ErrBadWeight   = errors.New("wsampler: item weight must be > 0")
	ErrBadU        = errors.New("wsampler: rng returned U outside (0,1)")
)

// Sampler is a weighted random sampler without replacement (A-Res).
type Sampler struct {
	mu   sync.RWMutex
	res  *wrs.Reservoir
	rng  func(i int) float64
	seen int
	kept int // elements currently retained in the reservoir; == res.Len()
}

// New validates k and rng and returns a ready sampler.
func New(k int, rng func(i int) float64) (*Sampler, error) {
	if k <= 0 {
		return nil, ErrBadCapacity
	}
	if rng == nil {
		return nil, ErrNilRNG
	}
	return &Sampler{res: wrs.New(k), rng: rng}, nil
}

// Feed offers a batch of items. The whole batch is validated first
// (values, weights, and every U_i drawn in advance); any rejection
// leaves seen/kept and the reservoir completely untouched.
func (s *Sampler) Feed(items []wrs.Item) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	us := make([]float64, len(items))
	for i, it := range items {
		if it.Val == "" {
			return ErrEmptyVal
		}
		if it.Weight <= 0 {
			return ErrBadWeight
		}
		u := s.rng(s.seen + i + 1)
		if u <= 0 || u >= 1 {
			return ErrBadU
		}
		us[i] = u
	}
	for i, it := range items {
		s.seen++
		s.res.Consider(it, wrs.Key(us[i], it.Weight))
	}
	s.kept = s.res.Len()
	return nil
}

// Sample returns the retained items, highest key first.
func (s *Sampler) Sample() []wrs.Item {
	s.mu.RLock()
	defer s.mu.RUnlock()
	slots := s.res.Items()
	out := make([]wrs.Item, len(slots))
	for i, sl := range slots {
		out[i] = sl.Item
	}
	return out
}

// Size reports how many elements are currently retained.
func (s *Sampler) Size() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.kept
}

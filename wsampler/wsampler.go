// Package wsampler drives a weighted reservoir: elements are fed in one by
// one, a seen counter and a retained counter are maintained, and the current
// sample can be read at any time.
package wsampler

import (
	"errors"
	"sync"

	"ontology/wrs"
)

// Distinguishable sentinel errors. Every rejected operation fails with one of
// these and leaves all state untouched.
var (
	ErrInvalidK          = errors.New("wsampler: capacity k must be positive")
	ErrNilRNG            = errors.New("wsampler: random source rng must not be nil")
	ErrUniformOutOfRange = errors.New("wsampler: rng returned U_i outside the open interval (0,1)")
	ErrInvalidItem       = errors.New("wsampler: item value must be non-empty and weight must be positive")
)

// Input is one element arriving at the sampler.
type Input struct {
	Val    string
	Weight float64
}

// Sampler is an online weighted-without-replacement sampler of fixed
// capacity k. Read methods are safe for concurrent use; Feed is serialised.
type Sampler struct {
	mu   sync.RWMutex
	k    int
	rng  func(i int) float64
	res  *wrs.Reservoir
	seen int // number of elements ever offered (N)
	n    int // number of elements currently retained; always min(k, seen)
}

// New creates a sampler. k must be positive and rng non-nil; rng maps the
// 1-based arrival step i to U_i in (0,1).
func New(k int, rng func(i int) float64) (*Sampler, error) {
	if k <= 0 {
		return nil, ErrInvalidK
	}
	if rng == nil {
		return nil, ErrNilRNG
	}
	return &Sampler{k: k, rng: rng, res: wrs.New(k)}, nil
}

// Feed offers a batch of elements. Everything happens under one write lock in
// three phases: every item shape is validated, then every U_i is range
// checked, and offers are made only if both phases pass. A rejected batch
// therefore leaves both the reservoir and the counters untouched.
func (s *Sampler) Feed(items []Input) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, it := range items {
		if it.Val == "" || it.Weight <= 0 {
			return ErrInvalidItem
		}
	}

	keys := make([]float64, len(items))
	for j := range items {
		u := s.rng(s.seen + j + 1)
		if u <= 0 || u >= 1 {
			return ErrUniformOutOfRange
		}
		keys[j] = wrs.Key(u, items[j].Weight)
	}

	for j, it := range items {
		s.res.Offer(it.Val, it.Weight, keys[j])
	}
	s.n = s.res.Len() // insertions fill up to k; later offers replace in place
	s.seen += len(items)
	return nil
}

// Sample returns a copy of the retained elements ordered by descending key
// (ties broken by ascending value), so concurrent readers observe identical
// results.
func (s *Sampler) Sample() []wrs.Slot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.res.Snapshot()
}

// Size reports the number of currently retained elements: min(k, N).
func (s *Sampler) Size() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.n
}

// Seen reports N, the number of elements offered so far.
func (s *Sampler) Seen() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.seen
}

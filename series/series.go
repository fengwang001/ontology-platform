// Package series manages many independent EWMA series keyed by a
// string. It depends only on package ewma.
package series

import (
	"sync"

	"ontology/ewma"
)

// Set is a concurrency-safe collection of per-key EWMA series.
type Set struct {
	mu          sync.RWMutex
	m           map[string]*ewma.EWMA
	alpha       float64
	seed        float64
	biasCorrect bool
}

// New returns an empty Set; every key created later shares the same
// alpha, seed and bias-correction setting.
func New(alpha, seed float64, biasCorrect bool) *Set {
	return &Set{m: make(map[string]*ewma.EWMA), alpha: alpha, seed: seed, biasCorrect: biasCorrect}
}

// Update folds x into key's series, creating it on first use.
func (s *Set) Update(key string, x float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.m[key]
	if !ok {
		e = ewma.New(s.alpha, s.seed, s.biasCorrect)
		s.m[key] = e
	}
	e.Update(x)
}

// Value returns key's current mean; ok is false if key was never updated.
func (s *Set) Value(key string) (v float64, ok bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	e, ok := s.m[key]
	if !ok {
		return 0, false
	}
	return e.Value(), true
}

// Count returns key's observation count; ok is false if key was never updated.
func (s *Set) Count(key string) (n int, ok bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	e, ok := s.m[key]
	if !ok {
		return 0, false
	}
	return e.Count(), true
}

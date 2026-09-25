// Package store manages versioned histories for many keys. It owns
// per-key version allocation (1-based, monotonic, never reused) and the
// single mutex that makes concurrent Puts to different keys and all reads
// race-free; retention itself is delegated to package hist.
package store

import (
	"errors"
	"sync"

	"ontology/hist"
)

// Sentinel errors are mutually distinct so callers can classify failures.
var (
	// ErrBadLimit is returned when the retention limit K is not positive.
	ErrBadLimit = errors.New("hist: retention limit K must be positive")
	// ErrEmptyKey is returned when an operation receives the empty key.
	ErrEmptyKey = errors.New("hist: key must not be empty")
	// ErrInvalidVersion is returned when GetAt receives version <= 0.
	ErrInvalidVersion = errors.New("hist: version must be positive")
)

// Store is a set of per-key bounded version histories.
type Store struct {
	mu sync.RWMutex
	k  int
	m  map[string]*hist.History
}

// New creates a Store retaining at most k versions per key.
func New(k int) (*Store, error) {
	if k <= 0 {
		return nil, ErrBadLimit
	}
	return &Store{k: k, m: make(map[string]*hist.History)}, nil
}

// K returns the configured retention limit.
func (s *Store) K() int { return s.k }

// Put appends value to key's history under the next version and returns
// that version. An empty key is rejected before any state is touched.
func (s *Store) Put(key, value string) (int64, error) {
	if key == "" {
		return 0, ErrEmptyKey
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	h := s.m[key]
	if h == nil {
		h = hist.New(s.k)
		s.m[key] = h
	}
	v := h.MaxVersion() + 1
	h.Append(v, value)
	return v, nil
}

// Get returns the latest value of key. Unknown key gives false.
func (s *Store) Get(key string) (string, bool, error) {
	if key == "" {
		return "", false, ErrEmptyKey
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if h := s.m[key]; h != nil {
		val, ok := h.Latest()
		return val, ok, nil
	}
	return "", false, nil
}

// GetAt returns the value of version v of key. It hits iff v is still in
// the retained window; cleaned/future versions and unknown keys miss
// (false, nil). v <= 0 is rejected before any state is touched.
func (s *Store) GetAt(key string, v int64) (string, bool, error) {
	if key == "" {
		return "", false, ErrEmptyKey
	}
	if v <= 0 {
		return "", false, ErrInvalidVersion
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if h := s.m[key]; h != nil {
		val, ok := h.At(v)
		return val, ok, nil
	}
	return "", false, nil
}

// Len returns the number of versions currently retained for key.
func (s *Store) Len(key string) int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if h := s.m[key]; h != nil {
		return h.Len()
	}
	return 0
}

// Retained lists the versions physically present for key, oldest-first.
// It exists for self-checks, not as part of the query surface.
func (s *Store) Retained(key string) ([]int64, error) {
	if key == "" {
		return nil, ErrEmptyKey
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	h := s.m[key]
	if h == nil {
		return nil, nil
	}
	es := h.All()
	vs := make([]int64, len(es))
	for i, e := range es {
		vs[i] = e.Version
	}
	return vs, nil
}

// Package store manages the version histories of many keys: version
// allocation and Put/Get/GetAt, delegating per-key retention to hist.
package store

import (
	"sync"

	"ontology/hist"
)

// Store keeps one bounded version history per key. It is safe for
// concurrent use: Puts on different keys and any mix of reads may run
// in parallel.
type Store struct {
	mu sync.RWMutex
	k  int
	m  map[string]*hist.Hist
}

// New returns a Store retaining at most k versions per key. k must be > 0.
func New(k int) *Store {
	return &Store{k: k, m: make(map[string]*hist.Hist)}
}

// Put appends value to key's history and returns the allocated version.
func (s *Store) Put(key, value string) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	h, ok := s.m[key]
	if !ok {
		h = hist.New(s.k)
		s.m[key] = h
	}
	return h.Put(value)
}

// Get returns the newest value of key.
func (s *Store) Get(key string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	h, ok := s.m[key]
	if !ok {
		return "", false
	}
	return h.Get()
}

// GetAt returns the value of version v of key, if still retained.
func (s *Store) GetAt(key string, v int64) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	h, ok := s.m[key]
	if !ok {
		return "", false
	}
	return h.GetAt(v)
}

// Len returns the number of versions currently retained for key.
func (s *Store) Len(key string) int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	h, ok := s.m[key]
	if !ok {
		return 0
	}
	return h.Len()
}

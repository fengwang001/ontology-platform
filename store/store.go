// Package store owns hot/cold tier membership, value retention, and the
// Put/Get/Value semantics incl. transparent promotion, eviction and
// capacity checks. It depends only on lru. Methods are goroutine-safe.
package store

import (
	"errors"
	"sort"
	"sync"

	"ontology/lru"
)

var (
	ErrBadCap   = errors.New("store: hotCap and maxCold must be positive")
	ErrEmptyKey = errors.New("store: empty key")
	ErrNotFound = errors.New("store: key not found")
	ErrColdFull = errors.New("store: cold tier is full")
)

// Store is a concurrency-safe hot/cold Key(string)->Value(int64) map.
type Store struct {
	mu              sync.Mutex
	hotCap, maxCold int
	hot             *lru.LRU
	cold            map[string]struct{}
	hotV, coldV     map[string]int64
}

// New creates a store; hotCap and maxCold must both be positive.
func New(hotCap, maxCold int) (*Store, error) {
	if hotCap <= 0 || maxCold <= 0 {
		return nil, ErrBadCap
	}
	return &Store{
		hotCap: hotCap, maxCold: maxCold, hot: lru.New(),
		cold: map[string]struct{}{}, hotV: map[string]int64{}, coldV: map[string]int64{},
	}, nil
}

// Put writes key=val at MRU, promoting a cold key, then evicts the hot
// LRU key once the hot tier exceeds hotCap. ErrColdFull leaves no trace.
func (s *Store) Put(key string, val int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if key == "" {
		return ErrEmptyKey
	}
	_, inCold := s.cold[key]
	// Only inserting a brand-new key grows the cold tier, so this is the
	// only overflow case; checking before any mutation makes it atomic.
	if !s.hot.Contains(key) && !inCold && s.hot.Len() == s.hotCap && len(s.cold) >= s.maxCold {
		return ErrColdFull
	}
	if inCold {
		delete(s.cold, key)
		delete(s.coldV, key)
	}
	s.hotV[key] = val
	s.hot.Touch(key)
	s.evict()
	return nil
}

// Get returns the value at MRU, transparently promoting a cold key and
// preserving its value. It never changes any stored value.
func (s *Store) Get(key string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if key == "" {
		return 0, ErrEmptyKey
	}
	if v, ok := s.hotV[key]; ok {
		s.hot.Touch(key)
		return v, nil
	}
	v, ok := s.coldV[key]
	if !ok {
		return 0, ErrNotFound
	}
	delete(s.cold, key)
	delete(s.coldV, key)
	s.hotV[key] = v // value retained across promotion
	s.hot.Touch(key)
	s.evict()
	return v, nil
}

// Value is a read-only probe: it neither advances the clock nor moves
// any key between tiers.
func (s *Store) Value(key string) (int64, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if v, ok := s.hotV[key]; ok {
		return v, true
	}
	v, ok := s.coldV[key]
	return v, ok
}

// HotKeys returns hot keys in MRU→LRU order.
func (s *Store) HotKeys() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hot.Keys()
}

// ColdKeys returns cold keys in ascending lexicographic order.
func (s *Store) ColdKeys() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.cold))
	for k := range s.cold {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// evict moves the hot LRU key to the cold tier retaining its value. The
// caller holds mu and has already guaranteed cold capacity.
func (s *Store) evict() {
	if s.hot.Len() <= s.hotCap {
		return
	}
	k, ok := s.hot.Evict()
	if !ok {
		return
	}
	s.cold[k] = struct{}{}
	s.coldV[k] = s.hotV[k]
	delete(s.hotV, k)
}

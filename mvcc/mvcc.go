// Package mvcc provides the global version counter, snapshot-isolated reads,
// the active-snapshot set, and old-version collection over per-key chains.
// A single RWMutex lets many readers (and snapshots/releases) proceed while
// writers are excluded; immutable chain nodes mean a reader never blocks on,
// and is never affected by, a write that landed after its read point.
package mvcc

import (
	"sync"

	"ontology/chain"
)

// Store holds the global version, per-key chains and active snapshots.
type Store struct {
	mu     sync.RWMutex
	t      int64
	keys   map[string]*chain.Chain
	active map[int64]struct{}
}

// New returns an empty store (global version starts at 0).
func New() *Store {
	return &Store{keys: map[string]*chain.Chain{}, active: map[int64]struct{}{}}
}

// Write prepends a new immutable version to key's chain, bumps the global
// version, and returns it. Old nodes are left in place (copy-on-write).
func (s *Store) Write(key, value string) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.t++
	ch := s.keys[key]
	if ch == nil {
		ch = chain.New()
		s.keys[key] = ch
	}
	ch.Prepend(value, s.t)
	return s.t
}

// Snapshot returns the current version as a read point and registers it.
func (s *Store) Snapshot() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.active[s.t] = struct{}{}
	return s.t
}

// Read returns the greatest version of key not newer than snap. Validation of
// snap is the api layer's job; here snap is assumed to be in range.
func (s *Store) Read(key string, snap int64) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock() // held through Visible: Collect/Write must not swap idx mid-read
	ch := s.keys[key]
	if ch == nil {
		return "", false
	}
	return ch.Visible(snap)
}

// Release removes a read point from the active set and reports whether it was
// registered (so the api layer can reject an inactive snapshot).
func (s *Store) Release(snap int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.active[snap]; !ok {
		return false
	}
	delete(s.active, snap)
	return true
}

// Active reports whether snap is a registered read point.
func (s *Store) Active(snap int64) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.active[snap]
	return ok
}

// T returns the current global version.
func (s *Store) T() int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.t
}

// Collect reclaims every non-head version V (version v, next-newer version
// vp) for which no active snapshot a satisfies v <= a < vp. Returns count.
func (s *Store) Collect() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, ch := range s.keys {
		n += ch.Collect(func(v, vp int64) bool {
			for a := range s.active {
				if v <= a && a < vp {
					return true
				}
			}
			return false
		})
	}
	return n
}

// Package ontology provides an in-memory equality index over entity
// attributes together with a selectivity estimator.
//
// The Store keeps a set of entities (ID + attribute map) and maintains
// exact equality indexes for a configured set of attributes. For indexed
// attributes it can answer lookup and estimation queries exactly; for
// other attributes estimation falls back to a deterministic-size sample
// with a guaranteed absolute error bound.
package ontology

import "sync"

// DefaultSampleSize is the number of rows examined when estimating the
// selectivity of an attribute that has no equality index.
const DefaultSampleSize = 64

// Store holds entities and their equality indexes. All methods are safe
// for concurrent use; a mutation is applied atomically so concurrent
// readers never observe a half-updated index.
type Store struct {
	mu         sync.RWMutex
	entities   map[string]map[string]any
	indexed    map[string]bool
	indexes    map[string]*attrIndex
	sampleSize int
}

// NewStore creates an empty Store that maintains equality indexes on the
// given attributes.
func NewStore(indexedAttrs ...string) *Store {
	s := &Store{
		entities:   make(map[string]map[string]any),
		indexed:    make(map[string]bool),
		indexes:    make(map[string]*attrIndex),
		sampleSize: DefaultSampleSize,
	}
	for _, attr := range indexedAttrs {
		s.indexed[attr] = true
		s.indexes[attr] = newAttrIndex()
	}
	return s
}

// SetSampleSize sets how many rows an estimation on a non-indexed
// attribute is allowed to examine. Values below 1 are ignored.
func (s *Store) SetSampleSize(n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if n >= 1 {
		s.sampleSize = n
	}
}

// TotalRows returns the number of entities currently stored.
func (s *Store) TotalRows() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.entities)
}

// Indexed reports whether attr has an equality index.
func (s *Store) Indexed(attr string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.indexed[attr]
}

// get returns a copy of the entity's attributes, or nil if absent.
// Callers must hold at least the read lock.
func (s *Store) get(id string) map[string]any {
	props, ok := s.entities[id]
	if !ok {
		return nil
	}
	out := make(map[string]any, len(props))
	for k, v := range props {
		out[k] = v
	}
	return out
}

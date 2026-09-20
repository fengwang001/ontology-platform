package ontology

import (
	"fmt"
	"sync"
)

// Store keeps entities in memory and maintains equality indexes on a
// configurable set of attributes. All methods are safe for concurrent use.
type Store struct {
	mu       sync.RWMutex
	entities map[string]map[string]any
	order    []string                                  // entity IDs in insertion order, for sampling
	pos      map[string]int                            // ID -> index in order
	indexed  map[string]bool                           // attributes that carry an equality index
	indexes  map[string]map[string]map[string]struct{} // attr -> valueKey -> ID set
}

// New creates a Store with equality indexes on the given attributes.
func New(indexedAttrs ...string) *Store {
	s := &Store{
		entities: make(map[string]map[string]any),
		pos:      make(map[string]int),
		indexed:  make(map[string]bool),
		indexes:  make(map[string]map[string]map[string]struct{}),
	}
	for _, a := range indexedAttrs {
		s.indexed[a] = true
		s.indexes[a] = make(map[string]map[string]struct{})
	}
	return s
}

// Upsert inserts or replaces the entity with the given ID. Repeated Upserts
// of the same ID still count as a single row. The attrs map is copied.
func (s *Store) Upsert(id string, attrs map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if old, ok := s.entities[id]; ok {
		s.removeFromIndexes(id, old)
	} else {
		s.pos[id] = len(s.order)
		s.order = append(s.order, id)
	}
	copied := make(map[string]any, len(attrs))
	for k, v := range attrs {
		copied[k] = v
	}
	s.entities[id] = copied
	s.addToIndexes(id, copied)
}

// Delete removes the entity and every index entry that referenced it.
func (s *Store) Delete(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	old, ok := s.entities[id]
	if !ok {
		return
	}
	s.removeFromIndexes(id, old)
	delete(s.entities, id)
	i := s.pos[id]
	last := s.order[len(s.order)-1]
	s.order[i] = last
	s.pos[last] = i
	s.order = s.order[:len(s.order)-1]
	delete(s.pos, id)
}

// Len reports the current number of entities.
func (s *Store) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.entities)
}

func (s *Store) addToIndexes(id string, attrs map[string]any) {
	for attr := range s.indexed {
		v, ok := attrs[attr]
		if !ok || v == nil {
			continue // missing and nil never enter the equality index
		}
		key := keyOf(v)
		bucket := s.indexes[attr][key]
		if bucket == nil {
			bucket = make(map[string]struct{})
			s.indexes[attr][key] = bucket
		}
		bucket[id] = struct{}{}
	}
}

func (s *Store) removeFromIndexes(id string, attrs map[string]any) {
	for attr := range s.indexed {
		v, ok := attrs[attr]
		if !ok || v == nil {
			continue
		}
		key := keyOf(v)
		bucket := s.indexes[attr][key]
		delete(bucket, id)
		if len(bucket) == 0 {
			delete(s.indexes[attr], key)
		}
	}
}

// keyOf maps a value to a canonical, type-aware string key so that, e.g.,
// the string "1" and the number 1 never collide in an index bucket.
func keyOf(v any) string {
	return fmt.Sprintf("%T:%#v", v, v)
}

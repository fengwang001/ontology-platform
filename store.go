// Package ontology provides an in-memory equality index over entity
// attributes together with selectivity estimation.
package ontology

import (
	"fmt"
	"sync"
)

// Store holds entities and equality indexes over a fixed set of
// attributes. All state lives in process memory. The zero value is
// not usable; construct one with NewStore.
type Store struct {
	mu sync.RWMutex

	// attrs is the ordered list of indexed attributes.
	attrs   []string
	attrSet map[string]bool

	// ents maps entity ID to its attribute map (owned copy).
	ents map[string]map[string]any
	// ids holds all live IDs in insertion order; pos maps ID to its
	// position in ids for O(1) deletion.
	ids []string
	pos map[string]int

	// index[attr][valueKey] = set of IDs whose attr equals the value.
	index map[string]map[string]map[string]struct{}
	// nilSet[attr] = set of IDs whose attr is present but nil.
	nilSet map[string]map[string]struct{}
	// present[attr] = set of IDs that have attr at all (nil or not).
	present map[string]map[string]struct{}
}

// NewStore creates a Store maintaining equality indexes on attrs.
func NewStore(attrs ...string) *Store {
	s := &Store{
		attrSet: make(map[string]bool, len(attrs)),
		ents:    make(map[string]map[string]any),
		pos:     make(map[string]int),
		index:   make(map[string]map[string]map[string]struct{}, len(attrs)),
		nilSet:  make(map[string]map[string]struct{}, len(attrs)),
		present: make(map[string]map[string]struct{}, len(attrs)),
	}
	for _, a := range attrs {
		if s.attrSet[a] {
			continue
		}
		s.attrSet[a] = true
		s.attrs = append(s.attrs, a)
		s.index[a] = make(map[string]map[string]struct{})
		s.nilSet[a] = make(map[string]struct{})
		s.present[a] = make(map[string]struct{})
	}
	return s
}

// RowCount returns the number of live entities.
func (s *Store) RowCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.ids)
}

// Indexed reports whether attr has an equality index.
func (s *Store) Indexed(attr string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.attrSet[attr]
}

// keyOf canonicalizes a non-nil value into a comparable index key.
// The boolean is false for nil, which is never indexed.
func keyOf(v any) (string, bool) {
	if v == nil {
		return "", false
	}
	return fmt.Sprintf("%T|%#v", v, v), true
}

// setAdd inserts id into the nested set, allocating as needed.
func setAdd(m map[string]map[string]struct{}, key, id string) {
	bucket, ok := m[key]
	if !ok {
		bucket = make(map[string]struct{})
		m[key] = bucket
	}
	bucket[id] = struct{}{}
}

// setRemove deletes id from the nested set, pruning empty buckets.
func setRemove(m map[string]map[string]struct{}, key, id string) {
	bucket, ok := m[key]
	if !ok {
		return
	}
	delete(bucket, id)
	if len(bucket) == 0 {
		delete(m, key)
	}
}

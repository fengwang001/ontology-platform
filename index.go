package ontology

import (
	"fmt"
	"sort"
)

// attrIndex is the per-attribute equality index. A value is indexed only
// when it is non-nil; explicit nils are tracked separately so IsNull can
// distinguish "attribute missing" from "attribute present but nil".
type attrIndex struct {
	buckets map[string]map[string]struct{} // value key -> set of IDs
	values  map[string]any                 // value key -> representative value
	present map[string]struct{}            // IDs with a non-nil value
	nilIDs  map[string]struct{}            // IDs whose value is explicitly nil
}

func newAttrIndex() *attrIndex {
	return &attrIndex{
		buckets: make(map[string]map[string]struct{}),
		values:  make(map[string]any),
		present: make(map[string]struct{}),
		nilIDs:  make(map[string]struct{}),
	}
}

// keyOf maps a non-nil attribute value to a deterministic, type-tagged
// string key. The second return value is false for nil, which is never
// indexed.
func keyOf(v any) (string, bool) {
	if v == nil {
		return "", false
	}
	return fmt.Sprintf("%T|%#v", v, v), true
}

// add records id under v. Callers must hold the write lock.
func (ix *attrIndex) add(id string, v any) {
	key, ok := keyOf(v)
	if !ok {
		ix.nilIDs[id] = struct{}{}
		return
	}
	bucket, ok := ix.buckets[key]
	if !ok {
		bucket = make(map[string]struct{})
		ix.buckets[key] = bucket
		ix.values[key] = v
	}
	bucket[id] = struct{}{}
	ix.present[id] = struct{}{}
}

// remove drops id from the value v. Callers must hold the write lock.
func (ix *attrIndex) remove(id string, v any) {
	key, ok := keyOf(v)
	if !ok {
		delete(ix.nilIDs, id)
		return
	}
	bucket, ok := ix.buckets[key]
	if !ok {
		return
	}
	delete(bucket, id)
	if len(bucket) == 0 {
		delete(ix.buckets, key)
		delete(ix.values, key)
	}
	delete(ix.present, id)
}

// Upsert inserts or replaces an entity. Re-upserting an existing ID
// replaces its previous index contributions exactly, so counts never
// inflate and no stale entries remain.
func (s *Store) Upsert(id string, props map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if old, ok := s.entities[id]; ok {
		s.removeFromIndexes(id, old)
	}
	stored := make(map[string]any, len(props))
	for k, v := range props {
		stored[k] = v
	}
	s.entities[id] = stored
	for attr, ix := range s.indexes {
		if v, ok := stored[attr]; ok {
			ix.add(id, v)
		}
	}
}

// Delete removes an entity and every index entry it contributed to.
func (s *Store) Delete(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	old, ok := s.entities[id]
	if !ok {
		return
	}
	s.removeFromIndexes(id, old)
	delete(s.entities, id)
}

// removeFromIndexes undoes the index contributions of props for id.
// Callers must hold the write lock.
func (s *Store) removeFromIndexes(id string, props map[string]any) {
	for attr, ix := range s.indexes {
		if v, ok := props[attr]; ok {
			ix.remove(id, v)
		}
	}
}

// Lookup returns the sorted IDs whose indexed attribute equals value.
// Missing attributes and nil values never match. The returned slice is a
// fresh copy and shares no state with the index.
func (s *Store) Lookup(attr string, value any) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ix, ok := s.indexes[attr]
	if !ok {
		return nil
	}
	key, ok := keyOf(value)
	if !ok {
		return nil
	}
	return sortedIDs(ix.buckets[key])
}

// DistinctValues returns how many different non-nil values the index on
// attr currently holds.
func (s *Store) DistinctValues(attr string) int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ix, ok := s.indexes[attr]
	if !ok {
		return 0
	}
	return len(ix.buckets)
}

// TotalEntries returns the total number of (value, ID) entries in the
// index on attr, i.e. the number of rows with a non-nil value for it.
func (s *Store) TotalEntries(attr string) int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ix, ok := s.indexes[attr]
	if !ok {
		return 0
	}
	return len(ix.present)
}

func sortedIDs(set map[string]struct{}) []string {
	if len(set) == 0 {
		return nil
	}
	ids := make([]string, 0, len(set))
	for id := range set {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

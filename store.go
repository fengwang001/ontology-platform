package ontology

import (
	"fmt"
	"sort"
	"sync"
)

// Inconsistency records one compensation (undo) step that could not be
// applied; the store is marked inconsistent and rejects further writes.
type Inconsistency struct {
	Step  string
	Cause string
}

// Store is the in-memory state: objects, relations, a mutation version
// counter and the consistency flag. All state lives in process memory.
type Store struct {
	mu           sync.RWMutex
	objects      map[string]*Object
	relations    map[string]*Relation
	version      uint64
	inconsistent bool
	failures     []Inconsistency
	undoFaultIDs map[string]bool
}

// NewStore returns an empty, consistent store.
func NewStore() *Store {
	return &Store{
		objects:      map[string]*Object{},
		relations:    map[string]*Relation{},
		undoFaultIDs: map[string]bool{},
	}
}

// Version is a monotonically increasing mutation counter, used to detect
// writes performed by read-only hooks.
func (s *Store) Version() uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.version
}

// Inconsistent reports whether a compensation step has failed.
func (s *Store) Inconsistent() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.inconsistent
}

// Inconsistencies returns every compensation step that failed, in order.
func (s *Store) Inconsistencies() []Inconsistency {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]Inconsistency(nil), s.failures...)
}

// InjectUndoFault forces the undo step touching id to fail. It exists so
// tests and operators can exercise the compensation-failure path.
func (s *Store) InjectUndoFault(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.undoFaultIDs[id] = true
}

func (s *Store) undoFault(id string) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.undoFaultIDs[id] {
		return fmt.Errorf("undo fault injected for %q", id)
	}
	return nil
}

func (s *Store) markInconsistent(step string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.inconsistent = true
	s.failures = append(s.failures, Inconsistency{Step: step, Cause: err.Error()})
}

// PutObject writes an object directly, bypassing transactions and undo
// tracking. It is an escape hatch: writes made from hooks are detected and
// fail the enclosing action.
func (s *Store) PutObject(o Object) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.inconsistent {
		return ErrInconsistent
	}
	cp := copyObject(&o)
	s.objects[o.ID] = &cp
	s.version++
	return nil
}

// DeleteObject removes an object directly; see PutObject for the contract.
func (s *Store) DeleteObject(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.inconsistent {
		return ErrInconsistent
	}
	delete(s.objects, id)
	s.version++
	return nil
}

// --- internal mutators, only reached through Tx or rollback ---

func (s *Store) addObject(o *Object) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.objects[o.ID] = o
	s.version++
}

func (s *Store) removeObject(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.objects, id)
	s.version++
}

func (s *Store) setProp(id, key string, val any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.objects[id].Props[key] = val
	s.version++
}

func (s *Store) delProp(id, key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.objects[id].Props, key)
	s.version++
}

func (s *Store) addRelation(r *Relation) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.relations[r.ID] = r
	s.version++
}

func (s *Store) removeRelation(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.relations, id)
	s.version++
}

// --- readers ---

func (s *Store) getObject(id string) (*Object, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	o, ok := s.objects[id]
	return o, ok
}

// GetObject returns a copy of the object with the given id.
func (s *Store) GetObject(id string) (Object, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	o, ok := s.objects[id]
	if !ok {
		return Object{}, false
	}
	return copyObject(o), true
}

// ListObjects returns copies of all objects, ordered by id.
func (s *Store) ListObjects() []Object {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ids := make([]string, 0, len(s.objects))
	for id := range s.objects {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]Object, 0, len(ids))
	for _, id := range ids {
		out = append(out, copyObject(s.objects[id]))
	}
	return out
}

// RelationsOf returns copies of every relation touching id, ordered by id.
func (s *Store) RelationsOf(id string) []Relation {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []Relation
	for _, r := range s.relations {
		if r.From == id || r.To == id {
			out = append(out, *r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// ObjectCount returns the number of stored objects.
func (s *Store) ObjectCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.objects)
}

// RelationCount returns the number of stored relations.
func (s *Store) RelationCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.relations)
}

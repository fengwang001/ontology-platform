package ontology

import "sync"

// Object is the only stored entity: an ID plus an integer counter.
type Object struct {
	ID    string
	Count int
}

// Store is an in-process object store. Once tainted it permanently rejects
// writes until Reset is called explicitly.
type Store struct {
	mu      sync.Mutex
	objects map[string]*Object
	tainted bool
	taintAt int
}

// NewStore returns an empty, clean store.
func NewStore() *Store {
	return &Store{objects: make(map[string]*Object)}
}

// Get returns a copy of the object and whether it exists.
func (s *Store) Get(id string) (Object, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	obj, ok := s.objects[id]
	if !ok {
		return Object{}, false
	}
	return *obj, true
}

// Put creates or overwrites the object. Writes are refused once tainted.
func (s *Store) Put(obj Object) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.tainted {
		return &TaintedError{FirstTaintedStep: s.taintAt}
	}
	cp := obj
	s.objects[obj.ID] = &cp
	return nil
}

// Add delta-adjusts the counter of an existing object. Refused when tainted.
func (s *Store) Add(id string, delta int) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.tainted {
		return 0, &TaintedError{FirstTaintedStep: s.taintAt}
	}
	obj, ok := s.objects[id]
	if !ok {
		obj = &Object{ID: id}
		s.objects[id] = obj
	}
	obj.Count += delta
	return obj.Count, nil
}

// Taint marks the store polluted at the given step. The earliest taint point
// wins; later calls never move it backwards.
func (s *Store) Taint(step int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.tainted || step < s.taintAt {
		s.tainted = true
		s.taintAt = step
	}
}

// Tainted reports the polluted state and the earliest polluting step.
func (s *Store) Tainted() (bool, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tainted, s.taintAt
}

// Reset clears all objects and the pollution flag.
// It is the only way to leave the tainted state.
func (s *Store) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.objects = make(map[string]*Object)
	s.tainted = false
	s.taintAt = 0
}

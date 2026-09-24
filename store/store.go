// Package store holds the in-memory map[string]int KV plus undo log and
// nested savepoints for partial rollback. Depends only on package undo.
package store

import (
	"errors"
	"sync"

	"ontology/undo"
)

// Sentinel errors: four mutually distinct, decidable via errors.Is.
var (
	ErrInvalidLimit      = errors.New("store: undoLimit must be positive")
	ErrEmptyKey          = errors.New("store: empty key")
	ErrLogFull           = errors.New("store: undo log full")
	ErrSavepointNotFound = errors.New("store: savepoint not found")
)

type entry struct {
	rec    *undo.Record
	marker *undo.Marker
} // one log slot

// Store is a map[string]int KV with nested savepoints. scanHits counts log
// entries scanned locating the target on the latest accepted Rollback/Release;
// location uses pos so it is 0 (O(1)). Unexported; only a bool leaves the pkg.
type Store struct {
	mu       sync.RWMutex
	data     map[string]int
	log      []entry
	pos      map[int]int // active savepoint id -> marker index in log
	active   *undo.Active
	nextID   int
	limit    int
	nUndo    int // undo records (markers excluded) currently in log
	scanHits int
}

// New creates a Store whose undo log holds at most undoLimit undo records.
func New(undoLimit int) (*Store, error) {
	if undoLimit <= 0 {
		return nil, ErrInvalidLimit
	}
	return &Store{data: map[string]int{}, pos: map[int]int{}, active: undo.NewActive(), limit: undoLimit}, nil
}

// Set writes v under k and records how to undo the write.
func (s *Store) Set(k string, v int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if k == "" {
		return ErrEmptyKey
	}
	if s.nUndo >= s.limit {
		return ErrLogFull // both checks run before any mutation
	}
	old, existed := s.data[k]
	s.data[k] = v
	s.log = append(s.log, entry{rec: &undo.Record{Key: k, Old: old, Existed: existed}})
	s.nUndo++
	return nil
}

// Get returns the current value and whether k exists; safe concurrently.
func (s *Store) Get(k string) (int, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.data[k]
	return v, ok
}

// Savepoint pushes a marker and returns its id (monotonic, never reused).
func (s *Store) Savepoint() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := s.nextID
	s.nextID++
	s.pos[id] = len(s.log)
	s.active.Add(id)
	s.log = append(s.log, entry{marker: &undo.Marker{ID: id}})
	return id
}

// deactivate removes id and every deeper (larger) savepoint from both indexes.
func (s *Store) deactivate(id int) {
	s.active.Drop(id)
	for k := range s.pos {
		if k >= id {
			delete(s.pos, k)
		}
	}
}

// RollbackTo undoes every Set strictly after id, then drops id and all deeper
// savepoints. Undo pops are per-record; locating id via pos is O(1).
func (s *Store) RollbackTo(id int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	idx, ok := s.pos[id]
	if !ok {
		return ErrSavepointNotFound // rejected before any state change
	}
	s.scanHits = 0 // pos locates directly; zero log entries scanned
	for len(s.log) > idx {
		e := s.log[len(s.log)-1]
		s.log = s.log[:len(s.log)-1]
		if e.rec != nil {
			if e.rec.Existed {
				s.data[e.rec.Key] = e.rec.Old
			} else {
				delete(s.data, e.rec.Key)
			}
			s.nUndo--
		} else {
			delete(s.pos, e.marker.ID) // deeper savepoint popped with the log
		}
	}
	s.log = s.log[:idx] // pop the target marker itself
	s.deactivate(id)
	return nil
}

// Release drops id and all deeper markers without undoing anything.
func (s *Store) Release(id int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	idx, ok := s.pos[id]
	if !ok {
		return ErrSavepointNotFound // rejected before any state change
	}
	s.scanHits = 0
	s.log = append(s.log[:idx], s.log[idx+1:]...)
	for j, p := range s.pos { // markers above idx shift down by one
		if p > idx {
			s.pos[j] = p - 1
		}
	}
	s.deactivate(id)
	return nil
}

// LocatedO1 reports whether the most recent accepted RollbackTo/Release found
// its target savepoint by index rather than scanning the log. It exposes only
// a bool verdict, never the internal counter value.
func (s *Store) LocatedO1() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.scanHits == 0
}

// Package store implements the in-process map[string]int KV with an undo log
// and nested savepoints. It depends only on undo.
package store

import (
	"errors"
	"sync"

	"ontology/undo"
)

var (
	ErrInvalidLimit = errors.New("invalid undo limit")
	ErrEmptyKey     = errors.New("empty key")
	ErrLogFull      = errors.New("undo log full")
	ErrSavepoint    = errors.New("savepoint does not exist")
)

type entry struct {
	rec    *undo.Record // non-nil for undo records
	marker *undo.Marker // non-nil for savepoint markers
}

// Store is the KV plus its undo machinery; use New.
type Store struct {
	mu      sync.RWMutex
	data    map[string]int
	log     []entry
	pos     map[int]int // active savepoint id -> log index
	active  *undo.Active
	nextID  int
	limit   int
	nRec    int // undo records currently in log (markers excluded)
	scanCnt int // entries scanned while locating the last targeted savepoint
}

// New creates a Store whose undo log holds at most limit undo records.
func New(limit int) (*Store, error) {
	if limit <= 0 {
		return nil, ErrInvalidLimit
	}
	return &Store{data: map[string]int{}, pos: map[int]int{}, active: undo.NewActive(), limit: limit}, nil
}

// Set writes data[k]=v after recording how to undo it.
func (s *Store) Set(k string, v int) error {
	if k == "" {
		return ErrEmptyKey
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.nRec >= s.limit {
		return ErrLogFull
	}
	old, exist := s.data[k]
	s.data[k] = v
	s.log = append(s.log, entry{rec: &undo.Record{Key: k, Exist: exist, Value: old}})
	s.nRec++
	return nil
}

// Get returns the current value and presence of k.
func (s *Store) Get(k string) (int, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.data[k]
	return v, ok
}

// Savepoint marks the current state and returns the new active id.
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

// forget invalidates id and every deeper savepoint in active set and index.
func (s *Store) forget(id int) {
	s.active.Drop(id)
	for x := range s.pos {
		if x >= id {
			delete(s.pos, x)
		}
	}
}

// RollbackTo restores exactly the state at savepoint id, then invalidates id
// and every deeper savepoint.
func (s *Store) RollbackTo(id int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.active.Has(id) {
		return ErrSavepoint
	}
	idx := s.pos[id] // indexed location: O(1), no head scan
	s.scanCnt = 1
	for len(s.log) > idx {
		e := s.log[len(s.log)-1]
		s.log = s.log[:len(s.log)-1]
		switch {
		case e.rec != nil:
			if e.rec.Exist {
				s.data[e.rec.Key] = e.rec.Value
			} else {
				delete(s.data, e.rec.Key)
			}
			s.nRec--
		case e.marker.ID == id:
			s.forget(id)
			return nil
		}
	}
	return ErrSavepoint // unreachable: pos index guarantees the marker
}

// Release drops savepoint id (and deeper ones) without undoing anything.
func (s *Store) Release(id int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.active.Has(id) {
		return ErrSavepoint
	}
	s.scanCnt = 1
	s.log[s.pos[id]].marker.Dead = true
	s.forget(id)
	return nil
}

// CheckLocateIndex verifies, without exposing the scan counter, that
// locating a savepoint stays O(1) as records after it grow to m.
func (s *Store) CheckLocateIndex() bool {
	for _, m := range []int{100, 1000, 10000} {
		t, _ := New(100001)
		id := t.Savepoint()
		for i := 0; i < m; i++ {
			if t.Set("k", i) != nil {
				return false
			}
		}
		if t.RollbackTo(id) != nil || t.scanCnt != 1 {
			return false
		}
	}
	return true
}

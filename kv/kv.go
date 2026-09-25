// Package kv holds committed key-value state, the global commit version,
// and retained read/write sets of committed transactions.
package kv

import "sync"

// Record is one committed transaction's retained footprint.
type Record struct {
	ReadSet  map[string]string
	WriteSet map[string]string
	Version  int
}

// Store is the committed state plus version and commit history.
type Store struct {
	mu      sync.RWMutex
	data    map[string]string
	version int
	records []Record
	// readers[key] reports whether some committed txn read key (for inConflict).
	readers map[string]bool
	// lastWriter[key] is the highest commit version that wrote key (for outConflict).
	lastWriter map[string]int
}

// New returns a Store seeded with init at version 0.
func New(init map[string]string) *Store {
	s := &Store{
		data:       map[string]string{},
		readers:    map[string]bool{},
		lastWriter: map[string]int{},
	}
	for k, v := range init {
		s.data[k] = v
	}
	return s
}

// Snapshot copies the committed state and returns it with the global version.
func (s *Store) Snapshot() (map[string]string, int) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	cp := make(map[string]string, len(s.data))
	for k, v := range s.data {
		cp[k] = v
	}
	return cp, s.version
}

// Committed returns a copy of the committed state.
func (s *Store) Committed() map[string]string {
	m, _ := s.Snapshot()
	return m
}

// Version returns the global commit version.
func (s *Store) Version() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.version
}

// ReadTouched reports whether any committed transaction read key.
func (s *Store) ReadTouched(key string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.readers[key]
}

// WrittenAfter reports whether key was committed at a version > v.
func (s *Store) WrittenAfter(key string, v int) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lastWriter[key] > v
}

// Apply commits ws with the given retained read set; returns the new version.
func (s *Store) Apply(rs, ws map[string]string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.version++
	for k, v := range ws {
		s.data[k] = v
		s.lastWriter[k] = s.version
	}
	for k := range rs {
		s.readers[k] = true
	}
	rc := Record{ReadSet: rs, WriteSet: ws, Version: s.version}
	s.records = append(s.records, rc)
	return s.version
}

// Records returns a copy of the committed-transaction history, in commit order.
func (s *Store) Records() []Record {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Record, len(s.records))
	copy(out, s.records)
	return out
}

// Package seq assigns stable sequence numbers to incoming changes and keeps an
// append-only per-key history ordered by sequence number. It depends on no
// other package in this module.
package seq

import "sync"

// Change is one CDC change as it arrives.
type Change struct {
	Key string
	Ver int64
	Val int64
}

// Stored is a Change together with its stable, globally ordered sequence number.
type Stored struct {
	Change
	SN int64
}

// Store hands out stable sequence numbers (1, 2, 3, ...) and stores each
// key's changes in arrival order, which is identical to ascending SN order.
type Store struct {
	mu   sync.Mutex
	next int64
	hist map[string][]Stored
}

// New creates an empty Store.
func New() *Store {
	return &Store{next: 1, hist: make(map[string][]Stored)}
}

// Next allocates and returns one stable sequence number.
func (s *Store) Next() int64 {
	s.mu.Lock()
	sn := s.next
	s.next++
	s.mu.Unlock()
	return sn
}

// Append records a change that has already been assigned sn. Appends happen in
// ascending SN order, so each per-key slice is always SN-sorted.
func (s *Store) Append(sn int64, c Change) {
	s.mu.Lock()
	s.hist[c.Key] = append(s.hist[c.Key], Stored{Change: c, SN: sn})
	s.mu.Unlock()
}

// Count returns how many changes have been stored for key.
func (s *Store) Count(key string) int {
	s.mu.Lock()
	n := len(s.hist[key])
	s.mu.Unlock()
	return n
}

// History returns a copy of key's changes in ascending SN order. The copy is
// independent of the store, so callers cannot mutate internal state.
func (s *Store) History(key string) []Stored {
	s.mu.Lock()
	src := s.hist[key]
	out := make([]Stored, len(src))
	copy(out, src)
	s.mu.Unlock()
	return out
}

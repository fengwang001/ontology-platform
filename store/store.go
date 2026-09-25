// Package store maintains the base snapshot and the append-only delta log,
// appends Set/Del entries, compacts automatically at threshold T, and serves
// merge-on-read with a merge-cost counter. It depends only on merge.
package store

import (
	"errors"
	"sort"
	"sync"
	"sync/atomic"

	"ontology/merge"
)

// Sentinel errors: the two failure modes are deliberately distinct.
var (
	ErrEmptyKey     = errors.New("store: key must not be empty")
	ErrBadThreshold = errors.New("store: T must be positive")
)

// Store is an in-process merge-on-read key-value store.
type Store struct {
	mu    sync.RWMutex
	t     int
	base  map[string]string
	delta []merge.Entry // arrival order; len(delta) < t after every write
	// lastReadCost is the number of delta entries the most recent Read
	// checked. Unexported; never exposed through the public API.
	lastReadCost atomic.Int64
}

// New creates a store with compaction threshold T (T must be positive).
func New(t int) (*Store, error) {
	if t <= 0 {
		return nil, ErrBadThreshold
	}
	return &Store{t: t, base: map[string]string{}}, nil
}

// Set appends a write entry for k=v.
func (s *Store) Set(k, v string) error { return s.append(merge.Entry{Key: k, Val: v}) }

// Del appends a delete tombstone for k.
func (s *Store) Del(k string) error { return s.append(merge.Entry{Key: k, Del: true}) }

// append validates the key, appends the entry, and compacts at threshold.
func (s *Store) append(e merge.Entry) error {
	if e.Key == "" {
		return ErrEmptyKey
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.delta = append(s.delta, e)
	if len(s.delta) >= s.t {
		s.compact()
	}
	return nil
}

// compact folds the whole delta into base in order, then clears it.
// Caller must hold s.mu for writing.
func (s *Store) compact() {
	for _, e := range s.delta {
		v, ok := s.base[e.Key]
		r := merge.Apply(merge.Result{Val: v, Ok: ok}, e)
		if r.Ok {
			s.base[e.Key] = r.Val
		} else {
			delete(s.base, e.Key) // tombstone leaves no empty-valued key
		}
	}
	s.delta = s.delta[:0]
}

// Read merges base[k] with every delta entry for k, head to tail.
func (s *Store) Read(k string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.base[k]
	r := merge.Result{Val: v, Ok: ok}
	n := 0
	for _, e := range s.delta {
		n++ // entries are not indexed by key: every one is checked
		if e.Key == k {
			r = merge.Apply(r, e)
		}
	}
	s.lastReadCost.Store(int64(n))
	return r.Val, r.Ok
}

// BaseKeys returns the keys present in the compacted base, sorted.
func (s *Store) BaseKeys() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	keys := make([]string, 0, len(s.base))
	for k := range s.base {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// DeltaLen returns the current delta length (always < T).
func (s *Store) DeltaLen() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.delta)
}

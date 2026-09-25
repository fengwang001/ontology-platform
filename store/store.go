// Package store maintains the base snapshot and the ordered delta log,
// appends Set/Del entries, auto-compacts, and serves merge-on-read.
// It depends only on the merge package.
package store

import (
	"errors"
	"sort"
	"sync"
	"sync/atomic"

	"ontology/merge"
)

// Sentinel errors; the two failure classes are intentionally distinct.
var (
	ErrEmptyKey     = errors.New("store: empty key")
	ErrBadThreshold = errors.New("store: compaction threshold T must be positive")
)

// Store is an in-process merge-on-read key/value store.
type Store struct {
	mu   sync.RWMutex
	t    int
	base map[string]string // only keys that currently exist
	delt []merge.Entry     // arrival order; len(delt) < t always

	// lastCost counts delta entries inspected by the most recent Read.
	// Unexported by design: it must never leave the package via the API.
	lastCost atomic.Int64
}

// New creates a store with positive compaction threshold T.
func New(t int) (*Store, error) {
	if t <= 0 {
		return nil, ErrBadThreshold
	}
	return &Store{t: t, base: make(map[string]string)}, nil
}

// Set appends a write entry, then compacts if the delta reached T.
func (s *Store) Set(k, v string) error {
	if k == "" {
		return ErrEmptyKey
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.delt = append(s.delt, merge.Entry{Key: k, Val: v})
	s.compactLocked()
	return nil
}

// Del appends a delete tombstone, then compacts if the delta reached T.
func (s *Store) Del(k string) error {
	if k == "" {
		return ErrEmptyKey
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.delt = append(s.delt, merge.Entry{Key: k, Del: true})
	s.compactLocked()
	return nil
}

// compactLocked folds the whole delta into base in order and clears it.
// Tombstones remove the key; nothing ever stores an "empty present" value.
func (s *Store) compactLocked() {
	if len(s.delt) < s.t {
		return
	}
	for _, e := range s.delt {
		if e.Del {
			delete(s.base, e.Key)
		} else {
			s.base[e.Key] = e.Val
		}
	}
	s.delt = s.delt[:0]
}

// Read merges base[k] with every delta entry for k, head to tail.
// Merge cost is the total delta length scanned, not just matching entries.
func (s *Store) Read(k string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	s.lastCost.Store(int64(len(s.delt)))
	res := merge.Result{}
	if v, ok := s.base[k]; ok {
		res = merge.Result{Val: v, OK: true}
	}
	for _, e := range s.delt {
		if e.Key == k {
			res = merge.Apply(res, e)
		}
	}
	return res.Val, res.OK
}

// BaseKeys returns a sorted snapshot copy of base keys.
func (s *Store) BaseKeys() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]string, 0, len(s.base))
	for k := range s.base {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// DeltaLen returns the current delta length (always < T).
func (s *Store) DeltaLen() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.delt)
}

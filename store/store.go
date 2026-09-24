// Package store simulates the target storage: an in-memory map with a
// unique-key constraint, idempotent puts, and injectable write failures.
package store

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// ErrConflict reports a put whose key exists with a different value.
var ErrConflict = errors.New("store: conflicting value for key")

// Store is a concurrency-safe key/value store with instrumentation.
type Store struct {
	mu       sync.Mutex
	data     map[string]string
	failAt   int64 // 1-based Put call index that fails once; <=0 disables
	calls    int64
	accesses int64 // Get/Visit access counter for complexity assertions
}

// New returns an empty store.
func New() *Store { return &Store{data: make(map[string]string)} }

// FailAt makes the call-index-th Put (1-based, cumulative) fail once.
func (s *Store) FailAt(call int64) {
	s.mu.Lock()
	s.failAt = call
	s.mu.Unlock()
}

// Put writes key=val. Rewriting the same value is an idempotent no-op
// reporting existed=true; a different value for an existing key conflicts.
func (s *Store) Put(key, val string) (existed bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	if s.calls == s.failAt {
		return false, errors.New("store: injected write failure")
	}
	if old, ok := s.data[key]; ok {
		if old != val {
			return false, fmt.Errorf("%w: %s", ErrConflict, key)
		}
		return true, nil
	}
	s.data[key] = val
	return false, nil
}

// Get returns the value for key.
func (s *Store) Get(key string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.accesses++
	v, ok := s.data[key]
	return v, ok
}

// Visit invokes fn for every entry while holding the store lock.
func (s *Store) Visit(fn func(k, v string)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, v := range s.data {
		s.accesses++
		fn(k, v)
	}
}

// Accesses returns the cumulative Get/Visit access count.
func (s *Store) Accesses() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.accesses
}

// ResetAccesses zeroes the access counter.
func (s *Store) ResetAccesses() {
	s.mu.Lock()
	s.accesses = 0
	s.mu.Unlock()
}

// Len returns the entry count.
func (s *Store) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.data)
}

// Bytes is a deterministic serialization (sorted by key) used to compare
// store contents byte for byte across runs.
func (s *Store) Bytes() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	keys := make([]string, 0, len(s.data))
	for k := range s.data {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		fmt.Fprintf(&b, "%s=%s\n", strconv.Quote(k), strconv.Quote(s.data[k]))
	}
	return []byte(b.String())
}

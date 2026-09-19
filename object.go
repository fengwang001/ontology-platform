// Package ontology implements an in-memory, string-keyed object collection
// with cursor-based snapshot traversal.
//
// This package intentionally contains only storage primitives and the
// paginating scanner: there are no links, actions, permissions or transports.
package ontology

import (
	"sort"
	"sync"
)

// Object is the only stored entity: a string primary key plus one int field.
type Object struct {
	Key   string
	Value int
}

// Store is a thread-safe collection kept ordered by key.
// The write lock is held only for the duration of the in-memory mutation;
// active scans are notified after the lock has been released so that writes
// are never blocked by traversals.
type Store struct {
	mu       sync.RWMutex
	data     map[string]int
	secret   []byte
	sessions struct {
		mu   sync.Mutex
		byID map[uint64]*Session
	}
}

// NewStore creates an empty collection.
func NewStore() *Store {
	s := &Store{data: make(map[string]int)}
	s.secret = newSecret()
	s.sessions.byID = make(map[uint64]*Session)
	return s
}

// Put inserts or replaces an object. It reports whether a new key was
// inserted (true) or an existing key was replaced (false).
func (s *Store) Put(obj Object) (inserted bool) {
	s.mu.Lock()
	_, existed := s.data[obj.Key]
	s.data[obj.Key] = obj.Value
	s.mu.Unlock()

	if !existed {
		s.notifyInsert(obj.Key)
	}
	return !existed
}

// Delete removes a key. It reports whether the key existed.
func (s *Store) Delete(key string) (deleted bool) {
	s.mu.Lock()
	if _, ok := s.data[key]; !ok {
		s.mu.Unlock()
		return false
	}
	delete(s.data, key)
	s.mu.Unlock()

	s.notifyDelete(key)
	return true
}

// Get returns the current value of a key.
func (s *Store) Get(key string) (Object, bool) {
	s.mu.RLock()
	v, ok := s.data[key]
	s.mu.RUnlock()
	return Object{Key: key, Value: v}, ok
}

// Len returns the number of stored objects.
func (s *Store) Len() int {
	s.mu.RLock()
	n := len(s.data)
	s.mu.RUnlock()
	return n
}

// snapshot returns the current keys in sorted order.
func (s *Store) snapshot() []string {
	s.mu.RLock()
	keys := make([]string, 0, len(s.data))
	for k := range s.data {
		keys = append(keys, k)
	}
	s.mu.RUnlock()
	sort.Strings(keys)
	return keys
}

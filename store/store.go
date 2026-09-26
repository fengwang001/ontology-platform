// Package store implements a concurrent, multi-version key-value store.
// Every Set bumps a monotonic version watermark; readers may ask for the
// value visible at any past version.
package store

import (
	"sort"
	"sync"
)

// Entry is one versioned value of a key.
type Entry struct {
	Version uint64
	Value   []byte
}

// WriteHook is called under the store write lock just before an overwrite
// becomes visible. oldVersion is 0 when the key did not exist before.
type WriteHook func(key string, oldValue []byte, oldVersion, newVersion uint64)

// Store is a multi-version key-value store safe for concurrent use.
type Store struct {
	mu      sync.RWMutex
	version uint64
	data    map[string][]Entry // per key, ascending by Version
	hooks   []WriteHook
}

// New returns an empty store.
func New() *Store {
	return &Store{data: make(map[string][]Entry)}
}

// AddHook registers a write hook (e.g. the snapshot manager's COW trigger).
func (s *Store) AddHook(h WriteHook) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hooks = append(s.hooks, h)
}

// Set writes value and returns the new store version. Empty keys and empty
// values are legal. The value is copied.
func (s *Store) Set(key string, value []byte) uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.version++
	var oldValue []byte
	var oldVersion uint64
	if hist := s.data[key]; len(hist) > 0 {
		last := hist[len(hist)-1]
		oldValue, oldVersion = last.Value, last.Version
	}
	for _, h := range s.hooks {
		h(key, oldValue, oldVersion, s.version)
	}
	cp := append([]byte(nil), value...)
	s.data[key] = append(s.data[key], Entry{Version: s.version, Value: cp})
	return s.version
}

// Version returns the current version watermark.
func (s *Store) Version() uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.version
}

// Get returns the latest value of key.
func (s *Store) Get(key string) ([]byte, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	hist := s.data[key]
	if len(hist) == 0 {
		return nil, false
	}
	return append([]byte(nil), hist[len(hist)-1].Value...), true
}

// GetAt returns the value of key visible at the given version, i.e. the
// newest entry with Version <= version.
func (s *Store) GetAt(key string, version uint64) ([]byte, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	hist := s.data[key]
	i := sort.Search(len(hist), func(i int) bool { return hist[i].Version > version })
	if i == 0 {
		return nil, false
	}
	return append([]byte(nil), hist[i-1].Value...), true
}

// KeysAt returns, in sorted order, the keys that exist at the given version.
func (s *Store) KeysAt(version uint64) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	keys := make([]string, 0, len(s.data))
	for k, hist := range s.data {
		if len(hist) > 0 && hist[0].Version <= version {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	return keys
}

// Package store provides a versioned, concurrency-safe key-value store.
package store

import (
	"sort"
	"sync"
	"sync/atomic"
)

// Snapshot is the interface the store uses to retain overwritten values.
type Snapshot interface {
	Version() uint64
	Retain(key string, value []byte, version uint64)
}

// Stats holds non-export counters for resource accounting.
type Stats struct {
	Reads   atomic.Uint64
	Retains atomic.Uint64
}

type entry struct {
	value   []byte
	version uint64
	deleted bool
}

// Store is a versioned key-value store.
type Store struct {
	mu      sync.Mutex
	data    map[string]entry
	version uint64
	snaps   []Snapshot
	Stats   Stats
}

// New returns an empty store.
func New() *Store {
	return &Store{data: make(map[string]entry)}
}

// Put sets key to value and bumps the version.
func (s *Store) Put(key string, value []byte) {
	s.mu.Lock()
	s.version++
	ver := s.version
	if old, ok := s.data[key]; ok {
		for _, snap := range s.snaps {
			if snap.Version() >= old.version {
				snap.Retain(key, old.value, old.version)
				s.Stats.Retains.Add(1)
			}
		}
	}
	cp := make([]byte, len(value))
	copy(cp, value)
	s.data[key] = entry{value: cp, version: ver}
	s.mu.Unlock()
}

// Delete removes key, recording a tombstone.
func (s *Store) Delete(key string) {
	s.mu.Lock()
	s.version++
	ver := s.version
	if old, ok := s.data[key]; ok && !old.deleted {
		for _, snap := range s.snaps {
			if snap.Version() >= old.version {
				snap.Retain(key, old.value, old.version)
				s.Stats.Retains.Add(1)
			}
		}
	}
	s.data[key] = entry{version: ver, deleted: true}
	s.mu.Unlock()
}

// GetAt returns the value visible at version ver.
func (s *Store) GetAt(key string, ver uint64) ([]byte, bool) {
	s.mu.Lock()
	e, ok := s.data[key]
	s.mu.Unlock()
	if !ok || e.deleted || e.version > ver {
		return nil, false
	}
	cp := make([]byte, len(e.value))
	copy(cp, e.value)
	return cp, true
}

// KeysAt returns the sorted keys present at version ver.
func (s *Store) KeysAt(ver uint64) []string {
	s.mu.Lock()
	keys := make([]string, 0, len(s.data))
	for k, e := range s.data {
		if !e.deleted && e.version <= ver {
			keys = append(keys, k)
		}
	}
	s.mu.Unlock()
	sort.Strings(keys)
	return keys
}

// Current returns the latest version.
func (s *Store) Current() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.version
}

// Attach registers a snapshot for copy-on-write retention.
func (s *Store) Attach(snap Snapshot) {
	s.mu.Lock()
	s.snaps = append(s.snaps, snap)
	s.mu.Unlock()
}

// Detach unregisters a snapshot.
func (s *Store) Detach(snap Snapshot) {
	s.mu.Lock()
	for i, other := range s.snaps {
		if other == snap {
			s.snaps = append(s.snaps[:i], s.snaps[i+1:]...)
			break
		}
	}
	s.mu.Unlock()
}

// Package store implements a versioned in-memory key-value store with
// copy-on-write retention for open snapshots.
package store

import (
	"sync"
	"sync/atomic"
)

// entry is a retained pre-overwrite state of a key: val/existed describe the
// value visible until version ver (exclusive).
type entry struct {
	ver     uint64
	val     []byte
	existed bool
}

// Store is a concurrent-safe versioned key-value store.
type Store struct {
	mu        sync.RWMutex
	data      map[string][]byte
	created   map[string]uint64
	version   uint64
	retained  map[string][]entry
	snapshots map[uint64]int // open snapshot watermarks -> refcount
	reads     atomic.Int64
}

// New returns an empty Store.
func New() *Store {
	return &Store{
		data:      make(map[string][]byte),
		created:   make(map[string]uint64),
		retained:  make(map[string][]entry),
		snapshots: make(map[uint64]int),
	}
}

// Put sets key to val and returns the new store version.
func (s *Store) Put(key string, val []byte) uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.version++
	if len(s.snapshots) > 0 {
		old, existed := s.data[key]
		s.retained[key] = append(s.retained[key], entry{ver: s.version, val: old, existed: existed})
		s.gcKey(key)
	}
	cp := make([]byte, len(val))
	copy(cp, val)
	s.data[key] = cp
	if _, ok := s.created[key]; !ok {
		s.created[key] = s.version
	}
	return s.version
}

// Get returns the current value of key.
func (s *Store) Get(key string) ([]byte, bool) {
	s.reads.Add(1)
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.data[key]
	return v, ok
}

// Version returns the current store version.
func (s *Store) Version() uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.version
}

// ReadAt returns the value of key visible at version w.
func (s *Store) ReadAt(w uint64, key string) ([]byte, bool) {
	s.reads.Add(1)
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, e := range s.retained[key] {
		if e.ver > w {
			return e.val, e.existed
		}
	}
	v, ok := s.data[key]
	return v, ok
}

// KeysAt returns all keys created at or before version w.
func (s *Store) KeysAt(w uint64) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	keys := make([]string, 0, len(s.data))
	for k, cv := range s.created {
		if cv <= w {
			keys = append(keys, k)
		}
	}
	return keys
}

// OpenSnapshot registers a snapshot at the current version and returns it.
func (s *Store) OpenSnapshot() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.snapshots[s.version]++
	return s.version
}

// CloseSnapshot unregisters the snapshot at version w and releases retained
// values no snapshot can still observe.
func (s *Store) CloseSnapshot(w uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.snapshots[w] > 1 {
		s.snapshots[w]--
	} else {
		delete(s.snapshots, w)
	}
	for k := range s.retained {
		s.gcKey(k)
	}
}

// gcKey drops retained entries of key whose visibility interval
// [prevVer, ver) contains no open snapshot watermark.
func (s *Store) gcKey(key string) {
	ents := s.retained[key]
	kept := ents[:0]
	var prev uint64
	for _, e := range ents {
		needed := false
		for w := range s.snapshots {
			if prev <= w && w < e.ver {
				needed = true
				break
			}
		}
		if needed {
			kept = append(kept, e)
		}
		prev = e.ver
	}
	if len(kept) == 0 {
		delete(s.retained, key)
		return
	}
	s.retained[key] = kept
}

// RetainedCount returns the number of retained copy-on-write values.
func (s *Store) RetainedCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	n := 0
	for _, ents := range s.retained {
		n += len(ents)
	}
	return n
}

// ReadCount returns the total number of Get/ReadAt calls.
func (s *Store) ReadCount() int64 { return s.reads.Load() }

// ResetReadCount resets the read counter to zero.
func (s *Store) ResetReadCount() { s.reads.Store(0) }

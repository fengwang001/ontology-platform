// Package snapshot provides read-only consistent views of a store.
package snapshot

import (
	"errors"
	"sync"

	"ontology/store"
)

// ErrClosed is returned when a closed snapshot is used.
var ErrClosed = errors.New("snapshot: closed")

// Snapshot is a read-only view of a Store at a fixed version watermark.
type Snapshot struct {
	st     *store.Store
	ver    uint64
	mu     sync.Mutex
	closed bool
}

// Open registers a snapshot at the store's current version.
func Open(st *store.Store) *Snapshot {
	return &Snapshot{st: st, ver: st.OpenSnapshot()}
}

// Version returns the snapshot's version watermark.
func (s *Snapshot) Version() uint64 { return s.ver }

// Get returns the value of key visible at the snapshot's watermark.
func (s *Snapshot) Get(key string) ([]byte, bool, error) {
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return nil, false, ErrClosed
	}
	v, ok := s.st.ReadAt(s.ver, key)
	return v, ok, nil
}

// Keys returns all keys visible at the snapshot's watermark.
func (s *Snapshot) Keys() ([]string, error) {
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return nil, ErrClosed
	}
	return s.st.KeysAt(s.ver), nil
}

// Close releases the snapshot and all values retained for it. It is safe to
// call multiple times and concurrent with reads; in-flight readers either
// finish before the close or observe ErrClosed afterwards.
func (s *Snapshot) Close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	s.mu.Unlock()
	s.st.CloseSnapshot(s.ver)
}

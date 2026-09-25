// Package snapshot provides consistent read-only snapshot handles over a store.
package snapshot

import (
	"errors"
	"sync"
	"sync/atomic"

	"ontology/store"
)

// ErrClosed is returned by reads on a closed snapshot.
var ErrClosed = errors.New("snapshot: closed")

// Source is the versioned store a snapshot reads from.
type Source interface {
	GetAt(key string, ver uint64) ([]byte, bool)
	KeysAt(ver uint64) []string
	Current() uint64
	Attach(snap store.Snapshot)
	Detach(snap store.Snapshot)
}

type retained struct {
	value   []byte
	version uint64
}

// Snapshot is a read-only consistent view at a fixed version.
type Snapshot struct {
	src      Source
	ver      uint64
	mu       sync.Mutex
	retained map[string]retained
	closed   bool
	wg       sync.WaitGroup
	reads    atomic.Uint64
}

// Take creates a snapshot of src at its current version.
func Take(src Source) *Snapshot {
	s := &Snapshot{src: src, ver: src.Current(), retained: make(map[string]retained)}
	src.Attach(s)
	return s
}

// Version returns the snapshot water-mark version.
func (s *Snapshot) Version() uint64 { return s.ver }

// Retain implements store.Snapshot; called by the store before overwrite.
func (s *Snapshot) Retain(key string, value []byte, version uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	if _, ok := s.retained[key]; ok {
		return
	}
	cp := make([]byte, len(value))
	copy(cp, value)
	s.retained[key] = retained{value: cp, version: version}
}

// Get returns the snapshot-visible value of key.
func (s *Snapshot) Get(key string) ([]byte, bool, error) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, false, ErrClosed
	}
	s.wg.Add(1)
	s.mu.Unlock()
	defer s.wg.Done()
	s.reads.Add(1)
	s.mu.Lock()
	r, ok := s.retained[key]
	s.mu.Unlock()
	if ok {
		cp := make([]byte, len(r.value))
		copy(cp, r.value)
		return cp, true, nil
	}
	v, ok := s.src.GetAt(key, s.ver)
	return v, ok, nil
}

// Keys returns the sorted keys visible in the snapshot.
func (s *Snapshot) Keys() ([]string, error) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, ErrClosed
	}
	s.wg.Add(1)
	s.mu.Unlock()
	defer s.wg.Done()
	return s.src.KeysAt(s.ver), nil
}

// RetainedCount reports how many old values are retained for this snapshot.
func (s *Snapshot) RetainedCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.retained)
}

// Reads reports how many Get calls were served.
func (s *Snapshot) Reads() uint64 { return s.reads.Load() }

// Close releases the snapshot: in-flight reads finish, then all
// retained values are dropped and the store stops retaining for us.
func (s *Snapshot) Close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	s.mu.Unlock()
	s.wg.Wait()
	s.src.Detach(s)
	s.mu.Lock()
	s.retained = make(map[string]retained)
	s.mu.Unlock()
}

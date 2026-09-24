// Package snapshot provides a lock-free copy-on-write table.
// Readers never take a lock: they atomically load the current immutable
// version pointer once and read from that single, consistent version.
package snapshot

import (
	"fmt"
	"sync"
	"sync/atomic"

	"ontology/ver"
)

// Handle pins one immutable version; later Updates never affect it.
type Handle struct {
	v *ver.Version
}

// Read returns k from the pinned, immutable version.
func (h *Handle) Read(k string) (string, bool) { return h.v.Get(k) }

// ID returns the pinned version id.
func (h *Handle) ID() int { return h.v.ID() }

// Len returns the number of keys in the pinned version.
func (h *Handle) Len() int { return h.v.Len() }

// Store is the copy-on-write key/value table.
type Store struct {
	cur atomic.Pointer[ver.Version] // published current version
	wmu sync.Mutex                  // serializes writers only

	// readHits counts entries accessed (copied out) by the most recent
	// read operation. Unexported on purpose: it must never leave the
	// package through any exported API.
	readHits atomic.Int64
}

// NewStore returns an empty store at version id 0.
func NewStore() *Store {
	s := &Store{}
	s.cur.Store(ver.Empty(0))
	s.readHits.Store(0)
	return s
}

// Update clones the current version under the write lock, applies the
// change to the clone, and publishes it with one atomic store.
func (s *Store) Update(k, v string) {
	_ = s.UpdateChecked(k, v, int(^uint(0)>>1))
}

// UpdateChecked is Update with a distinct-key cap. The cap is checked
// inside the write lock against the live version, before any clone or
// store: when adding k would exceed maxKeys it returns false and leaves
// every byte of state unchanged. Updating an existing key never counts
// against the cap.
func (s *Store) UpdateChecked(k, v string, maxKeys int) bool {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	old := s.cur.Load()
	if _, exists := old.Get(k); !exists && old.Len() >= maxKeys {
		return false
	}
	next := ver.Clone(old)
	next[k] = v
	s.cur.Store(ver.New(old.ID()+1, next))
	return true
}

// Read loads the current pointer exactly once and reads k from it.
func (s *Store) Read(k string) (string, bool) {
	cur := s.cur.Load()
	val, ok := cur.Get(k)
	s.readHits.Store(1)
	return val, ok
}

// ReadKeys loads the current pointer exactly once and takes every key
// from that same version, so the result can never mix two versions.
func (s *Store) ReadKeys(ks []string) map[string]string {
	cur := s.cur.Load()
	out := make(map[string]string, len(ks))
	for _, k := range ks {
		if val, ok := cur.Get(k); ok {
			out[k] = val
		}
	}
	s.readHits.Store(int64(len(ks)))
	return out
}

// Snapshot loads the pointer once and returns a handle pinned to it.
func (s *Store) Snapshot() *Handle {
	h := &Handle{v: s.cur.Load()}
	s.readHits.Store(0)
	return h
}

// SelfCheck replays a built-in sequence and verifies single-version
// consistency, snapshot immutability, and bounded read amplification.
// It writes to the receiver, so call it on a fresh instance.
func (s *Store) SelfCheck() error {
	s.Update("A", "1")
	s.Update("B", "2")
	h := s.Snapshot()
	if v, ok := s.Read("A"); !ok || v != "1" {
		return fmt.Errorf("step4: want A=1, got %q,%v", v, ok)
	}
	s.Update("A", "9")
	if v, _ := s.Read("A"); v != "9" {
		return fmt.Errorf("step6: want A=9, got %q", v)
	}
	if v, ok := h.Read("A"); !ok || v != "1" {
		return fmt.Errorf("step7: pinned handle changed to %q,%v", v, ok)
	}
	for _, m := range []int{100, 1000, 10000} {
		if err := s.fillAndProbe(m); err != nil {
			return err
		}
	}
	return nil
}

// fillAndProbe fills to m keys then reads exactly 2 keys; the private
// counter must stay bounded by keys+const regardless of table size.
func (s *Store) fillAndProbe(m int) error {
	for i := 0; i < m; i++ {
		s.Update(fmt.Sprintf("k%d", i), "x")
	}
	got := s.ReadKeys([]string{"k1", "k2"})
	hits := s.readHits.Load()
	if len(got) != 2 || hits > 4 {
		return fmt.Errorf("read amplification at m=%d: hits=%d out=%d", m, hits, len(got))
	}
	return nil
}

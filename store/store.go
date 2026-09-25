package store

import (
	"sort"
	"sync"
)

// cell is one versioned value on a key's version chain.
// value == nil means the key did not exist at that version (tombstone);
// an empty non-nil slice means a legitimately empty value.
type cell struct {
	ver   uint64
	value []byte
}

// Store is a concurrently writable key/value store with per-key version
// chains. Snapshots register a version watermark; superseded cells that may
// still be visible to an active snapshot are retained until pruned.
type Store struct {
	mu       sync.Mutex
	ver      uint64
	data     map[string]cell    // newest cell per key
	hist     map[string][]cell  // superseded cells, ascending version
	oldestFn func() (uint64, bool)
}

// New creates an empty store. bound returns the oldest active snapshot
// version and whether any snapshot is active; it is consulted under the
// store lock.
func New(bound func() (uint64, bool)) *Store {
	return &Store{data: map[string]cell{}, hist: map[string][]cell{}, oldestFn: bound}
}

// Put installs value at a new version, retaining the previous cell when an
// active snapshot may still need it.
func (s *Store) Put(key string, value []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ver++
	cur := s.data[key]
	if bound, ok := s.oldest(); ok && cur.ver > 0 && cur.ver <= bound {
		s.hist[key] = append(s.hist[key], cur)
	}
	stored := value
	if len(value) > 0 {
		stored = append([]byte(nil), value...)
	}
	s.data[key] = cell{ver: s.ver, value: stored}
	s.prune(key)
}

// Delete records a tombstone at a new version.
func (s *Store) Delete(key string) {
	s.Put(key, nil)
}

// Latest returns the current value; ok is false when the key does not exist.
func (s *Store) Latest(key string) ([]byte, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cur := s.data[key]
	if cur.ver == 0 || cur.value == nil {
		return nil, false
	}
	return append([]byte(nil), cur.value...), true
}

// At returns the value visible at version v.
func (s *Store) At(key string, v uint64) ([]byte, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.lookup(key, v)
	if c == nil || c.value == nil {
		return nil, false
	}
	return append([]byte(nil), c.value...), true
}

// Keys returns all keys existing at version v in ascending order.
func (s *Store) Keys(v uint64) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []string{}
	for k, cur := range s.data {
		c := &cur
		if c.ver > v {
			c = s.lookup(k, v)
		}
		if c != nil && c.value != nil {
			out = append(out, k)
		}
}
	sort.Strings(out)
	return out
}

// RetainedKeys counts keys that currently hold a retained (superseded) cell.
func (s *Store) RetainedKeys() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, h := range s.hist {
		if len(h) > 0 {
			n++
		}
	}
	return n
}

// Prune drops retained cells no longer visible to the oldest active snapshot.
func (s *Store) Prune() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k := range s.hist {
		s.prune(k)
	}
}

func (s *Store) oldest() (uint64, bool) {
	if s.oldestFn == nil {
		return 0, false
	}
	return s.oldestFn()
}

// lookup must be called with mu held.
func (s *Store) lookup(key string, v uint64) *cell {
	cur := s.data[key]
	if cur.ver > 0 && cur.ver <= v {
		return &cur
	}
	h := s.hist[key]
	i := sort.Search(len(h), func(i int) bool { return h[i].ver > v }) - 1
	if i >= 0 {
		return &h[i]
	}
	return nil
}

// prune must be called with mu held; it keeps only the newest retained cell
// still visible to the oldest active snapshot.
func (s *Store) prune(key string) {
	bound, ok := s.oldest()
	h := s.hist[key]
	if !ok {
		if len(h) != 0 {
			delete(s.hist, key)
		}
		return
	}
	i := len(h) - 1
	for i >= 0 && h[i].ver > bound {
		i--
	}
	if i < 0 {
		delete(s.hist, key)
	} else {
		s.hist[key] = h[i : i+1]
	}
}

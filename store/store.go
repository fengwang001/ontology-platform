// Package store implements the hot in-memory layer over an authoritative
// unbounded cold "disk" layer, with transparent promotion and LRU eviction.
// It depends only on package tier.
package store

import (
	"errors"
	"fmt"
	"sort"
	"sync"

	"ontology/tier"
)

// evictionScanBound is the m-independent cap on entries inspected to pick one
// victim: an ordered index yields the candidate directly.
const evictionScanBound = 2

// Store is a two-layer key/value state: cold is the authoritative full copy;
// hot holds at most memCap most recently accessed keys.
type Store struct {
	mu             sync.Mutex
	memCap         int
	clock          int64 // monotonic access clock
	hot            map[string]string
	cold           map[string]string // authoritative full copy (simulated disk)
	idx            *tier.Index
	diskReads      int
	lastEvictScans int // unexported: hot entries inspected at the last eviction
}

// New creates a Store whose hot layer holds at most memCap keys (memCap >= 1).
func New(memCap int) *Store {
	return &Store{
		memCap: memCap,
		hot:    make(map[string]string),
		cold:   make(map[string]string),
		idx:    tier.NewIndex(),
	}
}

// Write applies write-through (cold[k]=v first), then refreshes a hot k or
// promotes a cold k, evicting the LRU key when the hot layer exceeds memCap.
func (s *Store) Write(k, v string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.clock++
	s.cold[k] = v // disk is authoritative and always written first
	if _, ok := s.hot[k]; ok {
		s.hot[k] = v
		s.idx.Touch(k, s.clock)
		return
	}
	s.promote(k, v)
}

// Read returns the hot value on hit; on miss it counts one disk read, fetches
// the authoritative value and promotes it. A never-written key returns
// ("", false) without being promoted.
func (s *Store) Read(k string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if v, ok := s.hot[k]; ok {
		s.clock++
		s.idx.Touch(k, s.clock)
		return v, true
	}
	s.diskReads++ // the read fell through to disk
	v, ok := s.cold[k]
	if !ok {
		return "", false
	}
	s.clock++
	s.promote(k, v)
	return v, true
}

// promote makes k the MRU hot key and evicts the LRU key past memCap; the
// victim's value is already on disk. Caller holds s.mu.
func (s *Store) promote(k, v string) {
	s.hot[k] = v
	s.idx.Add(tier.Entry{Key: k, Stamp: s.clock})
	if len(s.hot) <= s.memCap {
		return
	}
	s.lastEvictScans = 0
	if e, ok := s.idx.Victim(); ok {
		s.lastEvictScans++ // the ordered index hands over exactly one candidate
		delete(s.hot, e.Key)
		s.idx.Remove(e.Key)
	}
}

// Count reports the number of distinct keys in the authoritative cold layer.
func (s *Store) Count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.cold)
}

// DiskReads reports how many Read calls were served from the cold layer.
func (s *Store) DiskReads() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.diskReads
}

// HotKeys reports the keys currently resident in the hot layer, sorted.
func (s *Store) HotKeys() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	keys := make([]string, 0, len(s.hot))
	for k := range s.hot {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// Stamp reports the last-access timestamp of a hot key.
func (s *Store) Stamp(k string) (int64, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.idx.Get(k)
	return e.Stamp, ok
}

// ColdValue reports the authoritative disk value for a key.
func (s *Store) ColdValue(k string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.cold[k]
	return v, ok
}

// ScanBoundVerified drives one eviction at several m and confirms the scan
// count stays within an m-independent constant, exposing only a verdict.
func ScanBoundVerified() error {
	for _, m := range []int{100, 1000, 10000} {
		s := New(m)
		for i := 0; i < m; i++ {
			s.Write(fmt.Sprintf("k%05d", i), "v")
		}
		s.Write("extra", "v") // forces exactly one eviction
		if s.lastEvictScans == 0 || s.lastEvictScans > evictionScanBound {
			return errors.New("store: eviction scan count is not O(1)")
		}
	}
	return nil
}

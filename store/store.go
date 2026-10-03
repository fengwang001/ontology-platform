// Package store is an ordered key/value store sorted by unsigned byte order
// of the fixed-length encoded keys.
package store

import (
	"encoding/binary"
	"sort"
	"sync"
)

// Entry is one scanned physical record. Key is a defensive copy.
type Entry struct {
	Key   []byte
	Value int64
}

// Store keeps entries sorted by unsigned big-endian key order. Keys are fixed
// 8-byte encodings; comparison is unsigned byte-by-byte.
type Store struct {
	mu   sync.RWMutex
	name string
	keys []uint64
	vals map[uint64]int64
}

// New creates an empty store. name is used in test logs.
func New(name string) *Store {
	return &Store{name: name, vals: make(map[uint64]int64)}
}

// Name returns the store's diagnostic name.
func (s *Store) Name() string { return s.name }

// Len returns the number of entries.
func (s *Store) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.keys)
}

func decodeKey(key []byte) uint64 {
	return binary.BigEndian.Uint64(key)
}

func encodeKey(k uint64) []byte {
	b := make([]byte, 8)
	binary.BigEndian.PutUint64(b, k)
	return b
}

// Put inserts or overwrites a value and reports whether the key already existed.
func (s *Store) Put(key []byte, value int64) bool {
	k := decodeKey(key)
	s.mu.Lock()
	defer s.mu.Unlock()
	_, existed := s.vals[k]
	if !existed {
		idx := sort.Search(len(s.keys), func(i int) bool { return s.keys[i] >= k })
		s.keys = append(s.keys, 0)
		copy(s.keys[idx+1:], s.keys[idx:])
		s.keys[idx] = k
	}
	s.vals[k] = value
	return existed
}

// Delete removes key; it reports whether the key existed.
func (s *Store) Delete(key []byte) bool {
	k := decodeKey(key)
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.vals[k]; !ok {
		return false
	}
	delete(s.vals, k)
	idx := sort.Search(len(s.keys), func(i int) bool { return s.keys[i] >= k })
	if idx < len(s.keys) && s.keys[idx] == k {
		s.keys = append(s.keys[:idx], s.keys[idx+1:]...)
	}
	return true
}

// Get returns (value, ok).
func (s *Store) Get(key []byte) (int64, bool) {
	k := decodeKey(key)
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.vals[k]
	return v, ok
}

// Scan ascends over entries with key in [low, high). A nil high means +inf.
// Every produced record increments *outCnt when outCnt is non-nil.
func (s *Store) Scan(low, high []byte, outCnt *uint64) []Entry {
	return s.ScanN(low, high, -1, outCnt)
}

// ScanN is like Scan but returns at most limit entries (limit < 0 = no limit).
func (s *Store) ScanN(low, high []byte, limit int, outCnt *uint64) []Entry {
	var lo uint64
	if low != nil {
		lo = decodeKey(low)
	}
	var hi uint64
	hasHigh := high != nil
	if hasHigh {
		hi = decodeKey(high)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	start := sort.Search(len(s.keys), func(i int) bool { return s.keys[i] >= lo })
	out := make([]Entry, 0)
	for i := start; i < len(s.keys); i++ {
		if limit >= 0 && len(out) >= limit {
			break
		}
		k := s.keys[i]
		if hasHigh && k >= hi {
			break
		}
		out = append(out, Entry{Key: encodeKey(k), Value: s.vals[k]})
		if outCnt != nil {
			*outCnt++
		}
	}
	return out
}

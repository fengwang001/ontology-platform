// Package ver stores (group,key) -> {Value,Version} records, enforces the
// one-group-per-key binding, and maintains per-group and total version
// stamps incrementally. It depends on nothing outside the standard library.
package ver

import (
	"errors"
	"sync"
)

// Sentinel errors for rejected writes; distinguishable via errors.Is.
var (
	ErrEmptyGroup  = errors.New("ver: empty group")
	ErrEmptyKey    = errors.New("ver: empty key")
	ErrKeyConflict = errors.New("ver: key already bound to another group")
)

type rec struct {
	value   int64
	version int64
}

// Store is safe for concurrent use.
type Store struct {
	mu    sync.RWMutex
	recs  map[string]map[string]*rec // group -> key -> record
	owner map[string]string          // key -> the group it is bound to
	stamp map[string]int64           // group -> sum of Versions in that group
	total int64                      // sum of all Versions
}

func NewStore() *Store {
	return &Store{
		recs:  make(map[string]map[string]*rec),
		owner: make(map[string]string),
		stamp: make(map[string]int64),
	}
}

// Write validates first and mutates nothing on rejection. On success it
// bumps Version by exactly 1, sets Value, and increments the stamps.
func (s *Store) Write(group, key string, val int64) error {
	if group == "" {
		return ErrEmptyGroup
	}
	if key == "" {
		return ErrEmptyKey
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if g, ok := s.owner[key]; ok && g != group {
		return ErrKeyConflict
	}
	m := s.recs[group]
	if m == nil {
		m = make(map[string]*rec)
		s.recs[group] = m
	}
	r := m[key]
	if r == nil {
		r = &rec{}
		m[key] = r
		s.owner[key] = group
	}
	r.version++
	r.value = val
	s.stamp[group]++
	s.total++
	return nil
}

// Stamp returns the current version stamp of one group in O(1).
func (s *Store) Stamp(group string) int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.stamp[group]
}

// TotalStamp returns the version stamp over all records in O(1).
func (s *Store) TotalStamp() int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.total
}

// SumGroup recomputes the sum of Values in one group (batch recompute).
func (s *Store) SumGroup(group string) int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var sum int64
	for _, r := range s.recs[group] {
		sum += r.value
	}
	return sum
}

// SumAll recomputes the sum of all Values (batch recompute).
func (s *Store) SumAll() int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var sum int64
	for _, m := range s.recs {
		for _, r := range m {
			sum += r.value
		}
	}
	return sum
}

// Groups lists all groups that currently hold at least one record.
func (s *Store) Groups() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	gs := make([]string, 0, len(s.recs))
	for g := range s.recs {
		gs = append(gs, g)
	}
	return gs
}

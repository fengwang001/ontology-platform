// Package snap stores per-partition events and produces aligned
// snapshots: Cut == min(per-partition counts), View = each partition's
// first Cut events concatenated in partition order. Depends on cut.
package snap

import (
	"errors"
	"fmt"
	"sync"

	"ontology/cut"
)

// Event is one delivered event.
type Event struct {
	Key string
	Val int64
}

// Distinguishable sentinel errors for rejected feeds.
var (
	ErrPartition    = errors.New("snap: partition out of range")
	ErrEmptyKey     = errors.New("snap: empty event key")
	ErrBackpressure = errors.New("snap: pending limit exceeded")
)

// Store holds all delivered events per partition. Safe for concurrent use.
type Store struct {
	mu   sync.RWMutex
	tr   *cut.Tracker
	parts [][]Event
	// lastReads records how many partition counters the most recent Feed
	// read to compute C. Non-exported on purpose: it proves the O(1)
	// incremental cut maintenance to in-package tests only.
	lastReads int
}

// New returns a Store for numPartitions partitions with the given
// per-partition pending limit.
func New(numPartitions, maxPending int) (*Store, error) {
	if numPartitions <= 0 {
		return nil, fmt.Errorf("snap: numPartitions must be > 0, got %d", numPartitions)
	}
	if maxPending < 0 {
		return nil, fmt.Errorf("snap: maxPending must be >= 0, got %d", maxPending)
	}
	return &Store{tr: cut.New(numPartitions, maxPending), parts: make([][]Event, numPartitions)}, nil
}

// Feed delivers ev to partition p. Invalid partition, empty Key, or a
// feed that would exceed the pending limit are rejected with distinct
// sentinel errors and leave all state unchanged.
func (s *Store) Feed(p int, ev Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p < 0 || p >= len(s.parts) {
		return ErrPartition
	}
	if ev.Key == "" {
		return ErrEmptyKey
	}
	ok, reads := s.tr.Feed(p)
	s.lastReads = reads
	if !ok {
		return ErrBackpressure
	}
	s.parts[p] = append(s.parts[p], ev)
	return nil
}

// Snapshot is a read-only, consistent view; it consumes nothing.
type Snapshot struct {
	Cut  int
	view []Event
}

// Snapshot returns the current aligned cut and per-partition prefixes.
func (s *Store) Snapshot() Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c := s.tr.Cut()
	view := make([]Event, 0, c*len(s.parts))
	for _, part := range s.parts {
		view = append(view, part[:c]...)
	}
	return Snapshot{Cut: c, view: view}
}

// View returns a copy of the snapshot's events: partition 0's first Cut
// events, then partition 1's, and so on. Folding it into a map in order
// gives the "later write overrides earlier write for the same Key" state.
func (s Snapshot) View() []Event {
	out := make([]Event, len(s.view))
	copy(out, s.view)
	return out
}

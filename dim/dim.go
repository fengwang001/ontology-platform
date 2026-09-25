// Package dim owns versioned dimension snapshots: the current table, the
// monotonic version number, retained snapshot history, byte accounting and
// oldest-first eviction under a memory cap. It depends on no other package.
package dim

import (
	"errors"
	"sort"
	"sync/atomic"
)

// Sentinel errors; all failure modes are distinguishable via errors.Is.
var (
	ErrEmptyKey   = errors.New("dim: empty key")   // Entry (or Fact) Key == ""
	ErrEmptyBatch = errors.New("dim: empty batch") // Broadcast with no entries
	ErrFuture     = errors.New("dim: version is in the future")
	ErrTooBig     = errors.New("dim: batch exceeds memory cap")
)

// Entry is one dimension row. Its on-record byte cost is len(Key) + 8.
type Entry struct {
	Key string
	Val int64
}

// LookupKind classifies a lookup. Stale and Miss are legal, not errors.
type LookupKind int

const (
	Found LookupKind = iota
	Miss
	Stale
)

// Snapshot is one immutable version of the dimension table.
type Snapshot struct {
	vsn   int64
	m     map[string]int64
	bytes int64
}

// Store is the versioned dimension store; use New. Package api serializes
// access, so Store itself is lock-free.
type Store struct {
	capBytes int64
	v        int64
	used     int64
	snaps    []*Snapshot // ascending Vsn; eviction removes only the prefix

	// lastProbe is the number of entries the most recent Lookup inspected to
	// locate a Key inside one snapshot. Unexported on purpose: a map probe is
	// O(1), a linear scan would be O(m). Only in-package tests read it.
	lastProbe atomic.Int64
}

// New creates a store holding only the empty version-0 snapshot (0 bytes).
func New(maxBytes int64) *Store {
	return &Store{capBytes: maxBytes,
		snaps: []*Snapshot{{vsn: 0, m: map[string]int64{}}}}
}

// Broadcast upserts the whole batch onto a copy of the current snapshot,
// appends the new version, then evicts oldest-first while used > cap, always
// keeping at least V and V-1. If the cap is still exceeded with only those
// two retained, it rejects: every receiver field stays untouched.
func (s *Store) Broadcast(batch []Entry) (int64, error) {
	if len(batch) == 0 {
		return 0, ErrEmptyBatch
	}
	cur := s.snaps[len(s.snaps)-1]
	m := make(map[string]int64, len(cur.m)+len(batch))
	for k, v := range cur.m { // all work happens on copies ...
		m[k] = v
	}
	nbytes := cur.bytes
	for _, e := range batch {
		if e.Key == "" {
			return 0, ErrEmptyKey
		}
		if _, ok := m[e.Key]; !ok { // overwrite adds no bytes
			nbytes += int64(len(e.Key)) + 8
		}
		m[e.Key] = e.Val
	}
	snaps := append(append(make([]*Snapshot, 0, len(s.snaps)+1), s.snaps...),
		&Snapshot{vsn: s.v + 1, m: m, bytes: nbytes})
	used := s.used + nbytes
	for used > s.capBytes && len(snaps) > 2 { // floor: keep V and V-1
		used -= snaps[0].bytes
		snaps = snaps[1:]
	}
	if used > s.capBytes {
		return 0, ErrTooBig // ... so a reject leaves no trace
	}
	s.v, s.used, s.snaps = s.v+1, used, snaps
	return s.v, nil
}

// Lookup reads key at version vsn: vsn > V is ErrFuture; vsn older than the
// oldest retained snapshot is Stale; otherwise one snapshot probe yields
// Found or Miss.
func (s *Store) Lookup(vsn int64, key string) (int64, LookupKind, error) {
	if vsn > s.v {
		return 0, 0, ErrFuture
	}
	oldest := s.snaps[0].vsn
	if vsn < oldest {
		return 0, Stale, nil
	}
	snap := s.snaps[vsn-oldest] // retained versions stay contiguous
	s.lastProbe.Store(1)        // one map probe, independent of snapshot size
	if val, ok := snap.m[key]; ok {
		return val, Found, nil
	}
	return 0, Miss, nil
}

// V returns the latest committed version number.
func (s *Store) V() int64 { return s.v }

// Used returns the sum of bytes of all retained snapshots.
func (s *Store) Used() int64 { return s.used }

// Oldest returns the Vsn of the oldest retained (oldest queryable) snapshot.
func (s *Store) Oldest() int64 { return s.snaps[0].vsn }

// Versions returns retained version numbers, oldest first.
func (s *Store) Versions() []int64 {
	out := make([]int64, len(s.snaps))
	for i, sn := range s.snaps {
		out[i] = sn.vsn
	}
	return out
}

// Current returns the entries of the current snapshot, sorted by Key.
func (s *Store) Current() []Entry {
	cur := s.snaps[len(s.snaps)-1]
	out := make([]Entry, 0, len(cur.m))
	for k, v := range cur.m {
		out = append(out, Entry{Key: k, Val: v})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

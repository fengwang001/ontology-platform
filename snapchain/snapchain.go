// Package snapchain implements the append-only chain of table snapshots:
// snapshot IDs, commit timestamps, per-snapshot file sets, the current
// snapshot and retention decisions (union of the three retention rules).
// It depends on no other package.
package snapchain

import (
	"errors"
	"sort"
)

// Sentinel errors (detectable with errors.Is). Name-related rejections
// (reused names, duplicates, intersection) live in fileref alongside the
// never-reuse name register.
var (
	ErrTSNotIncreasing  = errors.New("snapchain: ts must be strictly greater than the last accepted commit")
	ErrFileNotInCurrent = errors.New("snapchain: a removed file is not in the current snapshot")
)

// Snapshot is an immutable view of one existing snapshot: ID is the 1-based
// snapshot number (S1, S2, ...), Files is sorted ascending.
type Snapshot struct {
	ID    int
	TS    int64
	Files []string
}

// Chain is the in-memory snapshot chain. Use New.
type Chain struct {
	snaps []*snap
	seq   int // ever-increasing snapshot number, survives expiration
}

type snap struct {
	id    int
	ts    int64
	files map[string]struct{}
}

// New returns an empty chain.
func New() *Chain {
	return &Chain{}
}

// CheckCommit validates ts ordering and remove ⊆ current file set.
// It mutates nothing; name rules are checked by fileref.
func (c *Chain) CheckCommit(ts int64, remove []string) error {
	if n := len(c.snaps); n > 0 && ts <= c.snaps[n-1].ts {
		return ErrTSNotIncreasing
	}
	if len(c.snaps) > 0 {
		cur := c.snaps[len(c.snaps)-1].files
		for _, f := range remove {
			if _, ok := cur[f]; !ok {
				return ErrFileNotInCurrent
			}
		}
	} else if len(remove) > 0 {
		return ErrFileNotInCurrent
	}
	return nil
}

// Commit appends a validated snapshot whose file set is the current set
// minus remove plus add, and returns its ID with a sorted copy of its files.
func (c *Chain) Commit(ts int64, add, remove []string) (int, []string) {
	files := map[string]struct{}{}
	if n := len(c.snaps); n > 0 {
		for f := range c.snaps[n-1].files {
			files[f] = struct{}{}
		}
	}
	for _, f := range remove {
		delete(files, f)
	}
	for _, f := range add {
		files[f] = struct{}{}
	}
	c.seq++
	s := &snap{id: c.seq, ts: ts, files: files}
	c.snaps = append(c.snaps, s)
	return s.id, sorted(files)
}

// Partition applies the retention rules (union of the three) and drops the
// expired snapshots, returning the expired snapshots in ascending ID order.
//  1. among the newest N (N == 0 satisfies nobody; N >= count everybody)
//  2. ts > T (strictly)
//  3. the current snapshot, which never expires
func (c *Chain) Partition(N int, T int64) []Snapshot {
	n := len(c.snaps)
	if n == 0 {
		return nil
	}
	keep := make([]*snap, 0, n)
	var expired []Snapshot
	for i, s := range c.snaps {
		newestN := N > 0 && i >= n-N
		fresh := s.ts > T
		current := i == n-1
		if newestN || fresh || current {
			keep = append(keep, s)
		} else {
			expired = append(expired, view(s))
		}
	}
	c.snaps = keep
	return expired
}

// Snapshots returns a defensive copy of all existing snapshots, ascending.
func (c *Chain) Snapshots() []Snapshot {
	out := make([]Snapshot, len(c.snaps))
	for i, s := range c.snaps {
		out[i] = view(s)
	}
	return out
}

// Current returns the current (newest) snapshot.
func (c *Chain) Current() (Snapshot, bool) {
	if len(c.snaps) == 0 {
		return Snapshot{}, false
	}
	return view(c.snaps[len(c.snaps)-1]), true
}

// Len reports the number of existing snapshots.
func (c *Chain) Len() int {
	return len(c.snaps)
}

func view(s *snap) Snapshot {
	return Snapshot{ID: s.id, TS: s.ts, Files: sorted(s.files)}
}

func sorted(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for f := range m {
		out = append(out, f)
	}
	sort.Strings(out)
	return out
}

// Package api is the public entry point of the in-memory MVCC store.
package api

import (
	"errors"
	"fmt"

	"ontology/mvcc"
)

// Re-exported sentinel errors; callers decide failures with errors.Is.
var (
	ErrEmptyKey         = mvcc.ErrEmptyKey
	ErrSnapshotRange    = mvcc.ErrSnapshotRange
	ErrSnapshotInactive = mvcc.ErrSnapshotInactive
)

// Store is the process-local MVCC store.
type Store struct {
	m *mvcc.MVCC
}

// New creates an empty store.
func New() *Store { return &Store{m: mvcc.New()} }

// Write creates a new version and returns its number; an empty key is
// rejected (state untouched) and returns -1.
func (s *Store) Write(key, value string) int64 {
	if v, err := s.m.Write(key, value); err == nil {
		return v
	}
	return -1
}

// Snapshot returns and registers the current read point.
func (s *Store) Snapshot() int64 { return s.m.Snapshot() }

// Read returns the value visible at snapshot snap.
func (s *Store) Read(key string, snap int64) (string, bool, error) {
	return s.m.Read(key, snap)
}

// Release drops a snapshot from the active set.
func (s *Store) Release(snap int64) error { return s.m.Release(snap) }

// Collect reclaims dead old versions and returns how many were removed.
func (s *Store) Collect() int { return s.m.Collect() }

type histEntry struct {
	ver int64
	val string
}

// naiveRead keeps the ENTIRE history and scans for the greatest ver <= snap.
func naiveRead(hist map[string][]histEntry, key string, snap int64) (string, bool) {
	bestVer, bestVal := int64(-1), ""
	for _, h := range hist[key] {
		if h.ver <= snap && h.ver > bestVer {
			bestVer, bestVal = h.ver, h.val
		}
	}
	return bestVal, bestVer >= 0
}

// SelfCheck runs a built-in operation sequence and verifies the four
// invariants; it returns nil only if every check passes.
func (s *Store) SelfCheck() error {
	// Invariant 1: snapshot isolation — later writes never move an old read.
	hist := map[string][]histEntry{}
	hist["k"] = append(hist["k"], histEntry{s.Write("k", "v1"), "v1"})
	a := s.Snapshot()
	hist["k"] = append(hist["k"], histEntry{s.Write("k", "v2"), "v2"})
	s.Write("other", "x")
	if v, f, err := s.Read("k", a); err != nil || !f || v != "v1" {
		return fmt.Errorf("snapshot isolation: got %q,%v,%v", v, f, err)
	}

	// Invariant 2: agreement with the naive full-history reference.
	for _, w := range []struct{ k, v string }{
		{"p", "p1"}, {"k", "v3"}, {"p", "p2"}, {"k", "v4"},
	} {
		hist[w.k] = append(hist[w.k], histEntry{s.Write(w.k, w.v), w.v})
	}
	b := s.Snapshot()
	for _, snap := range []int64{a, b, 0} {
		for _, key := range []string{"k", "p", "missing"} {
			want, wf := naiveRead(hist, key, snap)
			got, gf, err := s.Read(key, snap)
			if err != nil || wf != gf || want != got {
				return fmt.Errorf("naive reference %s@%d: got %q,%v want %q,%v",
					key, snap, got, gf, want, wf)
			}
		}
	}

	// Invariant 3: Collect preserves every read at still-active snapshots.
	s.Write("k", "v5")
	type kv struct {
		snap int64
		val  string
		f    bool
	}
	before := []kv{}
	for _, snap := range []int64{a, b} {
		v, f, _ := s.Read("k", snap)
		before = append(before, kv{snap, v, f})
	}
	s.Collect()
	for _, q := range before {
		got, f, _ := s.Read("k", q.snap)
		if f != q.f || got != q.val {
			return fmt.Errorf("collect safety: k@%d changed %q,%v", q.snap, got, f)
		}
	}

	// Invariant 4: rejected operations fail decidably and leave no trace.
	guard, guardF, _ := s.Read("k", b)
	if v := s.Write("", "z"); v != -1 {
		return errors.New("empty-key write was accepted")
	}
	if _, _, err := s.Read("k", -1); !errors.Is(err, ErrSnapshotRange) {
		return errors.New("negative snapshot not rejected with ErrSnapshotRange")
	}
	if err := s.Release(1 << 40); !errors.Is(err, ErrSnapshotInactive) {
		return errors.New("inactive release not rejected with ErrSnapshotInactive")
	}
	now, nowF, _ := s.Read("k", b)
	if now != guard || nowF != guardF {
		return errors.New("state changed after rejected operations")
	}
	return nil
}

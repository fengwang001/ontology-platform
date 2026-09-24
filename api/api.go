// Package api is the public, concurrency-safe surface over the ranking view:
// New / Insert / Delete / Get / SelfCheck. It depends only on rank; an
// RWMutex lets Gets and SelfCheck run concurrently with each other.
package api

import (
	"errors"
	"sync"

	"ontology/ord"
	"ontology/rank"
)

// Re-exported decidable sentinel errors; the three are pairwise distinct.
var (
	ErrEmptyID     = rank.ErrEmptyID
	ErrDuplicateID = rank.ErrDuplicateID
	ErrIDNotFound  = rank.ErrIDNotFound
)

// View is the materialized per-element ranking, maintained incrementally.
type View struct {
	mu sync.RWMutex
	s  *rank.Set
}

// New returns an empty view.
func New() *View { return &View{s: rank.New()} }

// Insert adds id with score. Empty/duplicate ids are rejected before any
// state change, so every triple is left untouched on failure.
func (v *View) Insert(id string, score int64) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.s.Add(id, score)
}

// Delete removes id; an absent id fails with ErrIDNotFound without effect.
func (v *View) Delete(id string) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.s.Remove(id)
}

// Get returns the maintained 1-based triple; ok=false for unknown ids.
func (v *View) Get(id string) (rowNumber, rankValue, denseRank int, ok bool) {
	v.mu.RLock()
	defer v.mu.RUnlock()
	t, present := v.s.Get(id)
	if !present {
		return 0, 0, 0, false
	}
	return t.RowNumber, t.Rank, t.DenseRank, true
}

// verify checks invariants 1 and 3 at once against the naive batch result:
// per-element equality with the recomputation, ROW_NUMBER bijection on 1..n,
// equal scores sharing RANK/DENSE_RANK and adjacent groups differing by one.
func verify(s *rank.Set) bool {
	els := s.Snapshot()
	ord.Sort(els)
	want := ord.Batch(els)
	seen := map[int]bool{}
	var prev ord.Triple
	var prevSC int64
	for i, e := range els {
		t, ok := s.Get(e.ID)
		if !ok || t != want[e.ID] || t.RowNumber < 1 || t.RowNumber > len(els) || seen[t.RowNumber] {
			return false
		}
		if i > 0 && e.Score == prevSC && (t.Rank != prev.Rank || t.DenseRank != prev.DenseRank) {
			return false
		}
		if i > 0 && e.Score != prevSC && (t.Rank <= prev.Rank || t.DenseRank != prev.DenseRank+1) {
			return false
		}
		seen[t.RowNumber] = true
		prev, prevSC = t, e.Score
	}
	return len(seen) == len(els)
}

func save(s *rank.Set) map[string]ord.Triple {
	out := map[string]ord.Triple{}
	for _, e := range s.Snapshot() {
		out[e.ID], _ = s.Get(e.ID)
	}
	return out
}

func unchanged(s *rank.Set, old map[string]ord.Triple) bool {
	if len(s.Snapshot()) != len(old) {
		return false
	}
	for id, t := range old {
		if g, ok := s.Get(id); !ok || g != t {
			return false
		}
	}
	return true
}

// script exercises all four invariants on a throwaway set.
func script() bool {
	s := rank.New()
	seq := []ord.Element{{ID: "A", Score: 100}, {ID: "B", Score: 90},
		{ID: "C", Score: 100}, {ID: "D", Score: 80}, {ID: "E", Score: 90}}
	for _, e := range seq { // invariant 1 after every insertion
		if s.Add(e.ID, e.Score) != nil || !verify(s) {
			return false
		}
	}
	old := save(s)
	if !errors.Is(s.Add("", 1), ErrEmptyID) || // invariant 4
		!errors.Is(s.Add("A", 1), ErrDuplicateID) ||
		!errors.Is(s.Remove("nobody"), ErrIDNotFound) || !unchanged(s, old) {
		return false
	}
	if s.Add("X", 77) != nil || s.Remove("X") != nil || !unchanged(s, old) { // invariant 2
		return false
	}
	return s.Add("F", 90) == nil && verify(s) // still usable afterwards
}

// SelfCheck validates the built-in sequence (all four invariants) and the
// current view's consistency with a naive batch recomputation.
func (v *View) SelfCheck() bool {
	v.mu.RLock()
	ok := verify(v.s)
	v.mu.RUnlock()
	return ok && script()
}

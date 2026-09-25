// Package api is the outward face of the snapshot-compaction store.
// It depends on comp (and transitively seg); nothing depends back.
package api

import (
	"fmt"
	"sync"

	"ontology/comp"
	"ontology/seg"
)

// Store keeps all state in process memory. Versions are globally strictly
// increasing, so latest is a per-key pointer to the newest ingested record.
type Store struct {
	mu     sync.RWMutex
	maxVer int64
	latest map[string]seg.Record
	merger *comp.Merger
}

func New() *Store {
	return &Store{latest: make(map[string]seg.Record), merger: comp.NewMerger()}
}

// Ingest appends one record and advances the global max version.
// Any rejection is atomic: validation finishes before any state changes.
func (s *Store) Ingest(r seg.Record) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := r.Validate(); err != nil {
		return err
	}
	if r.Version <= s.maxVer {
		return fmt.Errorf("%w: %d not greater than current max %d",
			seg.ErrVersion, r.Version, s.maxVer)
	}
	s.latest[r.Key] = r // globally increasing version => r is the winner
	s.maxVer = r.Version
	return nil
}

// Compact merges the store's per-key latest snapshot with the given
// segments into one segment, then adopts it as the new state. Invalid
// input records reject the whole call before anything changes.
func (s *Store) Compact(segs ...[]seg.Record) (*comp.Segment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, sg := range segs {
		for _, r := range sg {
			if err := r.Validate(); err != nil {
				return nil, err
			}
		}
	}
	snap := make([]seg.Record, 0, len(s.latest))
	for _, r := range s.latest {
		snap = append(snap, r)
	}
	out := s.merger.Merge(append([][]seg.Record{snap}, segs...)...)
	s.latest = make(map[string]seg.Record, len(out.Records))
	for _, r := range out.Records { // output holds no tombstones
		s.latest[r.Key] = r
	}
	if out.Watermark > s.maxVer {
		s.maxVer = out.Watermark
	}
	return out, nil
}

// View returns a copy of every key's live value; tombstoned keys are absent.
func (s *Store) View() map[string]string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v := make(map[string]string, len(s.latest))
	for k, r := range s.latest {
		if !r.Tombstone() {
			v[k] = r.Val
		}
	}
	return v
}

// Watermark is the greatest version seen so far; it never decreases.
func (s *Store) Watermark() int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.maxVer
}

// SelfCheck verifies the four invariants on fresh internal stores,
// touching no receiver state, so concurrent calls are safe.
func (s *Store) SelfCheck() error {
	seq := []seg.Record{
		{Key: "a", Version: 1, Op: seg.OpPut, Val: "x"},
		{Key: "b", Version: 2, Op: seg.OpPut, Val: "y"},
		{Key: "a", Version: 3, Op: seg.OpPut, Val: "x2"},
		{Key: "b", Version: 4, Op: seg.OpDel},
		{Key: "c", Version: 5, Op: seg.OpPut, Val: "z"},
	}
	st := New()
	win := map[string]seg.Record{}
	for _, r := range seq {
		if err := st.Ingest(r); err != nil {
			return fmt.Errorf("selfcheck ingest: %w", err)
		}
		if st.Watermark() != r.Version { // invariant 3: watermark == max seen
			return fmt.Errorf("selfcheck: watermark %d != %d", st.Watermark(), r.Version)
		}
		if cur, ok := win[r.Key]; !ok || r.Newer(cur) {
			win[r.Key] = r
		}
	}
	view := st.View() // invariant 1: view == independent batch recompute
	if len(view) != 2 {
		return fmt.Errorf("selfcheck: view size %d != 2", len(view))
	}
	for k, r := range win {
		got, live := view[k]
		if r.Tombstone() == live || (live && got != r.Val) {
			return fmt.Errorf("selfcheck: view mismatch on key %q", k)
		}
	}
	out, err := st.Compact() // invariant 2: sorted, unique, del-free
	if err != nil {
		return fmt.Errorf("selfcheck compact: %w", err)
	}
	for i, r := range out.Records {
		if r.Tombstone() || (i > 0 && out.Records[i-1].Key >= r.Key) {
			return fmt.Errorf("selfcheck: bad compacted record %+v", r)
		}
	}
	bad := []seg.Record{ // invariant 4: rejections leave no trace
		{Key: "", Version: 6, Op: seg.OpPut, Val: "v"}, {Key: "d", Version: 0, Op: seg.OpPut, Val: "v"},
		{Key: "d", Version: 5, Op: seg.OpPut, Val: "v"}, {Key: "d", Version: 6, Op: "nop", Val: "v"},
		{Key: "d", Version: 6, Op: seg.OpPut}, {Key: "d", Version: 6, Op: seg.OpDel, Val: "v"},
	}
	wm, n := st.Watermark(), len(st.View())
	for _, r := range bad {
		if st.Ingest(r) == nil {
			return fmt.Errorf("selfcheck: %+v accepted, want rejection", r)
		}
	}
	if st.Watermark() != wm || len(st.View()) != n {
		return fmt.Errorf("selfcheck: rejected ingest mutated state")
	}
	return nil
}

// Package segstore implements an immutable segmented document store with
// delete tombstones, merges, and reference-counted physical segment release.
package segstore

import (
	"errors"
	"sort"
	"sync"
)

var (
	// ErrInvalid is returned for malformed arguments.
	ErrInvalid = errors.New("invalid argument")
	// ErrIDConflict is returned when a live id already exists in the view.
	ErrIDConflict = errors.New("segstore: id conflict")
	// ErrDocNotFound is returned when a delete targets a non-live document.
	ErrDocNotFound = errors.New("segstore: document not found")
	// ErrSegNotFound is returned when a referenced segment is not in the view.
	ErrSegNotFound = errors.New("segstore: segment not found")
)

// Doc is an input document.
type Doc struct {
	ID      string
	SortVal int64
}

// Key is the global sort key: (sortVal, segment number, in-segment ordinal).
type Key struct {
	SortVal int64
	Seg     int
	Idx     int
}

// Entry is a document together with its key.
type Entry struct {
	Doc
	Key Key
}

type seg struct {
	id   int
	docs []Doc // sorted by (SortVal, ID); ordinal is the slice index
	del  map[string]int64
	refs int
}

// Store holds the segments, the current view, and the physical release log.
// The ...Locked methods assume the caller holds mu; the exported wrappers lock.
type Store struct {
	mu       sync.Mutex
	segs     map[int]*seg
	view     map[int]struct{}
	live     map[string]int // live id -> segment, for conflict checks and deletes
	released []int
	nextSeg  int
}

// New creates an empty store.
func New() *Store {
	return &Store{
		segs:    map[int]*seg{},
		view:    map[int]struct{}{},
		live:    map[string]int{},
		nextSeg: 1,
	}
}

// AddSegment validates docs and adds a new segment; returns the new segment id.
func (s *Store) AddSegment(docs []Doc) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.AddSegmentLocked(docs)
}

// AddSegmentLocked is AddSegment without locking.
func (s *Store) AddSegmentLocked(docs []Doc) (int, error) {
	if err := s.CheckAddLocked(docs); err != nil {
		return 0, err
	}
	return s.commitAddLocked(docs), nil
}

// CheckAddLocked validates a batch without mutating state.
func (s *Store) CheckAddLocked(docs []Doc) error {
	if len(docs) < 1 || len(docs) > 10000 {
		return ErrInvalid
	}
	seen := make(map[string]struct{}, len(docs))
	for _, d := range docs {
		if d.ID == "" {
			return ErrInvalid
		}
		if _, dup := seen[d.ID]; dup {
			return ErrInvalid
		}
		seen[d.ID] = struct{}{}
		if _, ok := s.live[d.ID]; ok {
			return ErrIDConflict
		}
	}
	return nil
}

func (s *Store) commitAddLocked(docs []Doc) int {
	id := s.nextSeg
	s.nextSeg++
	ordered := make([]Doc, len(docs))
	copy(ordered, docs)
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].SortVal != ordered[j].SortVal {
			return ordered[i].SortVal < ordered[j].SortVal
		}
		return ordered[i].ID < ordered[j].ID
	})
	sg := &seg{id: id, docs: ordered, del: map[string]int64{}, refs: 1}
	s.segs[id] = sg
	s.view[id] = struct{}{}
	for _, d := range ordered {
		s.live[d.ID] = id
	}
	return id
}

// Delete tombstones a live document, recording the global operation number.
func (s *Store) Delete(id string, op int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.DeleteLocked(id, op)
}

// DeleteLocked is Delete without locking.
func (s *Store) DeleteLocked(id string, op int64) error {
	if err := s.CheckDeleteLocked(id); err != nil {
		return err
	}
	s.commitDeleteLocked(id, op)
	return nil
}

// CheckDeleteLocked reports whether id is live in the current view.
func (s *Store) CheckDeleteLocked(id string) error {
	if _, ok := s.live[id]; !ok {
		return ErrDocNotFound
	}
	return nil
}

func (s *Store) commitDeleteLocked(id string, op int64) {
	sid := s.live[id]
	s.segs[sid].del[id] = op
	delete(s.live, id)
}

// Merge merges live documents of the given view segments into a new segment.
// With no surviving documents no segment is created and 0 is returned.
func (s *Store) Merge(segIDs []int) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.MergeLocked(segIDs)
}

// MergeLocked is Merge without locking.
func (s *Store) MergeLocked(segIDs []int) (int, error) {
	if err := s.CheckMergeLocked(segIDs); err != nil {
		return 0, err
	}
	return s.commitMergeLocked(segIDs), nil
}

// CheckMergeLocked validates a merge request without mutating state.
func (s *Store) CheckMergeLocked(segIDs []int) error {
	if len(segIDs) < 2 || len(segIDs) > 10 {
		return ErrInvalid
	}
	seen := make(map[int]struct{}, len(segIDs))
	for _, sid := range segIDs {
		if _, dup := seen[sid]; dup {
			return ErrInvalid
		}
		seen[sid] = struct{}{}
		if _, ok := s.view[sid]; !ok {
			return ErrSegNotFound
		}
	}
	return nil
}

func (s *Store) commitMergeLocked(segIDs []int) int {
	var survivors []Doc
	for _, sid := range segIDs {
		sg := s.segs[sid]
		for _, d := range sg.docs {
			if _, deleted := sg.del[d.ID]; deleted {
				continue
			}
			survivors = append(survivors, d)
		}
	}

	// Old segments leave the view regardless of whether a new one is built.
	var freed []int
	for _, sid := range segIDs {
		delete(s.view, sid)
		s.segs[sid].refs--
		if s.segs[sid].refs == 0 {
			freed = append(freed, sid)
		}
	}
	if len(freed) > 0 {
		sort.Ints(freed) // same operation: ascending segment order
		start := len(s.released)
		s.released = append(s.released, freed...)
		sort.Ints(s.released[start:])
		for _, sid := range freed {
			delete(s.segs, sid)
		}
	}

	if len(survivors) == 0 {
		return 0
	}

	sort.Slice(survivors, func(i, j int) bool {
		if survivors[i].SortVal != survivors[j].SortVal {
			return survivors[i].SortVal < survivors[j].SortVal
		}
		return survivors[i].ID < survivors[j].ID
	})

	id := s.nextSeg
	s.nextSeg++
	sg := &seg{id: id, docs: survivors, del: map[string]int64{}, refs: 1}
	s.segs[id] = sg
	s.view[id] = struct{}{}
	for _, d := range survivors {
		delete(s.live, d.ID) // was mapped to one of the old segments
		s.live[d.ID] = id
	}
	return id
}

// AcquireLocked increments the reference count of an existing segment.
func (s *Store) AcquireLocked(sid int) { s.segs[sid].refs++ }

// ReleaseLocked decrements the reference count; the segment is physically
// freed when its last reference disappears. Callers must gather a batch of
// releases and then call FlushReleasedLocked for deterministic ordered logging.
func (s *Store) ReleaseLocked(sid int) {
	sg := s.segs[sid]
	sg.refs--
}

// FlushReleasedLocked physically frees zero-reference segments in the given
// list and appends their ids (ascending, deduplicated) to the release log.
func (s *Store) FlushReleasedLocked(sids []int) {
	byID := make(map[int]struct{}, len(sids))
	var batch []int
	for _, sid := range sids {
		if _, dup := byID[sid]; dup {
			continue
		}
		byID[sid] = struct{}{}
		sg := s.segs[sid]
		if sg.refs == 0 {
			batch = append(batch, sid)
		}
	}
	sort.Ints(batch) // one operation: ascending segment order
	s.released = append(s.released, batch...)
	for _, sid := range batch {
		delete(s.segs, sid)
	}
}

// ViewLocked returns a sorted snapshot of the current view segment ids.
func (s *Store) ViewLocked() []int {
	out := make([]int, 0, len(s.view))
	for sid := range s.view {
		out = append(out, sid)
	}
	sort.Ints(out)
	return out
}

// HasSegmentLocked reports whether sid exists and is retained.
func (s *Store) HasSegmentLocked(sid int) bool {
	_, ok := s.segs[sid]
	return ok
}

// ScanLocked returns entries visible at operation op across the given
// segments, strictly after key when non-nil, up to limit results in key order.
// A document is visible when not deleted, or deleted by an operation > op.
func (s *Store) ScanLocked(segIDs []int, op int64, after *Key, limit int) []Entry {
	type cand struct {
		entry Entry
	}
	var cands []cand
	for _, sid := range segIDs {
		sg, ok := s.segs[sid]
		if !ok {
			continue
		}
		for idx, d := range sg.docs {
			if dop, deleted := sg.del[d.ID]; deleted && dop <= op {
				continue
			}
			k := Key{SortVal: d.SortVal, Seg: sid, Idx: idx}
			if after != nil && !keyGreater(k, *after) {
				continue
			}
			cands = append(cands, cand{Entry{Doc: d, Key: k}})
		}
	}
	sort.Slice(cands, func(i, j int) bool { return keyLess(cands[i].entry.Key, cands[j].entry.Key) })
	if len(cands) > limit {
		cands = cands[:limit]
	}
	out := make([]Entry, len(cands))
	for i, c := range cands {
		out[i] = c.entry
	}
	return out
}

func keyLess(a, b Key) bool {
	if a.SortVal != b.SortVal {
		return a.SortVal < b.SortVal
	}
	if a.Seg != b.Seg {
		return a.Seg < b.Seg
	}
	return a.Idx < b.Idx
}

func keyGreater(a, b Key) bool { return keyLess(b, a) }

// Released returns a copy of the physical release log.
func (s *Store) Released() []int {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]int, len(s.released))
	copy(out, s.released)
	return out
}

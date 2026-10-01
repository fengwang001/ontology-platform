// Package dvvstore implements a dot-version-vector sibling set store.
//
// Each key keeps a set of concurrent sibling values together with the
// per-node "seen" vector (a version vector). Put writes a new dot (id,n),
// removes siblings already covered by the writer context, and Merge joins
// two stores following dot-version-vector reconciliation rules.
package dvvstore

import (
	"sort"
	"sync"
)

// sibling is the internal representation of one concurrent value.
type sibling struct {
	dot   Dot
	value string
}

// keyState is the per-key state: the sibling list and the seen vector.
// seen[id] is the greatest dot counter ever emitted by node id for this key.
type keyState struct {
	siblings []sibling
	seen     map[string]int64
}

// snapshotEntry is a detached copy of one key used while merging another
// store, so the other store is only locked for the duration of the copy.
type snapshotEntry struct {
	siblings []sibling
	seen     map[string]int64
}

// Errors returned by New and Put. They are sentinel errors so callers can
// distinguish every rejection reason with errors.Is.
var (
	ErrInvalidCap       = errInvalidCap{}
	ErrEmptyKey         = errEmptyKey{}
	ErrEmptyNodeID      = errEmptyNodeID{}
	ErrEmptyContextNode = errEmptyContextNode{}
	ErrContextAhead     = errContextAhead{}
	ErrCapExceeded      = errCapExceeded{}
)

type errInvalidCap struct{}

func (errInvalidCap) Error() string { return "dvvstore: cap must be at least 1" }

type errEmptyKey struct{}

func (errEmptyKey) Error() string { return "dvvstore: key must not be empty" }

type errEmptyNodeID struct{}

func (errEmptyNodeID) Error() string { return "dvvstore: node id must not be empty" }

type errEmptyContextNode struct{}

func (errEmptyContextNode) Error() string {
	return "dvvstore: context must not contain an empty node id"
}

type errContextAhead struct{}

func (errContextAhead) Error() string {
	return "dvvstore: context counter is ahead of the stored seen counter"
}

type errCapExceeded struct{}

func (errCapExceeded) Error() string {
	return "dvvstore: sibling count after removing covered entries would exceed cap"
}

// Dot identifies a single write event: the node id and the per-key,
// per-node monotonically increasing counter assigned by Put.
type Dot struct {
	ID string
	N  int64
}

// Sibling is one concurrent value together with the dot that produced it.
type Sibling struct {
	Dot   Dot
	Value string
}

// Snapshot is the observable state of one key: siblings sorted by
// (node id lexicographic, counter ascending) and a copy of the seen vector.
type Snapshot struct {
	Siblings []Sibling
	Context  map[string]int64
}

// Store is a concurrency-safe dot-version-vector sibling set store.
type Store struct {
	mu   sync.Mutex
	cap  int
	keys map[string]*keyState
}

// New creates a Store whose per-key sibling count limit is cap.
// cap must be at least 1.
func New(cap int) (*Store, error) {
	if cap < 1 {
		return nil, ErrInvalidCap
	}
	return &Store{
		cap:  cap,
		keys: make(map[string]*keyState),
	}, nil
}

// Put appends a new sibling written by id under writer context ctx.
//
// The new dot is (id, seen[id]+1). Every sibling (j,m) covered by ctx,
// i.e. ctx[j] >= m, is deleted first. Validation errors are reported in a
// fixed order and a rejected Put never mutates any state.
func (s *Store) Put(key, id string, ctx map[string]int64, value string) error {
	if key == "" {
		return ErrEmptyKey
	}
	if id == "" {
		return ErrEmptyNodeID
	}
	for node := range ctx {
		if node == "" {
			return ErrEmptyContextNode
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	st := s.keys[key]
	for node, counter := range ctx {
		var seenCounter int64
		if st != nil {
			seenCounter = st.seen[node]
		}
		if counter > seenCounter {
			return ErrContextAhead
		}
	}

	var newCounter int64 = 1
	if st != nil {
		newCounter = st.seen[id] + 1
	}

	// Compute the post-removal list into a detached slice so that a cap
	// rejection leaves the stored state untouched.
	var kept []sibling
	if st != nil {
		for _, sib := range st.siblings {
			if ctx[sib.dot.ID] < sib.dot.N {
				kept = append(kept, sib)
			}
		}
	}
	if len(kept)+1 > s.cap {
		return ErrCapExceeded
	}

	if st == nil {
		st = &keyState{seen: make(map[string]int64)}
		s.keys[key] = st
	}
	st.siblings = append(kept, sibling{
		dot:   Dot{ID: id, N: newCounter},
		value: value,
	})
	st.seen[id] = newCounter
	return nil
}

// Get returns the sorted sibling list and seen-vector copy for key.
// A missing key yields an empty (non-nil) sibling list and empty context;
// the returned maps and slices never alias internal state.
func (s *Store) Get(key string) Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()

	siblings := []Sibling{}
	context := map[string]int64{}
	if st := s.keys[key]; st != nil {
		for _, sib := range st.siblings {
			siblings = append(siblings, Sibling{Dot: sib.dot, Value: sib.value})
		}
		for node, counter := range st.seen {
			context[node] = counter
		}
	}
	sortSiblings(siblings)
	return Snapshot{Siblings: siblings, Context: context}
}

// Merge reconciles another store into s.
// other is snapshotted before any mutation of s, Merge never applies the
// cap, and Merge(s, s) is a legal no-op.
func (s *Store) Merge(other *Store) {
	if other == nil {
		return
	}

	snap := other.snapshot()

	s.mu.Lock()
	defer s.mu.Unlock()

	for key := range mergeKeySet(s.keys, snap) {
		otherEntry := snap[key]
		st := s.keys[key]
		if st == nil {
			s.keys[key] = &keyState{
				siblings: cloneSiblings(otherEntry.siblings),
				seen:     cloneSeen(otherEntry.seen),
			}
			continue
		}

		merged := make(map[Dot]sibling, len(st.siblings)+len(otherEntry.siblings))
		for _, sib := range st.siblings {
			if _, present := otherDot(otherEntry, sib.dot); present || otherEntry.seen[sib.dot.ID] < sib.dot.N {
				merged[sib.dot] = sib
			}
		}
		for _, sib := range otherEntry.siblings {
			if _, present := localDot(st, sib.dot); present || st.seen[sib.dot.ID] < sib.dot.N {
				if existing, ok := merged[sib.dot]; ok {
					if sib.value > existing.value {
						merged[sib.dot] = sib
					}
				} else {
					merged[sib.dot] = sib
				}
			}
		}

		combined := make([]sibling, 0, len(merged))
		for _, sib := range merged {
			combined = append(combined, sib)
		}
		sortInternalSiblings(combined)
		st.siblings = combined

		if st.seen == nil {
			st.seen = make(map[string]int64)
		}
		for node, counter := range otherEntry.seen {
			if counter > st.seen[node] {
				st.seen[node] = counter
			}
		}
	}
}

func mergeKeySet(local map[string]*keyState, remote map[string]snapshotEntry) map[string]struct{} {
	keys := make(map[string]struct{}, len(local)+len(remote))
	for key := range local {
		keys[key] = struct{}{}
	}
	for key := range remote {
		keys[key] = struct{}{}
	}
	return keys
}

// snapshot returns detached copies of every key while holding other's lock
// only for the duration of the copy.
func (s *Store) snapshot() map[string]snapshotEntry {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make(map[string]snapshotEntry, len(s.keys))
	for key, st := range s.keys {
		out[key] = snapshotEntry{
			siblings: cloneSiblings(st.siblings),
			seen:     cloneSeen(st.seen),
		}
	}
	return out
}

func otherDot(entry snapshotEntry, dot Dot) (sibling, bool) {
	for _, sib := range entry.siblings {
		if sib.dot == dot {
			return sib, true
		}
	}
	return sibling{}, false
}

func localDot(st *keyState, dot Dot) (sibling, bool) {
	for _, sib := range st.siblings {
		if sib.dot == dot {
			return sib, true
		}
	}
	return sibling{}, false
}

func cloneSiblings(in []sibling) []sibling {
	if len(in) == 0 {
		return []sibling{}
	}
	out := make([]sibling, len(in))
	copy(out, in)
	return out
}

func cloneSeen(in map[string]int64) map[string]int64 {
	out := make(map[string]int64, len(in))
	for node, counter := range in {
		out[node] = counter
	}
	return out
}

func sortSiblings(in []Sibling) {
	sort.Slice(in, func(i, j int) bool {
		if in[i].Dot.ID != in[j].Dot.ID {
			return in[i].Dot.ID < in[j].Dot.ID
		}
		return in[i].Dot.N < in[j].Dot.N
	})
}

func sortInternalSiblings(in []sibling) {
	sort.Slice(in, func(i, j int) bool {
		if in[i].dot.ID != in[j].dot.ID {
			return in[i].dot.ID < in[j].dot.ID
		}
		return in[i].dot.N < in[j].dot.N
	})
}

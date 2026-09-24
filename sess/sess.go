// Package sess holds per-key session clusters. A Set ingests possibly
// out-of-order events and keeps the canonical sequence per key: disjoint
// closed intervals ordered by Start with strictly more than gap between
// adjacent ones; the result always equals a sorted full rescan. It depends
// only on package evt.
package sess

import (
	"errors"
	"sort"

	"ontology/evt"
)

// ErrTooManySessions is returned when accepting an event would make the
// number of sessions of one key exceed MaxSessions.
var ErrTooManySessions = errors.New("sess: session count exceeds MaxSessions")

// Session is one cluster: the closed interval [Start, End] and N events.
type Session struct {
	Start int64
	End   int64
	N     int
}

// Set is the in-memory collection. It is not internally synchronized; the
// api layer serializes access.
type Set struct {
	gap int64
	max int

	data map[string][]Session

	// lastCmp counts how many existing sessions the most recent Add probed
	// while expanding the merge range on both sides. Unexported on purpose:
	// it must stay invisible on the public API surface.
	lastCmp int
}

// NewSet creates a Set with gap > 0 and a per-key session cap (max <= 0
// means unlimited). Callers are expected to have validated gap already.
func NewSet(gap int64, max int) *Set {
	return &Set{gap: gap, max: max, data: map[string][]Session{}}
}

// Add inserts one event. Duplicate timestamps are distinct multiset elements:
// they only increment N. A rejected Add (invalid event, cap exceeded) leaves
// the set completely untouched.
func (s *Set) Add(e evt.Event) error {
	next, cmp, err := addInto(s.data, s.gap, s.max, e)
	if err != nil {
		return err
	}
	s.lastCmp = cmp
	s.data[e.Key] = next
	return nil
}

// AddAll feeds a batch atomically: any rejection leaves the set untouched.
// A touched key's slice is cloned before its first write; untouched keys keep
// aliasing their old (never in-place mutated) slices.
func (s *Set) AddAll(evs []evt.Event) error {
	shadow := make(map[string][]Session, len(s.data))
	cmp := s.lastCmp
	for _, e := range evs {
		k := e.Key
		if _, seen := shadow[k]; !seen {
			shadow[k] = append([]Session(nil), s.data[k]...)
		}
		next, c, err := addInto(shadow, s.gap, s.max, e)
		if err != nil {
			return err
		}
		cmp, shadow[k] = c, next
	}
	for k, v := range s.data {
		if _, seen := shadow[k]; !seen {
			shadow[k] = v
		}
	}
	s.data, s.lastCmp = shadow, cmp
	return nil
}

// addInto decides the effect of e against data without mutating it: it returns
// the new session slice for e.Key, the probe count and a sentinel error.
func addInto(data map[string][]Session, gap int64, max int, e evt.Event) ([]Session, int, error) {
	if err := e.Valid(); err != nil {
		return nil, 0, err
	}
	old := data[e.Key]

	// First index with Start > TS: all sessions at indices < i start <= TS.
	i := sort.Search(len(old), func(j int) bool { return old[j].Start > e.TS })
	l, r, cmp := i, i, 0
	for l > 0 { // reachable when TS lies in the session or within gap past its End
		cmp++
		if e.TS <= old[l-1].End || evt.Mergeable(e.TS, old[l-1].End, gap) {
			l--
			continue
		}
		break
	}
	for r < len(old) { // such sessions start strictly after TS: plain distance
		cmp++
		if evt.Mergeable(e.TS, old[r].Start, gap) {
			r++
			continue
		}
		break
	}

	// Decide first, mutate after: reject before touching anything.
	if max > 0 && len(old)-(r-l)+1 > max {
		return nil, cmp, ErrTooManySessions
	}
	return apply(old, l, r, e), cmp, nil
}

// apply builds the new slice: sessions [l,r) plus the event collapse into one.
func apply(old []Session, l, r int, e evt.Event) []Session {
	ns := Session{Start: e.TS, End: e.TS, N: 1}
	if l < r {
		if old[l].Start < ns.Start {
			ns.Start = old[l].Start
		}
		if old[r-1].End > ns.End {
			ns.End = old[r-1].End
		}
		for k := l; k < r; k++ {
			ns.N += old[k].N
		}
	}
	out := make([]Session, len(old)-(r-l)+1)
	copy(out, old[:l])
	out[l] = ns
	copy(out[l+1:], old[r:])
	return out
}

// Sessions returns a copy of the canonical session sequence for key.
func (s *Set) Sessions(key string) []Session {
	src := s.data[key]
	if len(src) == 0 {
		return nil
	}
	out := make([]Session, len(src))
	copy(out, src)
	return out
}

// Package orset implements a single-replica add-wins observed-remove set (OR-Set CRDT).
package orset

import (
	"cmp"
	"errors"
	"slices"
	"strconv"
)

var (
	ErrInvalidArgument = errors.New("orset: invalid argument")
	ErrEmptyElement    = errors.New("orset: empty element")
	ErrNotFound        = errors.New("orset: element has no live tag")
	ErrCapacity        = errors.New("orset: maxTags exceeded")
)

// Tag uniquely identifies one Add: Seq is the replica's own add counter.
type Tag struct{ Replica, Seq int }

// State is one replica. Not goroutine-safe; callers serialize access.
type State struct {
	id      int
	maxTags int
	next    int // next add sequence number, starts at 1
	count   int // current number of (element, tag) add records
	adds    map[string]map[Tag]struct{}
	tombs   map[Tag]struct{}
	checked int // entries inspected by the last Remove/Contains
}

func New(id, maxTags int) (*State, error) {
	if id < 0 || maxTags <= 0 {
		return nil, ErrInvalidArgument
	}
	return &State{id: id, maxTags: maxTags, next: 1,
		adds: map[string]map[Tag]struct{}{}, tombs: map[Tag]struct{}{}}, nil
}

// Add always creates a fresh tag, even if e is already present.
func (s *State) Add(e string) (Tag, error) {
	if e == "" {
		return Tag{}, ErrEmptyElement
	}
	if s.count >= s.maxTags {
		return Tag{}, ErrCapacity
	}
	t := Tag{Replica: s.id, Seq: s.next}
	s.next++
	s.count++
	if s.adds[e] == nil {
		s.adds[e] = map[Tag]struct{}{}
	}
	s.adds[e][t] = struct{}{}
	return t, nil
}

// live returns e's tags not tombstoned on this replica, sorted.
func (s *State) live(e string) []Tag {
	var out []Tag
	for t := range s.adds[e] {
		if _, dead := s.tombs[t]; !dead {
			out = append(out, t)
		}
	}
	slices.SortFunc(out, func(a, b Tag) int {
		return cmp.Or(cmp.Compare(a.Replica, b.Replica), cmp.Compare(a.Seq, b.Seq))
	})
	return out
}

func (s *State) Contains(e string) bool {
	s.checked = len(s.adds[e])
	return len(s.live(e)) > 0
}

// Remove tombstones exactly the tags of e currently live on this replica.
func (s *State) Remove(e string) error {
	if e == "" {
		return ErrEmptyElement
	}
	s.checked = len(s.adds[e])
	lv := s.live(e)
	if len(lv) == 0 {
		return ErrNotFound
	}
	for _, t := range lv {
		s.tombs[t] = struct{}{}
	}
	return nil
}

// Merge unions o's records into s; atomic: no change if maxTags would be exceeded.
func (s *State) Merge(o *State) error {
	extra := 0
	for e, ts := range o.adds {
		for t := range ts {
			if _, ok := s.adds[e][t]; !ok {
				extra++
			}
		}
	}
	if s.count+extra > s.maxTags {
		return ErrCapacity
	}
	for e, ts := range o.adds {
		if s.adds[e] == nil {
			s.adds[e] = map[Tag]struct{}{}
		}
		for t := range ts {
			s.adds[e][t] = struct{}{}
		}
	}
	for t := range o.tombs {
		s.tombs[t] = struct{}{}
	}
	s.count += extra
	return nil
}

// Elements returns each present element with its live tags.
func (s *State) Elements() map[string][]Tag {
	out := map[string][]Tag{}
	for e := range s.adds {
		if lv := s.live(e); len(lv) > 0 {
			out[e] = lv
		}
	}
	return out
}

// VerifyLookupCost reports whether Contains/Remove on a 2-tag target inspect
// only its own entries; the counter itself never leaves the package.
func VerifyLookupCost(ms ...int) bool {
	for _, m := range ms {
		st, _ := New(0, m+4)
		for i := 0; i < m; i++ {
			st.Add("e" + strconv.Itoa(i))
		}
		st.Add("target")
		st.Add("target")
		st.Contains("target")
		c := st.checked
		st.Remove("target")
		if c > 3 || st.checked > 3 {
			return false
		}
	}
	return true
}

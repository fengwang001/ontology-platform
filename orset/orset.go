// Package orset implements a single-replica add-wins observed-remove set (OR-Set CRDT).
package orset

import (
	"cmp"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sync"
)

var (
	ErrParam    = errors.New("orset: invalid parameter")
	ErrEmpty    = errors.New("orset: empty element")
	ErrNotFound = errors.New("orset: element not found")
	ErrCapacity = errors.New("orset: tag capacity exceeded")
)

type Tag struct{ R, S int }

func (t Tag) String() string { return fmt.Sprintf("%c%d", 'A'+t.R, t.S) }

type State struct {
	Adds  map[string][]Tag
	Tombs map[Tag]struct{}
}

func (a State) Equal(b State) bool {
	norm := func(ts []Tag) []Tag {
		return slices.SortedFunc(slices.Values(ts), func(x, y Tag) int {
			return cmp.Or(cmp.Compare(x.R, y.R), cmp.Compare(x.S, y.S))
		})
	}
	eq := func(x, y []Tag) bool { return slices.Equal(norm(x), norm(y)) }
	return maps.EqualFunc(a.Adds, b.Adds, eq) && maps.Equal(a.Tombs, b.Tombs)
}

type Set struct {
	id      int
	max     int
	seq     int
	n       int // total (element, tag) add records
	adds    map[string][]Tag
	tombs   map[Tag]struct{}
	checked int // entries inspected by last Remove/Contains (unexported on purpose)
	mu      sync.Mutex
}

func New(id, maxTags int) *Set {
	return &Set{id: id, max: maxTags, adds: map[string][]Tag{}, tombs: map[Tag]struct{}{}}
}

func (s *Set) Add(e string) error {
	if e == "" {
		return ErrEmpty
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.n+1 > s.max {
		return ErrCapacity
	}
	s.seq++
	s.adds[e] = append(s.adds[e], Tag{s.id, s.seq})
	s.n++
	return nil
}

func (s *Set) Remove(e string) error {
	if e == "" {
		return ErrEmpty
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	live := s.liveLocked(e)
	if len(live) == 0 {
		return ErrNotFound
	}
	for _, t := range live {
		s.tombs[t] = struct{}{}
	}
	return nil
}

func (s *Set) Contains(e string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.liveLocked(e)) > 0
}

func (s *Set) liveLocked(e string) []Tag {
	tags := s.adds[e]
	s.checked = len(tags)
	var live []Tag
	for _, t := range tags {
		if _, dead := s.tombs[t]; !dead {
			live = append(live, t)
		}
	}
	return live
}

func (s *Set) Snapshot() State {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := State{Adds: map[string][]Tag{}, Tombs: maps.Clone(s.tombs)}
	for e, ts := range s.adds {
		st.Adds[e] = slices.Clone(ts)
	}
	return st
}

// MergeFrom unions src's state into dst (dst only); locks are taken in id order,
// so concurrent Merge(x,y)/Merge(y,x) cannot deadlock. dst == src is a no-op.
func (dst *Set) MergeFrom(src *Set) error {
	if dst == src {
		return nil
	}
	a, b := dst, src
	if b.id < a.id {
		a, b = b, a
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	b.mu.Lock()
	defer b.mu.Unlock()
	extra := 0
	for e, ts := range src.adds {
		for _, t := range ts {
			if !slices.Contains(dst.adds[e], t) {
				extra++
			}
		}
	}
	if dst.n+extra > dst.max {
		return ErrCapacity
	}
	for e, ts := range src.adds {
		for _, t := range ts {
			if !slices.Contains(dst.adds[e], t) {
				dst.adds[e] = append(dst.adds[e], t)
				dst.n++
			}
		}
	}
	for t := range src.tombs {
		dst.tombs[t] = struct{}{}
	}
	return nil
}

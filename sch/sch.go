// Package sch holds the pure materialized state: parent rows, child rows
// (ck -> parent) and the per-parent reference count. It depends on nothing.
package sch

import "sort"

// State is the in-memory materialization. All fields are unexported; the fk
// package mutates it only through the methods below.
type State struct {
	p    map[string]struct{}
	c    map[string]string // child key -> parent key
	refs map[string]int    // parent key -> number of children referencing it

	// lastDelScanned counts child rows scanned by the most recent
	// parent-delete "has references?" probe. A correct implementation answers
	// from refs, so it stays 0 regardless of table size. Unexported on
	// purpose: it must never leave the package (no exported accessor).
	lastDelScanned int
}

// NewState returns an empty state.
func NewState() *State {
	return &State{
		p:    map[string]struct{}{},
		c:    map[string]string{},
		refs: map[string]int{},
	}
}

// HasParent reports whether pk is currently present.
func (s *State) HasParent(pk string) bool {
	_, ok := s.p[pk]
	return ok
}

// AddParent inserts pk. The caller is expected to have checked existence
// (P.ins is idempotent), but a duplicate insert is harmless.
func (s *State) AddParent(pk string) {
	s.p[pk] = struct{}{}
}

// RemoveParent deletes pk; caller must have verified it exists and is
// unreferenced.
func (s *State) RemoveParent(pk string) {
	delete(s.p, pk)
}

// HasChild reports whether ck is currently present.
func (s *State) HasChild(ck string) bool {
	_, ok := s.c[ck]
	return ok
}

// AddChild inserts ck -> parent and maintains the reference count. Caller must
// have verified the child is absent and the parent present.
func (s *State) AddChild(ck, parent string) {
	s.c[ck] = parent
	s.refs[parent]++
}

// RemoveChild deletes ck, drops one reference from its parent and returns that
// parent key. Caller must have verified the child exists.
func (s *State) RemoveChild(ck string) string {
	parent := s.c[ck]
	delete(s.c, ck)
	s.refs[parent]--
	if s.refs[parent] == 0 {
		delete(s.refs, parent)
	}
	return parent
}

// RefCountOf returns the maintained number of children referencing pk. This
// is the business reference count (not the internal delete-probe scan
// counter, which stays unexported).
func (s *State) RefCountOf(pk string) int { return s.refs[pk] }

// ProbeParentDelete answers the P.del admission question purely from the
// reference count: whether pk exists and how many children reference it. No
// child table is traversed, so lastDelScanned is a size-independent constant.
func (s *State) ProbeParentDelete(pk string) (exists bool, refs int) {
	s.lastDelScanned = 0 // decision uses refs, not a scan over c
	if !s.HasParent(pk) {
		return false, 0
	}
	return true, s.refs[pk]
}

// Parents returns a sorted snapshot copy of the parent set.
func (s *State) Parents() []string {
	out := make([]string, 0, len(s.p))
	for pk := range s.p {
		out = append(out, pk)
	}
	sort.Strings(out)
	return out
}

// Children returns a snapshot copy of the ck -> parent map.
func (s *State) Children() map[string]string {
	out := make(map[string]string, len(s.c))
	for ck, parent := range s.c {
		out[ck] = parent
	}
	return out
}

// Package api is the outward-facing, concurrency-safe facade over casc.
package api

import (
	"errors"
	"fmt"
	"slices"

	"ontology/casc"
	"ontology/ent"
)

// API is a concurrency-safe handle to the entity set.
type API struct{ s *casc.Set }

func New() *API { return &API{s: casc.New()} }

func (a *API) Add(id, parent int) error       { return a.s.Add(id, parent) }
func (a *API) Delete(root int) ([]int, error) { return a.s.Delete(root) }
func (a *API) Orphans() []int                 { return a.s.Orphans() }
func (a *API) CleanupOrphans() []int          { return a.s.CleanupOrphans() }
func (a *API) Exists(id int) bool             { return a.s.Exists(id) }

// scenario is the fixed NOTES.md entity set, including orphan 10->99.
var scenario = [][2]int{{1, 0}, {2, 1}, {3, 1}, {4, 2}, {5, 2}, {6, 3}, {7, 4}, {8, 0}, {9, 8}, {10, 99}}

// SelfCheck verifies the four invariants on the built-in scenario set.
// It works on its own temporary set, never touching the receiver, so it
// is safe for concurrent use.
func (a *API) SelfCheck() error {
	s := casc.New()
	for _, p := range scenario {
		if err := s.Insert(p[0], p[1]); err != nil {
			return fmt.Errorf("selfcheck seed: %w", err)
		}
	}
	if err := checkDelete(s); err != nil {
		return err
	}
	return checkRejected(s)
}

// checkDelete pins invariants 1 (closure match), 2 (post-order) and 3
// (no dangling references, complete cascade).
func checkDelete(s *casc.Set) error {
	orphansBefore := s.Orphans()
	got, err := s.Delete(1)
	if err != nil {
		return fmt.Errorf("selfcheck delete: %w", err)
	}
	parentOf := map[int]int{}
	closure := map[int]bool{1: true}
	for _, p := range scenario {
		parentOf[p[0]] = p[1]
	}
	for grow := true; grow; {
		grow = false
		for _, p := range scenario {
			if closure[p[1]] && !closure[p[0]] {
				closure[p[0]] = true
				grow = true
			}
		}
	}
	if len(got) != len(closure) {
		return errors.New("selfcheck: delete set differs from closure")
	}
	pos := make(map[int]int, len(got))
	for i, id := range got {
		if !closure[id] {
			return errors.New("selfcheck: delete set has id outside closure")
		}
		pos[id] = i
	}
	for _, p := range scenario { // invariant 2: child before parent
		ci, cok := pos[p[0]]
		pi, pok := pos[p[1]]
		if cok && pok && ci > pi {
			return errors.New("selfcheck: parent placed before child")
		}
	}
	for i := 0; i < len(got); i++ { // invariant 2: siblings ascending
		for j := i + 1; j < len(got); j++ {
			if parentOf[got[i]] == parentOf[got[j]] && got[i] > got[j] {
				return errors.New("selfcheck: siblings not ascending")
			}
		}
	}
	if o := s.Orphans(); !subset(o, orphansBefore) { // invariant 3
		return errors.New("selfcheck: delete created new orphans")
	}
	for _, p := range scenario { // invariant 3: cascade completeness
		_, parGone := pos[p[1]]
		_, childGone := pos[p[0]]
		if p[1] != 0 && parGone && !childGone {
			return errors.New("selfcheck: cascade left a child behind")
		}
	}
	return nil
}

// checkRejected pins invariant 4: rejected ops fail decidably and leave
// no trace; the set keeps working afterwards.
func checkRejected(s *casc.Set) error {
	snap := func() string {
		b := fmt.Sprint(s.Orphans())
		for id := 0; id <= 12; id++ {
			b += fmt.Sprint(s.Exists(id))
		}
		return b
	}
	before := snap()
	rejects := []struct {
		want error
		op   func() error
	}{
		{ent.ErrInvalidID, func() error { return s.Add(0, 0) }},
		{ent.ErrDuplicateID, func() error { return s.Add(8, 0) }},
		{ent.ErrInvalidParent, func() error { return s.Add(11, 98) }},
		{casc.ErrNotFound, func() error { _, err := s.Delete(999); return err }},
	}
	for _, r := range rejects {
		if err := r.op(); !errors.Is(err, r.want) {
			return fmt.Errorf("selfcheck: want %v, got %v", r.want, err)
		}
	}
	if snap() != before {
		return errors.New("selfcheck: rejected op mutated state")
	}
	if err := s.Add(11, 8); err != nil { // still usable afterwards
		return fmt.Errorf("selfcheck: set broken after rejects: %w", err)
	}
	return nil
}

func subset(a, b []int) bool {
	for _, x := range a {
		if !slices.Contains(b, x) {
			return false
		}
	}
	return true
}

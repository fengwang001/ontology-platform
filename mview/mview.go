// Package mview is the materialized-view state machine: base updates bump a
// global epoch, views refresh in dependency order, and staleness is derived
// from revision comparisons against transitive base tables.
package mview

import (
	"errors"
	"maps"
	"sync"

	"ontology/dep"
)

// Expr recomputes a view from its direct dependencies' current values,
// supplied in graph Direct() order.
type Expr func(vals []int) int

// Decidable sentinel errors; the four failure modes are pairwise distinct.
var (
	ErrNotFound     = errors.New("mview: unknown name")
	ErrUpdateView   = errors.New("mview: UpdateBase targets a view")
	ErrRefreshBase  = errors.New("mview: Refresh targets a base")
	ErrDepsNotReady = errors.New("mview: Refresh rejected: a direct dependency is stale")
)

// State holds all in-memory view state.
type State struct {
	mu    sync.RWMutex
	g     *dep.Graph
	val   map[string]int
	rev   map[string]int
	expr  map[string]Expr
	epoch int
	// lastVisit counts nodes reached while propagating invalidation along
	// reverse edges after the latest UpdateBase. Unexported and never exposed.
	lastVisit int
}

// New constructs a state machine. initial covers every node, exprs every view.
func New(g *dep.Graph, initial map[string]int, exprs map[string]Expr) *State {
	return &State{g: g, val: maps.Clone(initial), rev: map[string]int{}, expr: maps.Clone(exprs)}
}

// UpdateBase changes a base table, advances the epoch and propagates
// invalidation along reverse edges. Validation precedes any mutation.
func (s *State) UpdateBase(name string, val int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	k, ok := s.g.KindOf(name)
	if !ok {
		return ErrNotFound
	}
	if k == dep.View {
		return ErrUpdateView
	}
	s.epoch++
	s.val[name] = val
	s.rev[name] = s.epoch
	s.lastVisit = s.propagate(name)
	return nil
}

// propagate walks reverse dependency edges from a changed base and returns
// the number of dependent nodes visited; unrelated branches are untouched.
func (s *State) propagate(base string) int {
	seen := map[string]bool{}
	frontier := []string{base}
	for len(frontier) > 0 {
		cur := frontier[0]
		frontier = frontier[1:]
		for _, d := range s.g.Dependents(cur) {
			if !seen[d] {
				seen[d] = true
				frontier = append(frontier, d)
			}
		}
	}
	return len(seen)
}

// Refresh recomputes a view iff all direct dependencies are currently valid.
func (s *State) Refresh(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	k, ok := s.g.KindOf(name)
	if !ok {
		return ErrNotFound
	}
	if k == dep.Base {
		return ErrRefreshBase
	}
	deps := s.g.Direct(name)
	for _, d := range deps {
		if s.staleLocked(d) {
			return ErrDepsNotReady
		}
	}
	vals := make([]int, len(deps))
	for i, d := range deps {
		vals[i] = s.val[d]
	}
	// All checks passed: only now does any state change.
	s.val[name] = s.expr[name](vals)
	s.rev[name] = s.epoch
	return nil
}

// IsStale reports a view's staleness (some transitive base out-revises it);
// base tables are never stale.
func (s *State) IsStale(name string) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if _, ok := s.g.KindOf(name); !ok {
		return false, ErrNotFound
	}
	return s.staleLocked(name), nil
}

func (s *State) staleLocked(name string) bool {
	k, ok := s.g.KindOf(name)
	if !ok || k == dep.Base {
		return false
	}
	for _, b := range s.g.TransitiveBases(name) {
		if s.rev[b] > s.rev[name] {
			return true
		}
	}
	return false
}

// Value returns the stored value of a node (stale value for a stale view).
func (s *State) Value(name string) (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if _, ok := s.g.KindOf(name); !ok {
		return 0, ErrNotFound
	}
	return s.val[name], nil
}

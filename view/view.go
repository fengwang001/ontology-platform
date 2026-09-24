// Package view holds view values, dirty marks, invalidation propagation
// and topological recomputation. It depends only on dag.
package view

import (
	"errors"

	"ontology/dag"
)

// Distinguishable sentinel errors.
var (
	ErrUnresolved = errors.New("view: unresolved view name")
	ErrNotBase    = errors.New("view: Set on a non-base view")
)

// Store owns the DAG, values, dirty marks and the evaluation counter.
type Store struct {
	g         *dag.Graph
	fns       map[string]func(...int64) int64
	val       map[string]int64
	hasVal    map[string]bool
	dirty     map[string]bool
	evalCount int // unexported: views actually evaluated by last Recompute
}

// New returns an empty store.
func New() *Store {
	return &Store{
		g:      dag.New(),
		fns:    map[string]func(...int64) int64{},
		val:    map[string]int64{},
		hasVal: map[string]bool{},
		dirty:  map[string]bool{},
	}
}

// AddView registers a view. dag.Add validates atomically; the store maps
// are touched only after it succeeds, so a rejection leaves no trace.
func (s *Store) AddView(name string, deps []string, fn func(...int64) int64) error {
	if err := s.g.Add(name, deps); err != nil {
		return err
	}
	s.fns[name] = fn
	return nil
}

// Set supplies a value to a base view and marks it and the transitive
// closure of its downstream views dirty (boolean marks: deduped).
func (s *Store) Set(name string, val int64) error {
	if !s.g.Has(name) {
		return ErrUnresolved
	}
	if len(s.g.Deps(name)) != 0 {
		return ErrNotBase
	}
	s.val[name] = val
	s.hasVal[name] = true
	s.dirty[name] = true
	for _, d := range s.g.Downstream(name) {
		s.dirty[d] = true
	}
	return nil
}

// closure expands set along registered dependency edges; it returns
// ErrUnresolved if any member still has an unregistered dependency.
func (s *Store) closure(set map[string]bool) (map[string]bool, error) {
	closed := map[string]bool{}
	stack := make([]string, 0, len(set))
	for n := range set {
		stack = append(stack, n)
	}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if closed[cur] {
			continue
		}
		closed[cur] = true
		for _, d := range s.g.Deps(cur) {
			if !s.g.Has(d) {
				return nil, ErrUnresolved
			}
			if !closed[d] {
				stack = append(stack, d)
			}
		}
	}
	return closed, nil
}

// Recompute re-evaluates every dirty derived view in dependency-first
// topological order. All validation happens before any value changes,
// and new values are committed in one step, so a failure leaves no trace
// and readers never observe a half-finished round.
func (s *Store) Recompute() error {
	if len(s.dirty) == 0 {
		s.evalCount = 0
		return nil
	}
	set, err := s.closure(s.dirty)
	if err != nil {
		return err
	}
	order, err := s.g.Topo(set)
	if err != nil {
		return err
	}
	next := map[string]int64{}
	n := 0
	for _, name := range order {
		if !s.dirty[name] {
			continue // dependency pulled in only for ordering: not evaluated
		}
		deps := s.g.Deps(name)
		args := make([]int64, len(deps))
		for i, d := range deps {
			if v, ok := next[d]; ok {
				args[i] = v
			} else {
				args[i] = s.val[d]
			}
		}
		if fn := s.fns[name]; fn != nil {
			next[name] = fn(args...)
			n++
		} else {
			next[name] = s.val[name] // dirty base view: value already Set
		}
	}
	for k, v := range next {
		s.val[k] = v
		s.hasVal[k] = true
	}
	s.dirty = map[string]bool{}
	s.evalCount = n
	return nil
}

// Get returns the current value of a view.
func (s *Store) Get(name string) (int64, bool, error) {
	if !s.g.Has(name) {
		return 0, false, ErrUnresolved
	}
	return s.val[name], s.hasVal[name], nil
}

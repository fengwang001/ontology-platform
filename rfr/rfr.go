// Package rfr executes the batch refresh: each dirty view recomputed once in
// topological order, emitting the -old/+new change log.
package rfr

import (
	"errors"
	"maps"
	"sync"

	"ontology/dag"
)

// The four decidable, pairwise-distinct failure classes.
var (
	ErrCycle        = dag.ErrCycle
	ErrEmptyName    = errors.New("rfr: empty view or base name")
	ErrUnknownName  = errors.New("rfr: unknown name")
	ErrTooManyViews = errors.New("rfr: view count exceeds maxViews")
)

var errRedeclared = errors.New("rfr: name already declared")

type Change struct {
	Name string
	Old  int64
	New  int64
}

// Refresher owns the graph and all in-process state.
type Refresher struct {
	mu         sync.RWMutex
	maxViews   int
	g          *dag.Graph
	bases      map[string]bool
	baseVal    map[string]int64 // base values already applied by prior Refresh
	viewVal    map[string]int64 // current view values
	pending    map[string]int64 // staged batch values; also promoted-base roots
	recomputes int              // unexported: recomputations in last Refresh
}

type Snapshot struct {
	Views []string
	Deps  map[string][]string
	Bases map[string]int64
}

func (r *Refresher) Snapshot() Snapshot {
	r.mu.RLock()
	defer r.mu.RUnlock()
	vs, deps := r.g.Views(), map[string][]string{}
	for _, v := range vs {
		deps[v] = r.g.Deps(v)
	}
	return Snapshot{Views: vs, Deps: deps, Bases: maps.Clone(r.baseVal)}
}

// New creates a Refresher; maxViews <= 0 means unlimited.
func New(maxViews int) *Refresher {
	return &Refresher{maxViews: maxViews, g: dag.New(), bases: map[string]bool{},
		baseVal: map[string]int64{}, viewVal: map[string]int64{}, pending: map[string]int64{}}
}

// AddView declares a view with immutable deps; an unseen leaf becomes a base,
// and a base may be promoted to a view (the only new multi-node cycle shape).
func (r *Refresher) AddView(name string, deps []string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if name == "" {
		return ErrEmptyName
	}
	for _, d := range deps {
		if d == "" {
			return ErrEmptyName
		}
	}
	if r.g.HasView(name) {
		return errRedeclared
	}
	// Promoting an existing base to a view adds no view, so it ignores the cap.
	if !r.bases[name] && r.maxViews > 0 && len(r.viewVal) >= r.maxViews {
		return ErrTooManyViews
	}
	if err := r.g.AddView(name, deps); err != nil {
		return err // self-edge or path closing back on name; graph untouched
	}
	r.viewVal[name] = r.baseVal[name] // promoted base: its old value is -old
	if r.bases[name] {
		delete(r.bases, name)
		delete(r.baseVal, name)
		r.pending[name] = 0 // root marker; descendants follow via the closure
	}
	for _, d := range deps {
		if !r.g.HasView(d) {
			r.bases[d], r.baseVal[d] = true, 0
		}
	}
	return nil
}

// SetBase stages a base change; within a batch only the last write survives.
func (r *Refresher) SetBase(name string, v int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if name == "" {
		return ErrEmptyName
	}
	if !r.bases[name] {
		return ErrUnknownName
	}
	r.pending[name] = v
	return nil
}

// Refresh applies the batch: base end values land first, then every dirty
// view is recomputed once in lexicographic topo order against current deps.
func (r *Refresher) Refresh() []Change {
	r.mu.Lock()
	defer r.mu.Unlock()
	roots := map[string]bool{}
	for b, v := range r.pending {
		roots[b] = true
		if r.bases[b] {
			r.baseVal[b] = v // promoted-base markers carry no value
		}
	}
	r.pending = map[string]int64{}
	order := r.g.Order(r.g.Descendants(roots))
	log := make([]Change, 0, len(order))
	r.recomputes = 0
	for _, v := range order {
		old := r.viewVal[v]
		var sum int64
		for _, d := range r.g.Deps(v) {
			if r.g.HasView(d) {
				sum += r.viewVal[d] // already-refreshed view: its new value
			} else {
				sum += r.baseVal[d] // base: this batch's end value
			}
		}
		r.viewVal[v], r.recomputes = sum, r.recomputes+1
		log = append(log, Change{Name: v, Old: old, New: sum})
	}
	return log
}

func (r *Refresher) Views() map[string]int64 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return maps.Clone(r.viewVal)
}

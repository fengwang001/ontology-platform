// Package prop holds current node values and propagates source changes
// level by level, producing a glitch-free change log. Depends on dag.
package prop

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"sort"
	"sync"

	"ontology/dag"
)

// ErrNotSource rejects Apply keys that name derived nodes.
var ErrNotSource = errors.New("prop: cannot set a derived node")

// Change is one log entry: a node's old and new value.
type Change struct {
	Name     string
	Old, New int64
}

// Engine stores values and applies updates. Safe for concurrent use.
type Engine struct {
	g       *dag.Graph
	mu      sync.RWMutex
	vals    map[string]int64
	checked int // nodes inspected during the last Apply (white-box tests only)
}

func eval(d dag.Def, vals map[string]int64) int64 {
	switch d.Kind {
	case "src":
		return d.K
	case "scale":
		return d.K * vals[d.Inputs[0]]
	case "add":
		return vals[d.Inputs[0]] + d.K
	}
	var s int64
	for _, in := range d.Inputs {
		s += vals[in]
	}
	return s
}

// New computes initial values by one full level-ordered evaluation.
func New(g *dag.Graph) *Engine {
	e := &Engine{g: g, vals: map[string]int64{}}
	names := make([]string, 0, len(g.Defs))
	for n := range g.Defs {
		names = append(names, n)
	}
	sort.Slice(names, func(i, j int) bool {
		if g.Levels[names[i]] != g.Levels[names[j]] {
			return g.Levels[names[i]] < g.Levels[names[j]]
		}
		return names[i] < names[j]
	})
	for _, n := range names {
		e.vals[n] = eval(g.Defs[n], e.vals)
	}
	return e
}

// Apply sets sources simultaneously and propagates by level rounds.
// Any rejected key fails the whole batch before any state changes.
func (e *Engine) Apply(sets map[string]int64) ([]Change, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	keys := make([]string, 0, len(sets))
	for k := range sets {
		if _, ok := e.g.Defs[k]; !ok {
			return nil, fmt.Errorf("%w: %q", dag.ErrUnknownNode, k)
		}
		keys = append(keys, k)
	}
	for _, k := range keys {
		if e.g.Defs[k].Kind != "src" {
			return nil, fmt.Errorf("%w: %q", ErrNotSource, k)
		}
	}
	sort.Strings(keys)
	e.checked = 0
	var log []Change
	changed := map[string]bool{}
	buckets := make([][]string, e.g.MaxLevel+1)
	queued := map[string]bool{}
	enqueue := func(n string) {
		if !queued[n] {
			queued[n] = true
			lv := e.g.Levels[n]
			buckets[lv] = append(buckets[lv], n)
		}
	}
	for _, k := range keys { // round 0: sources, name order
		e.checked++
		if old := e.vals[k]; old != sets[k] {
			e.vals[k] = sets[k]
			log = append(log, Change{k, old, sets[k]})
			changed[k] = true
			for _, c := range e.g.Consumers[k] {
				enqueue(c)
			}
		}
	}
	for r := 1; r <= e.g.MaxLevel; r++ {
		sort.Strings(buckets[r])
		for _, n := range buckets[r] {
			e.checked++
			if !slices.ContainsFunc(e.g.Defs[n].Inputs, func(s string) bool { return changed[s] }) {
				continue
			}
			e.checked++
			old := e.vals[n]
			if nv := eval(e.g.Defs[n], e.vals); nv != old {
				e.vals[n] = nv
				log = append(log, Change{n, old, nv})
				changed[n] = true
				for _, c := range e.g.Consumers[n] {
					enqueue(c)
				}
			}
		}
	}
	return log, nil
}

// Value returns one node's current value.
func (e *Engine) Value(name string) (int64, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	v, ok := e.vals[name]
	return v, ok
}

// View returns a snapshot copy of all current values.
func (e *Engine) View() map[string]int64 {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return maps.Clone(e.vals)
}

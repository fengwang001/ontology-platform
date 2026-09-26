// Package api is the public facade of the dominator engine.
package api

import (
	"errors"
	"fmt"
	"sync"

	"ontology/dgraph"
	"ontology/dom"
)

// ErrNotComputed reports IDom called before Compute.
var ErrNotComputed = errors.New("api: Compute has not been called")

// Engine wraps a graph and its dominator tree; methods are concurrency-safe.
type Engine struct {
	mu   sync.RWMutex
	g    *dgraph.Graph
	tree *dom.Tree
}

func New(n int) (*Engine, error) {
	g, err := dgraph.New(n)
	if err != nil {
		return nil, err
	}
	return &Engine{g: g}, nil
}

func (e *Engine) AddEdge(u, v int) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.g.AddEdge(u, v)
}

func (e *Engine) Compute() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.tree = dom.Compute(e.g)
}

func (e *Engine) IDom(v int) (int, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.tree == nil {
		return 0, ErrNotComputed
	}
	if v < 0 || v >= e.g.N() {
		return 0, dgraph.ErrNodeOutOfRange
	}
	return e.tree.IDom(v), nil
}

func (e *Engine) Dominates(a, b int) bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.tree != nil && e.tree.Dominates(a, b)
}

func (e *Engine) EdgeCount() int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.g.EdgeCount()
}

// reachableFrom0 skips node skip (-1: none); removal tests dominance.
func reachableFrom0(g *dgraph.Graph, skip int) []bool {
	vis := make([]bool, g.N())
	if skip == 0 {
		return vis
	}
	var dfs func(u int)
	dfs = func(u int) {
		vis[u] = true
		for _, v := range g.Succ(u) {
			if v != skip && !vis[v] {
				dfs(v)
			}
		}
	}
	dfs(0)
	return vis
}

// SelfCheck verifies the four invariants on built-in graphs.
func SelfCheck() error {
	cases := []struct {
		n     int
		edges [][2]int
	}{
		{7, [][2]int{{0, 1}, {0, 6}, {1, 2}, {1, 6}, {2, 3}, {2, 4}, {3, 5}, {4, 5}}},
		{5, [][2]int{{0, 1}, {1, 2}, {0, 2}, {2, 3}, {3, 4}, {4, 2}}}, // has a cycle
		{4, [][2]int{{0, 1}, {2, 3}}},                                 // 2,3 unreachable
	}
	for _, c := range cases {
		e, err := New(c.n)
		if err != nil {
			return err
		}
		for _, ed := range c.edges {
			if err := e.AddEdge(ed[0], ed[1]); err != nil {
				return err
			}
		}
		e.Compute()
		tr, reach := e.tree, reachableFrom0(e.g, -1)
		for a := 0; a < c.n; a++ {
			without := reachableFrom0(e.g, a)
			for b := 0; b < c.n; b++ {
				if want := reach[b] && !without[b]; tr.Dominates(a, b) != want {
					return fmt.Errorf("selfcheck: Dominates(%d,%d) mismatches definition", a, b)
				}
			}
		}
		for v := 1; v < c.n; v++ {
			iv := tr.IDom(v)
			if !reach[v] {
				if iv != -1 {
					return fmt.Errorf("selfcheck: idom(%d)=%d, want -1", v, iv)
				}
				continue
			}
			if iv == -1 || !tr.Dominates(iv, v) {
				return fmt.Errorf("selfcheck: idom(%d)=%d does not dominate v", v, iv)
			}
			for d := 0; d < c.n; d++ {
				if d != v && d != iv && tr.Dominates(d, v) && !tr.Dominates(d, iv) {
					return fmt.Errorf("selfcheck: idom(%d)=%d not deepest", v, iv)
				}
			}
		}
	}
	if _, err := New(0); !errors.Is(err, dgraph.ErrNonPositiveN) {
		return errors.New("selfcheck: non-positive n accepted")
	}
	e, err := New(3)
	if err != nil {
		return err
	}
	if e.AddEdge(0, 1) != nil || e.AddEdge(0, 3) == nil || e.AddEdge(1, 1) == nil ||
		e.AddEdge(0, 1) == nil || e.EdgeCount() != 1 {
		return errors.New("selfcheck: rejected op accepted or mutated state")
	}
	if !dom.QueryCostBounded(100, 1000, 10000) {
		return errors.New("selfcheck: query cost grows with graph size")
	}
	return nil
}

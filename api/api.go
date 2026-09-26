// Package api is the public facade of the min-cut service. It depends
// on sw (which depends on wg); the dependency direction is one-way.
package api

import (
	"errors"
	"fmt"
	"sync"

	"ontology/sw"
	"ontology/wg"
)

// Graph is a concurrency-safe weighted undirected graph.
type Graph struct {
	mu sync.RWMutex
	g  *wg.Graph
}

// New creates a graph with n nodes; n < 2 is rejected.
func New(n int) (*Graph, error) {
	g, err := wg.New(n)
	if err != nil {
		return nil, err
	}
	return &Graph{g: g}, nil
}

// AddEdge registers the undirected edge (u,v,w); rejections are atomic.
func (a *Graph) AddEdge(u, v int, w int64) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.g.AddEdge(u, v, w)
}

// MinCut returns the global minimum cut value.
func (a *Graph) MinCut() (int64, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return sw.MinCut(a.g), nil
}

// EdgeCount returns the number of registered edges.
func (a *Graph) EdgeCount() int {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.g.EdgeCount()
}

type edge struct{ u, v int }

// bruteMinCut enumerates all bipartitions (node 0 fixed in S) and
// returns the minimum crossing weight: the ground-truth reference.
func bruteMinCut(n int, es []edge, w map[[2]int]int64) int64 {
	inS := func(v int, mask int) bool { return v == 0 || mask>>(v-1)&1 == 1 }
	best := int64(-1)
	for mask := 0; mask < 1<<(n-1)-1; mask++ { // full mask would empty V\S
		var cut int64
		for _, e := range es {
			if inS(e.u, mask) != inS(e.v, mask) {
				cut += w[[2]int{e.u, e.v}]
			}
		}
		if best < 0 || cut < best {
			best = cut
		}
	}
	return best
}

// SelfCheck verifies the four invariants of NOTES.md on built-in
// graphs. It uses only local state and is safe for concurrent use.
func (a *Graph) SelfCheck() error {
	cases := []struct {
		n  int
		es [][3]int64
	}{
		{4, [][3]int64{{0, 1, 3}, {1, 2, 2}, {0, 2, 5}, {2, 3, 1}}},                         // NOTES example, cut 1
		{4, [][3]int64{{0, 1, 10}, {2, 3, 10}, {0, 2, 1}, {1, 2, 1}, {0, 3, 1}, {1, 3, 1}}}, // merge-sum discriminator, cut 4
		{3, nil}, // no edges: isolated nodes, cut 0
		{5, [][3]int64{{0, 1, 7}, {1, 2, 3}, {2, 3, 3}, {3, 0, 3}, {1, 3, 2}}},
	}
	for i, c := range cases {
		g, err := New(c.n)
		if err != nil {
			return err
		}
		var es []edge
		w := map[[2]int]int64{}
		for _, e := range c.es {
			if err := g.AddEdge(int(e[0]), int(e[1]), e[2]); err != nil {
				return fmt.Errorf("selfcheck: build graph %d: %w", i, err)
			}
			es = append(es, edge{int(e[0]), int(e[1])})
			w[[2]int{int(e[0]), int(e[1])}] = e[2]
		}
		got, err := g.MinCut()
		if err != nil {
			return err
		}
		if want := bruteMinCut(c.n, es, w); got != want { // invariants 1-3
			return fmt.Errorf("selfcheck: graph %d: mincut %d, want %d", i, got, want)
		}
	}
	// Invariant 4: rejected operations leave the graph untouched.
	g, err := New(3)
	if err != nil {
		return err
	}
	if err := g.AddEdge(0, 1, 5); err != nil {
		return err
	}
	for _, err := range []error{g.AddEdge(0, 3, 1), g.AddEdge(2, 2, 1), g.AddEdge(1, 0, 9)} {
		if err == nil {
			return errors.New("selfcheck: invalid edge accepted")
		}
	}
	if _, err := New(1); err == nil {
		return errors.New("selfcheck: n<2 accepted")
	}
	if g.EdgeCount() != 1 {
		return errors.New("selfcheck: rejected op changed state")
	}
	if err := g.AddEdge(1, 2, 4); err != nil {
		return errors.New("selfcheck: graph unusable after rejects")
	}
	return nil
}

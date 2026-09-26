// Package api is the public entry point for DAG minimum path cover.
package api

import (
	"errors"
	"fmt"
	"ontology/dag"
	"ontology/mpc"
	"sync"
)

var (
	ErrInvalidN   = dag.ErrInvalidN // five mutually distinct sentinel errors
	ErrOutOfRange = dag.ErrOutOfRange
	ErrSelfLoop   = dag.ErrSelfLoop
	ErrDuplicate  = dag.ErrDuplicate
	ErrCyclic     = dag.ErrCyclic
	ErrSelfCheck  = errors.New("api: self check failed")
)

type API struct {
	mu sync.RWMutex
	g  *dag.Graph
}

func New(n int) (*API, error) {
	g, err := dag.New(n)
	if err != nil {
		return nil, err
	}
	return &API{g: g}, nil
}
func (a *API) AddEdge(u, v int) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.g.AddEdge(u, v)
}
func (a *API) EdgeCount() int {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.g.EdgeCount()
}
func (a *API) Solve() (int, [][]int, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	r, err := mpc.Solve(a.g)
	if err != nil {
		return 0, nil, err
	}
	return r.Cover, r.Paths, nil
}

func (a *API) SelfCheck() error {
	cases := []struct {
		n    int
		es   [][2]int
		want int
	}{
		{6, [][2]int{{0, 1}, {0, 2}, {2, 1}, {3, 4}, {4, 5}}, 2}, {4, nil, 4},
		{4, [][2]int{{0, 1}, {1, 2}, {2, 3}}, 1},
		{7, [][2]int{{0, 1}, {0, 2}, {2, 1}, {3, 4}, {4, 5}}, 3},
	}
	for _, c := range cases {
		if err := verifyCase(c.n, c.es, c.want); err != nil {
			return fmt.Errorf("%w: %v", ErrSelfCheck, err)
		}
	}
	return verifyRejections()
}
func verifyCase(n int, edges [][2]int, want int) error {
	h, _ := New(n)
	exists := map[[2]int]bool{}
	for _, e := range edges {
		if err := h.AddEdge(e[0], e[1]); err != nil {
			return err
		}
		exists[e] = true
	}
	cover, paths, err := h.Solve()
	if err != nil || cover != want || len(paths) != cover {
		return fmt.Errorf("n=%d cover=%d want=%d err=%v", n, cover, want, err)
	}
	seen := make([]int, n)
	total := 0
	for _, p := range paths {
		for i, v := range p {
			if v < 0 || v >= n || seen[v] > 0 || (i > 0 && !exists[[2]int{p[i-1], v}]) {
				return fmt.Errorf("illegal chain at node %d", v)
			}
			seen[v]++
			total++
		}
	}
	if total != n {
		return fmt.Errorf("cover has %d nodes, want %d", total, n)
	}
	if ref := n - bruteMatching(edges); cover != ref {
		return fmt.Errorf("cover %d != n-|M| %d", cover, ref)
	}
	return nil
}

// bruteMatching enumerates all edge subsets (independent reference, invariants 2-3).
func bruteMatching(edges [][2]int) int {
	best := 0
	for mask := 0; mask < 1<<len(edges); mask++ {
		lu, ru, ok, size := map[int]bool{}, map[int]bool{}, true, 0
		for i, e := range edges {
			if mask>>uint(i)&1 == 0 {
				continue
			}
			if lu[e[0]] || ru[e[1]] {
				ok = false
				break
			}
			lu[e[0]], ru[e[1]], size = true, true, size+1
		}
		if ok && size > best {
			best = size
		}
	}
	return best
}
func verifyRejections() error {
	h, _ := New(2)
	if err := h.AddEdge(0, 1); err != nil {
		return err
	}
	for _, f := range []func() error{
		func() error { _, e := New(0); return e },
		func() error { return h.AddEdge(0, 5) },
		func() error { return h.AddEdge(0, 0) },
		func() error { return h.AddEdge(0, 1) },
	} {
		if f() == nil {
			return errors.New("a rejected operation returned nil")
		}
	}
	if h.EdgeCount() != 1 {
		return errors.New("rejected operations changed state")
	}
	cyc, _ := New(3)
	for _, e := range [][2]int{{0, 1}, {1, 2}, {2, 0}} {
		_ = cyc.AddEdge(e[0], e[1])
	}
	if _, _, e := cyc.Solve(); !errors.Is(e, ErrCyclic) {
		return fmt.Errorf("cycle returned %v", e)
	}
	return nil
}

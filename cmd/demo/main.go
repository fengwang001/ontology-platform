// Command demo prints OK/FAIL judgments for the DAG minimum-path-cover library.
package main

import (
	"errors"
	"fmt"
	"reflect"
	"sort"
	"sync"

	"ontology/api"
	"ontology/dag"
	"ontology/mpc"
)

var ex6 = [][2]int{{0, 1}, {0, 2}, {2, 1}, {3, 4}, {4, 5}}

// greedyCover mimics the deliberately wrong greedy matcher: left nodes in
// ascending order take the smallest free right node, no reassignment.
func greedyCover(n int, edges [][2]int) int {
	adj := make([][]int, n)
	for _, e := range edges {
		adj[e[0]] = append(adj[e[0]], e[1])
	}
	used := make([]bool, n)
	m := 0
	for u := 0; u < n; u++ {
		sort.Ints(adj[u])
		for _, v := range adj[u] {
			if !used[v] {
				used[v], m = true, m+1
				break
			}
		}
	}
	return n - m
}

func report(name string, ok bool) {
	if ok {
		fmt.Println("OK   " + name)
	} else {
		fmt.Println("FAIL " + name)
	}
}

func buildDAG(n int, edges [][2]int) *dag.Graph {
	g, _ := dag.New(n)
	for _, e := range edges {
		_ = g.AddEdge(e[0], e[1])
	}
	return g
}

func main() {
	// dag: five distinct sentinel rejections (incl. cycle), state unchanged.
	_, e0 := dag.New(0)
	g, _ := dag.New(3)
	a, _ := api.New(1)
	ok := errors.Is(e0, dag.ErrInvalidN) && a.SelfCheck() == nil
	ok = ok && errors.Is(g.AddEdge(0, 3), dag.ErrOutOfRange)
	ok = ok && errors.Is(g.AddEdge(0, 0), dag.ErrSelfLoop)
	ok = ok && g.AddEdge(0, 1) == nil
	ok = ok && errors.Is(g.AddEdge(0, 1), dag.ErrDuplicate)
	ok = ok && g.EdgeCount() == 1
	_ = g.AddEdge(1, 2)
	ok = ok && g.Acyclic()
	_ = g.AddEdge(2, 0)
	ok = ok && !g.Acyclic()
	report("dag+api: 4 edge errors & cycle distinct, rejected state intact", ok)

	// mpc: section-3 graph cover and concrete paths.
	r, e := mpc.Solve(buildDAG(6, ex6))
	want := [][]int{{0, 2, 1}, {3, 4, 5}}
	report(fmt.Sprintf("cover=2 paths=%v for the 5 edges", r.Paths),
		e == nil && r.Cover == 2 && reflect.DeepEqual(r.Paths, want))

	// (甲) each matched edge treated as its own path.
	report(fmt.Sprintf("trap A per-edge fragments=%d (correct 2)", 6-r.Cover), 6-r.Cover == 4)
	// (乙) greedy maximal, no backtracking.
	report(fmt.Sprintf("trap B greedy cover=%d (correct 2)", greedyCover(6, ex6)),
		greedyCover(6, ex6) == 3)
	// (丙) isolated node 6 dropped by a start-only-from-matched rebuild.
	r3, _ := mpc.Solve(buildDAG(7, ex6))
	dropped := 0
	for _, p := range r3.Paths {
		if len(p) > 1 {
			dropped++
		}
	}
	report(fmt.Sprintf("trap C isolated dropped -> %d (correct %d)", dropped, r3.Cover),
		dropped == 2 && r3.Cover == 3)

	// Large m: the probe-count <=1 assertion is pinned by mpc.TestProbeCountConstant
	// (counter is unexported); here the public result on the same graph is checked.
	const m = 10000
	big, _ := api.New(2*m + 2)
	for i := 0; i < m; i++ {
		_ = big.AddEdge(i, m+i) // m pre-matched left nodes -> distinct right nodes
	}
	_ = big.AddEdge(2*m, 2*m+1) // one more augment that probes a free right once
	c, _, ec := big.Solve()
	report(fmt.Sprintf("large-m=%d cover=%d (probe<=1 pinned internally)", m, c),
		ec == nil && c == m+1)

	report("concurrent Solve itemwise identical (go test -race)", concurrentIdentical())
}

func concurrentIdentical() bool {
	h, _ := api.New(6)
	for _, e := range ex6 {
		_ = h.AddEdge(e[0], e[1])
	}
	const n = 16
	var wg sync.WaitGroup
	covers, paths := make([]int, n), make([][][]int, n)
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			covers[i], paths[i], _ = h.Solve()
		}(i)
	}
	close(start)
	wg.Wait()
	for i := 1; i < n; i++ {
		if covers[i] != covers[0] || !reflect.DeepEqual(paths[i], paths[0]) {
			return false
		}
	}
	return true
}

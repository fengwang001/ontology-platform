// Command demo exercises the dominator engine end to end and prints OK/FAIL lines.
package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/api"
	"ontology/dgraph"
	"ontology/dom"
)

var failed bool

func check(name string, ok bool) {
	status := "OK"
	if !ok {
		status = "FAIL"
		failed = true
	}
	fmt.Printf("%s %s\n", status, name)
}

// edges7 is the section-3 graph; registration order keeps adjacency ascending.
var edges7 = [][2]int{{0, 1}, {0, 6}, {1, 2}, {1, 6}, {2, 3}, {2, 4}, {3, 5}, {4, 5}}

func build(n int, edges [][2]int) *api.Engine {
	e, err := api.New(n)
	if err != nil {
		failed = true
		return e
	}
	for _, ed := range edges {
		if e.AddEdge(ed[0], ed[1]) != nil {
			failed = true
		}
	}
	e.Compute()
	return e
}

// dfsParent returns the DFS tree parents from 0 (adjacency ascending).
func dfsParent(n int, edges [][2]int) []int {
	adj := make([][]int, n)
	for _, e := range edges {
		adj[e[0]] = append(adj[e[0]], e[1])
	}
	parent := make([]int, n)
	vis := make([]bool, n)
	var dfs func(u int)
	dfs = func(u int) {
		vis[u] = true
		for _, v := range adj[u] {
			if !vis[v] {
				parent[v] = u
				dfs(v)
			}
		}
	}
	dfs(0)
	return parent
}

func main() {
	// Four distinct decidable errors; rejected ops leave state unchanged.
	g, _ := dgraph.New(3)
	_ = g.AddEdge(0, 1)
	errs := []error{
		func() error { _, e := dgraph.New(0); return e }(),
		g.AddEdge(0, 3), g.AddEdge(1, 1), g.AddEdge(0, 1),
	}
	distinct := map[string]bool{}
	for _, e := range errs {
		distinct[e.Error()] = true
	}
	check("errors: 4 distinct sentinels", len(distinct) == 4 &&
		errors.Is(errs[0], dgraph.ErrNonPositiveN) && errors.Is(errs[1], dgraph.ErrNodeOutOfRange) &&
		errors.Is(errs[2], dgraph.ErrSelfLoop) && errors.Is(errs[3], dgraph.ErrDuplicateEdge))
	check("state unchanged after rejects", g.EdgeCount() == 1)

	// Section-3 graph: per-node idom table.
	e := build(7, edges7)
	ok := true
	for v, w := range []int{0, 0, 1, 2, 2, 2, 0} {
		got, err := e.IDom(v)
		ok = ok && err == nil && got == w
	}
	check("idom table [0 0 1 2 2 2 0]", ok)

	// Traps: DFS parent of 6 is 1 (correct 0); first pred of 5 is 3 (correct 2).
	parent := dfsParent(7, edges7)
	id6, _ := e.IDom(6)
	check("DFS-parent trap: wrong=1 correct=0", parent[6] == 1 && id6 == 0)
	firstPred := map[int]int{}
	for _, ed := range edges7 {
		if _, seen := firstPred[ed[1]]; !seen {
			firstPred[ed[1]] = ed[0]
		}
	}
	id5, _ := e.IDom(5)
	check("first-pred trap: wrong=3 correct=2", firstPred[5] == 3 && id5 == 2)

	// Unreachable node 7: sentinel -1 and false dominance.
	e8 := build(8, edges7)
	id7, err7 := e8.IDom(7)
	check("unreachable: IDom(7)=-1 Dominates(0,7)=false", err7 == nil && id7 == -1 && !e8.Dominates(0, 7))

	// Deep chains: per-query inspected-node count does not grow with m.
	check("deep-chain query cost <= 2", dom.QueryCostBounded(100, 1000, 10000))

	// Concurrency: 8 goroutines must get pairwise identical results.
	pairs := [][2]int{{0, 6}, {1, 6}, {2, 5}, {3, 5}, {0, 0}, {6, 6}, {2, 3}}
	results := make([][]bool, 8)
	var wg sync.WaitGroup
	for k := range results {
		wg.Add(1)
		go func(k int) {
			defer wg.Done()
			for _, p := range pairs {
				results[k] = append(results[k], e.Dominates(p[0], p[1]))
			}
		}(k)
	}
	wg.Wait()
	same := true
	for k := 1; k < len(results); k++ {
		for j := range pairs {
			same = same && results[k][j] == results[0][j]
		}
	}
	check("concurrent queries pairwise identical", same)
	check("SelfCheck", api.SelfCheck() == nil)

	if failed {
		os.Exit(1)
	}
}

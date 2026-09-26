// Command demo runs in-process checks for the DAG min-path-cover packages.
package main

import (
	"errors"
	"fmt"
	"reflect"
	"sync"

	"ontology/api"
	"ontology/dag"
	"ontology/mpc"
)

var notesEdges = [][2]int{{0, 1}, {0, 2}, {2, 1}, {3, 4}, {4, 5}}

func report(ok bool, name, detail string) {
	tag := "OK"
	if !ok {
		tag = "FAIL"
	}
	fmt.Printf("%s %s %s\n", tag, name, detail)
}

// greedyCoverSize mimics the non-backtracking greedy matching: left nodes in id
// order, each takes the smallest free right node. Returns the (wrong) cover.
func greedyCoverSize(n int, edges [][2]int) int {
	right := make([]int, n)
	for i := range right {
		right[i] = -1
	}
	matched := 0
	for u := 0; u < n; u++ {
		best := -1
		for _, e := range edges {
			if e[0] == u && right[e[1]] == -1 && (best == -1 || e[1] < best) {
				best = e[1]
			}
		}
		if best != -1 {
			right[best], matched = u, matched+1
		}
	}
	return n - matched
}

// links counts matched edges as they appear in the correct paths: it is the
// value a buggy "one path per matched edge, no chaining" reconstruction gives.
func links(paths [][]int) int {
	c := 0
	for _, p := range paths {
		c += len(p) - 1
	}
	return c
}

// multiNodePaths counts paths of length > 0: it is the value a buggy
// reconstruction gives when it starts only at nodes that have a successor and
// therefore drops isolated vertices (length-0 paths).
func multiNodePaths(paths [][]int) int {
	c := 0
	for _, p := range paths {
		if len(p) > 1 {
			c++
		}
	}
	return c
}

func main() {
	// 1. Section-3 five-edge graph: cover 2, paths [0 2 1] and [3 4 5].
	a, _ := api.New(6)
	for _, e := range notesEdges {
		_ = a.AddEdge(e[0], e[1])
	}
	cover, paths, err := a.Solve()
	want := [][]int{{0, 2, 1}, {3, 4, 5}}
	report(err == nil && cover == 2 && reflect.DeepEqual(paths, want),
		"section3", fmt.Sprintf("cover=%d paths=%v", cover, paths))

	// 2. Trap (Jia) per-edge paths, (Yi) greedy matching, (Bing) isolated drop.
	g7, _ := api.New(7)
	for _, e := range notesEdges {
		_ = g7.AddEdge(e[0], e[1])
	}
	c7, p7, _ := g7.Solve()
	report(links(paths) == 4 && greedyCoverSize(6, notesEdges) == 3 &&
		multiNodePaths(p7) == 2 && cover == 2 && c7 == 3,
		"traps", "perEdge=4(ok2) greedy=3(ok2) isolatedDrop=2(ok3)")

	// 3. Five pairwise-distinct decidable sentinel errors.
	bad, _ := api.New(3)
	_, eN := api.New(0)
	eRange := bad.AddEdge(3, 0)
	eSelf := bad.AddEdge(1, 1)
	_ = bad.AddEdge(0, 1)
	eDup := bad.AddEdge(0, 1)
	cyc, _ := api.New(3)
	for _, e := range [][2]int{{0, 1}, {1, 2}, {2, 0}} {
		_ = cyc.AddEdge(e[0], e[1])
	}
	_, _, eCycle := cyc.Solve()
	seen := map[error]bool{}
	for _, e := range []error{eN, eRange, eSelf, eDup, eCycle} {
		seen[e] = true
	}
	report(len(seen) == 5 && errors.Is(eCycle, dag.ErrCycle), "errors",
		fmt.Sprintf("%v|%v|%v|%v|%v", eN, eRange, eSelf, eDup, eCycle))

	// 4. Rejected operations leave no trace and the graph stays usable.
	g, _ := api.New(6)
	for _, e := range notesEdges {
		_ = g.AddEdge(e[0], e[1])
	}
	before := g.EdgeCount()
	for _, ev := range [][2]int{{6, 0}, {-1, 0}, {0, 0}, {0, 1}} {
		_ = g.AddEdge(ev[0], ev[1])
	}
	cAfter, pAfter, _ := g.Solve()
	report(g.EdgeCount() == before && cAfter == 2 && reflect.DeepEqual(pAfter, want),
		"noTrace", fmt.Sprintf("edges=%d cover=%d", g.EdgeCount(), cAfter))

	// 5. Occupancy-check count stays constant as m grows (100..10000).
	report(mpc.ProbeBoundHolds(), "probeBound", "probes<=1 for m=100,1000,10000")

	// 6. Concurrent Solve: every goroutine gets itemwise identical results.
	const N = 16
	var wg sync.WaitGroup
	covers := make([]int, N)
	allPaths := make([][][]int, N)
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			covers[i], allPaths[i], _ = a.Solve()
		}(i)
	}
	wg.Wait()
	same := true
	for i := 1; i < N; i++ {
		if covers[i] != covers[0] || !reflect.DeepEqual(allPaths[i], allPaths[0]) {
			same = false
		}
	}
	report(same, "concurrent", fmt.Sprintf("%d goroutines cover=%d identical", N, covers[0]))

	// 7. Self-check across the built-in graph set (all four invariants).
	chk, _ := api.New(1)
	report(chk.SelfCheck() == nil, "selfCheck", "invariants on built-in graphs")
}

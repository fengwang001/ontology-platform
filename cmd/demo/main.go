package main

import (
	"errors"
	"fmt"
	"math"
	"sync"

	"ontology/dij"
	"ontology/graph"
)

func main() {
	passed := 0
	check := func(name string, ok bool) {
		if ok {
			passed++
			fmt.Printf("OK %s\n", name)
			return
		}
		fmt.Printf("FAIL %s\n", name)
	}

	g := graph.New(2)
	g.AddEdge(0, 1, 3)
	edges := []graph.Edge{{0, 1, 4}, {0, 2, 1}, {2, 1, 1}, {1, 3, 1}}
	want := []float64{0, 2, 1, 3}
	dist, err := dij.ShortestPath(4, edges, 0)
	_, neg := dij.ShortestPath(2, []graph.Edge{{0, 1, -1}}, 0)
	_, badSrc := dij.ShortestPath(1, nil, 1)
	_, badEdge := dij.ShortestPath(2, []graph.Edge{{0, 2, 1}}, 0)
	unreachable, _ := dij.ShortestPath(2, nil, 0)

	concurrentOK := true
	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, e := dij.ShortestPath(4, edges, 0)
			if e != nil || len(got) != 4 {
				concurrentOK = false
			}
		}()
	}
	wg.Wait()

	check("graph-edge", g.Neighbors(0)[0].To == 1 && g.Neighbors(0)[0].Weight == 3)
	check("shortest-dist", err == nil && equal(dist, want))
	check("unreachable-inf", math.IsInf(unreachable[1], 1))
	check("negative-edge", errors.Is(neg, dij.ErrNegativeEdge))
	check("bad-source", errors.Is(badSrc, dij.ErrBadSrc))
	check("bad-edge", errors.Is(badEdge, dij.ErrBadEdge))
	check("stale-records", dist[1] == 2 && dist[3] == 3)
	check("concurrent-calls", concurrentOK)
	if passed == 8 {
		fmt.Printf("OK total: %d/8\n", passed)
	} else {
		fmt.Printf("FAIL total: %d/8\n", passed)
	}
}

func equal(a, b []float64) bool {
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

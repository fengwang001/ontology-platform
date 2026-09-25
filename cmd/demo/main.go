package main

import (
	"errors"
	"fmt"
	"math"
	"os"
	"slices"

	"ontology/check"
	"ontology/dij"
	"ontology/graph"
)

var passed, failed int

func report(name string, ok bool) {
	if ok {
		passed++
		fmt.Println("OK", name)
	} else {
		failed++
		fmt.Println("FAIL", name)
	}
}

func main() {
	g := graph.New(4)
	g.AddEdge(0, 1, 1)
	g.AddEdge(1, 2, 2)
	g.AddEdge(2, 3, 3)
	report("graph.AddEdge builds 3 edges", len(g.Edges()) == 3)

	dist, err := dij.ShortestPath(4, g.Edges(), 0)
	report("dij shortest dist", err == nil && dist[3] == 6)
	report("matches check.BellmanFord", slices.Equal(dist, check.BellmanFord(4, g.Edges(), 0)))

	far, _ := dij.ShortestPath(3, []graph.Edge{{From: 0, To: 1, W: 5}}, 0)
	report("unreachable is +Inf", math.IsInf(far[2], 1))

	_, err = dij.ShortestPath(2, []graph.Edge{{From: 0, To: 1, W: -1}}, 0)
	report("negative edge rejected", errors.Is(err, dij.ErrNegativeEdge))
	_, err = dij.ShortestPath(2, nil, 5)
	report("bad src rejected", errors.Is(err, dij.ErrBadSrc))
	_, err = dij.ShortestPath(2, []graph.Edge{{From: 0, To: 9, W: 1}}, 0)
	report("bad edge rejected", errors.Is(err, dij.ErrBadEdge))

	stale, _ := dij.ShortestPath(3, []graph.Edge{{From: 0, To: 1, W: 10}, {From: 0, To: 2, W: 1}, {From: 2, To: 1, W: 1}}, 0)
	report("stale records skipped", stale[1] == 2)

	fmt.Printf("OK total %d passed, %d failed\n", passed, failed)
	if failed > 0 {
		os.Exit(1)
	}
}

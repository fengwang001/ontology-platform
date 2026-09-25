package main

import (
	"errors"
	"fmt"
	"math"

	"ontology/dij"
	"ontology/graph"
)

var failures int

func report(name string, ok bool) {
	status := "OK"
	if !ok {
		status = "FAIL"
		failures++
	}
	fmt.Printf("%s %s\n", status, name)
}

func main() {
	edges := []graph.Edge{
		{From: 0, To: 1, Weight: 4},
		{From: 0, To: 2, Weight: 1},
		{From: 2, To: 1, Weight: 2},
		{From: 1, To: 3, Weight: 1},
	}
	dist, err := dij.ShortestPath(4, edges, 0)
	report("shortest distances", err == nil && dist[3] == 4)

	dist, err = dij.ShortestPath(3, nil, 0)
	report("unreachable is +Inf", err == nil && dist[0] == 0 && dist[2] == math.Inf(1))

	_, err = dij.ShortestPath(1, nil, 1)
	report("bad source rejected", errors.Is(err, dij.ErrBadSrc))

	_, err = dij.ShortestPath(2, []graph.Edge{{From: 0, To: 2, Weight: 1}}, 0)
	report("bad edge rejected", errors.Is(err, dij.ErrBadEdge))

	_, err = dij.ShortestPath(2, []graph.Edge{{From: 0, To: 1, Weight: -1}}, 0)
	report("negative edge rejected", errors.Is(err, dij.ErrNegativeEdge))

	report("single source distance zero", func() bool { d, e := dij.ShortestPath(1, nil, 0); return e == nil && d[0] == 0 }())
	report("total checks: 6/6", failures == 0)
}

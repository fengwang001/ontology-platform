package main

import (
	"errors"
	"fmt"
	"math"
	"os"

	"ontology/dij"
	"ontology/graph"
)

func main() {
	fails := 0
	check := func(name string, ok bool) {
		if !ok {
			fails++
			fmt.Println("FAIL", name)
			return
		}
		fmt.Println("OK", name)
	}
	edges := []graph.Edge{{From: 0, To: 1, W: 1}, {From: 1, To: 2, W: 2}, {From: 0, To: 2, W: 5}, {From: 2, To: 3, W: 1}}
	dist, err := dij.ShortestPath(4, edges, 0)
	check("shortest", err == nil && dist[2] == 3 && dist[3] == 4)
	one, _ := dij.ShortestPath(1, nil, 0)
	check("single-node", one[0] == 0)
	inf, _ := dij.ShortestPath(3, nil, 0)
	check("unreachable-inf", math.IsInf(inf[1], 1))
	_, err = dij.ShortestPath(2, []graph.Edge{{From: 0, To: 1, W: -1}}, 0)
	check("negative-edge", errors.Is(err, dij.ErrNegativeEdge))
	_, err = dij.ShortestPath(2, nil, 5)
	check("bad-src", errors.Is(err, dij.ErrBadSrc))
	_, err = dij.ShortestPath(2, []graph.Edge{{From: 0, To: 3, W: 1}}, 0)
	check("bad-edge", errors.Is(err, dij.ErrBadEdge))
	again, _ := dij.ShortestPath(4, edges, 0)
	check("deterministic", fmt.Sprint(dist) == fmt.Sprint(again))
	if fails > 0 {
		fmt.Printf("FAIL total %d failed\n", fails)
		os.Exit(1)
	}
	fmt.Println("OK total all passed")
}

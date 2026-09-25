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

func main() {
	ok, total := 0, 0
	assert := func(name string, cond bool) {
		total++
		if cond {
			ok++
			fmt.Println("OK", name)
		} else {
			fmt.Println("FAIL", name)
		}
	}
	edges := []graph.Edge{{U: 0, V: 1, W: 1}, {U: 0, V: 2, W: 4}, {U: 1, V: 2, W: 2}, {U: 2, V: 3, W: 1}}
	dist, err := dij.ShortestPath(5, edges, 0)
	assert("shortest dist", err == nil && slices.Equal(dist, []float64{0, 1, 3, 4, math.Inf(1)}))
	assert("matches reference", slices.Equal(dist, check.Reference(5, edges, 0)))
	_, err = dij.ShortestPath(2, []graph.Edge{{U: 0, V: 1, W: -1}}, 0)
	assert("negative edge", errors.Is(err, dij.ErrNegativeEdge))
	_, err = dij.ShortestPath(2, nil, 5)
	assert("bad src", errors.Is(err, dij.ErrBadSrc))
	_, err = dij.ShortestPath(2, []graph.Edge{{U: 0, V: 9, W: 1}}, 0)
	assert("bad edge", errors.Is(err, dij.ErrBadEdge))
	single, _ := dij.ShortestPath(1, nil, 0)
	assert("single node", slices.Equal(single, []float64{0}))
	isolated, _ := dij.ShortestPath(3, nil, 2)
	assert("unreachable +Inf", slices.Equal(isolated, []float64{math.Inf(1), math.Inf(1), 0}))
	if ok != total {
		fmt.Printf("FAIL total %d/%d\n", ok, total)
		os.Exit(1)
	}
	fmt.Printf("OK total %d/%d\n", ok, total)
}

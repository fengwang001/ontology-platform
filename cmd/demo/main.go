// Command demo exercises the graph, topo and check packages end to end.
package main

import (
	"errors"
	"fmt"
	"os"

	"ontology/check"
	"ontology/graph"
	"ontology/topo"
)

func main() {
	pass, total := 0, 0
	judge := func(name string, ok bool) {
		total++
		mark := "OK"
		if !ok {
			mark = "FAIL"
		} else {
			pass++
		}
		fmt.Printf("%s %s\n", mark, name)
	}
	g := graph.New(4)
	for _, e := range [][2]int{{0, 1}, {0, 2}, {1, 3}, {2, 3}} {
		g.AddEdge(e[0], e[1])
	}
	judge("graph adjacency", len(g.Adj(0)) == 2 && len(g.Adj(3)) == 0)
	order, err := topo.TopoSort(4, [][2]int{{0, 1}, {0, 2}, {1, 3}, {2, 3}})
	judge("topo sorts diamond DAG", err == nil && fmt.Sprint(order) == "[0 2 1 3]")
	_, err = topo.TopoSort(3, [][2]int{{0, 1}, {1, 2}, {2, 0}})
	judge("topo rejects cycle", errors.Is(err, topo.ErrCycle))
	_, err = topo.TopoSort(2, [][2]int{{0, 7}})
	judge("topo rejects bad edge", errors.Is(err, topo.ErrBadEdge))
	_, acyclic := check.Kahn(4, [][2]int{{0, 1}, {0, 2}, {1, 3}, {2, 3}})
	judge("kahn reference agrees", acyclic)
	empty, err := topo.TopoSort(0, nil)
	judge("empty graph empty order", err == nil && len(empty) == 0)
	mark := "OK"
	if pass != total {
		mark = "FAIL"
	}
	fmt.Printf("%s %d/%d checks passed\n", mark, pass, total)
	if pass != total {
		os.Exit(1)
	}
}

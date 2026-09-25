// demo 演示 topo 包的拓扑排序与环检测语义。
package main

import (
	"errors"
	"fmt"
	"os"
	"slices"

	"ontology/check"
	"ontology/topo"
)

func main() {
	pass, total := 0, 0
	judge := func(name string, ok bool) {
		total++
		word := "FAIL"
		if ok {
			pass++
			word = "OK"
		}
		fmt.Println(word, name)
	}
	diamond := []topo.Edge{{U: 0, V: 1}, {U: 0, V: 2}, {U: 1, V: 3}, {U: 2, V: 3}}
	order, err := topo.TopoSort(4, []topo.Edge{{U: 0, V: 1}, {U: 1, V: 2}, {U: 2, V: 3}})
	judge("chain topo order", err == nil && slices.Equal(order, []int{0, 1, 2, 3}))
	order, err = topo.TopoSort(0, nil)
	judge("empty graph", err == nil && len(order) == 0)
	_, err = topo.TopoSort(4, diamond)
	judge("diamond no false cycle", err == nil)
	_, err = topo.TopoSort(3, []topo.Edge{{U: 0, V: 1}, {U: 1, V: 2}, {U: 2, V: 0}})
	judge("cycle rejected", errors.Is(err, topo.ErrCycle) && !errors.Is(err, topo.ErrBadEdge))
	_, err = topo.TopoSort(2, []topo.Edge{{U: 0, V: 2}})
	judge("bad edge rejected", errors.Is(err, topo.ErrBadEdge) && !errors.Is(err, topo.ErrCycle))
	first, _ := topo.TopoSort(4, diamond)
	again, _ := topo.TopoSort(4, diamond)
	judge("deterministic", slices.Equal(first, again))
	_, kerr := check.Kahn(4, diamond)
	judge("kahn ref agrees", kerr == nil)
	word := "FAIL"
	if pass == total {
		word = "OK"
	}
	fmt.Printf("%s total %d/%d\n", word, pass, total)
	if pass != total {
		os.Exit(1)
	}
}

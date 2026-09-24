package main

import (
	"fmt"

	"ontology/graph"
)

func main() {
	b := graph.NewBuilder()
	b.AddEdge("a", "c")
	b.AddEdge("a", "b")
	b.AddEdge("a", "b")
	nb := b.Build().Neighbors("a")
	if len(nb) == 2 && nb[0] == "b" && nb[1] == "c" {
		fmt.Println("OK: graph edges sorted and deduped")
	} else {
		fmt.Println("FAIL: graph edges sorted and deduped")
	}
}

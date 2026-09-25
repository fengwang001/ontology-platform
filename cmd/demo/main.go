package main

import (
	"fmt"
	"os"

	"ontology/graph"
	"ontology/traverse"
)

type check struct {
	name string
	ok   bool
}

var checks []check

func report(name string, ok bool) {
	checks = append(checks, check{name, ok})
}

func main() {
	report("graph: diamond is NOT a cycle", diamondAcyclic())
	report("graph: true cycle A->B->C->A detected", realCycle())
	report("graph: edge validation (missing/self/dup)", edgeValidation())
	report("graph: DFS edge examinations <= V+E", examinationBound())
	report("traverse: BFS out/in/both orders + cycle dedup", walkOrders())

	failed := 0
	for _, c := range checks {
		tag := "OK"
		if !c.ok {
			tag = "FAIL"
			failed++
		}
		fmt.Printf("%s  %s\n", tag, c.name)
	}
	if failed > 0 {
		fmt.Printf("%d check(s) failed\n", failed)
		os.Exit(1)
	}
	fmt.Println("all checks passed")
}

func diamondAcyclic() bool {
	g := graph.New()
	for _, n := range []string{"A", "B", "C", "D"} {
		_ = g.AddNode(n)
	}
	_ = g.AddEdge("A", "B")
	_ = g.AddEdge("A", "C")
	_ = g.AddEdge("B", "D")
	_ = g.AddEdge("C", "D")
	return !g.HasCycle()
}

func realCycle() bool {
	g := graph.New()
	for _, n := range []string{"A", "B", "C"} {
		_ = g.AddNode(n)
	}
	_ = g.AddEdge("A", "B")
	_ = g.AddEdge("B", "C")
	_ = g.AddEdge("C", "A")
	return g.HasCycle()
}

func edgeValidation() bool {
	g := graph.New()
	_ = g.AddNode("A")
	if err := g.AddNode("A"); err != graph.ErrNodeExists {
		return false
	}
	if err := g.AddEdge("A", "B"); err != graph.ErrNodeNotFound {
		return false
	}
	_ = g.AddNode("B")
	if err := g.AddEdge("A", "A"); err != graph.ErrSelfLoop {
		return false
	}
	_ = g.AddEdge("A", "B")
	return g.AddEdge("A", "B") == graph.ErrDuplicateEdge
}

func examinationBound() bool {
	const v = 10000
	g := graph.New()
	for i := 0; i < v; i++ {
		_ = g.AddNode(fmt.Sprintf("n%d", i))
	}
	for i := 0; i+1 < v; i++ {
		_ = g.AddEdge(fmt.Sprintf("n%d", i), fmt.Sprintf("n%d", i+1))
	}
	cyclic := g.HasCycle()
	return !cyclic && g.EdgeExaminations() <= v+g.EdgeCount()
}

func walkOrders() bool {
	g := graph.New()
	for _, n := range []string{"A", "B", "C", "D"} {
		_ = g.AddNode(n)
	}
	_ = g.AddEdge("A", "B")
	_ = g.AddEdge("B", "C")
	_ = g.AddEdge("C", "A")
	_ = g.AddEdge("A", "D")

	out, err := traverse.Walk(g, "A", traverse.DirOut)
	if err != nil || !equal(out, []string{"A", "B", "D", "C"}) {
		return false
	}
	in, err := traverse.Walk(g, "D", traverse.DirIn)
	if err != nil || !equal(in, []string{"D", "A", "C", "B"}) {
		return false
	}
	both, err := traverse.Walk(g, "D", traverse.DirBoth)
	if err != nil || !equal(both, []string{"D", "A", "B", "C"}) {
		return false
	}
	// Cycle must not produce duplicates.
	if !unique(out) || !unique(in) || !unique(both) {
		return false
	}
	if _, err := traverse.Walk(g, "Z", traverse.DirOut); err != traverse.ErrStartNotFound {
		return false
	}
	_, err = traverse.Walk(g, "A", traverse.Dir(0))
	return err == traverse.ErrInvalidDir
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func unique(nodes []string) bool {
	seen := map[string]bool{}
	for _, n := range nodes {
		if seen[n] {
			return false
		}
		seen[n] = true
	}
	return true
}

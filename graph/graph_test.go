package graph

import (
	"errors"
	"fmt"
	"testing"
)

func TestGraphCases(t *testing.T) {
	chain := New()
	for _, id := range []string{"A", "B", "C"} {
		chain.AddTask(id)
	}
	for _, e := range [][2]string{{"A", "B"}, {"B", "C"}} {
		if err := chain.AddEdge(e[0], e[1]); err != nil {
			t.Fatalf("AddEdge %v: %v", e, err)
		}
	}
	self := New()
	self.AddTask("X")
	_ = self.AddEdge("X", "X")
	loop3 := New()
	for _, id := range []string{"a", "b", "c"} {
		loop3.AddTask(id)
	}
	for _, e := range [][2]string{{"a", "b"}, {"b", "c"}, {"c", "a"}} {
		_ = loop3.AddEdge(e[0], e[1])
	}
	long := New()
	for i := 0; i < 1000; i++ {
		long.AddTask(nodeName(i))
	}
	for i := 0; i < 999; i++ {
		_ = long.AddEdge(nodeName(i), nodeName(i+1))
	}

	cases := []struct {
		name    string
		g       *Graph
		wantCyc bool
		layers  int
	}{
		{"empty", New(), false, 0},
		{"single", singleGraph(), false, 1},
		{"no-deps", noDepGraph(5), false, 1},
		{"chain3", chain, false, 3},
		{"self-loop", self, true, 0},
		{"loop3", loop3, true, 0},
		{"chain1000", long, false, 1000},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cyc := tc.g.Cycle()
			if (cyc != nil) != tc.wantCyc {
				t.Fatalf("Cycle=%v, want cycle=%v", cyc, tc.wantCyc)
			}
			layers, ok := tc.g.Layers()
			if ok != !tc.wantCyc {
				t.Fatalf("Layers ok=%v want %v", ok, !tc.wantCyc)
			}
			if len(layers) != tc.layers {
				t.Fatalf("layers=%d want %d", len(layers), tc.layers)
			}
			if cyc != nil {
				assertCycleEdges(t, tc.g, cyc)
			}
		})
	}
}

func TestAddEdgeSemantics(t *testing.T) {
	g := New()
	g.AddTask("A")
	g.AddTask("B")
	before := g.E()
	if err := g.AddEdge("A", "ghost"); !errors.Is(err, ErrUnknownTask) {
		t.Fatalf("unknown dep: err=%v", err)
	}
	if err := g.AddEdge("A", "B"); err != nil {
		t.Fatalf("first edge: %v", err)
	}
	if err := g.AddEdge("A", "B"); err != nil {
		t.Fatalf("dup edge should be allowed: %v", err)
	}
	if g.E() != before+1 {
		t.Fatalf("dup edge not idempotent: E=%d", g.E())
	}
}

func assertCycleEdges(t *testing.T, g *Graph, cyc []string) {
	t.Helper()
	if len(cyc) < 2 || cyc[0] != cyc[len(cyc)-1] {
		t.Fatalf("cycle not closed: %v", cyc)
	}
	for i := 0; i+1 < len(cyc); i++ {
		if !g.succ[cyc[i]][cyc[i+1]] {
			t.Fatalf("cycle edge %s->%s not in graph, cycle=%v", cyc[i], cyc[i+1], cyc)
		}
	}
}

func singleGraph() *Graph {
	g := New()
	g.AddTask("only")
	return g
}

func noDepGraph(n int) *Graph {
	g := New()
	for i := 0; i < n; i++ {
		g.AddTask(nodeName(i))
	}
	return g
}

func nodeName(i int) string {
	return fmt.Sprintf("n%04d", i)
}

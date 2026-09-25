package graph

import (
	"fmt"
	"testing"
)

func nodes(g *Graph, names ...string) {
	for _, n := range names {
		if err := g.AddNode(n); err != nil {
			panic(err)
		}
	}
}

func TestAddValidation(t *testing.T) {
	tests := []struct {
		name string
		edge bool
		arg  [2]string
		set  func(*Graph)
		want error
	}{
		{"duplicate node", false, [2]string{"A", ""}, func(g *Graph) { nodes(g, "A") }, ErrNodeExists},
		{"missing from", true, [2]string{"X", "A"}, func(g *Graph) { nodes(g, "A") }, ErrNodeNotFound},
		{"missing to", true, [2]string{"A", "X"}, func(g *Graph) { nodes(g, "A") }, ErrNodeNotFound},
		{"self loop", true, [2]string{"A", "A"}, func(g *Graph) { nodes(g, "A") }, ErrSelfLoop},
		{"duplicate edge", true, [2]string{"A", "B"}, func(g *Graph) {
			nodes(g, "A", "B")
			_ = g.AddEdge("A", "B")
		}, ErrDuplicateEdge},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := New()
			tc.set(g)
			var err error
			if tc.edge {
				err = g.AddEdge(tc.arg[0], tc.arg[1])
			} else {
				err = g.AddNode(tc.arg[0])
			}
			if err != tc.want {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
		})
	}
}

func TestHasCycle(t *testing.T) {
	tests := []struct {
		name  string
		build func(*Graph)
		want  bool
	}{
		{"empty", func(g *Graph) {}, false},
		{"single node", func(g *Graph) { nodes(g, "A") }, false},
		{"diamond is acyclic", func(g *Graph) {
			nodes(g, "A", "B", "C", "D")
			_ = g.AddEdge("A", "B")
			_ = g.AddEdge("A", "C")
			_ = g.AddEdge("B", "D")
			_ = g.AddEdge("C", "D")
		}, false},
		{"linear chain", func(g *Graph) {
			nodes(g, "A", "B", "C")
			_ = g.AddEdge("A", "B")
			_ = g.AddEdge("B", "C")
		}, false},
		{"three node cycle", func(g *Graph) {
			nodes(g, "A", "B", "C")
			_ = g.AddEdge("A", "B")
			_ = g.AddEdge("B", "C")
			_ = g.AddEdge("C", "A")
		}, true},
		{"cycle plus tail", func(g *Graph) {
			nodes(g, "A", "B", "C", "D")
			_ = g.AddEdge("A", "B")
			_ = g.AddEdge("B", "C")
			_ = g.AddEdge("C", "A")
			_ = g.AddEdge("A", "D")
		}, true},
		{"disconnected acyclic", func(g *Graph) {
			nodes(g, "A", "B", "C", "D")
			_ = g.AddEdge("A", "B")
			_ = g.AddEdge("C", "D")
		}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := New()
			tc.build(g)
			if got := g.HasCycle(); got != tc.want {
				t.Fatalf("HasCycle = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestEdgeExaminationBound(t *testing.T) {
	sizes := []int{100, 10000}
	for _, v := range sizes {
		t.Run(fmt.Sprintf("V=%d", v), func(t *testing.T) {
			g := New()
			for i := 0; i < v; i++ {
				_ = g.AddNode(fmt.Sprintf("n%d", i))
			}
			// Acyclic chain plus a diamond every few nodes: E < 2V.
			for i := 0; i+1 < v; i++ {
				_ = g.AddEdge(fmt.Sprintf("n%d", i), fmt.Sprintf("n%d", i+1))
			}
			e := g.EdgeCount()
			if g.HasCycle() {
				t.Fatal("chain unexpectedly reported as cyclic")
			}
			examined := g.EdgeExaminations()
			if examined > v+e {
				t.Fatalf("examined %d > V+E %d", examined, v+e)
			}
			if examined != e {
				t.Fatalf("examined %d != E %d", examined, e)
			}
		})
	}
}

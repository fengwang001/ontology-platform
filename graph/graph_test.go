package graph

import (
	"errors"
	"fmt"
	"reflect"
	"testing"
)

func TestBuildAndEdges(t *testing.T) {
	cases := []struct {
		name  string
		build func() *Graph
		nodes []string
		errIs error
	}{
	{"empty", func() *Graph { return New() }, nil, nil},
		{"single", func() *Graph { g := New(); g.AddNode("A"); return g }, []string{"A"}, nil},
		{"missing_from", func() *Graph {
			g := New()
			g.AddNode("B")
			_ = g.AddEdge("X", "B")
			return g
		}, []string{"B"}, ErrMissingNode},
		{"missing_to", func() *Graph {
			g := New()
			g.AddNode("A")
			_ = g.AddEdge("A", "X")
			return g
		}, []string{"A"}, ErrMissingNode},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := tc.build()
			if got := g.Nodes(); !reflect.DeepEqual(got, tc.nodes) {
				t.Fatalf("nodes = %v, want %v", got, tc.nodes)
			}
			err := g.Validate()
			if tc.errIs != nil && !errors.Is(err, tc.errIs) {
				t.Fatalf("validate err = %v, want %v", err, tc.errIs)
			}
		})
	}
}

func TestDuplicateEdgeIdempotent(t *testing.T) {
	g := New()
	g.AddNode("A")
	g.AddNode("B")
	for i := 0; i < 5; i++ {
		if err := g.AddEdge("A", "B"); err != nil {
			t.Fatal(err)
		}
	}
	if got := g.Pred("B"); len(got) != 1 || got[0] != "A" {
		t.Fatalf("pred = %v, want [A]", got)
	}
	if got := g.Succ("A"); len(got) != 1 || got[0] != "B" {
		t.Fatalf("succ = %v, want [B]", got)
	}
}

func verifyCyclePath(g *Graph, p []string) bool {
	for i := 0; i+1 < len(p); i++ {
		if _, ok := g.succ[p[i]][p[i+1]]; !ok {
			return false
		}
	}
	return len(p) >= 2 && p[0] == p[len(p)-1]
}

func TestCycleDetection(t *testing.T) {
	cases := []struct {
		name  string
		build func() *Graph
	}{
		{"self_loop", func() *Graph {
			g := New()
			g.AddNode("A")
			_ = g.AddEdge("A", "A")
			return g
		}},
		{"triangle", func() *Graph {
			g := New()
			for _, n := range []string{"A", "B", "C"} {
				g.AddNode(n)
			}
			_ = g.AddEdge("A", "B")
			_ = g.AddEdge("B", "C")
			_ = g.AddEdge("C", "A")
			return g
		}},
		{"disjoint_cycle", func() *Graph {
			g := New()
			for _, n := range []string{"A", "B", "X", "Y"} {
				g.AddNode(n)
			}
			_ = g.AddEdge("A", "B")
			_ = g.AddEdge("X", "Y")
			_ = g.AddEdge("Y", "X")
			return g
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := tc.build()
			err := g.Validate()
			if !errors.Is(err, ErrCycle) {
				t.Fatalf("want ErrCycle, got %v", err)
			}
			var ce *CycleError
			if !errors.As(err, &ce) || !verifyCyclePath(g, ce.Path) {
				t.Fatalf("cycle path invalid: %v", err)
			}
		})
	}
}

func TestLayers(t *testing.T) {
	g := New()
	for _, n := range []string{"A", "B", "C", "D"} {
		g.AddNode(n)
	}
	_ = g.AddEdge("A", "B")
	_ = g.AddEdge("A", "C")
	_ = g.AddEdge("B", "D")
	_ = g.AddEdge("C", "D")
	want := [][]string{{"A"}, {"B", "C"}, {"D"}}
	got, err := g.Layers()
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("layers = %v, err = %v, want %v", got, err, want)
	}
}

func TestChain1000(t *testing.T) {
	g := New()
	for i := 0; i < 1000; i++ {
		g.AddNode(fmt.Sprintf("n%04d", i))
	}
	for i := 1; i < 1000; i++ {
		if err := g.AddEdge(fmt.Sprintf("n%04d", i-1), fmt.Sprintf("n%04d", i)); err != nil {
			t.Fatal(err)
		}
	}
	layers, err := g.Layers()
	if err != nil || len(layers) != 1000 {
		t.Fatalf("layers = %d, err = %v, want 1000", len(layers), err)
	}
}

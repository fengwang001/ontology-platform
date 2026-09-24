package graph

import (
	"errors"
	"fmt"
	"testing"
)

type tc struct {
	name string
	build func() *Graph
	wantErr error
	cycleOK bool
}

func TestBuildAndValidate(t *testing.T) {
	cases := []tc{
		{name: "empty", build: New},
		{name: "single", build: func() *Graph {
			g := New()
			g.AddNode("a")
			return g
		}},
		{name: "self loop", build: func() *Graph {
			g := New()
			g.AddNode("a")
			_ = g.AddEdge("a", "a")
			return g
		}, wantErr: ErrCycle, cycleOK: true},
		{name: "three node cycle", build: func() *Graph {
			g := New()
			for _, n := range []string{"a", "b", "c"} {
				g.AddNode(n)
			}
			_ = g.AddEdge("a", "b")
			_ = g.AddEdge("b", "c")
			_ = g.AddEdge("c", "a")
			return g
		}, wantErr: ErrCycle, cycleOK: true},
		{name: "diamond acyclic", build: func() *Graph {
			g := New()
			for _, n := range []string{"a", "b", "c", "d"} {
				g.AddNode(n)
			}
			_ = g.AddEdge("a", "b")
			_ = g.AddEdge("a", "c")
			_ = g.AddEdge("b", "d")
			_ = g.AddEdge("c", "d")
			return g
		}},
		{name: "duplicate edge idempotent", build: func() *Graph {
			g := New()
			g.AddNode("a")
			g.AddNode("b")
			_ = g.AddEdge("a", "b")
			_ = g.AddEdge("a", "b")
			return g
		}},
		{name: "unknown dependency", build: func() *Graph {
			g := New()
			g.AddNode("b")
			if err := g.AddEdge("ghost", "b"); err == nil {
				t.Fatalf("expected error for unknown dep")
			}
			return g
		}},
		{name: "chain 1000", build: func() *Graph {
			g := New()
			for i := 0; i < 1000; i++ {
				g.AddNode(fmt.Sprintf("n%04d", i))
			}
			for i := 1; i < 1000; i++ {
				_ = g.AddEdge(fmt.Sprintf("n%04d", i-1), fmt.Sprintf("n%04d", i))
			}
			return g
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			g := c.build()
			err := g.Validate()
			if c.wantErr == nil && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if c.wantErr != nil {
				if !errors.Is(err, c.wantErr) {
					t.Fatalf("want %v, got %v", c.wantErr, err)
				}
				if c.cycleOK && !verifyCycle(g, err) {
					t.Fatalf("reported cycle path is not a real closed path: %v", err)
				}
			}
		})
	}
}

func verifyCycle(g *Graph, err error) bool {
	var ce *CycleError
	if !errors.As(err, &ce) || len(ce.Path) < 2 || ce.Path[0] != ce.Path[len(ce.Path)-1] {
		return false
	}
	for i := 0; i+1 < len(ce.Path); i++ {
		if _, ok := g.succ[ce.Path[i]][ce.Path[i+1]]; !ok {
			return false
		}
	}
	return true
}

func TestLayers(t *testing.T) {
	cases := []struct {
		name string
		build func() *Graph
		wantLayers int
	}{
		{name: "empty", build: New, wantLayers: 0},
		{name: "diamond", build: func() *Graph {
			g := New()
			for _, n := range []string{"a", "b", "c", "d"} {
				g.AddNode(n)
			}
			_ = g.AddEdge("a", "b")
			_ = g.AddEdge("a", "c")
			_ = g.AddEdge("b", "d")
			_ = g.AddEdge("c", "d")
			return g
		}, wantLayers: 3},
		{name: "chain1000", build: func() *Graph {
			g := New()
			for i := 0; i < 1000; i++ {
				g.AddNode(fmt.Sprintf("n%04d", i))
			}
			for i := 1; i < 1000; i++ {
				_ = g.AddEdge(fmt.Sprintf("n%04d", i-1), fmt.Sprintf("n%04d", i))
			}
			return g
		}, wantLayers: 1000},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			layers, err := c.build().Layers()
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(layers) != c.wantLayers {
				t.Fatalf("want %d layers, got %d", c.wantLayers, len(layers))
			}
		})
	}
}

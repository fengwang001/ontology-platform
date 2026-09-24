package graph

import (
	"errors"
	"fmt"
	"testing"
)

func build(t *testing.T, nodes []string, edges [][2]string) *Graph {
	t.Helper()
	g := New()
	for _, id := range nodes {
		g.AddTask(id)
	}
	for _, e := range edges {
		if err := g.AddEdge(e[0], e[1]); err != nil {
			t.Fatalf("AddEdge(%s,%s): %v", e[0], e[1], err)
		}
	}
	return g
}

func TestLayers(t *testing.T) {
	chainN := 1000
	chainNodes := make([]string, chainN)
	chainEdges := make([][2]string, 0, chainN-1)
	for i := range chainNodes {
		chainNodes[i] = fmt.Sprintf("t%04d", i)
		if i > 0 {
			chainEdges = append(chainEdges, [2]string{chainNodes[i-1], chainNodes[i]})
		}
	}
	cases := []struct {
		name       string
		nodes      []string
		edges      [][2]string
		wantLayers int
		wantEdges  int
	}{
		{"empty", nil, nil, 0, 0},
		{"single", []string{"a"}, nil, 1, 0},
		{"no deps", []string{"a", "b", "c"}, nil, 1, 0},
		{"chain 1000", chainNodes, chainEdges, chainN, chainN - 1},
		{"duplicate edge", []string{"a", "b"}, [][2]string{{"a", "b"}, {"a", "b"}}, 2, 1},
		{"diamond", []string{"a", "b", "c", "d"}, [][2]string{{"a", "b"}, {"a", "c"}, {"b", "d"}, {"c", "d"}}, 3, 4},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := build(t, tc.nodes, tc.edges)
			if n, e := g.Size(); n != len(tc.nodes) || e != tc.wantEdges {
				t.Fatalf("Size() = %d,%d; want %d,%d", n, e, len(tc.nodes), tc.wantEdges)
			}
			layers, err := g.Layers()
			if err != nil {
				t.Fatalf("Layers: %v", err)
			}
			if len(layers) != tc.wantLayers {
				t.Fatalf("got %d layers; want %d", len(layers), tc.wantLayers)
			}
		})
	}
}

func TestAddEdgeUnknown(t *testing.T) {
	cases := []struct{ from, to string }{{"x", "a"}, {"a", "x"}, {"x", "y"}}
	for _, tc := range cases {
		g := build(t, []string{"a"}, nil)
		if err := g.AddEdge(tc.from, tc.to); !errors.Is(err, ErrUnknownTask) {
			t.Fatalf("AddEdge(%s,%s) = %v; want ErrUnknownTask", tc.from, tc.to, err)
		}
	}
}

func TestCycle(t *testing.T) {
	cases := []struct {
		name  string
		nodes []string
		edges [][2]string
	}{
		{"self loop", []string{"a"}, [][2]string{{"a", "a"}}},
		{"three cycle", []string{"a", "b", "c"}, [][2]string{{"a", "b"}, {"b", "c"}, {"c", "a"}}},
		{"cycle with tail", []string{"a", "b", "c", "d"}, [][2]string{{"a", "b"}, {"b", "c"}, {"c", "b"}, {"c", "d"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := build(t, tc.nodes, tc.edges)
			_, err := g.Layers()
			var ce *CycleError
			if !errors.As(err, &ce) || !errors.Is(err, ErrCycle) {
				t.Fatalf("Layers err = %v; want CycleError", err)
			}
			edgeSet := map[[2]string]bool{}
			for _, e := range tc.edges {
				edgeSet[e] = true
			}
			p := ce.Path
			if len(p) < 2 || p[0] != p[len(p)-1] {
				t.Fatalf("path %v not closed", p)
			}
			for i := 0; i+1 < len(p); i++ {
				if !edgeSet[[2]string{p[i], p[i+1]}] {
					t.Fatalf("path edge %s->%s not in input", p[i], p[i+1])
				}
			}
		})
	}
}

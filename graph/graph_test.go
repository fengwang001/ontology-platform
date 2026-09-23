package graph

import (
	"errors"
	"fmt"
	"testing"
)

func edgeExists(g *Graph, from, to string) bool {
	for _, v := range g.Successors(from) {
		if v == to {
			return true
		}
	}
	return false
}

func TestBuildAndValidate(t *testing.T) {
	cases := []struct {
		name      string
		build     func() (*Graph, error)
		wantErrIs error
		wantNodes  int
		wantEdges  int
	}{
		{"empty", func() (*Graph, error) { return New(), nil }, nil, 0, 0},
		{"single", func() (*Graph, error) {
			g := New()
			g.Add("a")
			return g, nil
		}, nil, 1, 0},
		{"unknown dep from", func() (*Graph, error) {
			g := New()
			g.Add("b")
			err := g.AddEdge("x", "b")
			return g, err
		}, ErrUnknownDep, 1, 0},
		{"unknown dep to", func() (*Graph, error) {
			g := New()
			g.Add("a")
			err := g.AddEdge("a", "x")
			return g, err
		}, ErrUnknownDep, 1, 0},
		{"dup edge idempotent", func() (*Graph, error) {
			g := New()
			g.Add("a")
			g.Add("b")
			if err := g.AddEdge("a", "b"); err != nil {
				return g, err
			}
			return g, g.AddEdge("a", "b")
		}, nil, 2, 1},
		{"cycle abc", func() (*Graph, error) {
			g := New()
			for _, n := range []string{"a", "b", "c"} {
				g.Add(n)
			}
			_ = g.AddEdge("a", "b")
			_ = g.AddEdge("b", "c")
			err := g.AddEdge("c", "a")
			return g, err
		}, ErrCycle, 3, 3},
		{"self loop", func() (*Graph, error) {
			g := New()
			g.Add("a")
			err := g.AddEdge("a", "a")
			return g, err
		}, ErrCycle, 1, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g, addErr := tc.build()
			if tc.wantErrIs != nil {
				if addErr != nil && !errors.Is(addErr, tc.wantErrIs) {
					t.Fatalf("add err = %v, want %v", addErr, tc.wantErrIs)
				}
				checkErr := g.Check()
				if addErr == nil && !errors.Is(checkErr, tc.wantErrIs) {
					t.Fatalf("check err = %v, want %v", checkErr, tc.wantErrIs)
				}
			} else if addErr != nil || g.Check() != nil {
				t.Fatalf("unexpected error: add=%v check=%v", addErr, g.Check())
			}
			if len(g.Nodes()) != tc.wantNodes {
				t.Fatalf("nodes = %d, want %d", len(g.Nodes()), tc.wantNodes)
			}
			if g.EdgeCount() != tc.wantEdges {
				t.Fatalf("edges = %d, want %d", g.EdgeCount(), tc.wantEdges)
			}
		})
	}
}

func TestCyclePathEdgesExist(t *testing.T) {
	cases := [][][]string{
		{{"a", "b"}, {"b", "c"}, {"c", "a"}},
		{{"a", "a"}},
		{{"x", "y"}, {"y", "z"}, {"z", "y"}},
	}
	for i, edges := range cases {
		t.Run(fmt.Sprintf("cycle%d", i), func(t *testing.T) {
			g := New()
			seen := map[string]bool{}
			for _, e := range edges {
				seen[e[0]], seen[e[1]] = true, true
			}
			for n := range seen {
				g.Add(n)
			}
			for _, e := range edges {
				_ = g.AddEdge(e[0], e[1])
			}
			path := g.Cycle()
			if path == nil || len(path) < 2 || path[0] != path[len(path)-1] {
				t.Fatalf("bad closed path: %v", path)
			}
			for j := 0; j+1 < len(path); j++ {
				if !edgeExists(g, path[j], path[j+1]) {
					t.Fatalf("reported edge %s->%s not in input; path=%v", path[j], path[j+1], path)
				}
			}
		})
	}
}

func TestLayersAndLongChain(t *testing.T) {
	lengths := []int{0, 1, 2, 5, 1000}
	for _, n := range lengths {
		t.Run(fmt.Sprintf("chain%d", n), func(t *testing.T) {
			g := New()
			for i := 0; i < n; i++ {
				g.Add(fmt.Sprintf("t%04d", i))
			}
			for i := 1; i < n; i++ {
				if err := g.AddEdge(fmt.Sprintf("t%04d", i-1), fmt.Sprintf("t%04d", i)); err != nil {
					t.Fatal(err)
				}
			}
			layers := g.Layers()
			if n == 0 {
				if layers != nil {
					t.Fatalf("empty graph layers = %v, want nil-ish", layers)
				}
				return
			}
			if len(layers) != n {
				t.Fatalf("layers = %d, want %d", len(layers), n)
			}
			topo := g.Topo()
			if len(topo) != n {
				t.Fatalf("topo len = %d, want %d", len(topo), n)
			}
		})
	}
}

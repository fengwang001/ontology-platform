package graph

import (
	"errors"
	"strings"
	"testing"

	"ontology/fail"
)

func TestGraphCases(t *testing.T) {
	chain := func(n int) *Graph {
		g := New()
		for i := 0; i < n; i++ {
			g.Add(nodeName(i))
		}
		for i := 1; i < n; i++ {
			if err := g.AddEdge(nodeName(i-1), nodeName(i)); err != nil {
				t.Fatal(err)
			}
		}
		return g
	}
	cases := []struct {
		name    string
		build   func() *Graph
		wantErr error
		check   func(t *testing.T, g *Graph)
	}{
		{
			name:  "empty graph has no layers",
			build: New,
			check: func(t *testing.T, g *Graph) {
				ls, err := g.Layers()
				if err != nil || len(ls) != 0 {
					t.Fatalf("layers=%v err=%v", ls, err)
				}
			},
		},
		{
			name: "single node is one layer",
			build: func() *Graph {
				g := New()
				g.Add("A")
				return g
			},
			check: func(t *testing.T, g *Graph) {
				ls, err := g.Layers()
				if err != nil || len(ls) != 1 || ls[0][0] != "A" {
					t.Fatalf("layers=%v err=%v", ls, err)
				}
			},
		},
		{
			name:  "chain of 1000 has 1000 layers",
			build: func() *Graph { return chain(1000) },
			check: func(t *testing.T, g *Graph) {
				ls, err := g.Layers()
				if err != nil {
					t.Fatal(err)
				}
				if len(ls) != 1000 || ls[999][0] != nodeName(999) {
					t.Fatalf("got %d layers", len(ls))
				}
			},
		},
		{
			name: "diamond layers",
			build: func() *Graph {
				g := New()
				g.Add("A", "B", "C", "D")
				g.MustAddEdge("A", "B")
				g.MustAddEdge("A", "C")
				g.MustAddEdge("B", "D")
				g.MustAddEdge("C", "D")
				return g
			},
			check: func(t *testing.T, g *Graph) {
				ls, err := g.Layers()
				if err != nil || len(ls) != 3 {
					t.Fatalf("layers=%v err=%v", ls, err)
				}
				if strings.Join(ls[1], ",") != "B,C" {
					t.Fatalf("layer1=%v", ls[1])
				}
			},
		},
		{
			name: "duplicate edge is idempotent",
			build: func() *Graph {
				g := New()
				g.Add("A", "B")
				g.MustAddEdge("A", "B")
				g.MustAddEdge("A", "B")
				return g
			},
			check: func(t *testing.T, g *Graph) {
				if g.Edges() != 1 {
					t.Fatalf("edges=%d", g.Edges())
				}
			},
		},
		{
			name: "unknown endpoint rejected",
			build: func() *Graph {
				g := New()
				g.Add("A")
				return g
			},
			check: func(t *testing.T, g *Graph) {
				if err := g.AddEdge("A", "B"); !errors.Is(err, fail.ErrNoSuchTask) {
					t.Fatalf("err=%v", err)
				}
				if err := g.AddEdge("B", "A"); !errors.Is(err, fail.ErrNoSuchTask) {
					t.Fatalf("err=%v", err)
				}
			},
		},
		{
			name: "three node cycle has closed path",
			build: func() *Graph {
				g := New()
				g.Add("A", "B", "C")
				g.MustAddEdge("A", "B")
				g.MustAddEdge("B", "C")
				g.MustAddEdge("C", "A")
				return g
			},
			wantErr: fail.ErrCyclic,
			check: func(t *testing.T, g *Graph) {
				path, err := g.Cycle()
				if !errors.Is(err, fail.ErrCyclic) {
					t.Fatalf("err=%v", err)
				}
				assertClosedPath(t, g, path)
			},
		},
		{
			name: "self loop detected",
			build: func() *Graph {
				g := New()
				g.Add("A")
				g.MustAddEdge("A", "A")
				return g
			},
			wantErr: fail.ErrCyclic,
			check: func(t *testing.T, g *Graph) {
				path, err := g.Cycle()
				if !errors.Is(err, fail.ErrCyclic) || len(path) != 2 {
					t.Fatalf("path=%v err=%v", path, err)
				}
				assertClosedPath(t, g, path)
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := tc.build()
			if tc.wantErr != nil {
				if _, err := g.Layers(); !errors.Is(err, tc.wantErr) {
					t.Fatalf("Layers err=%v want %v", err, tc.wantErr)
				}
			}
			if tc.check != nil {
				tc.check(t, g)
			}
		})
	}
}

func assertClosedPath(t *testing.T, g *Graph, path []string) {
	t.Helper()
	if len(path) < 2 || path[0] != path[len(path)-1] {
		t.Fatalf("path not closed: %v", path)
	}
	for i := 0; i+1 < len(path); i++ {
		if _, ok := g.out[path[i]][path[i+1]]; !ok {
			t.Fatalf("edge %s->%s not in graph; path=%v", path[i], path[i+1], path)
		}
	}
}

func nodeName(i int) string {
	return "n" + string(rune('a'+i%26)) + itoa(i/26)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

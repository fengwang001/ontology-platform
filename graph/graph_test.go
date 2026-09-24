package graph

import (
	"errors"
	"strconv"
	"testing"
)

type edge struct{ from, to string }

func build(nodes []string, edges []edge) *Graph {
	g := New()
	for _, n := range nodes {
		g.Add(n)
	}
	for _, e := range edges {
		if err := g.AddEdge(e.from, e.to); err != nil {
			panic(err)
		}
	}
	return g
}

func chain(n int) ([]string, []edge) {
	nodes := make([]string, n)
	var edges []edge
	for i := 0; i < n; i++ {
		nodes[i] = "t" + strconv.Itoa(i)
		if i > 0 {
			edges = append(edges, edge{nodes[i-1], nodes[i]})
		}
	}
	return nodes, edges
}

func verifyCycle(t *testing.T, g *Graph, cyc []string) {
	t.Helper()
	if len(cyc) < 2 || cyc[0] != cyc[len(cyc)-1] {
		t.Fatalf("cycle path not closed: %v", cyc)
	}
	for i := 0; i+1 < len(cyc); i++ {
		if _, ok := g.succ[cyc[i]][cyc[i+1]]; !ok {
			t.Fatalf("cycle edge %q->%q not in graph", cyc[i], cyc[i+1])
		}
	}
}

func TestGraph(t *testing.T) {
	chainNodes, chainEdges := chain(1000)
	cases := []struct {
		name       string
		nodes      []string
		edges      []edge
		wantCycle  bool
		wantLayers int
	}{
		{"empty", nil, nil, false, 0},
		{"single", []string{"a"}, nil, false, 1},
		{"independent", []string{"a", "b", "c"}, nil, false, 1},
		{"chain1000", chainNodes, chainEdges, false, 1000},
		{"diamond", []string{"a", "b", "c", "d"},
			[]edge{{"a", "b"}, {"a", "c"}, {"b", "d"}, {"c", "d"}}, false, 3},
		{"selfloop", []string{"a"}, []edge{{"a", "a"}}, true, 0},
		{"twocycle", []string{"a", "b"}, []edge{{"a", "b"}, {"b", "a"}}, true, 0},
		{"cycle-with-tail", []string{"a", "b", "c", "d"},
			[]edge{{"a", "b"}, {"b", "c"}, {"c", "b"}, {"c", "d"}}, true, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := build(tc.nodes, tc.edges)
			cyc := g.Cycle()
			if tc.wantCycle {
				verifyCycle(t, g, cyc)
			} else if cyc != nil {
				t.Fatalf("unexpected cycle %v", cyc)
			}
			layers := g.Layers()
			if tc.wantCycle {
				if layers != nil {
					t.Fatalf("cyclic graph must have nil layers, got %v", layers)
				}
			} else if len(layers) != tc.wantLayers {
				t.Fatalf("layers=%d want %d", len(layers), tc.wantLayers)
			}
		})
	}
}

func TestEdges(t *testing.T) {
	cases := []struct {
		name      string
		from, to  string
		wantErr   error
		wantIndeg int
	}{
		{"ok", "a", "b", nil, 1},
		{"duplicate", "a", "b", nil, 1},
		{"missing-from", "zz", "b", ErrMissingNode, 1},
		{"missing-to", "a", "zz", ErrMissingNode, 1},
	}
	g := build([]string{"a", "b"}, nil)
	for _, tc := range cases {
		err := g.AddEdge(tc.from, tc.to)
		if !errors.Is(err, tc.wantErr) {
			t.Fatalf("%s: err=%v want %v", tc.name, err, tc.wantErr)
		}
		if got := g.InDegree("b"); got != tc.wantIndeg {
			t.Fatalf("%s: indeg=%d want %d", tc.name, got, tc.wantIndeg)
		}
	}
}

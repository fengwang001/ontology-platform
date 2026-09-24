package graph

import (
	"errors"
	"fmt"
	"reflect"
	"testing"
)

func build(t *testing.T, nodes []string, edges [][2]string) *Graph {
	t.Helper()
	g := New()
	for _, n := range nodes {
		g.AddNode(n)
	}
	for _, e := range edges {
		if err := g.AddEdge(e[0], e[1]); err != nil {
			t.Fatalf("AddEdge(%s,%s): %v", e[0], e[1], err)
		}
	}
	return g
}

func verifyCycle(t *testing.T, g *Graph, path []string) {
	t.Helper()
	if len(path) < 2 || path[0] != path[len(path)-1] {
		t.Fatalf("cycle path not closed: %v", path)
	}
	for i := 0; i+1 < len(path); i++ {
		found := false
		for _, s := range g.Successors(path[i]) {
			if s == path[i+1] {
				found = true
			}
		}
		if !found {
			t.Fatalf("edge %s->%s of cycle %v not in graph", path[i], path[i+1], path)
		}
	}
}

func TestLayersAndCycles(t *testing.T) {
	cases := []struct {
		name    string
		nodes   []string
		edges   [][2]string
		layers  [][]string
		hasCyc  bool
		dupEdge bool
	}{
		{name: "empty"},
		{name: "single", nodes: []string{"a"}, layers: [][]string{{"a"}}},
		{name: "independent", nodes: []string{"b", "a", "c"}, layers: [][]string{{"a", "b", "c"}}},
		{
			name:   "diamond",
			nodes:  []string{"a", "b", "c", "d"},
			edges:  [][2]string{{"a", "b"}, {"a", "c"}, {"b", "d"}, {"c", "d"}},
			layers: [][]string{{"a"}, {"b", "c"}, {"d"}},
		},
		{
			name:    "duplicate edge idempotent",
			nodes:   []string{"a", "b"},
			edges:   [][2]string{{"a", "b"}, {"a", "b"}, {"a", "b"}},
			layers:  [][]string{{"a"}, {"b"}},
			dupEdge: true,
		},
		{
			name:   "cycle",
			nodes:  []string{"a", "b", "c", "d"},
			edges:  [][2]string{{"a", "b"}, {"b", "c"}, {"c", "a"}, {"c", "d"}},
			hasCyc: true,
		},
		{
			name:   "self loop",
			nodes:  []string{"a", "b"},
			edges:  [][2]string{{"a", "a"}, {"a", "b"}},
			hasCyc: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := build(t, tc.nodes, tc.edges)
			layers, cyc := g.Layers()
			if tc.hasCyc {
				if layers != nil || cyc == nil {
					t.Fatalf("want cycle, got layers=%v cycle=%v", layers, cyc)
				}
				verifyCycle(t, g, cyc)
				return
			}
			if cyc != nil || !reflect.DeepEqual(layers, tc.layers) {
				t.Fatalf("layers=%v cycle=%v, want %v", layers, cyc, tc.layers)
			}
			if tc.dupEdge && g.Edges() != 1 {
				t.Fatalf("duplicate edges not idempotent: %d edges", g.Edges())
			}
		})
	}
}

func TestUnknownNode(t *testing.T) {
	g := build(t, []string{"a"}, nil)
	for _, e := range [][2]string{{"a", "ghost"}, {"ghost", "a"}} {
		if err := g.AddEdge(e[0], e[1]); !errors.Is(err, ErrUnknownNode) {
			t.Fatalf("AddEdge(%s,%s)=%v, want ErrUnknownNode", e[0], e[1], err)
		}
	}
}

func TestLongChainNoOverflow(t *testing.T) {
	const n = 1000
	g := New()
	for i := 0; i < n; i++ {
		g.AddNode(fmt.Sprintf("t%04d", i))
		if i > 0 {
			if err := g.AddEdge(fmt.Sprintf("t%04d", i-1), fmt.Sprintf("t%04d", i)); err != nil {
				t.Fatal(err)
			}
		}
	}
	layers, cyc := g.Layers()
	if cyc != nil || len(layers) != n {
		t.Fatalf("chain of %d: layers=%d cycle=%v", n, len(layers), cyc)
	}
}

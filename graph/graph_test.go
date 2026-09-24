package graph

import (
	"errors"
	"fmt"
	"reflect"
	"testing"

	"ontology/fail"
)

func build(nodes []string, edges [][2]string) *Graph {
	g := New()
	for _, n := range nodes {
		g.AddTask(n)
	}
	for _, e := range edges {
		if err := g.AddEdge(e[0], e[1]); err != nil {
			panic(err)
		}
	}
	return g
}

func validPath(g *Graph, path []string) bool {
	if len(path) < 2 || path[0] != path[len(path)-1] {
		return false
	}
	for i := 0; i+1 < len(path); i++ {
		if !g.HasEdge(path[i], path[i+1]) {
			return false
		}
	}
	return true
}

func TestGraph(t *testing.T) {
	cases := []struct {
		name      string
		nodes     []string
		edges     [][2]string
		wantCycle bool
		layers    [][]string
	}{
		{"empty", nil, nil, false, nil},
		{"single", []string{"a"}, nil, false, [][]string{{"a"}}},
		{"no-deps", []string{"a", "b", "c"}, nil, false, [][]string{{"a", "b", "c"}}},
		{"chain", []string{"a", "b", "c"}, [][2]string{{"a", "b"}, {"b", "c"}}, false,
			[][]string{{"a"}, {"b"}, {"c"}}},
		{"diamond", []string{"a", "b", "c", "d"},
			[][2]string{{"a", "b"}, {"a", "c"}, {"b", "d"}, {"c", "d"}}, false,
			[][]string{{"a"}, {"b", "c"}, {"d"}}},
		{"cycle3", []string{"a", "b", "c"},
			[][2]string{{"a", "b"}, {"b", "c"}, {"c", "a"}}, true, nil},
		{"self-loop", []string{"x"}, [][2]string{{"x", "x"}}, true, nil},
		{"cycle2", []string{"a", "b"}, [][2]string{{"a", "b"}, {"b", "a"}}, true, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := build(tc.nodes, tc.edges)
			cyc := g.Cycle()
			if got := cyc != nil; got != tc.wantCycle {
				t.Fatalf("cycle=%v want %v (path %v)", got, tc.wantCycle, cyc)
			}
			if cyc != nil && !validPath(g, cyc) {
				t.Fatalf("cycle path %v not closed/edge-valid", cyc)
			}
			if got := g.Layers(); !reflect.DeepEqual(got, tc.layers) {
				t.Fatalf("layers=%v want %v", got, tc.layers)
			}
		})
	}
}

func TestEdgesAndDepth(t *testing.T) {
	t.Run("dup-edge-idempotent", func(t *testing.T) {
		g := build([]string{"a", "b"}, [][2]string{{"a", "b"}, {"a", "b"}, {"a", "b"}})
		if g.Edges() != 1 || len(g.Preds("b")) != 1 {
			t.Fatalf("dup edge not idempotent: edges=%d preds=%v", g.Edges(), g.Preds("b"))
		}
	})
	t.Run("unknown-dependency", func(t *testing.T) {
		g := build([]string{"a"}, nil)
		for _, e := range [][2]string{{"a", "ghost"}, {"ghost", "a"}} {
			if err := g.AddEdge(e[0], e[1]); !errors.Is(err, fail.ErrUnknown) {
				t.Fatalf("edge %v: err=%v want ErrUnknown", e, err)
			}
		}
	})
	t.Run("deep-chain-1000", func(t *testing.T) {
		g := New()
		for i := 0; i < 1000; i++ {
			g.AddTask(fmt.Sprintf("t%04d", i))
			if i > 0 {
				if err := g.AddEdge(fmt.Sprintf("t%04d", i-1), fmt.Sprintf("t%04d", i)); err != nil {
					t.Fatal(err)
				}
			}
		}
		if cyc := g.Cycle(); cyc != nil {
			t.Fatalf("false cycle: %v", cyc)
		}
		if n := len(g.Layers()); n != 1000 {
			t.Fatalf("layers=%d want 1000", n)
		}
	})
}

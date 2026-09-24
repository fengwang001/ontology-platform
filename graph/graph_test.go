package graph_test

import (
	"errors"
	"fmt"
	"reflect"
	"testing"

	"ontology/graph"
)

func build(t *testing.T, n int, edges [][2]int) *graph.Graph {
	t.Helper()
	g := graph.New()
	for i := 0; i < n; i++ {
		g.Add(fmt.Sprintf("t%03d", i))
	}
	for _, e := range edges {
		if err := g.Edge(fmt.Sprintf("t%03d", e[0]), fmt.Sprintf("t%03d", e[1])); err != nil {
			t.Fatalf("Edge: %v", err)
		}
	}
	return g
}

func TestLayers(t *testing.T) {
	chain := [][2]int{}
	for i := 0; i+1 < 1000; i++ {
		chain = append(chain, [2]int{i, i + 1})
	}
	cases := []struct {
		name  string
		n     int
		edges [][2]int
		want  [][]string
	}{
		{"empty", 0, nil, nil},
		{"single", 1, nil, [][]string{{"t000"}}},
		{"all independent", 3, nil, [][]string{{"t000", "t001", "t002"}}},
		{"chain", 3, [][2]int{{0, 1}, {1, 2}}, [][]string{{"t000"}, {"t001"}, {"t002"}}},
		{"diamond", 4, [][2]int{{0, 1}, {0, 2}, {1, 3}, {2, 3}},
			[][]string{{"t000"}, {"t001", "t002"}, {"t003"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := build(t, tc.n, tc.edges).Layers()
			if err != nil {
				t.Fatalf("Layers: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
	t.Run("chain-1000 no stack overflow", func(t *testing.T) {
		layers, err := build(t, 1000, chain).Layers()
		if err != nil || len(layers) != 1000 {
			t.Fatalf("layers=%d err=%v", len(layers), err)
		}
	})
}

func TestCycleDetection(t *testing.T) {
	cases := []struct {
		name  string
		n     int
		edges [][2]int
	}{
		{"self loop", 1, [][2]int{{0, 0}}},
		{"two cycle", 2, [][2]int{{0, 1}, {1, 0}}},
		{"three cycle with tail", 5, [][2]int{{0, 1}, {1, 2}, {2, 0}, {3, 0}, {2, 4}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := build(t, tc.n, tc.edges)
			_, err := g.Layers()
			var cerr *graph.CycleError
			if !errors.As(err, &cerr) || !errors.Is(err, graph.ErrCycle) {
				t.Fatalf("err=%v", err)
			}
			p := cerr.Path
			if len(p) < 2 || p[0] != p[len(p)-1] {
				t.Fatalf("path not closed: %v", p)
			}
			for i := 0; i+1 < len(p); i++ {
				if !g.HasEdge(p[i], p[i+1]) {
					t.Fatalf("edge %s->%s not in input, path %v", p[i], p[i+1], p)
				}
			}
		})
	}
}

func TestEdgeValidation(t *testing.T) {
	g := build(t, 2, nil)
	for _, e := range [][2]string{{"t000", "ghost"}, {"ghost", "t001"}} {
		if err := g.Edge(e[0], e[1]); !errors.Is(err, graph.ErrUnknownTask) {
			t.Fatalf("edge %v: %v", e, err)
		}
	}
	if err := g.Edge("t000", "t001"); err != nil {
		t.Fatal(err)
	}
	if err := g.Edge("t000", "t001"); err != nil {
		t.Fatal(err)
	}
	if got := g.Deps("t001"); len(got) != 1 {
		t.Fatalf("duplicate edge not idempotent: %v", got)
	}
}

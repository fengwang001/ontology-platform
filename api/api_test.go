package api

import (
	"errors"
	"testing"

	"ontology/graph"
	"ontology/traverse"
)

func build(t *testing.T, nodes []string, edges [][2]string) *graph.Graph {
	t.Helper()
	g := graph.New()
	for _, n := range nodes {
		if err := g.AddNode(n); err != nil {
			t.Fatalf("AddNode(%s): %v", n, err)
		}
	}
	for _, e := range edges {
		if err := g.AddEdge(e[0], e[1]); err != nil {
			t.Fatalf("AddEdge(%s,%s): %v", e[0], e[1], err)
		}
	}
	return g
}

func TestTraverseValidation(t *testing.T) {
	g := build(t, []string{"a"}, nil)
	cases := []struct {
		name string
		req  Request
		want error
	}{
		{"nil graph", Request{Start: "a", Dir: traverse.Out}, ErrNilGraph},
		{"missing start", Request{Graph: g, Start: "x", Dir: traverse.Out}, ErrNoStart},
		{"invalid dir", Request{Graph: g, Start: "a", Dir: traverse.Dir(-1)}, ErrInvalidDir},
	}
	for _, tc := range cases {
		if _, err := Traverse(tc.req); !errors.Is(err, tc.want) {
			t.Errorf("%s: got %v, want %v", tc.name, err, tc.want)
		}
	}
}

func TestTraverse(t *testing.T) {
	chain := build(t,
		[]string{"a", "b", "c", "d", "e"},
		[][2]string{{"a", "b"}, {"b", "c"}, {"c", "d"}, {"d", "e"}})
	cycle3 := build(t,
		[]string{"a", "b", "c"},
		[][2]string{{"a", "b"}, {"b", "c"}, {"c", "a"}})

	cases := []struct {
		name      string
		req       Request
		wantNodes int
		wantErr   error
	}{
		{"complete", Request{Graph: chain, Start: "a", Dir: traverse.Out}, 5, nil},
		{"limit cut", Request{Graph: chain, Start: "a", Dir: traverse.Out, Limit: 3}, 3, ErrLimitCut},
		{"limit exact complete", Request{Graph: chain, Start: "a", Dir: traverse.Out, Limit: 5}, 5, nil},
		{"depth cut", Request{Graph: chain, Start: "a", Dir: traverse.Out, MaxDepth: 2}, 3, ErrDepthCut},
		{"cycle complete", Request{Graph: cycle3, Start: "a", Dir: traverse.Out, MaxDepth: 2}, 3, nil},
		{"cycle depth cut", Request{Graph: cycle3, Start: "a", Dir: traverse.Out, MaxDepth: 1}, 2, ErrDepthCut},
		{"zero means unbounded", Request{Graph: cycle3, Start: "a", Dir: traverse.Out, MaxDepth: 0, Limit: 0}, 3, nil},
		{"negative means unbounded", Request{Graph: chain, Start: "a", Dir: traverse.Out, MaxDepth: -1, Limit: -2}, 5, nil},
		{"in direction", Request{Graph: chain, Start: "e", Dir: traverse.In, MaxDepth: 1}, 2, ErrDepthCut},
	}
	for _, tc := range cases {
		res, err := Traverse(tc.req)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if !errors.Is(res.Err, tc.wantErr) {
			t.Errorf("%s: Err=%v, want %v", tc.name, res.Err, tc.wantErr)
		}
		if len(res.Nodes) != tc.wantNodes {
			t.Errorf("%s: %d nodes, want %d", tc.name, len(res.Nodes), tc.wantNodes)
		}
	}
}

func TestHasCycle(t *testing.T) {
	diamond := build(t,
		[]string{"a", "b", "c", "d"},
		[][2]string{{"a", "b"}, {"a", "c"}, {"b", "d"}, {"c", "d"}})
	cycle := build(t,
		[]string{"a", "b", "c"},
		[][2]string{{"a", "b"}, {"b", "c"}, {"c", "a"}})

	cases := []struct {
		name string
		g    *graph.Graph
		want bool
	}{
		{"diamond", diamond, false},
		{"cycle", cycle, true},
	}
	for _, tc := range cases {
		got, err := HasCycle(tc.g)
		if err != nil || got != tc.want {
			t.Errorf("%s: got %v, %v; want %v", tc.name, got, err, tc.want)
		}
	}
	if _, err := HasCycle(nil); !errors.Is(err, ErrNilGraph) {
		t.Errorf("nil graph: %v", err)
	}
}

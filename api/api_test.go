package api

import (
	"errors"
	"slices"
	"testing"

	"ontology/graph"
)

func build(t *testing.T) *graph.Graph {
	t.Helper()
	g := graph.New()
	for _, n := range []string{"A", "B", "C", "D", "E"} {
		if err := g.AddNode(n); err != nil {
			t.Fatal(err)
		}
	}
	for _, e := range [][2]string{{"A", "B"}, {"B", "C"}, {"C", "D"}, {"D", "E"}} {
		if err := g.AddEdge(e[0], e[1]); err != nil {
			t.Fatal(err)
		}
	}
	return g
}

func TestTraverseValidation(t *testing.T) {
	g := build(t)
	cases := []struct {
		name string
		req  Request
	}{
		{"nil graph", Request{Start: "A", Dir: Out}},
		{"missing start", Request{Graph: g, Start: "X", Dir: Out}},
		{"invalid direction", Request{Graph: g, Start: "A", Dir: Direction(99)}},
		{"negative MaxDepth", Request{Graph: g, Start: "A", Dir: Out, MaxDepth: -1}},
		{"negative Limit", Request{Graph: g, Start: "A", Dir: Out, Limit: -1}},
	}
	for _, tc := range cases {
		if _, err := Traverse(tc.req); err == nil {
			t.Errorf("%s: want error, got nil", tc.name)
		}
	}
}

func TestTraverse(t *testing.T) {
	g := build(t)
	cases := []struct {
		name      string
		req       Request
		wantNodes []string
		wantErr   error
	}{
		{"zero values mean unbounded", Request{Graph: g, Start: "A", Dir: Out},
			[]string{"A", "B", "C", "D", "E"}, nil},
		{"limit cut", Request{Graph: g, Start: "A", Dir: Out, Limit: 2},
			[]string{"A", "B"}, ErrLimitCut},
		{"depth cut", Request{Graph: g, Start: "A", Dir: Out, MaxDepth: 1},
			[]string{"A", "B"}, ErrDepthCut},
		{"exact limit is complete", Request{Graph: g, Start: "A", Dir: Out, Limit: 5},
			[]string{"A", "B", "C", "D", "E"}, nil},
		{"in direction", Request{Graph: g, Start: "E", Dir: In, MaxDepth: 2},
			[]string{"E", "D", "C"}, ErrDepthCut},
	}
	for _, tc := range cases {
		resp, err := Traverse(tc.req)
		if err != nil {
			t.Errorf("%s: unexpected error: %v", tc.name, err)
			continue
		}
		if !slices.Equal(resp.Nodes, tc.wantNodes) {
			t.Errorf("%s: Nodes=%v, want %v", tc.name, resp.Nodes, tc.wantNodes)
		}
		if !errors.Is(resp.Err, tc.wantErr) {
			t.Errorf("%s: Err=%v, want %v", tc.name, resp.Err, tc.wantErr)
		}
	}
}

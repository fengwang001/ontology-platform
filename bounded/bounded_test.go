package bounded

import (
	"errors"
	"testing"

	"ontology/graph"
	"ontology/traverse"
)

func addAll(g *graph.Graph, nodes ...string) {
	for _, n := range nodes {
		_ = g.AddNode(n)
	}
}

func chainGraph() *graph.Graph {
	g := graph.New()
	addAll(g, "A", "B", "C", "D")
	_ = g.AddEdge("A", "B")
	_ = g.AddEdge("B", "C")
	_ = g.AddEdge("C", "D")
	return g
}

func cycle3Graph() *graph.Graph {
	g := graph.New()
	addAll(g, "A", "B", "C")
	_ = g.AddEdge("A", "B")
	_ = g.AddEdge("B", "C")
	_ = g.AddEdge("C", "A")
	return g
}

func diamondGraph() *graph.Graph {
	g := graph.New()
	addAll(g, "A", "B", "C", "D")
	_ = g.AddEdge("A", "B")
	_ = g.AddEdge("A", "C")
	_ = g.AddEdge("B", "D")
	_ = g.AddEdge("C", "D")
	return g
}

func TestRun(t *testing.T) {
	tests := []struct {
		name       string
		g          *graph.Graph
		start      string
		dir        traverse.Dir
		maxDepth   int
		limit      int
		wantNodes  []string
		wantStatus Status
	}{
		// --- limit semantics ---
		{"chain unlimited", chainGraph(), "A", traverse.DirOut, 10, 10, []string{"A", "B", "C", "D"}, Complete},
		{"limit exact reachable count is Complete", chainGraph(), "A", traverse.DirOut, 10, 4, []string{"A", "B", "C", "D"}, Complete},
		{"limit cuts", chainGraph(), "A", traverse.DirOut, 10, 2, []string{"A", "B"}, LimitCut},
		{"cycle fills limit, remaining cycle nodes unreturned", cycle3Graph(), "A", traverse.DirOut, 10, 2, []string{"A", "B"}, LimitCut},
		{"cycle reachable == limit is Complete (dedup)", cycle3Graph(), "A", traverse.DirOut, 10, 3, []string{"A", "B", "C"}, Complete},
		// --- depth semantics ---
		{"depth 0 only start", chainGraph(), "A", traverse.DirOut, 0, 10, []string{"A"}, DepthCut},
		{"depth cuts mid chain", chainGraph(), "A", traverse.DirOut, 1, 10, []string{"A", "B"}, DepthCut},
		{"depth exactly reaches end is Complete", chainGraph(), "A", traverse.DirOut, 3, 10, []string{"A", "B", "C", "D"}, Complete},
		{"cycle depth shorter than circumference is DepthCut", cycle3Graph(), "A", traverse.DirOut, 1, 10, []string{"A", "B"}, DepthCut},
		{"cycle depth full lap is Complete (back-edge not a cut)", cycle3Graph(), "A", traverse.DirOut, 2, 10, []string{"A", "B", "C"}, Complete},
		{"diamond depth 2 merges, no cut", diamondGraph(), "A", traverse.DirOut, 2, 10, []string{"A", "B", "C", "D"}, Complete},
		// --- precedence ---
		{"both bounds hit: limit wins", chainGraph(), "A", traverse.DirOut, 0, 1, []string{"A"}, LimitCut},
		{"in direction depth cut", chainGraph(), "D", traverse.DirIn, 1, 10, []string{"D", "C"}, DepthCut},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res, err := Run(tc.g, tc.start, tc.dir, tc.maxDepth, tc.limit)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if res.Status != tc.wantStatus {
				t.Fatalf("status = %s, want %s", res.Status, tc.wantStatus)
			}
			if !eq(res.Nodes, tc.wantNodes) {
				t.Fatalf("nodes = %v, want %v", res.Nodes, tc.wantNodes)
			}
			// Invariant: output + unreturned == in-bounds reachable.
			if res.Emitted+res.Unreturned != res.Reachable {
				t.Fatalf("counter invariant broken: %d+%d != %d", res.Emitted, res.Unreturned, res.Reachable)
			}
			if res.Emitted != len(res.Nodes) {
				t.Fatalf("emitted counter %d != len(nodes) %d", res.Emitted, len(res.Nodes))
			}
			// Three-state mutual exclusivity via errors.Is.
			isLimit := errors.Is(res.Status.Error(), ErrLimitCut)
			isDepth := errors.Is(res.Status.Error(), ErrDepthCut)
			if isLimit && isDepth {
				t.Fatal("LimitCut and DepthCut both matched")
			}
			switch res.Status {
			case LimitCut:
				if !isLimit || res.Unreturned == 0 {
					t.Fatal("LimitCut must be errors.Is-matchable with unreturned > 0")
				}
			case DepthCut:
				if !isDepth {
					t.Fatal("DepthCut must be errors.Is-matchable")
				}
			case Complete:
				if res.Status.Error() != nil {
					t.Fatal("Complete must map to nil error")
				}
			}
		})
	}
}

func TestRunValidation(t *testing.T) {
	g := chainGraph()
	cases := []struct {
		name            string
		start           string
		dir             traverse.Dir
		maxDepth, limit int
		want            error
	}{
		{"missing start", "Z", traverse.DirOut, 1, 1, traverse.ErrStartNotFound},
		{"bad dir", "A", traverse.Dir(0), 1, 1, traverse.ErrInvalidDir},
		{"negative depth", "A", traverse.DirOut, -1, 1, nil},
		{"zero limit", "A", traverse.DirOut, 1, 0, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Run(g, tc.start, tc.dir, tc.maxDepth, tc.limit)
			if tc.want != nil {
				if !errors.Is(err, tc.want) {
					t.Fatalf("got %v, want %v", err, tc.want)
				}
				return
			}
			if err == nil {
				t.Fatal("expected validation error, got nil")
			}
		})
	}
}

func eq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

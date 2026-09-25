package traverse

import (
	"slices"
	"testing"

	"ontology/graph"
)

func build(t *testing.T, nodes []string, edges [][2]string) *graph.Graph {
	t.Helper()
	g := graph.New()
	for _, n := range nodes {
		if err := g.AddNode(n); err != nil {
			t.Fatal(err)
		}
	}
	for _, e := range edges {
		if err := g.AddEdge(e[0], e[1]); err != nil {
			t.Fatal(err)
		}
	}
	return g
}

func TestWalk(t *testing.T) {
	diamond := []string{"A", "B", "C", "D", "E"}
	diamondEdges := [][2]string{{"A", "B"}, {"A", "C"}, {"B", "D"}, {"C", "D"}, {"D", "E"}}
	ring := []string{"A", "B", "C"}
	ringEdges := [][2]string{{"A", "B"}, {"B", "C"}, {"C", "A"}}

	cases := []struct {
		name  string
		nodes []string
		edges [][2]string
		start string
		dir   Direction
		want  []string
	}{
		{"out over diamond", diamond, diamondEdges, "A", Out, []string{"A", "B", "C", "D", "E"}},
		{"in over diamond", diamond, diamondEdges, "E", In, []string{"E", "D", "B", "C", "A"}},
		{"both from middle", diamond, diamondEdges, "C", Both, []string{"C", "D", "A", "E", "B"}},
		{"out on ring terminates", ring, ringEdges, "A", Out, []string{"A", "B", "C"}},
		{"in on ring terminates", ring, ringEdges, "A", In, []string{"A", "C", "B"}},
		{"both on ring visits once", ring, ringEdges, "B", Both, []string{"B", "C", "A"}},
		{"unreachable not visited", diamond, diamondEdges, "D", Out, []string{"D", "E"}},
	}
	for _, tc := range cases {
		g := build(t, tc.nodes, tc.edges)
		got, err := Walk(g, tc.start, tc.dir)
		if err != nil {
			t.Errorf("%s: unexpected error: %v", tc.name, err)
			continue
		}
		if !slices.Equal(got, tc.want) {
			t.Errorf("%s: Walk()=%v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestWalkErrors(t *testing.T) {
	g := build(t, []string{"A"}, nil)
	if _, err := Walk(g, "X", Out); err == nil {
		t.Error("missing start: want error, got nil")
	}
	if _, err := Walk(g, "A", Direction(99)); err == nil {
		t.Error("invalid direction: want error, got nil")
	}
}

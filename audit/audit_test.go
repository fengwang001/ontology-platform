package audit

import (
	"fmt"
	"testing"

	"ontology/graph"
)

func TestCheckSplits(t *testing.T) {
	cases := []struct {
		name   string
		build  func() *graph.Graph
		start  string
		budget int
	}{
		{"diamond with cycle", func() *graph.Graph {
			g := graph.New()
			for _, e := range [][2]string{{"a", "b"}, {"a", "c"}, {"b", "d"}, {"c", "d"}, {"d", "e"}, {"e", "b"}} {
				g.AddEdge(e[0], e[1])
			}
			return g
		}, "a", 5},
		{"chain", func() *graph.Graph {
			g := graph.New()
			for i := 0; i < 7; i++ {
				g.AddEdge(fmt.Sprintf("n%d", i), fmt.Sprintf("n%d", i+1))
			}
			return g
		}, "n0", 8},
		{"star", func() *graph.Graph {
			g := graph.New()
			for i := 0; i < 50; i++ {
				g.AddEdge("hub", fmt.Sprintf("leaf%02d", i))
			}
			return g
		}, "hub", 30},
		{"budget beyond graph", func() *graph.Graph {
			g := graph.New()
			g.AddEdge("a", "b")
			return g
		}, "a", 10},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := CheckSplits(tc.build(), tc.start, tc.budget); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestCheckSplitsPropagatesStartError(t *testing.T) {
	if err := CheckSplits(graph.New(), "ghost", 3); err == nil {
		t.Fatal("missing start must surface as an error")
	}
}

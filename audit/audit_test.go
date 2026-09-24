package audit

import (
	"fmt"
	"slices"
	"testing"

	"ontology/graph"
)

func sampleGraphs() map[string]*graph.Graph {
	chain := graph.New()
	for i := 0; i < 9; i++ {
		chain.AddEdge(fmt.Sprintf("n%d", i), fmt.Sprintf("n%d", i+1))
	}
	dense := graph.New()
	for _, e := range [][2]string{
		{"a", "b"}, {"a", "c"}, {"a", "d"}, {"b", "c"}, {"b", "e"},
		{"c", "d"}, {"c", "e"}, {"d", "a"}, {"d", "e"}, {"e", "e"},
	} {
		dense.AddEdge(e[0], e[1])
	}
	return map[string]*graph.Graph{"chain": chain, "dense": dense}
}

func TestEquivalentAllSplitPoints(t *testing.T) {
	cases := []struct {
		graph, start string
		budget       int
	}{
		{"chain", "n0", 10},
		{"dense", "a", 5},
		{"dense", "a", 100}, // budget beyond reachable: splits still agree
	}
	graphs := sampleGraphs()
	for _, tc := range cases {
		if err := Equivalent(graphs[tc.graph], tc.start, tc.budget); err != nil {
			t.Errorf("%v: %v", tc, err)
		}
	}
}

func TestRunSplitMatchesRunOnceElementWise(t *testing.T) {
	g := sampleGraphs()["dense"]
	once, err := RunOnce(g, "a", 5)
	if err != nil {
		t.Fatal(err)
	}
	for a := 1; a < 5; a++ {
		joined, err := RunSplit(g, "a", 5, a)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(joined, once) {
			t.Fatalf("split %d: got %v, want %v", a, joined, once)
		}
	}
}

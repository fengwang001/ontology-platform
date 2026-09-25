package traverse

import (
	"reflect"
	"testing"

	"ontology/graph"
)

func buildCycleGraph() *graph.Graph {
	g := graph.New()
	for _, n := range []string{"A", "B", "C", "D"} {
		_ = g.AddNode(n)
	}
	_ = g.AddEdge("A", "B")
	_ = g.AddEdge("B", "C")
	_ = g.AddEdge("C", "A")
	_ = g.AddEdge("A", "D")
	return g
}

func TestWalk(t *testing.T) {
	g := buildCycleGraph()
	_ = g.AddNode("Solo")
	tests := []struct {
		name  string
		start string
		dir   Dir
		want  []string
	}{
		{"out from A", "A", DirOut, []string{"A", "B", "D", "C"}},
		{"out from C closes cycle once", "C", DirOut, []string{"C", "A", "B", "D"}},
		{"in from A", "A", DirIn, []string{"A", "C", "B"}},
		{"in from D", "D", DirIn, []string{"D", "A", "C", "B"}},
		{"both from D reaches whole graph", "D", DirBoth, []string{"D", "A", "B", "C"}},
		{"isolated node", "Solo", DirBoth, []string{"Solo"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Walk(g, tc.start, tc.dir)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			if dup := firstDuplicate(got); dup != "" {
				t.Fatalf("node %q visited twice", dup)
			}
		})
	}
}

func TestWalkErrors(t *testing.T) {
	g := buildCycleGraph()
	if _, err := Walk(g, "Z", DirOut); err != ErrStartNotFound {
		t.Fatalf("missing start: got %v", err)
	}
	if _, err := Walk(g, "A", Dir(0)); err != ErrInvalidDir {
		t.Fatalf("invalid dir: got %v", err)
	}
}

func firstDuplicate(nodes []string) string {
	seen := map[string]bool{}
	for _, n := range nodes {
		if seen[n] {
			return n
		}
		seen[n] = true
	}
	return ""
}

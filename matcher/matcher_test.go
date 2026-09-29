package matcher

import (
	"fmt"
	"sync"
	"testing"

	"ontology/graph"
)

type testLogger struct {
	mu    sync.Mutex
	lines []string
}

func (l *testLogger) Printf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, fmt.Sprintf(format, args...))
}

func (l *testLogger) contains(sub string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, line := range l.lines {
		if containsSub(line, sub) {
			return true
		}
	}
	return false
}

func containsSub(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func buildDiamondGraph(t *testing.T, edgeOrder [][3]string) *graph.Graph {
	t.Helper()
	g := graph.New()
	mustAdd := func(o graph.Object) {
		t.Helper()
		if err := g.AddObject(o); err != nil {
			t.Fatalf("add object %s: %v", o.ID, err)
		}
	}
	mustAdd(graph.Object{ID: "p1", Type: "Person",
		Attributes: map[string]any{"age": 30, "name": "alice"}})
	mustAdd(graph.Object{ID: "p2", Type: "Person",
		Attributes: map[string]any{"age": 25, "name": "bob"}})
	mustAdd(graph.Object{ID: "c1", Type: "Company",
		Attributes: map[string]any{"size": 100}})
	mustAdd(graph.Object{ID: "c2", Type: "Company",
		Attributes: map[string]any{"size": 5}})
	for _, edge := range edgeOrder {
		if err := g.AddLink(graph.Link{Type: edge[0], Source: edge[1], Target: edge[2]}); err != nil {
			t.Fatalf("add link %v: %v", edge, err)
		}
	}
	return g
}

func diamondEdges() [][3]string {
	return [][3]string{
		{"worksAt", "p1", "c1"},
		{"worksAt", "p2", "c1"},
		{"knows", "p1", "p2"},
	}
}

func assertMatches(t *testing.T, got, want []Match) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("matches count = %d, want %d\ngot=%v\nwant=%v", len(got), len(want), got, want)
	}
	for i := range want {
		if !sameBinding(got[i], want[i]) {
			t.Fatalf("match %d = %v, want %v", i, got[i], want[i])
		}
	}
}

func sameBinding(a, b Match) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

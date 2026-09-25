package graph

import (
	"errors"
	"fmt"
	"testing"
)

func mustNode(t *testing.T, g *Graph, name string) {
	t.Helper()
	if err := g.AddNode(name); err != nil {
		t.Fatalf("AddNode(%s): %v", name, err)
	}
}

func mustEdge(t *testing.T, g *Graph, from, to string) {
	t.Helper()
	if err := g.AddEdge(from, to); err != nil {
		t.Fatalf("AddEdge(%s,%s): %v", from, to, err)
	}
}

func TestAddValidation(t *testing.T) {
	g := New()
	mustNode(t, g, "a")
	mustNode(t, g, "b")

	cases := []struct {
		name string
		err  error
		fn   func() error
	}{
		{"dup node", ErrNodeExists, func() error { return g.AddNode("a") }},
		{"self loop", ErrSelfLoop, func() error { return g.AddEdge("a", "a") }},
		{"from missing", ErrNodeNotFound, func() error { return g.AddEdge("x", "a") }},
		{"to missing", ErrNodeNotFound, func() error { return g.AddEdge("a", "x") }},
	}
	for _, tc := range cases {
		if err := tc.fn(); !errors.Is(err, tc.err) {
			t.Errorf("%s: got %v, want %v", tc.name, err, tc.err)
		}
	}
	mustEdge(t, g, "a", "b")
	if err := g.AddEdge("a", "b"); !errors.Is(err, ErrEdgeExists) {
		t.Errorf("dup edge: got %v, want %v", err, ErrEdgeExists)
	}
}

func TestHasCycle(t *testing.T) {
	cases := []struct {
		name  string
		nodes []string
		edges [][2]string
		want  bool
	}{
		{"diamond no cycle", []string{"a", "b", "c", "d"},
			[][2]string{{"a", "b"}, {"a", "c"}, {"b", "d"}, {"c", "d"}}, false},
		{"true cycle", []string{"a", "b", "c"},
			[][2]string{{"a", "b"}, {"b", "c"}, {"c", "a"}}, true},
		{"chain no cycle", []string{"a", "b", "c"},
			[][2]string{{"a", "b"}, {"b", "c"}}, false},
		{"disconnected cycle", []string{"a", "b", "x", "y"},
			[][2]string{{"a", "b"}, {"x", "y"}, {"y", "x"}}, true},
		{"single node", []string{"a"}, nil, false},
	}
	for _, tc := range cases {
		g := New()
		for _, n := range tc.nodes {
			mustNode(t, g, n)
		}
		for _, e := range tc.edges {
			mustEdge(t, g, e[0], e[1])
		}
		if got := g.HasCycle(); got != tc.want {
			t.Errorf("%s: HasCycle=%v, want %v", tc.name, got, tc.want)
		}
	}
}

// O(V+E) 不变量：节点访问 + 边考察总次数 ≤ V+E，V=100 与 V=10000 两档。
func TestHasCycleLinear(t *testing.T) {
	for _, v := range []int{100, 10000} {
		g := New()
		for i := 0; i < v; i++ {
			mustNode(t, g, fmt.Sprintf("n%d", i))
		}
		for i := 0; i+1 < v; i++ { // 长链 + 末端回边成环
			mustEdge(t, g, fmt.Sprintf("n%d", i), fmt.Sprintf("n%d", i+1))
		}
		mustEdge(t, g, fmt.Sprintf("n%d", v-1), "n0")
		nodes, edges := g.Size()
		if !g.HasCycle() {
			t.Fatalf("V=%d: expected cycle", v)
		}
		if g.Examined() > nodes+edges {
			t.Errorf("V=%d: examined %d > V+E %d", v, g.Examined(), nodes+edges)
		}
	}
}

package graph

import (
	"fmt"
	"testing"
)

func mustGraph(t *testing.T, nodes []string, edges [][2]string) *Graph {
	t.Helper()
	g := New()
	for _, n := range nodes {
		if err := g.AddNode(n); err != nil {
			t.Fatalf("AddNode(%q): %v", n, err)
		}
	}
	for _, e := range edges {
		if err := g.AddEdge(e[0], e[1]); err != nil {
			t.Fatalf("AddEdge(%q,%q): %v", e[0], e[1], err)
		}
	}
	return g
}

func TestAddValidation(t *testing.T) {
	g := mustGraph(t, []string{"A", "B"}, nil)
	cases := []struct {
		name string
		add  func() error
	}{
		{"empty node name", func() error { return g.AddNode("") }},
		{"duplicate node", func() error { return g.AddNode("A") }},
		{"edge from missing node", func() error { return g.AddEdge("X", "A") }},
		{"edge to missing node", func() error { return g.AddEdge("A", "X") }},
		{"self-loop", func() error { return g.AddEdge("A", "A") }},
	}
	for _, tc := range cases {
		if err := tc.add(); err == nil {
			t.Errorf("%s: want error, got nil", tc.name)
		}
	}
	if err := g.AddEdge("A", "B"); err != nil {
		t.Fatalf("AddEdge(A,B): %v", err)
	}
	if err := g.AddEdge("A", "B"); err == nil {
		t.Error("duplicate edge: want error, got nil")
	}
}

func TestHasCycle(t *testing.T) {
	cases := []struct {
		name  string
		nodes []string
		edges [][2]string
		want  bool
	}{
		{"diamond is not a cycle", []string{"A", "B", "C", "D"},
			[][2]string{{"A", "B"}, {"A", "C"}, {"B", "D"}, {"C", "D"}}, false},
		{"true cycle", []string{"A", "B", "C"},
			[][2]string{{"A", "B"}, {"B", "C"}, {"C", "A"}}, true},
		{"chain", []string{"A", "B", "C"},
			[][2]string{{"A", "B"}, {"B", "C"}}, false},
		{"cycle in disconnected component", []string{"A", "B", "C", "D"},
			[][2]string{{"A", "B"}, {"C", "D"}, {"D", "C"}}, true},
		{"two-path revisit after cycle-free join", []string{"A", "B", "C", "D", "E"},
			[][2]string{{"A", "B"}, {"A", "C"}, {"B", "D"}, {"C", "D"}, {"D", "E"}}, false},
	}
	for _, tc := range cases {
		g := mustGraph(t, tc.nodes, tc.edges)
		if got := g.HasCycle(); got != tc.want {
			t.Errorf("%s: HasCycle()=%v, want %v", tc.name, got, tc.want)
		}
	}
}

// 不变量：边考察次数 <= V + E（O(V+E)），对 V=100 与 V=10000 两档验证。
func TestHasCycleComplexity(t *testing.T) {
	for _, v := range []int{100, 10000} {
		for _, cyclic := range []bool{false, true} {
			g := New()
			for i := 0; i < v; i++ {
				if err := g.AddNode(fmt.Sprintf("n%d", i)); err != nil {
					t.Fatal(err)
				}
			}
			e := 0
			edge := func(a, b int) {
				if err := g.AddEdge(fmt.Sprintf("n%d", a), fmt.Sprintf("n%d", b)); err != nil {
					t.Fatal(err)
				}
				e++
			}
			for i := 0; i+1 < v; i++ {
				edge(i, i+1) // 链
			}
			for i := 0; i+2 < v; i += 2 {
				edge(i, i+2) // 菱形汇合边，制造大量「指向黑节点」的边
			}
			if cyclic {
				edge(v-1, 0)
			}
			if got := g.HasCycle(); got != cyclic {
				t.Fatalf("V=%d cyclic=%v: HasCycle()=%v", v, cyclic, got)
			}
			if g.edgeExams > v+e {
				t.Errorf("V=%d cyclic=%v: edgeExams=%d > V+E=%d", v, cyclic, g.edgeExams, v+e)
			}
		}
	}
}

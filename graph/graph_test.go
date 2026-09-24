package graph_test

import (
	"slices"
	"testing"

	"ontology/graph"
)

// 构造表：每组边乱序给出，断言邻接表读出时按字典序且保留重复边。
func TestEdgesSortedWithDuplicates(t *testing.T) {
	cases := []struct {
		name  string
		edges [][2]string
		node  string
		want  []string
	}{
		{"乱序插入", [][2]string{{"a", "c"}, {"a", "b"}, {"a", "a"}, {"a", "b"}}, "a", []string{"a", "b", "b", "c"}},
		{"无出边节点", [][2]string{{"x", "y"}}, "y", nil},
		{"空图", nil, "ghost", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := graph.New()
			for _, e := range tc.edges {
				g.AddEdge(e[0], e[1])
			}
			got, ok := g.Edges(tc.node)
			if !slices.Equal(got, tc.want) {
				t.Fatalf("Edges(%q) = %v, want %v", tc.node, got, tc.want)
			}
			if ok != g.Has(tc.node) {
				t.Fatalf("Edges ok=%v 与 Has=%v 不一致", ok, g.Has(tc.node))
			}
		})
	}
}

func TestNodesAndSize(t *testing.T) {
	cases := []struct {
		name  string
		edges [][2]string
		nodes []string
		want  []string
	}{
		{"端点自动注册", [][2]string{{"b", "c"}, {"a", "c"}}, nil, []string{"a", "b", "c"}},
		{"孤立节点保留", [][2]string{{"a", "b"}}, []string{"z"}, []string{"a", "b", "z"}},
		{"重复注册幂等", nil, []string{"n", "n"}, []string{"n"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := graph.New()
			for _, e := range tc.edges {
				g.AddEdge(e[0], e[1])
			}
			for _, n := range tc.nodes {
				g.AddNode(n)
			}
			if got := g.Nodes(); !slices.Equal(got, tc.want) {
				t.Fatalf("Nodes() = %v, want %v", got, tc.want)
			}
			if g.Size() != len(tc.want) {
				t.Fatalf("Size() = %d, want %d", g.Size(), len(tc.want))
			}
		})
	}
}

func TestRemoveNode(t *testing.T) {
	g := graph.New()
	g.AddEdge("a", "b")
	g.AddEdge("b", "c")
	g.RemoveNode("b")
	if g.Has("b") {
		t.Fatal("RemoveNode 后 Has(b) 仍为 true")
	}
	if _, ok := g.Edges("b"); ok {
		t.Fatal("RemoveNode 后 Edges(b) 仍存在")
	}
	// 悬挂边：a 仍列出已删除的 b，由 walk 包在访问时报错。
	if outs, _ := g.Edges("a"); !slices.Equal(outs, []string{"b"}) {
		t.Fatalf("a 的出边 = %v, want [b]", outs)
	}
	if g.Size() != 2 {
		t.Fatalf("Size() = %d, want 2", g.Size())
	}
}

func TestDegree(t *testing.T) {
	g := graph.New()
	g.AddEdge("a", "b")
	g.AddEdge("a", "b")
	g.AddEdge("a", "c")
	if got := g.Degree("a"); got != 3 {
		t.Fatalf("Degree(a) = %d, want 3（重复边计入）", got)
	}
	if got := g.Degree("ghost"); got != 0 {
		t.Fatalf("Degree(ghost) = %d, want 0", got)
	}
}

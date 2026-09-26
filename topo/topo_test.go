package topo

import (
	"testing"

	"ontology/dag"
)

// TestCheckedBounded 证明选节点用堆而非全表扫描：m 个互不相连的节点，
// 每步选出下一节点时检查的候选个数不随 m 线性增长（≤2）。
func TestCheckedBounded(t *testing.T) {
	for _, m := range []int{100, 500, 1000, 5000, 10000} {
		s := &Sorter{}
		out, err := s.Sort(dag.New(m))
		if err != nil || len(out) != m {
			t.Fatalf("m=%d: sort failed: %v", m, err)
		}
		if s.checked > 2 {
			t.Fatalf("m=%d: checked %d candidates, grows with m", m, s.checked)
		}
	}
}

// TestSevenEdgeOrder 钉住第三节推导出的唯一拓扑序。
func TestSevenEdgeOrder(t *testing.T) {
	g := dag.New(7)
	for _, e := range [][2]int{{2, 0}, {2, 1}, {4, 1}, {3, 1}, {5, 6}, {0, 6}, {1, 6}} {
		if err := g.AddEdge(e[0], e[1]); err != nil {
			t.Fatal(err)
		}
	}
	out, err := new(Sorter).Sort(g)
	if err != nil {
		t.Fatal(err)
	}
	want := []int{2, 0, 3, 4, 1, 5, 6}
	for i := range want {
		if out[i] != want[i] {
			t.Fatalf("got %v want %v", out, want)
		}
	}
}

// TestCycleFailsWhole 含环必须整体失败：返回 ErrCycle 且不产生部分序列。
func TestCycleFailsWhole(t *testing.T) {
	for _, edges := range [][][2]int{
		{{0, 1}, {1, 0}},                 // 二元环
		{{0, 1}, {1, 2}, {2, 0}},         // 三元环
		{{2, 0}, {0, 6}, {5, 6}, {0, 2}}, // 部分可排 + 环
	} {
		g := dag.New(7)
		for _, e := range edges {
			if err := g.AddEdge(e[0], e[1]); err != nil {
				t.Fatal(err)
			}
		}
		out, err := new(Sorter).Sort(g)
		if err != dag.ErrCycle || out != nil {
			t.Fatalf("edges=%v: got %v %v, want nil ErrCycle", edges, out, err)
		}
	}
}

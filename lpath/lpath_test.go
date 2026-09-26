package lpath

import (
	"testing"

	"ontology/wdag"
)

// TestPredChecksIndependentOfM 证明 dist 按入度枚举直接前驱（邻接表），
// 而不是每次扫描全部 m 个节点：检查个数不超过入度加一个与 m 无关的小常数。
func TestPredChecksIndependentOfM(t *testing.T) {
	for _, m := range []int{100, 500, 2000, 10000} {
		g, err := wdag.New(m)
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i+1 < m; i++ { // 链 i→i+1：拓扑序唯一，末节点只有 1 个直接前驱
			if err := g.AddEdge(i, i+1, 1); err != nil {
				t.Fatal(err)
			}
		}
		s := New(g)
		total, path, err := s.Solve()
		if err != nil {
			t.Fatalf("m=%d: %v", m, err)
		}
		if total != int64(m-1) || len(path) != m {
			t.Fatalf("m=%d: total=%d len(path)=%d", m, total, len(path))
		}
		if indeg := len(g.InEdges(m - 1)); s.lastChecks > indeg+2 {
			t.Fatalf("m=%d: 检查个数 %d 超过入度 %d 加小常数", m, s.lastChecks, indeg)
		}
	}
}

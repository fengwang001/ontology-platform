package lpath

import (
	"testing"

	"ontology/wdag"
)

// 目标节点只有 1 个直接前驱时，为求其 dist 枚举前驱的检查个数
// 不随总节点数 m 增长（不超过入度加一个与 m 无关的小常数），
// 证明按邻接表枚举前驱而不是每次扫描全部 m 个节点。
func TestPredChecksIndependentOfM(t *testing.T) {
	for _, m := range []int{100, 300, 1000, 3000, 10000} {
		g, err := wdag.New(m)
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i+1 < m; i++ { // 链：目标节点 m-1 只有 1 个直接前驱
			if err := g.AddEdge(i, i+1, 1); err != nil {
				t.Fatal(err)
			}
		}
		s := NewSolver()
		if _, _, err := s.Solve(g); err != nil {
			t.Fatalf("m=%d: %v", m, err)
		}
		// 拓扑序最后处理的是目标节点 m-1，入度 1
		if s.lastChecks > 2 {
			t.Fatalf("m=%d: lastChecks=%d, want <= indegree(1)+const", m, s.lastChecks)
		}
	}
}

package tc

import (
	"testing"

	"ontology/dg"
)

// TestReachChecksConstantNodes 证明 Reach 是 O(1)：
// 对不同规模 m 的图，Compute 后每次 Reach 查询检查的节点个数
// 不随 m 增长（恒 ≤1），而不是每次从 i 做一遍 O(m) 的 DFS。
func TestReachChecksConstantNodes(t *testing.T) {
	for _, m := range []int{100, 300, 1000, 3000, 10000} {
		g, err := dg.New(m)
		if err != nil {
			t.Fatal(err)
		}
		// 星形加边 0→i：节点 0 可达全部 m-1 个节点，
		// 若 Reach 每次从 i 做 DFS 要扫 O(m) 个节点。
		for i := 1; i < m; i++ {
			if err := g.AddEdge(0, i); err != nil {
				t.Fatal(err)
			}
		}
		c := Compute(g)
		for k := 0; k < 1000; k++ {
			i, j := 0, (k*13+5)%m // 从 0 出发：DFS 实现要扫满 O(m) 个节点
			c.Reach(i, j)
			if got := c.lastChecked.Load(); got > 1 {
				t.Fatalf("m=%d: Reach(%d,%d) checked %d nodes, want <=1", m, i, j, got)
			}
		}
	}
}

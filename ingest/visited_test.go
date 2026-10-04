package ingest

import (
	"math"
	"testing"
)

// TestIngestVisitedBound 白盒证明：在 g 个缺口环境下，
// 一次按序到达或缺口内落点的 Ingest 对归宿区间集合的查找节点数
// 不超过 2⌈log2(g+2)⌉+4，g=10 与 g=10000 两档对照，不随 g 线性增长。
func TestIngestVisitedBound(t *testing.T) {
	for _, g := range []int{10, 10000} {
		g := g
		t.Run("", func(t *testing.T) {
			r := NewRegistry()
			if err := r.Register("d", Config{Lm: 1, K: 1, Tq: 1e9, R: 1}); err != nil {
				t.Fatal(err)
			}
			// 构造 g 个“已收点 + 缺口”：在偶数位置收到，hi 推到 2g。
			d := r.devs["d"]
			hi := int64(2 * g)
			for i := 0; i < g; i++ {
				seq := int64(2*i + 1)
				if err := r.Ingest("d", seq, 0); err != nil {
					t.Fatal(err)
				}
			}
			if err := r.Ingest("d", hi, 0); err != nil {
				t.Fatal(err)
			}
			bound := 2*int(math.Ceil(math.Log2(float64(g+2)))) + 4
			worst := 0
			// 命中已收点（按序/落点）与落入缺口两类各探测。
			for i := 0; i < g; i++ {
				hit := int64(2*i + 1)
				miss := int64(2*i + 2)
				d.recv.Contains(hit)
				if v := d.recv.Visited(); v > worst {
					worst = v
				}
				d.recv.Contains(miss)
				if v := d.recv.Visited(); v > worst {
					worst = v
				}
			}
			t.Logf("g=%d 归宿段数=%d worst visited=%d bound=%d", g, d.recv.Segments(), worst, bound)
			if worst > bound {
				t.Fatalf("g=%d visited=%d 超过界 %d", g, worst, bound)
			}
			if g == 10000 && worst >= g {
				t.Fatalf("visited 随 g 线性增长: %d", worst)
			}
		})
	}
}

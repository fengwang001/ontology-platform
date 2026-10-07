package cardinality

import (
	"fmt"
	"testing"
)

// TestPendingCountIndependentOfHistory 待处理数量的查询与统计
// 不得随历史调整次数增长：制造 200 次反复调整但最终无待处理，
// 再制造大量调整但保留固定数量待处理，计数必须恒等于当前待处理集合大小。
func TestPendingCountIndependentOfHistory(t *testing.T) {
	e := NewEngine(nil)
	k := keyOf("owns", "H")
	createN(t, e, "H", 10)

	wantCurrent := func(want int) {
		t.Helper()
		if got := e.PendingCount(k); got != want {
			t.Fatalf("PendingCount=%d want %d", got, want)
		}
		if got := len(e.PendingLinks(k)); got != want {
			t.Fatalf("len(PendingLinks)=%d want %d", got, want)
		}
	}

	// 100 次下调/上调震荡，最终恢复到 10：待处理为 0。
	for i := 0; i < 100; i++ {
		e.SetLimit(k, 3)
		e.SetLimit(k, 10)
	}
	wantCurrent(0)

	// 再震荡 100 次但停在下限：待处理恒为 7（与调整 200 次还是 1 次无关）。
	for i := 0; i < 100; i++ {
		e.SetLimit(k, 8)
		e.SetLimit(k, 3)
	}
	wantCurrent(7)

	// 处置删除两条，计数立即下降，与此前的全部历史无关。
	pending := pendingIDs(e.PendingLinks(k))
	if _, err := e.Finalize(pending[0], DispositionDelete); err != nil {
		t.Fatal(err)
	}
	wantCurrent(6)
}

// BenchmarkPendingCount 证明计数查询是对桶内增量字段的 O(1) 读取：
// 调整 2000 次与调整 2 次后，单次查询耗时没有结构性差异
// （可用 benchstat 对照两个子结果，ns/op 应处于同一量级且无增长趋势）。
func BenchmarkPendingCount(b *testing.B) {
	measure := func(b *testing.B, adjustments int) {
		e := NewEngine(nil)
		k := keyOf("owns", "B")
		for i := 0; i < 50; i++ {
			id := fmt.Sprintf("B-L%02d", i)
			if r := e.CreateLink("owns", "B", fmt.Sprintf("T-%d", i), id); !r.Accepted {
				b.Fatal(r.RejectReason)
			}
		}
		for i := 0; i < adjustments; i++ {
			if i%2 == 0 {
				e.SetLimit(k, 10)
			} else {
				e.SetLimit(k, 50)
			}
		}
		b.ResetTimer()
		var sink int
		for i := 0; i < b.N; i++ {
			sink = e.PendingCount(k)
		}
		_ = sink
	}

	b.Run("2_adjustments", func(b *testing.B) { measure(b, 2) })
	b.Run("2000_adjustments", func(b *testing.B) { measure(b, 2000) })
}

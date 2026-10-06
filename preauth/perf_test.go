package preauth

import (
	"fmt"
	"testing"
)

// TestQueryComplexityIndependentOfHistory 用可验证的方式证明：
// 查询可用额度的开销不随该账户历史授权总数增长。
//
// 做法：先制造 N 笔“已过期且已终结”的历史授权（它们在被接受操作折叠时
// 已永久出堆），再测量只查询当前时刻可用额度的耗时；分别对 N=2k/8k/32k
// 取值。已折叠历史不残留堆条目，故查询耗时应基本恒定（允许 3 倍抖动）。
func TestQueryComplexityIndependentOfHistory(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping complexity probe in -short mode")
	}
	measure := func(n int) (ns float64, heapLen int) {
		s := NewSystem(Config{ValidityDays: 1, ToleranceBPS: 0})
		mustOK(t, s.CreateAccount("C", 1_000_000_000, 0), "create")
		// 每笔授权在 day 2k 创建（有效期 1 天，截止日 2k），day 2k+1 用一次
		// “被接受的操作”推进时间，使其折叠出堆。用小额捕获制造入账历史。
		for i := 0; i < n; i++ {
			d := int64(2 * i)
			id := fmt.Sprintf("h%d", i)
			mustOK(t, s.Authorize(id, "C", 1, d), "auth")
			// 在下一天查询会折叠，但查询不持久；这里用 Void（被接受操作）
			// 在截止日当天不行（会释放）。改为在下一天对“另一新授权”操作，
			// 间接触发本账户 foldPersist。
			mustOK(t, s.Authorize(fmt.Sprintf("h%db", i), "C", 1, d+1), "auth next day folds prior")
			// 立即撤销后一笔，使其也成为历史，避免堆积活跃持有。
			mustOK(t, s.Void(fmt.Sprintf("h%db", i), d+1), "void next-day")
		}
		now := int64(2*n + 5)
		res := testing.Benchmark(func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				if _, err := s.Available("C", now); err != nil {
					b.Fatal(err)
				}
			}
		})
		s.accounts["C"].mu.Lock()
		for _, e := range s.accounts["C"].expiry.entries {
			if !e.dead {
				heapLen++
			}
		}
		s.accounts["C"].mu.Unlock()
		return float64(res.T.Nanoseconds()) / float64(res.N), heapLen
	}

	ns2k, hl2k := measure(2000)
	ns8k, hl8k := measure(8000)
	ns32k, hl32k := measure(32000)
	t.Logf("N=2000  query=%8.1fns heap=%d", ns2k, hl2k)
	t.Logf("N=8000  query=%8.1fns heap=%d", ns8k, hl8k)
	t.Logf("N=32000 query=%8.1fns heap=%d", ns32k, hl32k)

	// 历史扩大 16 倍，每查询耗时增长必须远小于 16 倍（这里取 4 倍上界）。
	if ns32k > 4*ns2k && ns32k > 400 {
		t.Fatalf("query time scales with history: %v -> %v ns", ns2k, ns32k)
	}
	// 已折叠历史不应残留任何“活”条目（dead 是撤销/替换的惰性条目，不计持有）。
	if hl2k != 0 || hl8k != 0 || hl32k != 0 {
		t.Fatalf("expired history leaked into heap: %d %d %d", hl2k, hl8k, hl32k)
	}
}

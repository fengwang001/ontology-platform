package sorting

import (
	"fmt"
	"testing"
)

// BenchmarkAddParcelByHistorySize 验证加入快件的判定开销
// 不随场内集袋总数、历史快件总数增长：
// 先在目标网点以外的网点沉淀 history 件历史快件（分布于大量已封存集袋），
// 再测量向热点网点持续加入快件的稳态耗时。
//
// 运行：go test -run=NONE -bench=BenchmarkAddParcelByHistorySize ./sorting
// 预期：各历史规模下 ns/op 基本持平（map 查找 + 聚合计数，均为 O(1)）。
func BenchmarkAddParcelByHistorySize(b *testing.B) {
	for _, history := range []int{1_000, 10_000, 100_000, 1_000_000} {
		b.Run(fmt.Sprintf("history=%d", history), func(b *testing.B) {
			cfg := Config{MaxBagCount: 100, MaxBagWeight: 1_000_000, DwellLimit: 1 << 60}
			h, err := NewHub(cfg)
			if err != nil {
				b.Fatalf("NewHub: %v", err)
			}
			// 沉淀历史：历史快件分布在大量已封存集袋中。
			for i := 0; i < history; i++ {
				site := fmt.Sprintf("HIST-%d", i%1000)
				if _, err := h.AddParcel(Parcel{
					Waybill:  fmt.Sprintf("HIST-WB-%d", i),
					Site:     site,
					Weight:   1,
					Category: CategoryNormal,
				}, int64(i)); err != nil {
					b.Fatalf("沉淀历史失败: %v", err)
				}
			}
			// 热点网点：单袋 100 件即换袋，持续产生新袋。
			now := int64(history)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				now++
				if _, err := h.AddParcel(Parcel{
					Waybill:  fmt.Sprintf("HOT-WB-%d", i),
					Site:     "HOT",
					Weight:   1,
					Category: CategoryNormal,
				}, now); err != nil {
					b.Fatalf("AddParcel: %v", err)
				}
			}
		})
	}
}

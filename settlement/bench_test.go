package settlement_test

import (
	"fmt"
	"testing"

	"ontology/settlement"
)

// BenchmarkDayCost 证明单日批处理开销只与当日候选指令数相关：
// 对“历史已了结指令量”分别为 N 和 10N 的系统，同一候选规模下单日耗时应同阶。
// 运行：go test -bench=BenchmarkDayCost -benchtime=100x ./settlement
func BenchmarkDayCost(b *testing.B) {
	for _, closed := range []int{1000, 10000} {
		for _, open := range []int{50, 200} {
			name := fmt.Sprintf("closed=%d/open=%d", closed, open)
			b.Run(name, func(b *testing.B) {
				b.StopTimer()
				for n := 0; n < b.N; n++ {
					s := mustSys(b, []int64{1, 2}, 100000, 100)
					mustAdd(b, s, "B", nil, 1_000_000_000)
					mustAdd(b, s, "S", map[int64]int64{10: 1_000_000_000}, 0)
					for i := 0; i < closed; i++ {
						mustReg(b, s, settlement.Order{ID: int64(i + 1), Security: 10,
							Buyer: "B", Seller: "S", Qty: 1, Price: 1, SettleDay: 1})
					}
					if err := s.RunBatch(1, map[int64]int64{10: 1}); err != nil {
						b.Fatal(err)
					}
					base := int64(closed + 1)
					for i := 0; i < open; i++ {
						mustReg(b, s, settlement.Order{ID: base + int64(i), Security: 10,
							Buyer: "B", Seller: "S", Qty: 1, Price: 1, SettleDay: 2})
					}
					b.StartTimer()
					if err := s.RunBatch(2, map[int64]int64{10: 1}); err != nil {
						b.Fatal(err)
					}
					b.StopTimer()
				}
			})
		}
	}
}

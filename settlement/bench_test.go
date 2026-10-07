package settlement_test

import (
	"fmt"
	"testing"

	"ontology/settlement"
)

// BenchmarkSettleSteadyState 证明每个营业日的结算开销与历史流水总数、
// 历史批次总数无关：先构建 1k/10k/100k 个营业日的历史（每日 3 笔流水、
// 每日留存并到期释放一批保证金），再测量稳态下单日结算的耗时。
// 若实现退化为随历史增长，三档 ns/op 会显著拉开；预期三者基本持平。
func BenchmarkSettleSteadyState(b *testing.B) {
	cfg := settlement.Config{DelayDays: 1, ReserveBps: 1000, HorizonDays: 5}
	for _, history := range []int{1_000, 10_000, 100_000} {
		b.Run(fmt.Sprintf("history_days=%d", history), func(b *testing.B) {
			total := history + b.N + 16
			cal := contiguousCal(1, int64(total))
			e := settlement.NewEngine(cal)
			if err := e.AddMerchant(1, "m", cfg); err != nil {
				b.Fatal(err)
			}
			// 构建历史：每日 3 笔流水并逐日结算。
			seq := 0
			for day := int64(1); day <= int64(history); day++ {
				for k := 0; k < 3; k++ {
					id := fmt.Sprintf("h%d-%d", day, k)
					if err := e.PostTransaction(day, "m", id, day, 1000+int64(k)); err != nil {
						b.Fatal(err)
					}
					seq++
				}
				if _, err := e.Settle(day, "m", day); err != nil {
					b.Fatal(err)
				}
			}
			snap, err := e.Snapshot("m")
			if err != nil {
				b.Fatal(err)
			}
			b.Logf("历史构建完成: days=%d settledTxSum=%d activeBatches=%d",
				history, snap.SettledTxSum, len(snap.Batches))

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				day := int64(history + i + 1)
				for k := 0; k < 3; k++ {
					id := fmt.Sprintf("b%d-%d", i, k)
					if err := e.PostTransaction(day, "m", id, day, 1000+int64(k)); err != nil {
						b.Fatal(err)
					}
				}
				if _, err := e.Settle(day, "m", day); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

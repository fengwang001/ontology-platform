package microgrid

import (
	"fmt"
	"testing"
)

// 验证登记实际值的开销不随历史已执行时隙数增长：
// 在不同历史长度下保持恒定数量的已接受时隙，单次登记耗时应基本持平。
func BenchmarkRecordActual(b *testing.B) {
	for _, history := range []int{0, 1000, 10000} {
		b.Run(fmt.Sprintf("history=%d", history), func(b *testing.B) {
			cfg := Config{
				Capacity:             1 << 30,
				MinSoC:               0,
				MaxSoC:               1 << 30,
				MaxChargePerSlot:     10,
				MaxDischargePerSlot:  10,
				MaintenanceThreshold: 1 << 30,
				DeviationTolerance:   0,
				InitialSoC:           1 << 29,
			}
			c, err := NewController(cfg)
			if err != nil {
				b.Fatal(err)
			}
			cur := 0
			for ; cur < history; cur++ {
				if _, err := c.RecordActual(cur, ActionIdle, 0); err != nil {
					b.Fatal(err)
				}
			}
			const tail = 64 // 恒定的已接受时隙窗口
			if _, err := c.UpdateForecast(cur+1, make([]int, tail+1)); err != nil {
				b.Fatal(err)
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := c.UpdateForecast(cur+tail, []int{0}); err != nil {
					b.Fatal(err)
				}
				if err := c.SubmitPlan(cur+tail, []PlanAction{{ActionIdle, 0}}); err != nil {
					b.Fatal(err)
				}
				if _, err := c.RecordActual(cur, ActionIdle, 0); err != nil {
					b.Fatal(err)
				}
				cur++
			}
		})
	}
}

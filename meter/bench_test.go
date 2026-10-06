package meter

import "testing"

// 在已有 history 条读数/事件的账户上追加一条读数，
// 对比不同 history 的 ns/op，验证不随历史规模增长。
func BenchmarkAddReadingHistory(b *testing.B) {
	for _, history := range []int64{100, 1000, 10000} {
		b.Run("h", func(b *testing.B) {
			cfg := baseCfg()
			cfg.CycleLength = 1 << 40
			c := New(cfg, 1<<50)
			var i int64
			for i = 1; i <= history; i++ {
				if _, err := c.AddReading(i, i*10); err != nil {
					b.Fatal(err)
				}
			}
			b.ResetTimer()
			for k := 0; k < b.N; k++ {
				if _, err := c.AddReading(history+int64(k)+1,
					(history+int64(k)+1)*10); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// 友好时段判定不随节假日数量增长。
func BenchmarkFriendlyLookup(b *testing.B) {
	for _, n := range []int{100, 10000, 1000000} {
		cfg := baseCfg()
		cfg.Holidays = make(map[int64]struct{}, n)
		for d := 0; d < n; d++ {
			cfg.Holidays[int64(d)] = struct{}{}
		}
		t := Tick(50*86400 + 3600)
		b.Run("h", func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				if !cfg.inFriendly(t) {
					b.Fatal("expected friendly")
				}
			}
		})
	}
}

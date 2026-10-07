package snapshot

import "testing"

// BenchmarkClassify 复核归属判定的工作量与数据总规模无关：
// 对任意规模的日志，单次判定都是一次整数比较。
// 运行：go test -bench=Classify -benchmem ./snapshot
func BenchmarkClassify(b *testing.B) {
	for _, size := range []int64{1_000, 1_000_000} {
		b.Run(itoa(uint64(size)), func(b *testing.B) {
			c := NewClassifier(LSN(size/2), nil)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				c.Classify(LSN(int64(i)%size) + 1)
			}
		})
	}
}

package meterengine

import "testing"

// BenchmarkRegisterAndQuery 验证：
// 在单表已有大量读数时，追加/替换一条读数与一次小区间查询
// 都只付出 O(log n) 与 O(k+log n) 的开销。
func BenchmarkRegisterAndQuery(b *testing.B) {
	for _, n := range []int{1000, 10000, 100000} {
		b.Run("n="+itoa(n), func(b *testing.B) {
			e := NewEngine()
			if err := e.SetPointLimit("p", 1<<40); err != nil {
				b.Fatal(err)
			}
			if err := e.RegisterMeter("m", 6, 1); err != nil {
				b.Fatal(err)
			}
			if err := e.InstallMeter("p", "m", 1, 0); err != nil {
				b.Fatal(err)
			}
			for i := 2; i <= n; i++ {
				if err := e.RegisterReading("m", int64(i), int64(i%900000), Actual); err != nil {
					b.Fatal(err)
				}
			}
			b.ResetTimer()
			b.Run("append", func(b *testing.B) {
				for i := 0; i < b.N; i++ {
					tm := int64(n + 1 + i)
					if err := e.RegisterReading("m", tm, tm%900000, Estimated); err != nil {
						b.Fatal(err)
					}
					if err := e.DeleteEstimated("m", tm); err != nil {
						b.Fatal(err)
					}
				}
			})
			b.Run("smallQuery", func(b *testing.B) {
				for i := 0; i < b.N; i++ {
					if _, err := e.Query("p", int64(n/2), int64(n/2+10)); err != nil {
						b.Fatal(err)
					}
				}
			})
		})
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [12]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

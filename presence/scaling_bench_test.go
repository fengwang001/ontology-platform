package presence

import "testing"

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(buf[pos:])
}

func mustNew(tb testing.TB, lease int64) *Service {
	tb.Helper()
	s, err := New(lease)
	if err != nil {
		tb.Fatal(err)
	}
	return s
}

// BenchmarkCoreReportScaling 在不触发通知 fanout 的前提下，度量系统内已有
// n 个在线用户（各自一台长期租约设备）时，对单个用户续租的稳态成本。
// 预期：1k -> 1M 用户下单次 Report 近乎常数，证明不随全体用户数增长。
func BenchmarkCoreReportScaling(b *testing.B) {
	for _, n := range []int{1000, 10000, 100000, 1000000} {
		b.Run("users="+itoa(n), func(b *testing.B) {
			s := mustNew(b, 3600)
			for i := 0; i < n; i++ {
				if err := s.Report("u"+itoa(i), "d", StatusOnline, 0); err != nil {
					b.Fatal(err)
				}
			}
			b.ResetTimer()
			clock := int64(1)
			for i := 0; i < b.N; i++ {
				clock++
				if err := s.Report("u"+itoa(i%n), "d", StatusOnline, clock); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkCoreDrainScaling 度量无到期、无积压时单个观察者 Drain 的成本，
// 与系统内订阅总数无关。
func BenchmarkCoreDrainScaling(b *testing.B) {
	for _, n := range []int{1000, 10000, 100000} {
		b.Run("subs="+itoa(n), func(b *testing.B) {
			s := mustNew(b, 3600)
			var clock int64
			for i := 0; i < n; i++ {
				target := "t" + itoa(i)
				viewer := "w" + itoa(i)
				clock++
				_ = s.Report(target, "d", StatusOnline, clock)
				clock++
				_ = s.Report(viewer, "d", StatusOnline, clock)
				clock++
				_ = s.Subscribe(viewer, target, clock)
			}
			b.ResetTimer()
			start := clock
			for i := 0; i < b.N; i++ {
				now := start + int64(i) + 1
				if _, err := s.Drain("w"+itoa(i%n), now); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkExpiryCost 验证一次操作触发 k 台设备到期时，额外成本只与 k 相关
// （这里度量到期处理本身；通过首次 Query 惰性触发）。
func BenchmarkExpiryCost(b *testing.B) {
	for _, k := range []int{1, 10, 100} {
		b.Run("expiring="+itoa(k), func(b *testing.B) {
			var built bool
			var s *Service
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if !built {
					b.StopTimer()
					s = mustNew(b, 10)
					for j := 0; j < k; j++ {
						if err := s.Report("exp"+itoa(j), "d", StatusOnline, 0); err != nil {
							b.Fatal(err)
						}
					}
					if _, err := s.Query("exp0", "exp0", 10); err != nil {
						b.Fatal(err)
					}
					built = true
					b.StartTimer()
				}
			}
		})
	}
}

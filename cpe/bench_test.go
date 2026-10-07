package cpe

import (
	"fmt"
	"testing"
	"time"
)

// 大周期配置：周期不会关闭，历史记录持续累积在同一周期内。
func benchCfg() Config {
	return Config{
		CycleLengthDays:      1 << 30,
		TotalRequired:        1 << 30,
		MandatoryMin:         0,
		MandatoryCap:         1 << 30,
		ElectiveCap:          1 << 30,
		GraceDays:            3,
		CarryoverCap:         10,
		CorrectionWindowDays: 1 << 30,
	}
}

func fillService(b testing.TB, s *Service, n int, startNow int) {
	b.Helper()
	for i := 0; i < n; i++ {
		now := startNow + i
		if _, err := s.RegisterCredit("h", Category(i%3), 1, now, "org", now); err != nil {
			b.Fatalf("fill: %v", err)
		}
	}
}

// 周期核算开销不得随持证人历史记录总量增长：
// 比较历史早期与晚期的单操作耗时，比率应接近 1（允许噪声余量）。
func TestAccountingCostIndependentOfHistory(t *testing.T) {
	if testing.Short() {
		t.Skip("timing test disabled in short mode")
	}
	s := newTestService(t, benchCfg())
	mustHolder(t, s, "h", 0, 0)
	const blocks = 8
	const perBlock = 5000
	avg := make([]time.Duration, 0, blocks)
	now := 1
	for b := 0; b < blocks; b++ {
		start := time.Now()
		for i := 0; i < perBlock; i++ {
			if _, err := s.RegisterCredit("h", Category(i%3), 1, now, "org", now); err != nil {
				t.Fatalf("register: %v", err)
			}
			now++
		}
		avg = append(avg, time.Since(start)/perBlock)
	}
	early := (avg[0] + avg[1]) / 2
	late := (avg[blocks-2] + avg[blocks-1]) / 2
	t.Logf("per-op cost: early=%v late=%v ratio=%.2f (history=%d records)",
		early, late, float64(late)/float64(early), blocks*perBlock)
	// 核算为 O(1) 增量聚合，比率应远小于随历史线性增长的水平；留足噪声余量。
	if late > early*5+2*time.Microsecond {
		t.Fatalf("per-op cost grows with history: early=%v late=%v", early, late)
	}
}

func BenchmarkRegisterCredit(b *testing.B) {
	s, err := NewService(benchCfg())
	if err != nil {
		b.Fatal(err)
	}
	if err := s.RegisterHolder("h", 0, 0); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		now := i + 1
		if _, err := s.RegisterCredit("h", Category(i%3), 1, now, "org", now); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkGetAccountingAt(b *testing.B) {
	s, err := NewService(benchCfg())
	if err != nil {
		b.Fatal(err)
	}
	if err := s.RegisterHolder("h", 0, 0); err != nil {
		b.Fatal(err)
	}
	fillService(b, s, 20000, 1)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := s.GetAccountingAt("h", (i%20000)+1); err != nil {
			b.Fatal(err)
		}
	}
}

// 证明核算不扫描历史：随着历史增长，单操作耗时的基准对照（供 -bench 验证）。
func BenchmarkRegisterCreditHistoryScaling(b *testing.B) {
	for _, prefill := range []int{0, 50000} {
		b.Run(fmt.Sprintf("prefill=%d", prefill), func(b *testing.B) {
			s, err := NewService(benchCfg())
			if err != nil {
				b.Fatal(err)
			}
			if err := s.RegisterHolder("h", 0, 0); err != nil {
				b.Fatal(err)
			}
			fillService(b, s, prefill, 1)
			now := prefill + 1
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := s.RegisterCredit("h", Category(i%3), 1, now, "org", now); err != nil {
					b.Fatal(err)
				}
				now++
			}
		})
	}
}

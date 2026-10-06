package hd

import (
	"fmt"
	"testing"
)

// BenchmarkBookNearby 在同一机位上预置 n 次互不相邻的历史治疗，
// 再在"末尾附近"做一次机位寻找（通过 BookTreatment 申请必然失败/成功的时间点），
// 对照小/大两档历史规模。若单次寻位复杂度与历史总量无关，两档耗时应基本持平。
func BenchmarkBookNearby(b *testing.B) {
	for _, n := range []int{1_000, 100_000} {
		b.Run(fmt.Sprintf("history=%d", n), func(b *testing.B) {
			s, _ := New(Config{CleanNegative: 10, CleanHBV: 10, CleanHCV: 10,
				DeepClean: 60, MinRecovery: 10})
			if err := s.RegisterBay(0, "G1", ZoneGeneral, false, true); err != nil {
				b.Fatal(err)
			}
			if err := s.RegisterPatient(0, "p", InfectionNegative); err != nil {
				b.Fatal(err)
			}
			// 每次治疗间隔 100 分钟（满足常规消毒与恢复），历史全部落在 G1 上。
			for i := 0; i < n; i++ {
				start := i * 100
				if _, err := s.BookTreatment(start, fmt.Sprintf("h%d", i), "p", start, 10); err != nil {
					b.Fatal(err)
				}
			}
			// 在最后一次治疗之后紧邻的时间点反复查询：用新患者避免恢复间隔，
			// 该时间点恰好需要深度/常规消毒比较，只触及末尾附近的治疗。
			if err := s.RegisterPatient(n*100, "q", InfectionNegative); err != nil {
				b.Fatal(err)
			}
			lastEnd := (n-1)*100 + 10
			b.ResetTimer()
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				// 早一分钟：只需查前驱即可拒绝，访问量与历史总量无关。
				_, _ = s.BookTreatment(n*100+i, fmt.Sprintf("probe%d", i), "q",
					lastEnd+9, 10)
			}
		})
	}
}

// BenchmarkNaiveScan 用朴素模型做同样的事作对照：其判定随历史线性增长。
func BenchmarkNaiveScan(b *testing.B) {
	for _, n := range []int{1_000, 10_000} {
		b.Run(fmt.Sprintf("history=%d", n), func(b *testing.B) {
			m := newNaive(Config{CleanNegative: 10, CleanHBV: 10, CleanHCV: 10,
				DeepClean: 60, MinRecovery: 10})
			if e := m.registerBay(0, "G1", ZoneGeneral, false); e != nil {
				b.Fatal(e)
			}
			if e := m.registerPatient(0, "p", InfectionNegative); e != nil {
				b.Fatal(e)
			}
			for i := 0; i < n; i++ {
				start := i * 100
				if _, e := m.book(start, fmt.Sprintf("h%d", i), "p", start, 10); e != nil {
					b.Fatal(e)
				}
			}
			if e := m.registerPatient(n*100, "q", InfectionNegative); e != nil {
				b.Fatal(e)
			}
			lastEnd := (n-1)*100 + 10
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_, _ = m.book(n*100+i, fmt.Sprintf("probe%d", i), "q", lastEnd+9, 10)
			}
		})
	}
}

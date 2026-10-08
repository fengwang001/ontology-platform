package subro_test

import (
	"fmt"
	"testing"

	"ontology/subro"
)

// 证明每次操作后的结清开销不随该案件历史回收笔数增长：
// 预置 0 / 1k / 100k 笔历史回收后，单次回收（含结清）的耗时应基本持平。
// 运行: go test ./subro/ -bench BenchmarkSettleVsRecoveryHistory -benchtime 2000x
func BenchmarkSettleVsRecoveryHistory(b *testing.B) {
	for _, hist := range []int{0, 1_000, 100_000} {
		b.Run(fmt.Sprintf("history=%d", hist), func(b *testing.B) {
			s := subro.NewService()
			if err := s.RegisterCase(0, subro.CaseInput{
				CaseID: "c", TotalLoss: 1 << 50, InsurerPaid: 1 << 49, Deadline: 1 << 60, RatioBP: 10000,
			}); err != nil {
				b.Fatal(err)
			}
			for i := 0; i < hist; i++ {
				if _, err := s.Recover(int64(i+1), "c", 1, 0); err != nil {
					b.Fatal(err)
				}
			}
			now := int64(hist + 1)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				now++
				// 净零回收：走完整的校验+分配+结清路径，但不改变金额，
				// 使各档位的结清工作量可直接对比。
				if _, err := s.Recover(now, "c", 0, 0); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// 证明每次操作的结清开销不随案件总数增长：
// 注册表中有 1 / 10k 个案件时，对同一案件的单次操作耗时应基本持平。
// 运行: go test ./subro/ -bench BenchmarkOpVsCaseCount -benchtime 2000x
func BenchmarkOpVsCaseCount(b *testing.B) {
	for _, n := range []int{1, 10_000} {
		b.Run(fmt.Sprintf("cases=%d", n), func(b *testing.B) {
			s := subro.NewService()
			for i := 0; i < n; i++ {
				if err := s.RegisterCase(0, subro.CaseInput{
					CaseID: fmt.Sprintf("case-%d", i), TotalLoss: 1 << 50, InsurerPaid: 1 << 49,
					Deadline: 1 << 60, RatioBP: 10000,
				}); err != nil {
					b.Fatal(err)
				}
			}
			var now int64
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				now++
				if _, err := s.Recover(now, "case-0", 0, 0); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

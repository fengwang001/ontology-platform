package chargeback_test

import (
	"fmt"
	"testing"

	"ontology/chargeback"
)

// buildHistory 构造含 n 笔交易与 n 个案件的历史，用于验证查询开销
// 不随历史案件总数或交易总数增长。
func buildHistory(b *testing.B, n int) *chargeback.Engine {
	b.Helper()
	e := chargeback.New(testConfig())
	for i := 0; i < n; i++ {
		if err := e.AddTransaction(i, chargeback.Transaction{
			ID:         fmt.Sprintf("txn-%d", i),
			SettleDay:  i,
			Amount:     1000,
			CardID:     fmt.Sprintf("card-%d", i%8),
			MerchantID: fmt.Sprintf("mch-%d", i%4),
		}); err != nil {
			b.Fatal(err)
		}
		if err := e.OpenDispute(i, fmt.Sprintf("case-%d", i),
			fmt.Sprintf("txn-%d", i), chargeback.ReasonFraud, 500); err != nil {
			b.Fatal(err)
		}
	}
	return e
}

// BenchmarkMerchantBalanceScaling 验证商户余额查询为 O(1)：
// 历史规模从 1k 到 100k 增长 100 倍，单次查询耗时应保持平坦。
// 验证方法：go test -bench=Scaling -benchmem ./chargeback/
func BenchmarkMerchantBalanceScaling(b *testing.B) {
	for _, n := range []int{1_000, 10_000, 100_000} {
		e := buildHistory(b, n)
		b.Run(fmt.Sprintf("history=%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if _, err := e.MerchantBalance(2*n, "mch-1"); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkDisputableAmountScaling 验证交易可拒付余额查询为 O(1)。
func BenchmarkDisputableAmountScaling(b *testing.B) {
	for _, n := range []int{1_000, 10_000, 100_000} {
		e := buildHistory(b, n)
		b.Run(fmt.Sprintf("history=%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if _, err := e.DisputableAmount(2*n, "txn-0"); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkCaseStateScaling 验证案件状态查询为 O(1)。
func BenchmarkCaseStateScaling(b *testing.B) {
	for _, n := range []int{1_000, 10_000, 100_000} {
		e := buildHistory(b, n)
		b.Run(fmt.Sprintf("history=%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if _, err := e.CaseState(2*n, "case-0"); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

package chargeback_test

import (
	"fmt"
	"testing"

	cb "ontology/chargeback"
)

// BenchmarkQueryScaling verifies that merchant balance and disputable queries
// do not grow with the total number of closed cases/transactions. Compare
// across b.N-driven histories: ns/op should stay ~flat as history grows.
func BenchmarkMerchantBalanceHistory(b *testing.B) {
	for _, n := range []int{100, 1000, 10000} {
		b.Run(fmt.Sprintf("history%d", n), func(b *testing.B) {
			cfg := cb.Config{FraudWindowDays: 1_000_000, NotReceivedWindowDays: 1_000_000,
				DuplicateWindowDays: 1_000_000, DuplicateRangeDays: 1_000_000,
				DefenseDays: 1_000_000, ReviewDays: 1_000_000}
			s := cb.New(cfg)
			_ = s.CreditMerchant(0, "m", 1<<50)
			for i := 0; i < n; i++ {
				tid := fmt.Sprintf("t%d", i)
				cid := fmt.Sprintf("c%d", i)
				_ = s.RegisterTransaction(0, cb.Transaction{
					ID: tid, CardID: "c", MerchantID: "m", Amount: 1, SettlementDay: 0})
				if err := s.File(0, tid, cb.ReasonFraud, 1, cid); err != nil {
					b.Fatal(err)
				}
				_ = s.Defend(0, cid)
				if err := s.PreArbitration(0, cid); err != nil {
					b.Fatal(err)
				}
				if err := s.Rule(0, cid, cb.OutcomeMerchantWin); err != nil {
					b.Fatal(err)
				}
			}
			// One live case on a distinct transaction drives the query.
			_ = s.RegisterTransaction(0, cb.Transaction{
				ID: "live", CardID: "c", MerchantID: "m", Amount: 1, SettlementDay: 0})
			_ = s.File(0, "live", cb.ReasonNotReceived, 1, "liveCase")
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := s.MerchantBalance(10, "m"); err != nil {
					b.Fatal(err)
				}
				if _, err := s.Disputable(10, "live"); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

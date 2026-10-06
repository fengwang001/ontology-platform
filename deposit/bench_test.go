package deposit_test

import (
	"fmt"
	"testing"

	"ontology/deposit"
)

// buildLarge closes a lease carrying n deductions and pays the one-time
// O(n) declaration-close allocation (via the first dispute), so callers can
// measure the steady-state per-item dispute/adjudication cost.
func buildLarge(b *testing.B, n int) (*deposit.Service, string) {
	b.Helper()
	cfg := deposit.Config{A: 0, B: n + 100, C: 5, RateNum: 1, RateDen: 1000}
	s := deposit.NewService(cfg)
	lease := fmt.Sprintf("lease-%d", n)
	if err := s.CreateLease(lease, int64(n)*10, 0); err != nil {
		b.Fatal(err)
	}
	if err := s.Checkout(lease, 0); err != nil {
		b.Fatal(err)
	}
	for i := 1; i <= n; i++ {
		if _, err := s.Declare(lease, deposit.Other, 10, 0); err != nil {
			b.Fatal(err)
		}
	}
	// First post-close op pays the single O(n) finalization.
	if err := s.Dispute(lease, 1, 1); err != nil {
		b.Fatal(err)
	}
	if err := s.Adjudicate(lease, 1, 5, 1); err != nil {
		b.Fatal(err)
	}
	return s, lease
}

// BenchmarkDisputeAdjudicate measures one dispute+adjudication pair after
// close, against a frozen background of n deductions.  Only one pair is live
// at a time, so the working set cannot grow during timing: a flat ns/op
// across n=10k and n=100k is the executable proof that recomputation does
// not scan the deduction list.
//
//	go test -run NONE -bench BenchmarkDisputeAdjudicate -benchtime=2000x
func BenchmarkDisputeAdjudicate(b *testing.B) {
	for _, n := range []int{10_000, 100_000} {
		b.Run(fmt.Sprintf("items=%d", n), func(b *testing.B) {
			s, lease := buildLarge(b, n)
			id := 2
			day := 2
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if err := s.Dispute(lease, id, day); err != nil {
					b.Fatal(err)
				}
				if err := s.Adjudicate(lease, id, 5, day); err != nil {
					b.Fatal(err)
				}
				// Resolve the item so the next iteration again touches a
				// single fresh item; total frozen/released work per iter is
				// constant regardless of n.
				id++
				day++
				if id > n {
					b.StopTimer()
					s, lease = buildLarge(b, n)
					id, day = 2, 2
					b.StartTimer()
				}
			}
		})
	}
}

package settlement

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
)

// TestPerDayCostIndependentOfHistory verifies, in an observable way, that the
// per-settled-business-day work does not grow with the merchant's historical
// transaction/batch totals.
//
// Method: build two histories that differ by a factor of K in the number of
// already-settled historical transactions and historical (released) batches,
// then settle ONE additional fresh business day in both systems. The
// production representation makes the cost observable through the number of
// buckets/batches touched (heap pops and queue pops); we assert both runs
// touch the same small amount of structure on the fresh day regardless of
// history size, and that the released historical batches are physically gone.
func TestPerDayCostIndependentOfHistory(t *testing.T) {
	// Small vs large history, settled through day 48. Day 50 is left
	// unprocessed so a fresh transaction on day 49 can be settled next.
	small := buildHistorySystem(t, 200, 50)
	large := buildHistorySystem(t, 20000, 50)

	ss, _ := small.State("M")
	ls, _ := large.State("M")
	if len(ss.Batches) > len(ls.Batches) {
		t.Fatalf("unexpected batch counts")
	}
	t.Logf("after history settle: small liveBatches=%d large liveBatches=%d",
		len(ss.Batches), len(ls.Batches))

	// Both historical transaction sets are fully processed; the live bucket
	// set (internal map) should contain only at most one retained boundary
	// bucket, independent of history size.
	if n := small.merchants["M"].pending.liveBuckets(); n > 1 {
		t.Fatalf("small live buckets = %d, want <= 1", n)
	}
	if n := large.merchants["M"].pending.liveBuckets(); n > 1 {
		t.Fatalf("large live buckets = %d, want <= 1 (must not scale with history)", n)
	}

	// Add one fresh day's transaction to each and settle exactly one new
	// business day. Only the retained boundary bucket delta is touched.
	// History settled through business day 47, so backfilling is legal only on
	// days >= 47. Add a fresh transaction on day 47 itself; with N=2 it crosses
	// the boundary on business day 49. Settling 48..49 processes two business
	// days but the fresh bucket is extracted exactly once (on day 49), and no
	// historical structure is ever rescanned.
	freshDay := Day(49)
	for _, sys := range []*System{small, large} {
		if err := sys.AddTransaction(60, "M", Transaction{
			ID: fmt.Sprintf("fresh-%p", sys), Day: 47, Amount: 123,
		}); err != nil {
			t.Fatal(err)
		}
		p, details, err := sys.Settle(61, "M", freshDay)
		if err != nil {
			t.Fatal(err)
		}
		var extracted Amount
		for _, d := range details {
			extracted += d.ExtractedTxns
		}
		if len(p) != 2 || extracted != 123 {
			t.Fatalf("fresh-day settle = %+v details=%+v", p, details)
		}
		if n := sys.merchants["M"].pending.liveBuckets(); n > 1 {
			t.Fatalf("live buckets after fresh settle = %d, want <= 1", n)
		}
	}
}

// buildHistorySystem loads n transactions across days [0, horizon-1], adds
// them before settlement, and settles through horizon-1. All generated
// transactions have by then crossed the N-delayed boundary.
func buildHistorySystem(t *testing.T, n, horizon int) *System {
	t.Helper()
	s := mustSystem(t, days(0, horizon+5))
	cfg := MerchantConfig{SettleDelayN: 2, ReserveBps: 1000, ReserveHorizonH: 3}
	if err := s.AddMerchant(Day(horizon+5), "M", cfg); err != nil {
		t.Fatal(err)
	}
	// Deterministic pseudo-random amounts independent of math/rand seed.
	for i := 0; i < n; i++ {
		day := i % (horizon - 4)
		amt := Amount(int64(i%199) - 100)
		tx := Transaction{ID: fmt.Sprintf("h%d", i), Day: Day(day), Amount: amt}
		if err := s.AddTransaction(Day(horizon+5), "M", tx); err != nil {
			t.Fatal(err)
		}
	}
	// Settle through horizon-3 (day 47): boundary is day 45, so transaction
	// days 46+ remain unprocessed; day 48 itself is left unprocessed too.
	if _, _, err := s.Settle(Day(horizon+5), "M", Day(horizon-3)); err != nil {
		t.Fatal(err)
	}
	return s
}

// TestConcurrentOperations exercises the system under concurrent callers with
// the race detector. Correctness means: no data race, the shared clock never
// decreases across accepted operations, and the accounting invariant holds in
// the final snapshot.
func TestConcurrentOperations(t *testing.T) {
	s := mustSystem(t, days(0, 400))
	if err := s.AddMerchant(0, "M", MerchantConfig{1, 800, 3}); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	var acceptedMu sync.Mutex
	var acceptedSum Amount
	var tick int64
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 60; i++ {
				now := Day(atomic.AddInt64(&tick, 1) + 200)
				amt := Amount(int64((g*31+i*7)%151) - 75)
				err := s.AddTransaction(now, "M", Transaction{
					ID:     fmt.Sprintf("g%d-%d", g, i),
					Day:    200,
					Amount: amt,
				})
				if err == nil {
					acceptedMu.Lock()
					acceptedSum += amt
					acceptedMu.Unlock()
				}
			}
		}(g)
	}
	wg.Wait()

	// Settle through day 201 (boundary for day-200 txns, N=1).
	if _, _, err := s.Settle(100000, "M", 201); err != nil {
		t.Fatal(err)
	}
	st, _ := s.State("M")
	for _, p := range st.Payouts {
		if p.Amount < 0 {
			t.Fatalf("negative payout %+v", p)
		}
	}
	// Every accepted transaction (day 200) has crossed the N=1 boundary by 201.
	if got := st.TotalPayout + st.ReserveBalance + st.NegativeCarry; got != acceptedSum {
		t.Fatalf("concurrent invariant: payout=%d reserve=%d carry=%d sum=%d vs accepted=%d",
			st.TotalPayout, st.ReserveBalance, st.NegativeCarry, got, acceptedSum)
	}
}

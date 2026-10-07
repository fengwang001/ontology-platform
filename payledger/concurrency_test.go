package payledger

import (
	"fmt"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
)

// TestConcurrentOps hammers the engine from many goroutines. The result must
// equal some serial execution: every observed available credit must be
// non-negative, and the final state must satisfy the ledger conservation
// identity posted == sum(captured) - sum(refunded) per account. Run with
// -race to also prove the absence of data races.
func TestConcurrentOps(t *testing.T) {
	e := newEngine(t, 3, 100)
	const accounts = 4
	for i := 0; i < accounts; i++ {
		mustOK(t, e.CreateAccount(fmt.Sprintf("acct-%d", i), 1<<40, 0))
	}

	var dayCounter atomic.Int64
	var idCounter atomic.Int64
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(seed))
			for i := 0; i < 500; i++ {
				now := dayCounter.Add(int64(rng.Intn(2))) - 1
				if now < 0 {
					now = 0
				}
				acct := fmt.Sprintf("acct-%d", rng.Intn(accounts))
				switch rng.Intn(6) {
				case 0:
					id := fmt.Sprintf("auth-%d", idCounter.Add(1))
					_ = e.Authorize(acct, id, int64(1+rng.Intn(1000)), now)
				case 1:
					id := fmt.Sprintf("auth-%d", 1+rng.Int63n(idCounter.Load()+1))
					_ = e.Increment(id, int64(1+rng.Intn(500)), now)
				case 2:
					id := fmt.Sprintf("auth-%d", 1+rng.Int63n(idCounter.Load()+1))
					_ = e.Capture(id, int64(1+rng.Intn(500)), rng.Intn(3) == 0, now)
				case 3:
					id := fmt.Sprintf("auth-%d", 1+rng.Int63n(idCounter.Load()+1))
					_ = e.Reverse(id, now)
				case 4:
					id := fmt.Sprintf("auth-%d", 1+rng.Int63n(idCounter.Load()+1))
					_ = e.Refund(id, int64(1+rng.Intn(200)), now)
				case 5:
					avail, err := e.Available(acct, now)
					if err == nil && avail < 0 {
						t.Errorf("negative available %d on %s", avail, acct)
					}
				}
			}
		}(int64(g))
	}
	wg.Wait()

	// Conservation: for every account, limit - available - activeHolds must
	// equal the sum of (captured - refunded) over its authorizations.
	now := dayCounter.Load() + 1
	capturedMinusRefunded := map[string]int64{}
	activeHolds := map[string]int64{}
	for i := int64(1); i <= idCounter.Load(); i++ {
		view, err := e.AuthState(fmt.Sprintf("auth-%d", i), now)
		if err != nil {
			continue
		}
		capturedMinusRefunded[view.AccountID] += view.TotalCaptured - view.TotalRefunded
		activeHolds[view.AccountID] += view.RemainingHold
	}
	for i := 0; i < accounts; i++ {
		acct := fmt.Sprintf("acct-%d", i)
		avail, err := e.Available(acct, now)
		mustOK(t, err)
		if avail < 0 {
			t.Fatalf("negative final available %d on %s", avail, acct)
		}
		posted := (1 << 40) - avail - activeHolds[acct]
		if posted != capturedMinusRefunded[acct] {
			t.Fatalf("%s: posted %d != captured-refunded %d", acct, posted, capturedMinusRefunded[acct])
		}
	}
}

// BenchmarkAvailable measures pure query cost with a large terminated
// history plus live holds: run with -bench=. to verify it stays flat.
func BenchmarkAvailable(b *testing.B) {
	e, err := NewEngine(5, 0)
	if err != nil {
		b.Fatal(err)
	}
	day := int64(0)
	mustOKB(b, e.CreateAccount("acct", 1<<40, day))
	for i := 0; i < 100000; i++ {
		id := fmt.Sprintf("hist-%d", i)
		mustOKB(b, e.Authorize("acct", id, 1, day))
		mustOKB(b, e.Reverse(id, day))
		day++
	}
	for i := 0; i < 100; i++ {
		mustOKB(b, e.Authorize("acct", fmt.Sprintf("live-%d", i), 10, day))
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := e.Available("acct", day); err != nil {
			b.Fatal(err)
		}
	}
}

func mustOKB(b *testing.B, err error) {
	b.Helper()
	if err != nil {
		b.Fatal(err)
	}
}

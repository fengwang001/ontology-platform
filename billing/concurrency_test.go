package billing

import (
	"fmt"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
)

// Concurrent payments, queries and view reads must behave as some
// serial execution: conservation holds, no bill is ever over-offset,
// and the accepted payment total matches the ledger exactly.
func TestConcurrentPaymentsAndQueries(t *testing.T) {
	s := newService(t, testCfg())
	const households = 8
	bills := map[string][]string{}
	for i := 0; i < households; i++ {
		h := fmt.Sprintf("H%d", i)
		addHousehold(t, s, h, 0)
		for j := 0; j < 5; j++ {
			b := genBill(t, s, h, fmt.Sprintf("P%d", j), int64(500+100*j), int64(3*j), 0)
			bills[h] = append(bills[h], b)
		}
	}
	var acceptedSum atomic.Int64 // sum of accepted payment amounts
	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(g) + 1))
			for i := 0; i < 250; i++ {
				h := fmt.Sprintf("H%d", rng.Intn(households))
				switch rng.Intn(4) {
				case 0, 1:
					amount := int64(1 + rng.Intn(400))
					if _, err := s.Pay(h, amount, 30); err != nil {
						t.Errorf("pay: %v", err)
						return
					}
					acceptedSum.Add(amount)
				case 2:
					if _, err := s.TotalDue(h, 30); err != nil {
						t.Errorf("query: %v", err)
						return
					}
				default:
					if _, err := s.HouseholdView(h); err != nil {
						t.Errorf("view: %v", err)
						return
					}
				}
			}
		}(g)
	}
	wg.Wait()

	var totalPaid int64
	for i := 0; i < households; i++ {
		h := fmt.Sprintf("H%d", i)
		hv, err := s.HouseholdView(h)
		must(t, err)
		if hv.TotalPaid != hv.AllocatedPrincipal+hv.AllocatedLateFee+hv.Prepay {
			t.Fatalf("conservation violated for %s: %+v", h, hv)
		}
		totalPaid += hv.TotalPaid
		for _, b := range bills[h] {
			v := billView(t, s, h, b)
			if v.PrincipalPaid > v.Principal {
				t.Fatalf("bill %s over-offset: principal paid %d > %d", b, v.PrincipalPaid, v.Principal)
			}
			if v.LateFeePaid > v.LateFee {
				t.Fatalf("bill %s over-offset: late fee paid %d > accrued %d", b, v.LateFeePaid, v.LateFee)
			}
		}
	}
	if totalPaid != acceptedSum.Load() {
		t.Fatalf("ledger total paid %d != accepted payment sum %d", totalPaid, acceptedSum.Load())
	}
}

// Concurrent duplicate operations (the same dispute submitted by many
// goroutines) must succeed at most once.
func TestConcurrentDuplicateDispute(t *testing.T) {
	s := newService(t, testCfg())
	addHousehold(t, s, "HA", 0)
	b := genBill(t, s, "HA", "P0", 1000, 10, 0)

	var successes atomic.Int64
	var wg sync.WaitGroup
	for g := 0; g < 32; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.Dispute("HA", b, 20); err == nil {
				successes.Add(1)
			}
		}()
	}
	wg.Wait()
	if got := successes.Load(); got != 1 {
		t.Fatalf("duplicate dispute succeeded %d times, want exactly 1", got)
	}
	if v := billView(t, s, "HA", b); !v.Disputed {
		t.Fatalf("bill should be disputed: %+v", v)
	}
}

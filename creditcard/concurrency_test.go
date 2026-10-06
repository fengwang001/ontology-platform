package creditcard

import (
	"sync"
	"sync/atomic"
	"testing"
)

// Concurrent operations must behave as some serial execution: run with
// -race, balances stay non-negative, and a money-conservation invariant
// holds at the end.
func TestConcurrentOpsConservation(t *testing.T) {
	s := NewService()
	p := zeroRates(0, 1000, 5, 20)
	p.RateBps = [NumCategories]int64{5000, 3000, 1000}
	mustCreate(t, s, "x", p, 0)

	var day atomic.Int64
	day.Store(1)
	var chargeSum, paySum atomic.Int64

	const workers = 8
	const opsEach = 200
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < opsEach; i++ {
				now := day.Add(1)
				switch (w + i) % 5 {
				case 0, 1:
					amt := int64(1 + (w*7+i)%1000)
					if err := s.Charge("x", Category(i%NumCategories), amt, now); err == nil {
						chargeSum.Add(amt)
					}
				case 2, 3:
					amt := int64(1 + (w*13+i)%500)
					if _, err := s.Pay("x", amt, now); err == nil {
						paySum.Add(amt)
					}
				default:
					// G=0 and strictly increasing days: billing is valid
					// whenever accepted; rejections only come from the
					// clock race and are ignored here.
					_, _ = s.Bill("x", now)
				}
			}
		}(w)
	}
	wg.Wait()

	bal, err := s.Balances("x")
	if err != nil {
		t.Fatal(err)
	}
	over, err := s.Overpayment("x")
	if err != nil {
		t.Fatal(err)
	}
	for c := Category(0); c < NumCategories; c++ {
		if bal[c] < 0 {
			t.Fatalf("negative balance %v: %v", c, bal)
		}
	}
	if over < 0 {
		t.Fatalf("negative overpayment %d", over)
	}

	bills, err := s.Bills("x")
	if err != nil {
		t.Fatal(err)
	}
	var interestSum, lateFeeSum int64
	for _, b := range bills {
		interestSum += b.InterestTotal()
		lateFeeSum += b.LateFee
	}

	// Net debt conservation: balances - overpayment ==
	// charges + interest + late fees - payments.
	got := bal[Cash] + bal[Installment] + bal[Purchase] - over
	want := chargeSum.Load() + interestSum + lateFeeSum - paySum.Load()
	if got != want {
		t.Fatalf("conservation violated: balances-overpayment=%d, charges+interest+latefees-payments=%d",
			got, want)
	}
	t.Logf("accepted charges=%d payments=%d bills=%d interest=%d lateFees=%d final=%d",
		chargeSum.Load(), paySum.Load(), len(bills), interestSum, lateFeeSum, got)
}

// Concurrent queries must not mutate state.
func TestConcurrentQueriesAreReadOnly(t *testing.T) {
	s := NewService()
	p := zeroRates(2, 1000, 5, 20)
	p.RateBps = [NumCategories]int64{5000, 3000, 1000}
	mustCreate(t, s, "x", p, 0)
	mustCharge(t, s, "x", Purchase, 1000, 1)
	mustBill(t, s, "x", 3)

	before, _ := s.Bills("x")
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				_, _ = s.Balances("x")
				_, _ = s.Overpayment("x")
				_, _ = s.Bills("x")
				_, _ = s.Payments("x")
			}
		}()
	}
	wg.Wait()
	after, _ := s.Bills("x")
	if len(before) != len(after) || before[0] != after[0] {
		t.Fatalf("queries mutated state: %+v vs %+v", before, after)
	}
}

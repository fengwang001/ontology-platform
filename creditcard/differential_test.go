package creditcard

import (
	"fmt"
	"math/rand"
	"slices"
	"testing"
)

// TestDifferentialRandom replays randomized operation sequences against
// both the real Service and the independent naive model, comparing every
// error, balance, bill and payment record after every single operation.
// Each step is logged with its inputs, outputs and decision basis.
func TestDifferentialRandom(t *testing.T) {
	const seeds = 60
	const opsPerSeed = 150

	for seed := int64(0); seed < seeds; seed++ {
		seed := seed
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			t.Parallel()
			rng := rand.New(rand.NewSource(seed))
			real := NewService()
			naive := newNaiveService()

			ids := []string{"a", "b"}
			for i, id := range ids {
				p := Params{
					RateBps: [NumCategories]int64{
						rng.Int63n(40000), rng.Int63n(40000), rng.Int63n(40000),
					},
					GraceDays:      rng.Int63n(4),
					MinPayRatioBps: []int64{0, 1000, 5000, 10000}[rng.Intn(4)],
					MinPayFloor:    []int64{0, 5, 100}[rng.Intn(3)],
					LateFeeCap:     []int64{0, 7, 50}[rng.Intn(3)],
				}
				now := int64(i) // 0, 1: keep global clock nondecreasing
				errR := real.CreateAccount(id, p, now)
				errN := naive.create(id, p, now)
				if errR != errN {
					t.Fatalf("create %q: real=%v naive=%v", id, errR, errN)
				}
				t.Logf("create %q params=%+v at %d", id, p, now)
			}

			clock := int64(1)
			amount := func() int64 {
				switch rng.Intn(10) {
				case 0:
					return 1 + rng.Int63n(5) // tiny: exercises remainders
				case 1:
					return 1 + rng.Int63n(1_000_000_000_000) // huge
				default:
					return 1 + rng.Int63n(3000)
				}
			}

			for step := 0; step < opsPerSeed; step++ {
				id := ids[rng.Intn(len(ids))]
				if rng.Intn(20) == 0 {
					id = "ghost" // nonexistent account
				}
				now := clock + rng.Int63n(3)
				if rng.Intn(25) == 0 && clock > 0 {
					now = clock - 1 // clock rollback attempt
				}

				var errR, errN error
				var desc string
				switch rng.Intn(10) {
				case 0, 1, 2, 3: // charge
					cat := Category(rng.Intn(NumCategories))
					amt := amount()
					if rng.Intn(30) == 0 {
						amt = 0 // invalid
					}
					errR = real.Charge(id, cat, amt, now)
					errN = naive.charge(id, cat, amt, now)
					desc = fmt.Sprintf("charge %q %v %d", id, cat, amt)
				case 4, 5, 6, 7: // pay
					amt := amount()
					if rng.Intn(30) == 0 {
						amt = 0 // invalid
					}
					var recR, recN PaymentRecord
					recR, errR = real.Pay(id, amt, now)
					recN, errN = naive.pay(id, amt, now)
					if errR == nil && errN == nil && recR != recN {
						t.Fatalf("step %d: payment record mismatch:\nreal  %+v\nnaive %+v", step, recR, recN)
					}
					desc = fmt.Sprintf("pay %q %d", id, amt)
				default: // bill
					var bR, bN Bill
					bR, errR = real.Bill(id, now)
					bN, errN = naive.bill(id, now)
					if errR == nil && errN == nil && bR != bN {
						t.Fatalf("step %d: bill mismatch:\nreal  %+v\nnaive %+v", step, bR, bN)
					}
					desc = fmt.Sprintf("bill %q", id)
					if errR == nil {
						t.Logf("step %d @%d: %s -> bill total=%d min=%d interest=%v lateFee=%d "+
							"(basis: prev paidByDue/total decided grace & late fee)",
							step, now, desc, bR.Total, bR.MinPayment, bR.Interest, bR.LateFee)
					}
				}
				if errR != errN {
					t.Fatalf("step %d @%d: %s: error mismatch: real=%v naive=%v",
						step, now, desc, errR, errN)
				}
				if errR == nil && now > clock {
					clock = now
				}
				t.Logf("step %d @%d: %s -> err=%v", step, now, desc, errR)

				// Full state comparison after every operation.
				for _, aid := range ids {
					balR, _ := real.Balances(aid)
					ovR, _ := real.Overpayment(aid)
					biR, _ := real.Bills(aid)
					paR, _ := real.Payments(aid)
					na := naive.accounts[aid]
					if balR != na.bal || ovR != na.over {
						t.Fatalf("step %d: state mismatch %q: real bal=%v over=%d, naive bal=%v over=%d",
							step, aid, balR, ovR, na.bal, na.over)
					}
					if !slices.Equal(biR, na.bills) {
						t.Fatalf("step %d: bills mismatch %q:\nreal  %+v\nnaive %+v", step, aid, biR, na.bills)
					}
					if !slices.Equal(paR, na.pays) {
						t.Fatalf("step %d: payments mismatch %q:\nreal  %+v\nnaive %+v", step, aid, paR, na.pays)
					}
				}
			}

			for _, aid := range ids {
				biR, _ := real.Bills(aid)
				balR, _ := real.Balances(aid)
				t.Logf("final %q: bills=%d balances=%v", aid, len(biR), balR)
			}
		})
	}
}

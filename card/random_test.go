package card_test

import (
	"fmt"
	"math/rand"
	"testing"

	"ontology/card"
)

// TestRandomAgainstNaiveModel replays many random operation sequences
// against both the ledger and the independent naive model, comparing
// balances, overpayment, bills and repayment allocations after every
// single operation. Every operation, its outcome and the check basis
// are logged (run with -v to see them).
func TestRandomAgainstNaiveModel(t *testing.T) {
	const (
		seeds    = 12
		accounts = 3
		ops      = 500
	)
	for seed := int64(0); seed < seeds; seed++ {
		t.Run(fmt.Sprintf("seed_%d", seed), func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))
			l := card.NewLedger()

			ids := make([]string, accounts)
			params := make([]card.Params, accounts)
			logs := make([][]nop, accounts)
			for i := range ids {
				ids[i] = fmt.Sprintf("acct%d", i)
				params[i] = randomParams(rng)
				if err := l.CreateAccount(ids[i], params[i], 0); err != nil {
					t.Fatalf("create %s: %v", ids[i], err)
				}
			}
			t.Logf("params: %+v", params)

			var now int64
			for step := 0; step < ops; step++ {
				now += rng.Int63n(3) // 0, 1 or 2: same-day ops happen
				ai := rng.Intn(accounts)
				id := ids[ai]
				if rng.Intn(100) < 5 {
					id = "ghost" // unknown account
				}
				opDay := now
				if rng.Intn(100) < 3 {
					opDay = now - 1 - rng.Int63n(3) // stale clock attempt
				}

				switch kind := rng.Intn(100); {
				case kind < 45:
					cat := card.Category(rng.Intn(3))
					if rng.Intn(100) < 2 {
						cat = card.Category(7) // invalid category
					}
					amount := rng.Int63n(20_000)
					if rng.Intn(100) < 3 {
						amount = 0 // invalid amount
					}
					err := l.Charge(id, cat, amount, opDay)
					t.Logf("step=%d day=%d charge %s %s %d -> err=%v", step, opDay, id, cat, amount, err)
					if err == nil {
						logs[ai] = append(logs[ai], nop{kind: nCharge, day: opDay, cat: cat, amount: amount})
						checkAccount(t, l, ids[ai], params[ai], logs[ai], step)
					}

				case kind < 80:
					amount := rng.Int63n(30_000)
					if rng.Intn(100) < 3 {
						amount = 0
					}
					rec, err := l.Repay(id, amount, opDay)
					t.Logf("step=%d day=%d repay %s %d -> err=%v alloc=%v toMin=%d over=%d",
						step, opDay, id, amount, err, rec.Alloc, rec.ToMin, rec.Over)
					if err == nil {
						logs[ai] = append(logs[ai], nop{kind: nRepay, day: opDay, amount: amount})
						st := replayNaive(params[ai], 0, logs[ai])
						want := st.reps[len(st.reps)-1]
						if rec != want {
							t.Fatalf("step %d: repayment mismatch\n got: %+v\nwant: %+v\nbasis: allocation = min part (cash->installment->purchase) then rate desc",
								step, rec, want)
						}
						checkAccount(t, l, ids[ai], params[ai], logs[ai], step)
					}

				default:
					b, err := l.Bill(id, opDay)
					t.Logf("step=%d day=%d bill %s -> err=%v total=%d minDue=%d interest=%v lateFee=%d",
						step, opDay, id, err, b.Total, b.MinDue, b.Interest, b.LateFee)
					if err == nil {
						logs[ai] = append(logs[ai], nop{kind: nBill, day: opDay})
						st := replayNaive(params[ai], 0, logs[ai])
						want := st.bills[len(st.bills)-1]
						if b.Total != want.total || b.MinDue != want.minDue ||
							b.Interest != want.interest || b.LateFee != want.lateFee ||
							b.DueDay != want.dueDay {
							t.Fatalf("step %d: bill mismatch\n got: %+v\nwant: %+v\nbasis: interest=(base+carry)/3650000 floor, fee=min(F, minDue-windowSum)",
								step, b, want)
						}
						checkAccount(t, l, ids[ai], params[ai], logs[ai], step)
					}
				}
			}

			// Final full-history comparison for every account.
			for i := range ids {
				st := replayNaive(params[i], 0, logs[i])
				bills, err := l.Bills(ids[i])
				if err != nil {
					t.Fatal(err)
				}
				if len(bills) != len(st.bills) {
					t.Fatalf("%s: %d bills, want %d", ids[i], len(bills), len(st.bills))
				}
				for j, b := range bills {
					w := st.bills[j]
					if b.Total != w.total || b.MinDue != w.minDue || b.Interest != w.interest ||
						b.LateFee != w.lateFee || b.DueDay != w.dueDay || b.BillDay != w.billDay ||
						b.PaidInWindow != w.paidInWindow {
						t.Fatalf("%s bill %d mismatch\n got: %+v\nwant: %+v", ids[i], j, b, w)
					}
				}
				reps, err := l.Repayments(ids[i])
				if err != nil {
					t.Fatal(err)
				}
				if len(reps) != len(st.reps) {
					t.Fatalf("%s: %d repayments, want %d", ids[i], len(reps), len(st.reps))
				}
				for j, r := range reps {
					if r != st.reps[j] {
						t.Fatalf("%s repayment %d mismatch\n got: %+v\nwant: %+v", ids[i], j, r, st.reps[j])
					}
				}
				t.Logf("%s: final state matches naive model (%d bills, %d repayments, %d ops)",
					ids[i], len(st.bills), len(st.reps), len(logs[i]))
			}
		})
	}
}

// checkAccount compares the ledger's snapshot of one account with the
// naive model recomputed from scratch, and verifies invariants.
func checkAccount(t *testing.T, l *card.Ledger, id string, p card.Params, log []nop, step int) {
	t.Helper()
	st := replayNaive(p, 0, log)
	snap, err := l.Snapshot(id)
	if err != nil {
		t.Fatalf("step %d: snapshot: %v", step, err)
	}
	if snap.Balances != st.bal || snap.Overpayment != st.over {
		t.Fatalf("step %d: state mismatch\n got: balances=%v over=%d\nwant: balances=%v over=%d\nbasis: replay of %d accepted ops",
			step, snap.Balances, snap.Overpayment, st.bal, st.over, len(log))
	}
	for c, b := range snap.Balances {
		if b < 0 {
			t.Fatalf("step %d: negative balance %d in category %d", step, b, c)
		}
	}
	if snap.Overpayment < 0 {
		t.Fatalf("step %d: negative overpayment %d", step, snap.Overpayment)
	}
}

func randomParams(rng *rand.Rand) card.Params {
	p := card.Params{
		GraceDays:   rng.Int63n(6),
		MinPayRatio: rng.Int63n(2001),
		MinPayFloor: rng.Int63n(200),
		LateFeeCap:  rng.Int63n(300),
	}
	for c := range p.Rates {
		p.Rates[c] = rng.Int63n(4000)
	}
	if rng.Intn(100) < 30 {
		// Equal rates exercise the fixed tie-break order.
		p.Rates[1], p.Rates[2] = p.Rates[0], p.Rates[0]
	}
	return p
}

package deposit_test

import (
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"testing"

	"ontology/deposit"
)

func errCode(err error) deposit.ErrorCode {
	if err == nil {
		return 0
	}
	switch {
	case errors.Is(err, deposit.ErrInvalidParam):
		return deposit.ErrInvalidParameter
	case errors.Is(err, deposit.ErrClockRollback):
		return deposit.ErrClockBackward
	case errors.Is(err, deposit.ErrNoLease):
		return deposit.ErrLeaseNotFound
	case errors.Is(err, deposit.ErrNotOut):
		return deposit.ErrNotCheckedOut
	case errors.Is(err, deposit.ErrLateDeclare):
		return deposit.ErrDeclarationLate
	case errors.Is(err, deposit.ErrLateDispute):
		return deposit.ErrDisputeLate
	case errors.Is(err, deposit.ErrState):
		return deposit.ErrIllegalState
	case errors.Is(err, deposit.ErrAmount):
		return deposit.ErrAmountOutOfRange
	}
	return deposit.ErrUnknown
}

// op is one step in a randomly generated scenario.
type op struct {
	kind   string
	id     int // deduction id, 0 = pick/any
	cat    deposit.Category
	amount int64
	award  int64
	day    int
	badID  bool
	badCat bool
}

// runScenario replays the same op log on the real service and the naive
// model and compares the decision (accept/reject code) and every observable
// snapshot field after each step.
func runScenario(t *testing.T, seed int64, n int, verbose bool) {
	t.Helper()
	rng := rand.New(rand.NewSource(seed))
	cfg := deposit.Config{
		A: int(rng.Intn(6)), B: 1 + int(rng.Intn(5)), C: int(rng.Intn(8)),
		RateNum: 1, RateDen: 100,
	}
	svc := deposit.NewService(cfg)
	nav := NewNaive(cfg)

	dep := int64(200 + rng.Intn(2000))
	checkout := 5 + rng.Intn(10)

	play := func(o op) {
		lease := "L"
		if o.badID {
			lease = "missing"
		}
		var codeS, codeN deposit.ErrorCode
		var detail string

		switch o.kind {
		case "declare":
			_, e1 := svc.Declare(lease, o.cat, o.amount, o.day)
			_, e2 := nav.Declare(lease, o.cat, o.amount, o.day)
			codeS, codeN = errCode(e1), errCode(e2)
			detail = fmt.Sprintf("cat=%d amount=%d", o.cat, o.amount)
		case "revoke":
			e1 := svc.Revoke(lease, o.id, o.day)
			e2 := nav.Revoke(lease, o.id, o.day)
			codeS, codeN = errCode(e1), errCode(e2)
			detail = fmt.Sprintf("ded=%d", o.id)
		case "dispute":
			e1 := svc.Dispute(lease, o.id, o.day)
			e2 := nav.Dispute(lease, o.id, o.day)
			codeS, codeN = errCode(e1), errCode(e2)
			detail = fmt.Sprintf("ded=%d", o.id)
		case "adjudicate":
			e1 := svc.Adjudicate(lease, o.id, o.award, o.day)
			e2 := nav.Adjudicate(lease, o.id, o.award, o.day)
			codeS, codeN = errCode(e1), errCode(e2)
			detail = fmt.Sprintf("ded=%d award=%d", o.id, o.award)
		case "refund":
			r1, e1 := svc.Refund(lease, o.day)
			r2, e2 := nav.Refund(lease, o.day)
			codeS, codeN = errCode(e1), errCode(e2)
			detail = "refund"
			if e1 == nil && e2 == nil {
				if r1.Amount != r2.Amount || r1.Penalty.Cmp(r2.Penalty) != 0 {
					t.Fatalf("refund mismatch real=(%d,%s) naive=(%d,%s)",
						r1.Amount, r1.Penalty, r2.Amount, r2.Penalty)
				}
				detail = fmt.Sprintf("refund amount=%d penalty=%s", r1.Amount, r1.Penalty)
			}
		case "snapshot":
			s1, e1 := svc.Snapshot(lease, o.day)
			codeS = errCode(e1)
			nv := nav.ViewAt(lease, o.day)
			if nv == nil {
				codeN = deposit.ErrClockBackward
			}
			detail = "snapshot"
			if e1 == nil && nv != nil && lease == "L" {
				cmp := func(name string, a, b int64) {
					if a != b {
						t.Fatalf("day %d snapshot %s real=%d naive=%d", o.day, name, a, b)
					}
				}
				cmp("refunded", s1.Refunded, nv.refunded)
				cmp("landlord", s1.Landlord, nv.landlord)
				cmp("frozen", s1.Frozen, nv.frozen)
				cmp("pending", s1.Pending, nv.pending)
				cmp("receivable", s1.Receivable, nv.receivable)
				cmp("refundable", s1.Refundable, nv.refundable)
				if s1.PenaltyDue.Cmp(nv.penalty) != 0 {
					t.Fatalf("day %d penalty real=%s naive=%s", o.day, s1.PenaltyDue, nv.penalty)
				}
				for _, d := range s1.Deductions {
					if d.Satisfied != nv.satisfied[d.ID] {
						t.Fatalf("day %d ded %d satisfied real=%d naive=%d",
							o.day, d.ID, d.Satisfied, nv.satisfied[d.ID])
					}
					aw := nv.award[d.ID]
					if d.Adjudicated != (aw >= 0) || (aw >= 0 && d.Awarded != aw) {
						t.Fatalf("day %d ded %d award real=%d naive=%d", o.day, d.ID, d.Awarded, aw)
					}
				}
				sum := s1.Refunded + s1.Landlord + s1.Frozen + s1.Awaiting + s1.Pending
				if sum != s1.Deposit {
					t.Fatalf("day %d conservation broken sum=%d dep=%d", o.day, sum, s1.Deposit)
				}
			}
		}

		accepted := codeS == 0 && codeN == 0
		if verbose {
			decision := "ACCEPT"
			if !accepted {
				decision = fmt.Sprintf("REJECT real=%d naive=%d", codeS, codeN)
			}
			t.Logf("day=%2d %-10s %-28s -> %s", o.day, o.kind, detail, decision)
		}
		if codeS != codeN {
			t.Fatalf("day %d %s %s: real code=%d naive code=%d", o.day, o.kind, detail, codeS, codeN)
		}
	}

	// Setup can itself diverge only on bugs, so run it through both directly.
	if e := svc.CreateLease("L", dep, 0); e != nil {
		t.Fatal(e)
	}
	if e := nav.CreateLease("L", dep, 0); e != nil {
		t.Fatal(e)
	}
	if e := svc.Checkout("L", checkout); e != nil {
		t.Fatal(e)
	}
	if e := nav.Checkout("L", checkout); e != nil {
		t.Fatal(e)
	}
	if verbose {
		t.Logf("cfg A=%d B=%d C=%d deposit=%d checkout=%d", cfg.A, cfg.B, cfg.C, dep, checkout)
	}

	declClose := checkout + cfg.A
	disputeClose := declClose + cfg.B
	horizon := disputeClose + cfg.C + 20

	knownIDs := []int{}
	_ = []int{}
	nextDeclID := 1

	day := checkout
	for i := 0; i < n; i++ {
		// Track the high-water mark explicitly so a rejected backwards op
		// does not permanently move the timeline backwards.
		switch rng.Intn(10) {
		case 1, 2:
			day += 1 + rng.Intn(6)
		default:
			day += rng.Intn(2)
		}
		if day > horizon+30 {
			day = horizon + 30
		}
		effDay := day
		if rng.Intn(12) == 0 && day > checkout {
			effDay = checkout + rng.Intn(day-checkout) // rejected rollback
		}
		kind := rng.Intn(6)
		switch kind {
		case 0:
			cat := deposit.Category(rng.Intn(4))
			amount := int64(1 + rng.Intn(900))
			o := op{kind: "declare", cat: cat, amount: amount, day: effDay}
			if rng.Intn(8) == 0 {
				o.amount = 0
			}
			if rng.Intn(15) == 0 {
				o.cat = deposit.Category(7)
			}
			// Only an in-window declaration consumes an id on both sides.
			if effDay >= checkout && effDay <= declClose && o.amount > 0 && o.cat <= deposit.Other {
				knownIDs = append(knownIDs, nextDeclID)
				nextDeclID++
			}
			play(o)
		case 1:
			if len(knownIDs) > 0 {
				play(op{kind: "revoke", id: knownIDs[rng.Intn(len(knownIDs))], day: effDay})
			}
		case 2:
			id := 1 + rng.Intn(8)
			if len(knownIDs) > 0 && rng.Intn(2) == 0 {
				id = knownIDs[rng.Intn(len(knownIDs))]
			}
			o := op{kind: "dispute", id: id, day: effDay}
			if rng.Intn(12) == 0 {
				o.badID = true
			}
			play(o)
		case 3:
			id := 1 + rng.Intn(8)
			award := int64(rng.Intn(1200))
			play(op{kind: "adjudicate", id: id, award: award, day: effDay})
		case 4:
			o := op{kind: "refund", day: effDay}
			if rng.Intn(12) == 0 {
				o.badID = true
			}
			play(o)
		case 5:
			play(op{kind: "snapshot", day: effDay})
		}
		_ = kind
	}
}

func TestDifferentialRandom(t *testing.T) {
	if !testing.Verbose() {
		t.Log("re-run with -v to print every step's input, output and reason")
	}
	for iter := 0; iter < 120; iter++ {
		t.Run(fmt.Sprintf("scenario-%03d", iter), func(t *testing.T) {
			runScenario(t, 20261006+int64(iter)*7919, 150, testing.Verbose())
		})
	}
}

// TestConcurrentRefundNoDoublePay hammers refund from many goroutines; only
// one can win each available tranche and conservation always holds.
func TestConcurrentRefundNoDoublePay(t *testing.T) {
	cfg := deposit.Config{A: 2, B: 3, C: 2, RateNum: 1, RateDen: 100}
	s := deposit.NewService(cfg)
	if err := s.CreateLease("L", 1000, 0); err != nil {
		t.Fatal(err)
	}
	if err := s.Checkout("L", 0); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	var wins int64
	var mu sync.Mutex
	for g := 0; g < 32; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rec, err := s.Refund("L", 10)
			if err == nil {
				mu.Lock()
				wins += rec.Amount
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	snap, err := s.Snapshot("L", 10)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Refunded != 1000 || wins != 1000 {
		t.Fatalf("double refund: refunded=%d wins=%d", snap.Refunded, wins)
	}
	if snap.Landlord+snap.Frozen+snap.Awaiting+snap.Pending != 0 {
		t.Fatalf("money left after full refund: %+v", snap)
	}
}

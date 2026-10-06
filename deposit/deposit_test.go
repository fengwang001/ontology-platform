package deposit_test

import (
	"errors"
	"math/big"
	"testing"

	"ontology/deposit"
)

func testCfg() deposit.Config {
	// A=10 declaration days, B=5 dispute days, C=7 refund days, 0.1%/day.
	return deposit.Config{A: 10, B: 5, C: 7, RateNum: 1, RateDen: 1000}
}

func ratEqual(t *testing.T, name string, got *big.Rat, num, den int64) {
	t.Helper()
	want := new(big.Rat).SetFrac(big.NewInt(num), big.NewInt(den))
	if got.Cmp(want) != 0 {
		t.Fatalf("%s = %s, want %s", name, got, want)
	}
}

func mustCode(t *testing.T, err error, want deposit.ErrorCode) {
	t.Helper()
	if deposit.CodeOf(err) != want {
		t.Fatalf("err=%v code=%d want %d", err, deposit.CodeOf(err), want)
	}
}

func satMap(s *deposit.Snapshot) map[int]int64 {
	m := map[int]int64{}
	for _, d := range s.Deductions {
		m[d.ID] = d.Satisfied
	}
	return m
}

func checkConservation(t *testing.T, s *deposit.Snapshot) {
	t.Helper()
	sum := s.Refunded + s.Landlord + s.Frozen + s.Awaiting + s.Pending
	if sum != s.Deposit {
		t.Fatalf("conservation broken: %d+%d+%d+%d+%d = %d != deposit %d",
			s.Refunded, s.Landlord, s.Frozen, s.Awaiting, s.Pending, sum, s.Deposit)
	}
}

// Declaration window: day A allowed, day A+1 rejected with no trace.
func TestDeclarationBoundary(t *testing.T) {
	s := deposit.NewService(testCfg())
	if err := s.CreateLease("L", 1000, 0); err != nil {
		t.Fatal(err)
	}
	if err := s.Checkout("L", 10); err != nil {
		t.Fatal(err)
	}
	first, err := s.Declare("L", deposit.Rent, 100, 20)
	if err != nil {
		t.Fatal(err) // last legal day: 10+10=20
	}
	_, err = s.Declare("L", deposit.Cleaning, 50, 21)
	mustCode(t, err, deposit.ErrDeclarationLate)

	// Pre-close query shows the deposit entirely undetermined.
	pre, err := s.Snapshot("L", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(pre.Deductions) != 1 || pre.Pending != 1000 {
		t.Fatalf("pre-close items=%d pending=%d", len(pre.Deductions), pre.Pending)
	}

	// Revocation on the last legal day succeeds.
	if err := s.Revoke("L", first, 20); err != nil {
		t.Fatal(err)
	}
	second, err := s.Declare("L", deposit.Other, 10, 20)
	if err != nil {
		t.Fatal(err)
	}
	// After close (clock pushed by a legitimate dispute on the new item),
	// revocation is rejected as late.
	if err := s.Dispute("L", second, 22); err != nil {
		t.Fatal(err)
	}
	if err := s.Revoke("L", second, 22); !errors.Is(err, deposit.ErrLateDeclare) {
		t.Fatalf("revoke after close: %v", err)
	}

	// The day-21 late declaration left no trace; only the two real items.
	snap, err := s.Snapshot("L", 22)
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Deductions) != 2 {
		t.Fatalf("late declaration left a trace: %d items", len(snap.Deductions))
	}
	// first was revoked (0), second frozen at 10, rest awaiting refund.
	if snap.Frozen != 10 || snap.Awaiting != 990 {
		t.Fatalf("frozen=%d awaiting=%d want 10/990", snap.Frozen, snap.Awaiting)
	}
	checkConservation(t, snap)
}

// Dispute window [declClose+1, declClose+B]: boundaries and repeat dispute.
func TestDisputeBoundary(t *testing.T) {
	s := deposit.NewService(testCfg())
	_ = s.CreateLease("L", 1000, 0)
	_ = s.Checkout("L", 10)
	_, _ = s.Declare("L", deposit.Rent, 100, 12)

	mustCode(t, s.Dispute("L", 1, 20), deposit.ErrDisputeLate)
	mustCode(t, s.Dispute("L", 1, 26), deposit.ErrDisputeLate)

	if err := s.Dispute("L", 1, 25); err != nil {
		t.Fatalf("dispute on last legal day: %v", err)
	}
	mustCode(t, s.Dispute("L", 1, 25), deposit.ErrIllegalState)
}

// Category order, within-category FIFO, exact cover and short-by-one.
func TestSatisfaction(t *testing.T) {
	cfg := testCfg()
	s := deposit.NewService(cfg)
	_ = s.CreateLease("L", 1000, 0)
	_ = s.Checkout("L", 0)
	clean, _ := s.Declare("L", deposit.Cleaning, 300, 1)
	rent1, _ := s.Declare("L", deposit.Rent, 400, 1)
	rent2, _ := s.Declare("L", deposit.Rent, 300, 2)
	dmg, _ := s.Declare("L", deposit.Damage, 200, 2)

	// First post-close mutation performs the one-time allocation.  Nothing is
	// refundable here (allocation exhausts the deposit), so the refund is
	// rejected while still finalizing.
	if _, err := s.Refund("L", 11); !errors.Is(err, deposit.ErrState) {
		t.Fatalf("zero-refundable refund: %v", err)
	}
	snap, _ := s.Snapshot("L", 11)
	got := satMap(snap)
	if got[rent1] != 400 || got[rent2] != 300 || got[dmg] != 200 || got[clean] != 100 {
		t.Fatalf("satisfied = %v", got)
	}
	if snap.Receivable != 200 {
		t.Fatalf("receivable=%d want 200", snap.Receivable)
	}
	checkConservation(t, snap)

	s2 := deposit.NewService(cfg)
	_ = s2.CreateLease("L", 1000, 0)
	_ = s2.Checkout("L", 0)
	a, _ := s2.Declare("L", deposit.Other, 600, 1)
	b, _ := s2.Declare("L", deposit.Other, 400, 1)
	if _, err := s2.Refund("L", 11); !errors.Is(err, deposit.ErrState) {
		t.Fatalf("refund with zero refundable must be rejected: %v", err)
	}
	snap2, _ := s2.Snapshot("L", 11)
	g2 := satMap(snap2)
	if g2[a] != 600 || g2[b] != 400 || snap2.Receivable != 0 {
		t.Fatalf("exact cover: %v recv=%d", g2, snap2.Receivable)
	}

	s3 := deposit.NewService(cfg)
	_ = s3.CreateLease("L", 999, 0)
	_ = s3.Checkout("L", 0)
	c, _ := s3.Declare("L", deposit.Rent, 1000, 1)
	if _, err := s3.Refund("L", 11); !errors.Is(err, deposit.ErrState) {
		t.Fatalf("zero refundable: %v", err)
	}
	snap3, _ := s3.Snapshot("L", 11)
	g3 := satMap(snap3)
	if g3[c] != 999 || snap3.Receivable != 1 {
		t.Fatalf("short-by-one sat=%d recv=%d", g3[c], snap3.Receivable)
	}
}

// Frozen money belongs to nobody and accrues no penalty; the undisputed base
// tranche refunds independently; paying exactly on day C is penalty-free.
func TestFreezeRefundAndPenalty(t *testing.T) {
	cfg := testCfg()
	s := deposit.NewService(cfg)
	_ = s.CreateLease("L", 1000, 0)
	_ = s.Checkout("L", 0)
	rent, _ := s.Declare("L", deposit.Rent, 400, 1)
	_, _ = s.Declare("L", deposit.Damage, 300, 1)
	if err := s.Dispute("L", rent, 12); err != nil {
		t.Fatal(err)
	}
	snap, _ := s.Snapshot("L", 12)
	if snap.Frozen != 400 || snap.Landlord != 300 || snap.Refundable != 300 || snap.Pending != 0 || snap.Awaiting != 300 {
		t.Fatalf("buckets frozen=%d landlord=%d refundable=%d pending=%d",
			snap.Frozen, snap.Landlord, snap.Refundable, snap.Pending)
	}
	checkConservation(t, snap)

	// Pre-close refund is rejected on a fresh lease (day 12 <= close 10).
	pre := deposit.NewService(cfg)
	_ = pre.CreateLease("P", 1000, 0)
	_ = pre.Checkout("P", 10)
	if _, err := pre.Refund("P", 12); !errors.Is(err, deposit.ErrState) {
		t.Fatalf("refund before close: %v", err)
	}
	rec, err := s.Refund("L", 17)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Amount != 300 || rec.Penalty.Sign() != 0 {
		t.Fatalf("deadline-day refund amount=%d penalty=%v", rec.Amount, rec.Penalty)
	}
	if _, err := s.Refund("L", 18); !errors.Is(err, deposit.ErrState) {
		t.Fatalf("refund when nothing refundable: %v", err)
	}

	late, _ := s.Snapshot("L", 50)
	if late.Frozen != 400 || late.PenaltyDue.Sign() != 0 {
		t.Fatalf("frozen=%d penalty=%v while frozen", late.Frozen, late.PenaltyDue)
	}
	checkConservation(t, late)

	for _, tc := range []struct {
		payDay int
		days   int64
	}{{18, 0}, {19, 1}, {27, 9}} {
		s := deposit.NewService(cfg)
		_ = s.CreateLease("L", 1000, 0)
		_ = s.Checkout("L", 0)
		rec, err := s.Refund("L", tc.payDay)
		if err != nil {
			t.Fatal(err)
		}
		ratEqual(t, "penalty", rec.Penalty, tc.days, 1)
	}
}

// Award below the frozen amount releases a fresh C-day tranche; other items'
// allocations never move; zero award releases everything.
func TestAdjudication(t *testing.T) {
	cfg := testCfg()
	s := deposit.NewService(cfg)
	_ = s.CreateLease("L", 1000, 0)
	_ = s.Checkout("L", 0)
	rent, _ := s.Declare("L", deposit.Rent, 400, 1)
	dmg, _ := s.Declare("L", deposit.Damage, 300, 1)
	_ = s.Dispute("L", rent, 12)
	if err := s.Adjudicate("L", rent, 250, 30); err != nil {
		t.Fatal(err)
	}
	snap, _ := s.Snapshot("L", 30)
	if snap.Landlord != 550 || snap.Frozen != 0 || snap.Refundable != 450 {
		t.Fatalf("landlord=%d frozen=%d refundable=%d", snap.Landlord, snap.Frozen, snap.Refundable)
	}
	if got := satMap(snap); got[dmg] != 300 {
		t.Fatalf("damage allocation changed: %d", got[dmg])
	}
	mustCode(t, s.Adjudicate("L", rent, 200, 31), deposit.ErrIllegalState)
	mustCode(t, s.Adjudicate("L", rent, 401, 31), deposit.ErrIllegalState)

	// Amount range checked on a still-adjudicable item in a fresh lease.
	sa := deposit.NewService(testCfg())
	_ = sa.CreateLease("L", 1000, 0)
	_ = sa.Checkout("L", 0)
	ra, _ := sa.Declare("L", deposit.Rent, 100, 1)
	_ = sa.Dispute("L", ra, 12)
	mustCode(t, sa.Adjudicate("L", ra, 101, 20), deposit.ErrAmountOutOfRange)
	mustCode(t, sa.Adjudicate("L", ra, -1, 20), deposit.ErrAmountOutOfRange)

	// The base 300 (overdue since day 18) must be refunded together with the
	// released 150; partial-only refunds are impossible.
	rec, err := s.Refund("L", 37)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Amount != 450 {
		t.Fatalf("refund amount=%d want 450", rec.Amount)
	}
	ratEqual(t, "base penalty at day 37", rec.Penalty, 300*19, 1000)

	// Late release refund: base 600 overdue 21 days + release 150 overdue 1.
	s2 := deposit.NewService(cfg)
	_ = s2.CreateLease("L", 1000, 0)
	_ = s2.Checkout("L", 0)
	r2, _ := s2.Declare("L", deposit.Rent, 400, 1)
	_ = s2.Dispute("L", r2, 12)
	_ = s2.Adjudicate("L", r2, 250, 30)
	rec2, err := s2.Refund("L", 39)
	if err != nil {
		t.Fatal(err)
	}
	if rec2.Amount != 750 {
		t.Fatalf("combined refund amount=%d", rec2.Amount)
	}
	ratEqual(t, "mixed penalty", rec2.Penalty, 600*21+150, 1000)

	s3 := deposit.NewService(cfg)
	_ = s3.CreateLease("L", 1000, 0)
	_ = s3.Checkout("L", 0)
	r3, _ := s3.Declare("L", deposit.Rent, 100, 1)
	_ = s3.Dispute("L", r3, 12)
	if err := s3.Adjudicate("L", r3, 0, 30); err != nil {
		t.Fatal(err)
	}
	snap3, _ := s3.Snapshot("L", 30)
	if snap3.Landlord != 0 || snap3.Refundable != 1000 || snap3.Receivable != 0 {
		t.Fatalf("zero award landlord=%d refundable=%d recv=%d",
			snap3.Landlord, snap3.Refundable, snap3.Receivable)
	}
	checkConservation(t, snap3)

	// Full uphold within the frozen amount.
	s4 := deposit.NewService(cfg)
	_ = s4.CreateLease("L", 1000, 0)
	_ = s4.Checkout("L", 0)
	r4, _ := s4.Declare("L", deposit.Rent, 100, 1)
	_ = s4.Dispute("L", r4, 12)
	if err := s4.Adjudicate("L", r4, 100, 30); err != nil {
		t.Fatal(err)
	}
	snap4, _ := s4.Snapshot("L", 30)
	if snap4.Landlord != 100 || snap4.Frozen != 0 || snap4.Refundable != 900 {
		t.Fatalf("full uphold landlord=%d frozen=%d refundable=%d",
			snap4.Landlord, snap4.Frozen, snap4.Refundable)
	}
	checkConservation(t, snap4)

	// Uphold an item the deposit did not fully cover: award above the frozen
	// part is receivable only; nothing extra leaves the deposit.
	s5 := deposit.NewService(cfg)
	_ = s5.CreateLease("L", 50, 0)
	_ = s5.Checkout("L", 0)
	r5, _ := s5.Declare("L", deposit.Rent, 100, 1)
	_ = s5.Dispute("L", r5, 12)
	if err := s5.Adjudicate("L", r5, 100, 30); err != nil {
		t.Fatal(err)
	}
	snap5, _ := s5.Snapshot("L", 30)
	if snap5.Landlord != 50 || snap5.Receivable != 50 || snap5.Refundable != 0 {
		t.Fatalf("uncov uphold landlord=%d recv=%d refundable=%d",
			snap5.Landlord, snap5.Receivable, snap5.Refundable)
	}
	checkConservation(t, snap5)
}

// Rejection precedence and the rule that rejections leave no trace or clock
// movement.
func TestRejectionOrdering(t *testing.T) {
	s := deposit.NewService(testCfg())

	mustCode(t, s.CreateLease("", 100, 5), deposit.ErrInvalidParameter)
	_, e := s.Declare("X", deposit.Category(9), 10, 5)
	mustCode(t, e, deposit.ErrInvalidParameter)
	mustCode(t, s.Checkout("X", -5), deposit.ErrLeaseNotFound)

	if err := s.CreateLease("L", 1000, 10); err != nil {
		t.Fatal(err)
	}
	mustCode(t, s.Checkout("L", 9), deposit.ErrClockBackward)
	_, e = s.Declare("L", deposit.Rent, 100, 10)
	mustCode(t, e, deposit.ErrNotCheckedOut)

	if err := s.Checkout("L", 10); err != nil {
		t.Fatal(err)
	}
	mustCode(t, s.Checkout("L", 10), deposit.ErrIllegalState)
	_, e = s.Declare("L", deposit.Rent, 0, 11)
	mustCode(t, e, deposit.ErrAmountOutOfRange)
	_, e = s.Declare("L", deposit.Rent, -5, 11)
	mustCode(t, e, deposit.ErrAmountOutOfRange)

	id, err := s.Declare("L", deposit.Rent, 200, 11)
	if err != nil {
		t.Fatal(err)
	}
	// A rejected late dispute must not advance the clock: the valid earlier
	// dispute afterwards must still succeed.
	mustCode(t, s.Dispute("L", id, 40), deposit.ErrDisputeLate)
	if err := s.Dispute("L", id, 22); err != nil {
		t.Fatalf("clock moved after rejected op: %v", err)
	}

	snap, _ := s.Snapshot("L", 22)
	if snap.Landlord != 0 || snap.Frozen != 200 || snap.Refundable != 800 {
		t.Fatalf("buckets landlord=%d frozen=%d refundable=%d",
			snap.Landlord, snap.Frozen, snap.Refundable)
	}
	checkConservation(t, snap)

	// Unknown deduction id is a state error; window checks come first.
	mustCode(t, s.Dispute("L", 999, 23), deposit.ErrIllegalState)
	mustCode(t, s.Adjudicate("L", 999, 10, 23), deposit.ErrIllegalState)

	// Pre-close refund is a state error on a fresh lease.
	sr := deposit.NewService(testCfg())
	_ = sr.CreateLease("R", 1000, 0)
	_ = sr.Checkout("R", 10)
	mustCode(t, sr.Revoke("R", 99, 12), deposit.ErrIllegalState)
	_, rerr := sr.Refund("R", 12)
	mustCode(t, rerr, deposit.ErrIllegalState)

	// Revoked item cannot be disputed (fresh lease to respect the clock).
	sv := deposit.NewService(testCfg())
	_ = sv.CreateLease("V", 1000, 0)
	_ = sv.Checkout("V", 10)
	id2, _ := sv.Declare("V", deposit.Cleaning, 50, 12)
	if err := sv.Revoke("V", id2, 12); err != nil {
		t.Fatal(err)
	}
	mustCode(t, sv.Dispute("V", id2, 22), deposit.ErrIllegalState)
}

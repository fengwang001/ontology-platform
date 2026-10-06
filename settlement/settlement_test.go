package settlement

import (
	"errors"
	"fmt"
	"testing"
)

func days(a, b int) []Day {
	out := make([]Day, 0, b-a+1)
	for d := a; d <= b; d++ {
		out = append(out, Day(d))
	}
	return out
}

func mustSystem(t *testing.T, ds []Day) *System {
	t.Helper()
	s, err := NewSystem(ds)
	if err != nil {
		t.Fatalf("NewSystem: %v", err)
	}
	return s
}

func addTxn(t *testing.T, s *System, now Day, m string, id string, d Day, amt Amount) {
	t.Helper()
	if err := s.AddTransaction(now, m, Transaction{ID: id, Day: d, Amount: amt}); err != nil {
		t.Fatalf("AddTransaction(%s day=%d amt=%d): %v", id, d, amt, err)
	}
}

func assertErrIs(t *testing.T, got, want error) {
	t.Helper()
	if !errors.Is(got, want) {
		t.Fatalf("error = %v, want %v", got, want)
	}
}

func settlePayouts(t *testing.T, s *System, now Day, m string, d Day) []Payout {
	t.Helper()
	p, _, err := s.Settle(now, m, d)
	if err != nil {
		t.Fatalf("Settle(through %d): %v", d, err)
	}
	return p
}

// TestSettleBoundaryExactAndOffByOne: a transaction on day t becomes
// settleable exactly on business day t+N, not one day earlier.
func TestSettleBoundaryExactAndOffByOne(t *testing.T) {
	// Sparse business calendar: business days are 0,2,4,6,... With N=2 a
	// transaction on business day 4 is settleable on business day 8 (t is the
	// 1st, day 6 the 2nd on/before t=6, so at t=6 it is still one business day
	// young), and exactly settleable at t=8.
	s := mustSystem(t, []Day{0, 2, 4, 6, 8, 10})
	if err := s.AddMerchant(10, "M", MerchantConfig{SettleDelayN: 2, ReserveBps: 0, ReserveHorizonH: 2}); err != nil {
		t.Fatal(err)
	}
	addTxn(t, s, 10, "M", "tx1", 4, 100)

	// Through business day 6: the 2nd business day on/before 6 is day 4, so a
	// transaction on day 4 is exactly at the boundary. First prove off-by-one
	// at day 4 itself: the 2nd business day on/before 4 is day 0.
	p := settlePayouts(t, s, 10, "M", 4)
	if p[2].Amount != 0 {
		t.Fatalf("day4 payout = %d, want zero (two business days young)", p[2].Amount)
	}
	p = settlePayouts(t, s, 10, "M", 6)
	if p[0].Day != 6 || p[0].Amount != 100 {
		t.Fatalf("day6 payout = %+v, want exactly 100 on the boundary", p)
	}
}

// TestReserveReleaseExactlyHthDay: a batch retained on day t releases on the
// H-th business day strictly after t, not earlier.
func TestReserveReleaseExactlyHthDay(t *testing.T) {
	s := mustSystem(t, days(0, 10))
	// N=1, 10% reserve, H=2.
	if err := s.AddMerchant(10, "M", MerchantConfig{1, 1000, 2}); err != nil {
		t.Fatal(err)
	}
	addTxn(t, s, 10, "M", "tx1", 2, 100)

	p := settlePayouts(t, s, 10, "M", 2)
	if p[2].Amount != 90 {
		t.Fatalf("day2 payout = %d, want 90", p[2].Amount)
	}
	// Days 3 and 4: nothing matures until the 2nd business day after day 2,
	// which is day 4.
	p = settlePayouts(t, s, 10, "M", 3)
	if p[0].Amount != 0 {
		t.Fatalf("day3 payout = %d, want 0 (reserve not yet due)", p[0].Amount)
	}
	p = settlePayouts(t, s, 10, "M", 4)
	if p[0].Amount != 10 {
		t.Fatalf("day4 payout = %d, want 10 (exact H-th day release)", p[0].Amount)
	}
	st, _ := s.State("M")
	if st.ReserveBalance != 0 {
		t.Fatalf("reserve after release = %d, want 0", st.ReserveBalance)
	}
}

// TestNegativeNetConsumesReservePartially: negative net first books matured
// release, then consumes older non-due batches in retention-day order; a
// partially consumed batch later releases only its remainder.
func TestNegativeNetConsumesReservePartially(t *testing.T) {
	s := mustSystem(t, days(0, 12))
	// N=1, reserve 10%, H=5.
	if err := s.AddMerchant(1, "M", MerchantConfig{1, 1000, 5}); err != nil {
		t.Fatal(err)
	}
	// Day 1: +100 -> retain 10 (releases day 6).
	addTxn(t, s, 2, "M", "a", 1, 100)
	settlePayouts(t, s, 2, "M", 1)
	// Day 2: +200 -> retain 20 (releases day 7).
	addTxn(t, s, 3, "M", "b", 2, 200)
	settlePayouts(t, s, 3, "M", 2)
	if st, _ := s.State("M"); st.ReserveBalance != 30 {
		t.Fatalf("reserve = %d, want 30", st.ReserveBalance)
	}
	// Day 3: -15 chargeback -> net -15; nothing is due; consume the day-1
	// batch (10) fully and 5 of the day-2 batch (20 -> 15).
	addTxn(t, s, 4, "M", "c", 3, -15)
	p := settlePayouts(t, s, 4, "M", 3)
	if p[0].Amount != 0 {
		t.Fatalf("day3 payout = %d, want 0", p[0].Amount)
	}
	st, _ := s.State("M")
	if st.ReserveBalance != 15 || len(st.Batches) != 1 || st.Batches[0].RetainDay != 2 || st.Batches[0].Consumed != 5 {
		t.Fatalf("after consumption state = %+v", st)
	}
	// Day 6: the day-1 batch is gone (fully consumed), nothing releases.
	p = settlePayouts(t, s, 6, "M", 6)
	if total := sumPayouts(p); total != 0 {
		t.Fatalf("days4-6 payout total = %d, want 0", total)
	}
	// Day 7: the partially consumed day-2 batch releases its remainder 15.
	p = settlePayouts(t, s, 7, "M", 7)
	if p[0].Amount != 15 {
		t.Fatalf("day7 payout = %d, want 15 (remainder released)", p[0].Amount)
	}
}

// TestNegativeCarryMultiDay: a deficit larger than reserve carries as a
// negative balance and can only be erased by later positive net or releases.
func TestNegativeCarryMultiDay(t *testing.T) {
	s := mustSystem(t, days(0, 8))
	// N=1, 0% reserve, H=2 (irrelevant without retention).
	if err := s.AddMerchant(1, "M", MerchantConfig{1, 0, 2}); err != nil {
		t.Fatal(err)
	}
	addTxn(t, s, 2, "M", "a", 1, -30)
	p := settlePayouts(t, s, 2, "M", 1)
	if p[0].Amount != 0 {
		t.Fatalf("day1 payout = %d, want 0", p[0].Amount)
	}
	st, _ := s.State("M")
	if st.NegativeCarry != -30 {
		t.Fatalf("carry = %d, want -30", st.NegativeCarry)
	}
	// Day 2: no transactions -> net stays -30, carries again, payout 0.
	p = settlePayouts(t, s, 3, "M", 2)
	if p[0].Amount != 0 {
		t.Fatalf("day2 payout = %d, want 0", p[0].Amount)
	}
	st, _ = s.State("M")
	if st.NegativeCarry != -30 {
		t.Fatalf("carry = %d, want still -30", st.NegativeCarry)
	}
	// Day 3: +10 -> net -20, still negative.
	addTxn(t, s, 4, "M", "b", 3, 10)
	settlePayouts(t, s, 4, "M", 3)
	st, _ = s.State("M")
	if st.NegativeCarry != -20 {
		t.Fatalf("carry = %d, want -20", st.NegativeCarry)
	}
	// Day 4: +50 -> net +30, paid out fully (0 bps).
	addTxn(t, s, 5, "M", "c", 4, 50)
	p = settlePayouts(t, s, 5, "M", 4)
	if p[0].Amount != 30 {
		t.Fatalf("day4 payout = %d, want 30", p[0].Amount)
	}
	st, _ = s.State("M")
	if st.NegativeCarry != 0 {
		t.Fatalf("carry = %d, want cleared", st.NegativeCarry)
	}
	// Invariant: total payout + reserve + carry == processed net (-30+10+50=30).
	if st.TotalPayout+st.ReserveBalance+st.NegativeCarry != 30 {
		t.Fatalf("invariant broken: %d + %d + %d", st.TotalPayout, st.ReserveBalance, st.NegativeCarry)
	}
}

// TestCatchUpEqualsDayByDay: with the same set of transactions loaded
// up front, one catch-up settlement through day D must produce exactly the
// same payouts, batches, reserve and carry as independently settling each
// business day in order. This is the property stated in the specification:
// per-day calls and a single catch-up are indistinguishable.
func TestCatchUpEqualsDayByDay(t *testing.T) {
	for seed := int64(0); seed < 40; seed++ {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			cfg, txns := genLoadedDataset(seed)
			const horizon = 30

			// System A: one catch-up through the final day.
			a := mustSystem(t, days(0, horizon))
			if err := a.AddMerchant(horizon, "M", cfg); err != nil {
				t.Fatal(err)
			}
			for _, tx := range txns {
				if err := a.AddTransaction(horizon, "M", tx); err != nil {
					t.Fatal(err)
				}
			}
			pa, _, err := a.Settle(horizon+1, "M", horizon)
			if err != nil {
				t.Fatal(err)
			}
			sa, _ := a.State("M")

			// System B: settle each business day one at a time.
			b := mustSystem(t, days(0, horizon))
			if err := b.AddMerchant(horizon, "M", cfg); err != nil {
				t.Fatal(err)
			}
			for _, tx := range txns {
				if err := b.AddTransaction(horizon, "M", tx); err != nil {
					t.Fatal(err)
				}
			}
			var pb []Payout
			for d := Day(0); d <= horizon; d++ {
				p, _, err := b.Settle(horizon+1, "M", d)
				if err != nil {
					t.Fatal(err)
				}
				pb = append(pb, p...)
			}
			sb, _ := b.State("M")

			if len(pa) != len(pb) {
				t.Fatalf("payout count %d vs %d", len(pa), len(pb))
			}
			for i := range pa {
				if pa[i] != pb[i] {
					t.Fatalf("payout[%d] catchup=%+v daybyday=%+v", i, pa[i], pb[i])
				}
			}
			assertStatesEqual(t, []MerchantState{sa}, []MerchantState{sb})
		})
	}
}

// TestSealedBookRejectsBackfill: after settlement reaches day d, a
// transaction dated before d is rejected and leaves no trace.
func TestSealedBookRejectsBackfill(t *testing.T) {
	s := mustSystem(t, days(0, 8))
	if err := s.AddMerchant(2, "M", MerchantConfig{1, 0, 2}); err != nil {
		t.Fatal(err)
	}
	addTxn(t, s, 3, "M", "a", 2, 100)
	settlePayouts(t, s, 3, "M", 2)
	before, _ := s.State("M")

	err := s.AddTransaction(4, "M", Transaction{ID: "late", Day: 1, Amount: 50})
	assertErrIs(t, err, ErrBookSealed)

	after, _ := s.State("M")
	assertStatesEqual(t, []MerchantState{after}, []MerchantState{before})
	// The rejected id must not be registered.
	err = s.AddTransaction(4, "M", Transaction{ID: "late", Day: 2, Amount: 7})
	if err != nil {
		t.Fatalf("rejected op left a trace: %v", err)
	}
}

// TestRejectedOpsLeaveNoTraceAndErrorPriority exercises every rejection class
// and confirms state (including the clock) is unchanged.
func TestRejectedOpsLeaveNoTraceAndErrorPriority(t *testing.T) {
	s := mustSystem(t, days(0, 8))
	if err := s.AddMerchant(5, "M", MerchantConfig{1, 0, 2}); err != nil {
		t.Fatal(err)
	}
	addTxn(t, s, 6, "M", "dup", 5, 10)
	settlePayouts(t, s, 6, "M", 6)
	snap, _ := s.State("M")
	clockSnap, _ := s.Clock()

	// Invalid argument beats clock rollback.
	assertErrIs(t, s.AddTransaction(1, "M", Transaction{ID: ""}), ErrInvalidArgument)
	// Clock rollback beats missing merchant.
	assertErrIs(t, s.AddTransaction(5, "Z", Transaction{ID: "x", Day: 4, Amount: 1}), ErrClockRolledBack)
	// Missing merchant beats duplicate / date / sealed.
	assertErrIs(t, s.AddTransaction(6, "Z", Transaction{ID: "x", Day: 4, Amount: 1}), ErrMerchantMissing)
	// Duplicate beats invalid date beats sealed.
	assertErrIs(t, s.AddTransaction(6, "M", Transaction{ID: "dup", Day: 9, Amount: 1}), ErrDuplicateTxnID)
	assertErrIs(t, s.AddTransaction(6, "M", Transaction{ID: "new", Day: 9, Amount: 1}), ErrInvalidDate)
	assertErrIs(t, s.AddTransaction(7, "M", Transaction{ID: "new2", Day: 4, Amount: 1}), ErrBookSealed)

	// Settlement priority chain.
	var err error
	_, _, err = s.Settle(5, "M", 7)
	assertErrIs(t, err, ErrClockRolledBack)
	_, _, err = s.Settle(6, "Z", 7)
	assertErrIs(t, err, ErrMerchantMissing)
	_, _, err = s.Settle(6, "M", 70)
	assertErrIs(t, err, ErrNonBusinessDay) // non-business beats duplicate
	_, _, err = s.Settle(6, "M", 6)
	assertErrIs(t, err, ErrDuplicateSettle)

	got, _ := s.State("M")
	assertStatesEqual(t, []MerchantState{got}, []MerchantState{snap})
	if clockAfter, _ := s.Clock(); clockAfter != clockSnap {
		t.Fatalf("clock changed after rejected ops: %d -> %d", clockSnap, clockAfter)
	}
}

// TestNonBusinessDayRejection verifies the settle guard independently.
func TestNonBusinessDayRejection(t *testing.T) {
	s := mustSystem(t, []Day{0, 2, 4})
	if err := s.AddMerchant(4, "M", MerchantConfig{1, 0, 2}); err != nil {
		t.Fatal(err)
	}
	_, _, err := s.Settle(4, "M", 3)
	assertErrIs(t, err, ErrNonBusinessDay)
}

func sumPayouts(ps []Payout) Amount {
	var t Amount
	for _, p := range ps {
		t += p.Amount
	}
	return t
}

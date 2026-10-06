package chargeback

import (
	"errors"
	"sync"
	"testing"
)

func TestMultiCaseExhaustDisputable(t *testing.T) {
	s := New(testCfg())
	regTxn(t, s, "T", "c", "m", 100, 0)
	must(t, s.File(0, "T", ReasonFraud, 40, "C1"))
	must(t, s.File(1, "T", ReasonNotReceived, 60, "C2"))
	d, _ := s.Disputable(1, "T")
	if d != 0 {
		t.Fatalf("disputable: %d", d)
	}
	wantErr(t, s.File(1, "T", ReasonFraud, 1, "C3"), ErrAmountExceeded)

	// Merchant win frees occupancy of the closed amount.
	must(t, s.Defend(1, "C1"))
	must(t, s.AcceptDefense(1, "C1"))
	d, _ = s.Disputable(1, "T")
	if d != 40 {
		t.Fatalf("disputable after merchant win: %d", d)
	}
}

func TestMerchantWinBlocksSameReasonOnly(t *testing.T) {
	s := New(testCfg())
	regTxn(t, s, "T", "c", "m", 100, 0)
	must(t, s.File(0, "T", ReasonFraud, 30, "C1"))
	must(t, s.Defend(0, "C1"))
	must(t, s.AcceptDefense(0, "C1"))
	wantErr(t, s.File(1, "T", ReasonFraud, 1, "C2"), ErrDuplicateFiling)
	must(t, s.File(1, "T", ReasonNotReceived, 10, "C3"))
}

func TestDuplicateBasisRangeAndOccupancy(t *testing.T) {
	// Range boundary: settlement diff exactly DuplicateRangeDays is valid;
	// one day more has no basis.
	s2 := New(testCfg())
	regTxn(t, s2, "T0", "c", "m", 50, 0)
	regTxn(t, s2, "T3", "c", "m", 50, 3)
	regTxn(t, s2, "T5", "c", "m", 50, 5)
	must(t, s2.File(5, "T5", ReasonDuplicate, 50, "D1"))
	// Occupied basis: T0 supports live D1, so a duplicate on T3 is rejected.
	wantErr(t, s2.File(5, "T3", ReasonDuplicate, 50, "D2"), ErrNoBasis)
	regTxn(t, s2, "T11", "c", "m", 50, 11)
	wantErr(t, s2.File(11, "T11", ReasonDuplicate, 50, "D3"), ErrNoBasis)

	// Issuer-win default ending keeps the basis occupied (no release).
	st, out, _ := s2.CaseStatus(13, "D1")
	if st != StatusClosed || out != OutcomeIssuerWin {
		t.Fatalf("D1 day13: %v %v", st, out)
	}
	wantErr(t, s2.File(13, "T3", ReasonDuplicate, 50, "D2b"), ErrNoBasis)

	// Merchant-win ending releases the basis so it can support another case.
	s3 := New(testCfg())
	regTxn(t, s3, "T0", "c", "m", 50, 0)
	regTxn(t, s3, "T3", "c", "m", 50, 3)
	regTxn(t, s3, "T5", "c", "m", 50, 5)
	must(t, s3.File(5, "T5", ReasonDuplicate, 50, "E1"))
	must(t, s3.Defend(5, "E1"))
	must(t, s3.AcceptDefense(5, "E1"))
	must(t, s3.File(6, "T3", ReasonDuplicate, 50, "E2"))
}

func TestPreArbitrationMoney(t *testing.T) {
	s := New(testCfg())
	regTxn(t, s, "T", "c", "m", 100, 0)
	must(t, s.CreditMerchant(0, "m", 1000))
	must(t, s.File(0, "T", ReasonFraud, 40, "C"))
	must(t, s.Defend(0, "C"))
	must(t, s.PreArbitration(1, "C"))
	wantErr(t, s.AcceptDefense(2, "C"), ErrStatusNotAllowed)
	wantErr(t, s.Defend(2, "C"), ErrStatusNotAllowed)
	must(t, s.Rule(2, "C", OutcomeMerchantWin))
	if bal, _ := s.MerchantBalance(2, "m"); bal != 1000 {
		t.Fatalf("merchant bal %d", bal)
	}
	if iss, _ := s.IssuerAccount(2); iss != -9 {
		t.Fatalf("issuer %d", iss)
	}

	s2 := New(testCfg())
	regTxn(t, s2, "T", "c", "m2", 100, 0)
	must(t, s2.CreditMerchant(0, "m2", 30))
	must(t, s2.File(0, "T", ReasonFraud, 40, "C"))
	must(t, s2.Defend(0, "C"))
	must(t, s2.PreArbitration(1, "C"))
	must(t, s2.Rule(50, "C", OutcomeIssuerWin))
	if bal, _ := s2.MerchantBalance(50, "m2"); bal != -19 {
		t.Fatalf("merchant bal %d", bal)
	}
	if iss, _ := s2.IssuerAccount(50); iss != 40 {
		t.Fatalf("issuer %d", iss)
	}
	wantErr(t, s2.Rule(51, "C", OutcomeMerchantWin), ErrStatusNotAllowed)
}

func TestClockRollbackAndRejectedNoTrace(t *testing.T) {
	s := New(testCfg())
	regTxn(t, s, "T", "c", "m", 100, 10)
	must(t, s.CreditMerchant(20, "m", 1000))
	must(t, s.File(20, "T", ReasonFraud, 40, "C"))

	before, _ := s.MerchantBalance(20, "m")
	wantErr(t, s.File(19, "T", ReasonFraud, 1, "X"), ErrClockRollback)
	after, _ := s.MerchantBalance(20, "m")
	if before != after {
		t.Fatalf("money changed: %d -> %d", before, after)
	}
	if _, _, err := s.CaseStatus(20, "X"); !errors.Is(err, ErrCaseNotFound) {
		t.Fatalf("rejected case left a trace: %v", err)
	}

	// File error precedence.
	wantErr(t, s.File(5, "", ReasonFraud, 1, "Y"), ErrInvalidArgument)
	wantErr(t, s.File(5, "T", ReasonFraud, 1, "Y"), ErrClockRollback)
	wantErr(t, s.File(21, "NOPE", ReasonFraud, 1, "Y"), ErrTransactionNotFound)
	// window beats duplicate-reason: expired fraud on a merchant-win reason.
	s2 := New(testCfg())
	regTxn(t, s2, "T", "c", "m", 100, 0)
	must(t, s2.File(0, "T", ReasonFraud, 30, "C1"))
	must(t, s2.Defend(0, "C1"))
	must(t, s2.AcceptDefense(0, "C1"))
	wantErr(t, s2.File(11, "T", ReasonFraud, 1, "C2"), ErrWindowExpired)

	// Other-op precedence: invalid > clock > missing > status.
	wantErr(t, s.Defend(0, ""), ErrInvalidArgument)
	wantErr(t, s.Defend(1, "C"), ErrClockRollback)
	wantErr(t, s.AcceptDefense(21, "GHOST"), ErrCaseNotFound)
	wantErr(t, s.AcceptDefense(21, "C"), ErrStatusNotAllowed) // C closed issuer-win
}

func TestConservationInvariant(t *testing.T) {
	s := New(testCfg())
	regTxn(t, s, "T1", "c", "m", 100, 0)
	regTxn(t, s, "T2", "c", "m", 100, 1)
	must(t, s.CreditMerchant(1, "m", 500))

	// Checkpoint at day 0 from a fresh replay so earlier-day queries do not
	// fix the monotonic clock before later operations are accepted.
	chk := func(day int, want int64) {
		c := New(testCfg())
		regTxn(t, c, "T1", "c", "m", 100, 0)
		regTxn(t, c, "T2", "c", "m", 100, 1)
		must(t, c.CreditMerchant(1, "m", 500))
		mb, _ := c.MerchantBalance(day, "m")
		iss, _ := c.IssuerAccount(day)
		pend, _ := c.PendingHeld(day)
		if got := mb + iss + pend; got != want {
			t.Fatalf("day %d: %d+%d+%d=%d want %d", day, mb, iss, pend, got, want)
		}
	}
	chk(1, 500)

	must(t, s.File(1, "T1", ReasonFraud, 100, "A"))
	must(t, s.File(1, "T2", ReasonNotReceived, 50, "B"))
	must(t, s.Defend(2, "B"))

	for _, day := range []int{2, 3, 5, 6, 20, 50} {
		mb, _ := s.MerchantBalance(day, "m")
		iss, _ := s.IssuerAccount(day)
		pend, _ := s.PendingHeld(day)
		// Fees excluded: merchant + issuer + pending == total credited.
		if got := mb + iss + pend; got != 500 {
			t.Fatalf("day %d: conservation broken: %d+%d+%d=%d", day, mb, iss, pend, got)
		}
	}
}

func TestReplayDeterminism(t *testing.T) {
	steps := func(s *System) {
		regTxn(t, s, "T", "c", "m", 100, 0)
		must(t, s.CreditMerchant(0, "m", 500))
		must(t, s.File(0, "T", ReasonFraud, 60, "C"))
		must(t, s.Defend(2, "C"))
		must(t, s.PreArbitration(3, "C"))
		must(t, s.Rule(9, "C", OutcomeMerchantWin))
	}
	a := New(testCfg())
	b := New(testCfg())
	steps(a)
	steps(b)
	for day := 0; day <= 12; day++ {
		ba, _ := a.MerchantBalance(day, "m")
		bb, _ := b.MerchantBalance(day, "m")
		if ba != bb {
			t.Fatalf("day %d balances differ %d vs %d", day, ba, bb)
		}
		ia, _ := a.IssuerAccount(day)
		ib, _ := b.IssuerAccount(day)
		if ia != ib {
			t.Fatalf("day %d issuer differs %d vs %d", day, ia, ib)
		}
	}
}

func TestConcurrentLinearizability(t *testing.T) {
	s := New(testCfg())
	regTxn(t, s, "T", "c", "m", 100, 0)
	must(t, s.CreditMerchant(0, "m", 1000))

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			day := 1 + (i % 3)
			_ = s.File(day, "T", ReasonNotReceived, 10, caseName(i))
		}(i)
	}
	wg.Wait()
	// No money was created or destroyed; pending <= transaction amount.
	bal, _ := s.MerchantBalance(5, "m")
	pend, _ := s.PendingHeld(5)
	iss, _ := s.IssuerAccount(5)
	if got := bal + pend + iss; got != 1000 {
		t.Fatalf("conservation after concurrent ops: %d", got)
	}
	d, _ := s.Disputable(5, "T")
	if d < 0 || d > 100 {
		t.Fatalf("disputable out of range: %d", d)
	}
}

func caseName(i int) string {
	const digits = "abcdefghijklmnopqrstuvwxyz"
	if i < len(digits) {
		return "K" + string(digits[i])
	}
	return "K" + string(rune('A'+i%26)) + string(rune('0'+i/26))
}

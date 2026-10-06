package chargeback

import (
	"errors"
	"testing"
)

func testCfg() Config {
	return Config{
		FraudWindowDays:       10,
		NotReceivedWindowDays: 20,
		DuplicateWindowDays:   15,
		DuplicateRangeDays:    5,
		DefenseDays:           7,
		ReviewDays:            3,
		ArbitrationFee:        9,
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func wantErr(t *testing.T, got, want error) {
	t.Helper()
	if !errors.Is(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func regTxn(t *testing.T, s *System, id, card, merch string, amt int64, settle int) {
	t.Helper()
	must(t, s.RegisterTransaction(settle, Transaction{
		ID: id, CardID: card, MerchantID: merch, Amount: amt, SettlementDay: settle,
	}))
}

func TestFileWindowBoundary(t *testing.T) {
	s := New(testCfg())
	regTxn(t, s, "T", "c", "m", 100, 100)
	must(t, s.File(110, "T", ReasonFraud, 10, "C1")) // diff == window allowed

	s2 := New(testCfg())
	regTxn(t, s2, "T", "c", "m", 100, 100)
	wantErr(t, s2.File(111, "T", ReasonFraud, 10, "C1"), ErrWindowExpired)

	// NR window 20: diff 20 allowed, 21 expired.
	s3 := New(testCfg())
	regTxn(t, s3, "T", "c", "m", 100, 100)
	must(t, s3.File(120, "T", ReasonNotReceived, 10, "C1"))
	s4 := New(testCfg())
	regTxn(t, s4, "T", "c", "m", 100, 100)
	wantErr(t, s4.File(121, "T", ReasonNotReceived, 10, "C1"), ErrWindowExpired)
}

func TestDefenseAndReviewDeadlineBoundary(t *testing.T) {
	s := New(testCfg())
	regTxn(t, s, "T", "c", "m", 100, 0)
	must(t, s.CreditMerchant(0, "m", 1000))
	must(t, s.File(0, "T", ReasonFraud, 40, "C"))

	st, _, err := s.CaseStatus(7, "C") // file + R is still the last valid day
	must(t, err)
	if st != StatusAwaitingDefense {
		t.Fatalf("day 7 status %v", st)
	}
	must(t, s.Defend(7, "C"))

	st, _, _ = s.CaseStatus(10, "C") // defense + P last valid day
	if st != StatusAwaitingReview {
		t.Fatalf("day 10 status %v", st)
	}
	st, out, _ := s.CaseStatus(11, "C") // default accept
	if st != StatusClosed || out != OutcomeMerchantWin {
		t.Fatalf("day 11 status=%v outcome=%v", st, out)
	}
	bal, _ := s.MerchantBalance(11, "m")
	if bal != 1000 {
		t.Fatalf("balance after default accept: %d", bal)
	}
	wantErr(t, s.AcceptDefense(11, "C"), ErrStatusNotAllowed)
	wantErr(t, s.PreArbitration(11, "C"), ErrStatusNotAllowed)
	wantErr(t, s.Rule(12, "C", OutcomeIssuerWin), ErrStatusNotAllowed)
}

func TestDefenseDefaultIssuerWin(t *testing.T) {
	s := New(testCfg())
	regTxn(t, s, "T", "c", "m", 100, 0)
	must(t, s.CreditMerchant(0, "m", 1000))
	must(t, s.File(0, "T", ReasonFraud, 40, "C"))

	st, out, _ := s.CaseStatus(8, "C")
	if st != StatusClosed || out != OutcomeIssuerWin {
		t.Fatalf("day 8 status=%v outcome=%v", st, out)
	}
	bal, _ := s.MerchantBalance(8, "m")
	if bal != 960 {
		t.Fatalf("balance %d", bal)
	}
	iss, _ := s.IssuerAccount(8)
	if iss != 40 {
		t.Fatalf("issuer %d", iss)
	}
	d, _ := s.Disputable(8, "T")
	if d != 60 {
		t.Fatalf("disputable after issuer win: %d", d)
	}
	p, _ := s.PendingHeld(8)
	if p != 0 {
		t.Fatalf("pending: %d", p)
	}
}

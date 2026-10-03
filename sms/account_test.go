package sms

import (
	"errors"
	"testing"
)

func newTestMeter(t *testing.T, P, T0, p1, p2 int64, MI int) *Meter {
	t.Helper()
	meter, err := New(P, T0, p1, p2, MI)
	if err != nil {
		t.Fatal(err)
	}
	return meter
}

func TestSpecExample(t *testing.T) {
	meter := newTestMeter(t, 100, 3, 10, 6, 200)
	if err := meter.Deposit("alice", 1000); err != nil {
		t.Fatal(err)
	}

	first, err := meter.Send("alice", repeatRune('a', 161), false, 5)
	if err != nil || first != (SendResult{Encoding: "GSM", Segments: 2, Cost: 20}) {
		t.Fatalf("first = %+v, %v", first, err)
	}

	text := repeatRune('a', 152) + "{" + repeatRune('b', 10)
	second, err := meter.Send("alice", text, true, 7)
	if err != nil || second != (SendResult{Encoding: "GSM", Segments: 2, Cost: 32}) {
		t.Fatalf("second = %+v, %v", second, err)
	}
	if got := meter.accounts["alice"]; got.balance != 948 || got.used != 4 || got.first != 3 || got.period != 0 {
		t.Fatalf("state = %+v, want balance 948 used 4 first 3 period 0", got)
	}

	third, err := meter.Send("alice", "a", false, 120)
	if err != nil || third != (SendResult{Encoding: "GSM", Segments: 1, Cost: 10}) {
		t.Fatalf("third = %+v, %v", third, err)
	}
	if got := meter.accounts["alice"]; got.balance != 938 || got.used != 1 || got.first != 4 || got.period != 1 {
		t.Fatalf("state = %+v, want carried T=4", got)
	}

	gapped := newTestMeter(t, 100, 3, 10, 6, 200)
	if err := gapped.Deposit("alice", 1000); err != nil {
		t.Fatal(err)
	}
	if _, err := gapped.Send("alice", repeatRune('a', 4), false, 5); err != nil {
		t.Fatal(err)
	}
	if _, err := gapped.Send("alice", "a", false, 350); err != nil {
		t.Fatal(err)
	}
	if got := gapped.accounts["alice"]; got.first != 3 || got.period != 3 {
		t.Fatalf("gapped first = %d, period = %d, want 3 and 3", got.first, got.period)
	}
}

func TestCarriedCapacityThresholds(t *testing.T) {
	for _, used := range []int64{3, 4} {
		meter := newTestMeter(t, 100, 3, 10, 6, 200)
		if err := meter.Deposit("a", 1_000_000); err != nil {
			t.Fatal(err)
		}
		for index := int64(0); index < used; index++ {
			if _, err := meter.Send("a", "a", false, index); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := meter.Send("a", "a", false, 100); err != nil {
			t.Fatal(err)
		}
		want := int64(3)
		if used == 4 {
			want = 4
		}
		if got := meter.accounts["a"].first; got != want {
			t.Fatalf("used %d carried first = %d, want %d", used, got, want)
		}
	}
}

func TestTierBoundaryAndSingleInternationalRounding(t *testing.T) {
	meter := newTestMeter(t, 100, 1, 10, 6, 101)
	if err := meter.Deposit("a", 1000); err != nil {
		t.Fatal(err)
	}

	quote, err := meter.Quote("a", repeatRune('a', 161), true, 0)
	if err != nil {
		t.Fatal(err)
	}
	if quote.Cost != 17 {
		t.Fatalf("whole-message rounding cost = %d, want 17; per-segment rounding would be 18", quote.Cost)
	}
	sent, err := meter.Send("a", repeatRune('a', 161), true, 0)
	if err != nil || sent != quote {
		t.Fatalf("sent = %+v, %v; quote = %+v", sent, err, quote)
	}
}

func TestQuoteConsistencyAndRejectionsDoNotMutate(t *testing.T) {
	meter := newTestMeter(t, 100, 3, 10, 6, 200)
	if err := meter.Deposit("a", 1000); err != nil {
		t.Fatal(err)
	}
	if _, err := meter.Send("a", repeatRune('a', 161), false, 5); err != nil {
		t.Fatal(err)
	}

	quote, err := meter.Quote("a", "x", true, 6)
	if err != nil {
		t.Fatal(err)
	}
	sent, err := meter.Send("a", "x", true, 6)
	if err != nil || quote != sent {
		t.Fatalf("quote = %+v, sent = %+v, %v", quote, sent, err)
	}

	snapshot := *meter.accounts["a"]
	maxNow := meter.maxNow
	_, err = meter.Send("a", repeatRune('a', 10*gsmPartUnits+1), false, 7)
	if !errors.Is(err, ErrTooManySegments) {
		t.Fatalf("long error = %v", err)
	}
	_, err = meter.Quote("a", "expensive", false, 4)
	if !errors.Is(err, ErrClockRolledBack) {
		t.Fatalf("quote rollback error = %v", err)
	}
	_, err = meter.Send("missing", "a", false, 4)
	if !errors.Is(err, ErrAccountNotFound) {
		t.Fatalf("missing account error = %v", err)
	}
	if got := *meter.accounts["a"]; got != snapshot || meter.maxNow != maxNow {
		t.Fatalf("state changed after rejection: record %+v (want %+v), maxNow %d (want %d)", got, snapshot, meter.maxNow, maxNow)
	}
}

func TestExactBalanceAndDepositValidation(t *testing.T) {
	meter := newTestMeter(t, 100, 0, 7, 7, 100)
	if err := meter.Deposit("a", 14); err != nil {
		t.Fatal(err)
	}
	result, err := meter.Send("a", repeatRune('a', 161), false, 0)
	if err != nil || result.Cost != 14 {
		t.Fatalf("result = %+v, %v", result, err)
	}
	if meter.accounts["a"].balance != 0 {
		t.Fatalf("balance = %d, want 0", meter.accounts["a"].balance)
	}

	if err := meter.Deposit("", 1); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("empty account error = %v", err)
	}
	if err := meter.Deposit("b", 0); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("zero deposit error = %v", err)
	}
	if err := meter.Deposit("a", 1_000_000_000_001); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("large deposit error = %v", err)
	}
	if meter.accounts["a"].balance != 0 {
		t.Fatalf("rejected deposit changed balance to %d", meter.accounts["a"].balance)
	}
}

func TestConstructionAndRejectionOrder(t *testing.T) {
	if _, err := New(0, 0, 0, 0, 100); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("New invalid period error = %v", err)
	}
	if _, err := New(1, 0, 0, 0, 99); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("New invalid multiplier error = %v", err)
	}

	meter := newTestMeter(t, 100, 3, 10, 6, 200)
	if err := meter.Deposit("a", 1); err != nil {
		t.Fatal(err)
	}
	_, err := meter.Send("a", "", false, -1)
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("invalid now before text error = %v", err)
	}
	if _, err := meter.Send("a", "", false, 0); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("empty text error = %v", err)
	}
	if _, err := meter.Send("b", "a", false, 0); !errors.Is(err, ErrAccountNotFound) {
		t.Fatalf("account not found before clock error = %v", err)
	}

	rolledBack := newTestMeter(t, 100, 3, 10, 6, 200)
	if err := rolledBack.Deposit("a", 1_000_000); err != nil {
		t.Fatal(err)
	}
	if _, err := rolledBack.Send("a", "a", false, 10); err != nil {
		t.Fatal(err)
	}
	if _, err := rolledBack.Send("a", repeatRune('a', 10*gsmPartUnits+1), false, 9); !errors.Is(err, ErrClockRolledBack) {
		t.Fatalf("clock rollback before too many segments error = %v", err)
	}
}

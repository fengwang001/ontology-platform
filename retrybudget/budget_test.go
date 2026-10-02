package retrybudget

import (
	"errors"
	"testing"
)

func mustNew(t *testing.T, wd, wc, d, c, r, mx, cm int64) *Budget {
	t.Helper()
	b, err := New(wd, wc, d, c, r, mx, cm)
	if err != nil {
		t.Fatalf("New(%d,%d,%d,%d,%d,%d,%d): %v", wd, wc, d, c, r, mx, cm, err)
	}
	return b
}

func mustRequest(t *testing.T, b *Budget, now int64) {
	t.Helper()
	if err := b.Request(now); err != nil {
		t.Fatalf("Request(%d): %v", now, err)
	}
}

func mustBalance(t *testing.T, b *Budget, now int64) int64 {
	t.Helper()
	bal, err := b.Balance(now)
	if err != nil {
		t.Fatalf("Balance(%d): %v", now, err)
	}
	return bal
}

func tryRetry(t *testing.T, b *Budget, now int64) bool {
	t.Helper()
	ok, err := b.TryRetry(now)
	if err != nil {
		t.Fatalf("TryRetry(%d): %v", now, err)
	}
	return ok
}

// TestExampleScenario replays the worked example from the specification:
// Wd=10, Wc=20, D=1, C=2, R=3, Mx=3, Cm=6.
func TestExampleScenario(t *testing.T) {
	b := mustNew(t, 10, 20, 1, 2, 3, 3, 6)

	if !tryRetry(t, b, 0) { // k=0, cost=2, balance 3 >= 2
		t.Fatal("TryRetry(0) should be allowed")
	}
	mustRequest(t, b, 1)
	mustRequest(t, b, 2)
	mustRequest(t, b, 3)
	if !tryRetry(t, b, 3) { // k=1, cost=4, balance 3+3-2=4 >= 4
		t.Fatal("TryRetry(3) should be allowed")
	}
	if tryRetry(t, b, 4) { // k=2, cost=6, balance 3+3-6=0 < 6
		t.Fatal("TryRetry(4) should be rejected")
	}
	for now := int64(5); now <= 10; now++ {
		mustRequest(t, b, now)
	}
	if got := mustBalance(t, b, 10); got != 3 { // 9 valid deposits capped at 6
		t.Fatalf("Balance(10) = %d, want 3", got)
	}
	if got := mustBalance(t, b, 11); got != 3 { // deposit@1 expired, 8 valid, still capped
		t.Fatalf("Balance(11) = %d, want 3", got)
	}
	if got := mustBalance(t, b, 13); got != 3 { // deposits@2,3 expired, 6 valid
		t.Fatalf("Balance(13) = %d, want 3", got)
	}
	if got := mustBalance(t, b, 15); got != 2 { // deposit@5 expired, 5 valid: 3+5-6
		t.Fatalf("Balance(15) = %d, want 2", got)
	}
	if tryRetry(t, b, 15) { // k=2, cost=6 > 2
		t.Fatal("TryRetry(15) should be rejected")
	}
	if got := mustBalance(t, b, 20); got != -1 { // all deposits and withdrawal@0 expired
		t.Fatalf("Balance(20) = %d, want -1", got)
	}
	if tryRetry(t, b, 20) { // k=1, cost=4 > -1
		t.Fatal("TryRetry(20) should be rejected")
	}
	if !tryRetry(t, b, 23) { // withdrawal@3 expired, k=0, cost=2 <= 3
		t.Fatal("TryRetry(23) should be allowed")
	}
}

// TestDepositExpiryBoundary: a deposit at t is valid at t+Wd-1 and expired
// exactly at t+Wd.
func TestDepositExpiryBoundary(t *testing.T) {
	b := mustNew(t, 10, 100, 1, 1, 0, 1, 10)
	mustRequest(t, b, 5)
	if got := mustBalance(t, b, 14); got != 1 { // 5+10 > 14: still valid
		t.Fatalf("Balance(14) = %d, want 1", got)
	}
	if got := mustBalance(t, b, 15); got != 0 { // 5+10 == 15: expired
		t.Fatalf("Balance(15) = %d, want 0", got)
	}
}

// TestWithdrawalExpiryBoundary: a withdrawal at t is valid at t+Wc-1 and
// expired exactly at t+Wc.
func TestWithdrawalExpiryBoundary(t *testing.T) {
	b := mustNew(t, 100, 10, 1, 1, 5, 1, 10)
	if !tryRetry(t, b, 0) { // cost=1, balance 5
		t.Fatal("TryRetry(0) should be allowed")
	}
	if got := mustBalance(t, b, 9); got != 4 { // 0+10 > 9: still valid
		t.Fatalf("Balance(9) = %d, want 4", got)
	}
	if got := mustBalance(t, b, 10); got != 5 { // 0+10 == 10: expired
		t.Fatalf("Balance(10) = %d, want 5", got)
	}
}

// TestUnequalWindowsExpireSeparately: deposits expire on Wd while
// withdrawals expire on Wc, independently.
func TestUnequalWindowsExpireSeparately(t *testing.T) {
	b := mustNew(t, 3, 100, 1, 1, 0, 1, 10)
	mustRequest(t, b, 0)
	if !tryRetry(t, b, 0) { // cost=1, balance 0+1=1
		t.Fatal("TryRetry(0) should be allowed")
	}
	if got := mustBalance(t, b, 3); got != -1 { // deposit gone, withdrawal stays
		t.Fatalf("Balance(3) = %d, want -1", got)
	}
	if got := mustBalance(t, b, 100); got != 0 { // withdrawal expired too
		t.Fatalf("Balance(100) = %d, want 0", got)
	}
}

// TestBalanceExactlyEqualsCost: balance == cost allows, one less rejects.
func TestBalanceExactlyEqualsCost(t *testing.T) {
	eq := mustNew(t, 100, 100, 1, 5, 5, 1, 10)
	if !tryRetry(t, eq, 0) { // cost=5, balance=5
		t.Fatal("balance exactly equal to cost should be allowed")
	}
	oneLess := mustNew(t, 100, 100, 1, 5, 4, 1, 10)
	if tryRetry(t, oneLess, 0) { // cost=5, balance=4
		t.Fatal("balance one less than cost should be rejected")
	}
}

// TestEscalatingPricingAndCap: cost climbs C*1, C*2, ... and stops at C*Mx.
func TestEscalatingPricingAndCap(t *testing.T) {
	b := mustNew(t, 1000, 1000, 1, 2, 100, 3, 10)
	wantCosts := []int64{2, 4, 6, 6, 6} // multipliers 1,2,3 then capped at Mx=3
	balance := int64(100)
	for i, want := range wantCosts {
		now := int64(i)
		if !tryRetry(t, b, now) {
			t.Fatalf("TryRetry(%d) should be allowed", now)
		}
		got := b.withdrawals[len(b.withdrawals)-1].cost
		if got != want {
			t.Fatalf("retry %d frozen cost = %d, want %d", i, got, want)
		}
		balance -= want
		if got := mustBalance(t, b, now); got != balance {
			t.Fatalf("Balance(%d) = %d, want %d", now, got, balance)
		}
	}
}

// TestFrozenCostNotRepriced: a withdrawal keeps its frozen cost even after
// k (the number of valid withdrawals) drops.
func TestFrozenCostNotRepriced(t *testing.T) {
	b := mustNew(t, 100, 5, 1, 1, 100, 10, 10)
	tryRetry(t, b, 0) // k=0, cost=1
	tryRetry(t, b, 1) // k=1, cost=2
	tryRetry(t, b, 2) // k=2, cost=3 (frozen)
	// At now=6 the withdrawals at 0 and 1 have expired, k drops to 1, but
	// the event at 2 still carries its frozen cost 3.
	if got := mustBalance(t, b, 6); got != 97 { // 100-3, not 100-2
		t.Fatalf("Balance(6) = %d, want 97 (frozen cost must not be repriced)", got)
	}
	if !tryRetry(t, b, 6) { // k=1, cost=2
		t.Fatal("TryRetry(6) should be allowed")
	}
	if got := mustBalance(t, b, 6); got != 95 { // 100-3-2
		t.Fatalf("Balance(6) after retry = %d, want 95", got)
	}
	if got := mustBalance(t, b, 7); got != 98 { // event@2 expired: 100-2
		t.Fatalf("Balance(7) = %d, want 98", got)
	}
}

// TestDepositCap: deposits beyond Cm add nothing; the balance only starts
// dropping once the valid count falls below Cm.
func TestDepositCap(t *testing.T) {
	b := mustNew(t, 10, 1000, 1, 1, 0, 1, 3)
	for now := int64(0); now <= 5; now++ {
		mustRequest(t, b, now)
	}
	if got := mustBalance(t, b, 5); got != 3 { // 6 valid, capped at 3
		t.Fatalf("Balance(5) = %d, want 3", got)
	}
	mustRequest(t, b, 6)
	if got := mustBalance(t, b, 6); got != 3 { // 7 valid, still capped
		t.Fatalf("Balance(6) = %d, want 3", got)
	}
	if got := mustBalance(t, b, 10); got != 3 { // 6 valid, still capped
		t.Fatalf("Balance(10) = %d, want 3", got)
	}
	if got := mustBalance(t, b, 13); got != 3 { // 3 valid, exactly at cap
		t.Fatalf("Balance(13) = %d, want 3", got)
	}
	if got := mustBalance(t, b, 14); got != 2 { // 2 valid, below cap: drops
		t.Fatalf("Balance(14) = %d, want 2", got)
	}
}

// TestNegativeBalance: deposits expiring while withdrawals stay valid can
// drive the balance negative.
func TestNegativeBalance(t *testing.T) {
	b := mustNew(t, 5, 100, 1, 2, 1, 2, 10)
	mustRequest(t, b, 0)
	if !tryRetry(t, b, 0) { // cost=2, balance 1+1=2
		t.Fatal("TryRetry(0) should be allowed")
	}
	if got := mustBalance(t, b, 5); got != -1 { // 1+0-2
		t.Fatalf("Balance(5) = %d, want -1", got)
	}
}

// TestZeroReserveNoRequest: with R=0 and no deposit, no retry is allowed.
func TestZeroReserveNoRequest(t *testing.T) {
	b := mustNew(t, 100, 100, 1, 1, 0, 1, 10)
	if tryRetry(t, b, 0) {
		t.Fatal("TryRetry(0) with R=0 and no request should be rejected")
	}
	if got := mustBalance(t, b, 0); got != 0 {
		t.Fatalf("Balance(0) = %d, want 0", got)
	}
}

// TestRejectedRetryNotRecorded: a rejected retry adds no withdrawal event,
// so k (and therefore the next cost) is unchanged.
func TestRejectedRetryNotRecorded(t *testing.T) {
	b := mustNew(t, 100, 100, 1, 2, 3, 3, 5)
	if !tryRetry(t, b, 0) { // k=0, cost=2, balance 3 -> 1
		t.Fatal("TryRetry(0) should be allowed")
	}
	if tryRetry(t, b, 1) { // k=1, cost=4 > 1
		t.Fatal("TryRetry(1) should be rejected")
	}
	if b.validWithdrawals != 1 || len(b.withdrawals) != 1 {
		t.Fatalf("rejected retry recorded an event: valid=%d len=%d",
			b.validWithdrawals, len(b.withdrawals))
	}
	mustRequest(t, b, 2)
	mustRequest(t, b, 3)
	mustRequest(t, b, 4) // balance 3-2+3=4
	// k is still 1, so cost is 4 (not 6 as it would be if the rejection
	// had been recorded).
	if !tryRetry(t, b, 4) {
		t.Fatal("TryRetry(4) should be allowed with cost 4")
	}
	if got := b.withdrawals[len(b.withdrawals)-1].cost; got != 4 {
		t.Fatalf("frozen cost = %d, want 4", got)
	}
}

// TestSameTimestampOrdering: at the same timestamp, a Request before a
// TryRetry counts toward that retry's balance; the reverse order does not.
func TestSameTimestampOrdering(t *testing.T) {
	reqFirst := mustNew(t, 100, 100, 1, 1, 0, 1, 10)
	mustRequest(t, reqFirst, 5)
	if !tryRetry(t, reqFirst, 5) {
		t.Fatal("Request(5) then TryRetry(5) should be allowed")
	}

	retryFirst := mustNew(t, 100, 100, 1, 1, 0, 1, 10)
	if tryRetry(t, retryFirst, 5) {
		t.Fatal("TryRetry(5) before any Request(5) should be rejected")
	}
	mustRequest(t, retryFirst, 5)
	if !tryRetry(t, retryFirst, 5) {
		t.Fatal("TryRetry(5) after Request(5) should be allowed")
	}
}

// TestRejectedOpsNoStateChange: operations rejected with ErrInvalidTime or
// ErrClockRegression leave the ledgers and maxNow untouched, and the
// invalid-time reason is reported before clock regression.
func TestRejectedOpsNoStateChange(t *testing.T) {
	b := mustNew(t, 10, 20, 1, 2, 3, 3, 6)
	mustRequest(t, b, 5)
	tryRetry(t, b, 5)

	type snapshot struct {
		validDeposits, validWithdrawals, withdrawCostSum, maxNow int64
		depLen, wdLen                                            int
		cleaned, examined                                        int64
	}
	take := func() snapshot {
		return snapshot{b.validDeposits, b.validWithdrawals, b.withdrawCostSum,
			b.maxNow, len(b.deposits), len(b.withdrawals), b.cleanedEvents, b.examinedEvents}
	}
	before := take()

	// Invalid time: reported even though it is also a clock regression.
	if _, err := b.TryRetry(-1); !errors.Is(err, ErrInvalidTime) {
		t.Fatalf("TryRetry(-1) err = %v, want ErrInvalidTime", err)
	}
	if _, err := b.Balance(1_000_000_000_000_001); !errors.Is(err, ErrInvalidTime) {
		t.Fatalf("Balance(1e15+1) err = %v, want ErrInvalidTime", err)
	}
	if err := b.Request(-100); !errors.Is(err, ErrInvalidTime) {
		t.Fatalf("Request(-100) err = %v, want ErrInvalidTime", err)
	}
	// Clock regression.
	if err := b.Request(4); !errors.Is(err, ErrClockRegression) {
		t.Fatalf("Request(4) err = %v, want ErrClockRegression", err)
	}
	if _, err := b.TryRetry(0); !errors.Is(err, ErrClockRegression) {
		t.Fatalf("TryRetry(0) err = %v, want ErrClockRegression", err)
	}
	if _, err := b.Balance(4); !errors.Is(err, ErrClockRegression) {
		t.Fatalf("Balance(4) err = %v, want ErrClockRegression", err)
	}
	if after := take(); after != before {
		t.Fatalf("rejected ops changed state: before %+v, after %+v", before, after)
	}

	// Boundary: exactly 1e15 is legal (on a fresh budget).
	fresh := mustNew(t, 10, 20, 1, 2, 3, 3, 6)
	if _, err := fresh.Balance(1_000_000_000_000_000); err != nil {
		t.Fatalf("Balance(1e15) err = %v, want nil", err)
	}
}

// TestInvalidConfig: every out-of-range parameter rejects the whole config.
func TestInvalidConfig(t *testing.T) {
	if _, err := New(10, 20, 1, 2, 3, 3, 6); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
	cases := []struct {
		name                    string
		wd, wc, d, c, r, mx, cm int64
	}{
		{"Wd=0", 0, 20, 1, 2, 3, 3, 6},
		{"Wd=1e9+1", 1_000_000_001, 20, 1, 2, 3, 3, 6},
		{"Wc=0", 10, 0, 1, 2, 3, 3, 6},
		{"Wc=1e9+1", 10, 1_000_000_001, 1, 2, 3, 3, 6},
		{"D=0", 10, 20, 0, 2, 3, 3, 6},
		{"D=1e6+1", 10, 20, 1_000_001, 2, 3, 3, 6},
		{"C=0", 10, 20, 1, 0, 3, 3, 6},
		{"C=1e6+1", 10, 20, 1, 1_000_001, 3, 3, 6},
		{"R=-1", 10, 20, 1, 2, -1, 3, 6},
		{"R=1e9+1", 10, 20, 1, 2, 1_000_000_001, 3, 6},
		{"Mx=0", 10, 20, 1, 2, 3, 0, 6},
		{"Mx=11", 10, 20, 1, 2, 3, 11, 6},
		{"Cm=0", 10, 20, 1, 2, 3, 3, 0},
		{"Cm=1e6+1", 10, 20, 1, 2, 3, 3, 1_000_001},
	}
	for _, tc := range cases {
		_, err := New(tc.wd, tc.wc, tc.d, tc.c, tc.r, tc.mx, tc.cm)
		if !errors.Is(err, ErrInvalidConfig) {
			t.Errorf("%s: err = %v, want ErrInvalidConfig", tc.name, err)
		}
	}
	// Boundary values are all legal.
	if _, err := New(1, 1, 1, 1, 0, 1, 1); err != nil {
		t.Errorf("lower boundary rejected: %v", err)
	}
	if _, err := New(1_000_000_000, 1_000_000_000, 1_000_000, 1_000_000, 1_000_000_000, 10, 1_000_000); err != nil {
		t.Errorf("upper boundary rejected: %v", err)
	}
}

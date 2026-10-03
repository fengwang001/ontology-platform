package throttler

import (
	"errors"
	"fmt"
	"math/big"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
)

func mustNew(t *testing.T, budget int64, n int, periodLen int64, weights []int64, catchUp int64) *Throttler {
	t.Helper()
	th, err := New(budget, n, periodLen, weights, catchUp)
	if err != nil {
		t.Fatalf("New(%d, %d, %d, %v, %d) = %v", budget, n, periodLen, weights, catchUp, err)
	}
	return th
}

func mustAllowance(t *testing.T, th *Throttler, now int64) int64 {
	t.Helper()
	a, err := th.Allowance(now)
	if err != nil {
		t.Fatalf("Allowance(%d) = %v", now, err)
	}
	return a
}

func stateOf(th *Throttler) string {
	return fmt.Sprintf("spent=%d maxNow=%d cur=%d A=%d ps=%d",
		th.spent, th.maxNow, th.cur, th.allow, th.ps)
}

func TestPromptExample(t *testing.T) {
	th := mustNew(t, 1000, 4, 10, []int64{1, 3, 1, 5}, 50)
	if err := th.Try(60, 3); err != nil {
		t.Fatalf("Try(60,3): %v", err)
	}
	if err := th.Try(50, 5); !errors.Is(err, ErrThrottled) {
		t.Fatalf("Try(50,5) = %v, want ErrThrottled", err)
	}
	if err := th.Try(130, 6); !errors.Is(err, ErrAmountTooLarge) {
		t.Fatalf("Try(130,6) = %v, want ErrAmountTooLarge", err)
	}
	if got := mustAllowance(t, th, 25); got != 150 {
		t.Fatalf("Allowance(25) = %d, want 150", got)
	}
	if err := th.Try(150, 25); err != nil {
		t.Fatalf("Try(150,25): %v", err)
	}
	if err := th.Refund(30, 26); err != nil {
		t.Fatalf("Refund(30,26): %v", err)
	}
	if th.spent != 180 || th.ps != 120 {
		t.Fatalf("after refund: %s", stateOf(th))
	}
	if err := th.Try(30, 27); err != nil {
		t.Fatalf("Try(30,27): %v", err)
	}
	if th.spent != 210 || th.ps != 150 {
		t.Fatalf("final: %s", stateOf(th))
	}
}

func TestConstructorValidation(t *testing.T) {
	good := []int64{1, 1}
	big := make([]int64, 1440)
	for i := range big {
		big[i] = 1
	}
	cases := []struct {
		name    string
		budget  int64
		n       int
		period  int64
		weights []int64
		catchUp int64
		ok      bool
	}{
		{"B=0", 0, 2, 10, good, 0, false},
		{"B=1e12+1", 1_000_000_000_001, 2, 10, good, 0, false},
		{"n=0", 100, 0, 10, good, 0, false},
		{"n=1441", 100, 1441, 10, good, 0, false},
		{"L=0", 100, 2, 0, good, 0, false},
		{"L=1e6+1", 100, 2, 1_000_001, good, 0, false},
		{"len(w)!=n", 100, 3, 10, good, 0, false},
		{"w=0", 100, 2, 10, []int64{1, 0}, 0, false},
		{"w=1e6+1", 100, 2, 10, []int64{1, 1_000_001}, 0, false},
		{"m=-1", 100, 2, 10, good, -1, false},
		{"m=10001", 100, 2, 10, good, 10_001, false},
		{"min ok", 1, 1, 1, []int64{1}, 0, true},
		{"max ok", 1_000_000_000_000, 1440, 1_000_000, big, 10_000, true},
	}
	for _, c := range cases {
		_, err := New(c.budget, c.n, c.period, c.weights, c.catchUp)
		if c.ok && err != nil {
			t.Errorf("%s: unexpected error %v", c.name, err)
		}
		if !c.ok && !errors.Is(err, ErrInvalidParam) {
			t.Errorf("%s: got %v, want ErrInvalidParam", c.name, err)
		}
	}
}

// ps+a == A_i is admitted; ps+a == A_i+1 is throttled.
func TestThrottleBoundary(t *testing.T) {
	th := mustNew(t, 1000, 2, 10, []int64{1, 1}, 0) // q_0 = 500
	if err := th.Try(499, 0); err != nil {
		t.Fatalf("Try(499,0): %v", err)
	}
	if err := th.Try(1, 1); err != nil { // ps+a = 500 == A_0
		t.Fatalf("Try(1,1) at boundary: %v", err)
	}
	if err := th.Try(1, 2); !errors.Is(err, ErrThrottled) { // ps+a = 501 > 500
		t.Fatalf("Try(1,2) = %v, want ErrThrottled", err)
	}
	if th.spent != 500 || th.ps != 500 {
		t.Fatalf("state: %s", stateOf(th))
	}
}

// a == A_i is admitted; a == A_i+1 is rejected as a single oversize amount.
func TestAmountTooLargeBoundary(t *testing.T) {
	th := mustNew(t, 1000, 2, 10, []int64{1, 1}, 0) // A_0 = 500
	if err := th.Try(501, 0); !errors.Is(err, ErrAmountTooLarge) {
		t.Fatalf("Try(501,0) = %v, want ErrAmountTooLarge", err)
	}
	if err := th.Try(500, 0); err != nil {
		t.Fatalf("Try(500,0): %v", err)
	}
}

// Rounding can produce q_i == 0 periods; nothing may be spent there.
func TestZeroQuotaPeriod(t *testing.T) {
	th := mustNew(t, 1, 3, 5, []int64{1, 1, 1}, 0) // tgt = 0,0,1; q = 0,0,1
	if got := mustAllowance(t, th, 0); got != 0 {
		t.Fatalf("Allowance(0) = %d, want 0", got)
	}
	if err := th.Try(1, 0); !errors.Is(err, ErrAmountTooLarge) {
		t.Fatalf("Try(1,0) = %v, want ErrAmountTooLarge", err)
	}
	if x, err := th.TryUpTo(5, 0); x != 0 || !errors.Is(err, ErrThrottled) {
		t.Fatalf("TryUpTo(5,0) = (%d,%v), want (0,ErrThrottled)", x, err)
	}
	if err := th.Try(1, 10); err != nil { // period 2, q_2 = 1
		t.Fatalf("Try(1,10): %v", err)
	}
	if th.spent != 1 {
		t.Fatalf("state: %s", stateOf(th))
	}
}

// m = 0 disables catch-up entirely.
func TestNoCatchUp(t *testing.T) {
	th := mustNew(t, 100, 2, 10, []int64{1, 1}, 0)
	// skip period 0: deficit 50, but cap = q*m/100 = 0
	if got := mustAllowance(t, th, 10); got != 50 {
		t.Fatalf("Allowance(10) = %d, want 50", got)
	}
}

// Deficit below the catch-up cap is added in full.
func TestDeficitBelowCap(t *testing.T) {
	th := mustNew(t, 1000, 2, 10, []int64{1, 1}, 50) // q = 500, cap = 250
	if err := th.Try(400, 0); err != nil {
		t.Fatalf("Try(400,0): %v", err)
	}
	// s_1 = 400, d_1 = 500-400 = 100 < 250 -> A_1 = 600
	if got := mustAllowance(t, th, 10); got != 600 {
		t.Fatalf("Allowance(10) = %d, want 600", got)
	}
}

// Deficit above the catch-up cap is truncated to the cap.
func TestDeficitAboveCap(t *testing.T) {
	th := mustNew(t, 1000, 2, 10, []int64{1, 1}, 50) // q = 500, cap = 250
	if err := th.Try(100, 0); err != nil {
		t.Fatalf("Try(100,0): %v", err)
	}
	// s_1 = 100, d_1 = 400 > 250 -> A_1 = 500+250 = 750
	if got := mustAllowance(t, th, 10); got != 750 {
		t.Fatalf("Allowance(10) = %d, want 750", got)
	}
}

// Skipping several periods accumulates the deficit of all of them, but the
// catch-up is still capped by q_i*m/100 of the entered period only.
func TestSkipMultiplePeriods(t *testing.T) {
	th := mustNew(t, 1000, 4, 10, []int64{1, 1, 1, 1}, 100) // q = 250 each
	// enter period 3 directly: d_3 = tgt_2 - 0 = 750, cap = 250
	if got := mustAllowance(t, th, 30); got != 500 {
		t.Fatalf("Allowance(30) = %d, want 500", got)
	}
	if err := th.Try(500, 30); err != nil {
		t.Fatalf("Try(500,30): %v", err)
	}
}

// A_i is truncated by the remaining daily budget B - s_i.
func TestAllowanceTruncatedByBudget(t *testing.T) {
	th := mustNew(t, 100, 2, 10, []int64{1, 1}, 10_000)
	// enter period 1 with s_1 = 0: q = 50, d = 50, cap huge -> truncated to 100
	if got := mustAllowance(t, th, 10); got != 100 {
		t.Fatalf("Allowance(10) = %d, want 100", got)
	}
	if err := th.Try(100, 10); err != nil {
		t.Fatalf("Try(100,10): %v", err)
	}
	if th.spent != 100 {
		t.Fatalf("state: %s", stateOf(th))
	}
}

// A refund inside the same period lowers ps and frees allowance.
func TestRefundFreesAllowanceSamePeriod(t *testing.T) {
	th := mustNew(t, 1000, 2, 10, []int64{1, 1}, 0) // A_0 = 500
	if err := th.Try(500, 0); err != nil {
		t.Fatalf("Try(500,0): %v", err)
	}
	if err := th.Try(1, 1); !errors.Is(err, ErrThrottled) {
		t.Fatalf("Try(1,1) = %v, want ErrThrottled", err)
	}
	if err := th.Refund(200, 2); err != nil {
		t.Fatalf("Refund(200,2): %v", err)
	}
	if got := mustAllowance(t, th, 3); got != 200 {
		t.Fatalf("Allowance(3) = %d, want 200", got)
	}
	if err := th.Try(200, 4); err != nil {
		t.Fatalf("Try(200,4): %v", err)
	}
}

// With ps == 0 a refund only lowers spent: it does not raise the current
// period's allowance, and the next period's deficit grows.
func TestRefundWithZeroPeriodSpend(t *testing.T) {
	th := mustNew(t, 1200, 3, 10, []int64{1, 1, 1}, 200) // q = 400, cap = 800
	if err := th.Try(400, 0); err != nil {
		t.Fatalf("Try(400,0): %v", err)
	}
	// first accepted op of period 1 is the refund; A_1 fixed with s_1 = 400
	if err := th.Refund(100, 10); err != nil {
		t.Fatalf("Refund(100,10): %v", err)
	}
	if th.spent != 300 || th.ps != 0 {
		t.Fatalf("state: %s", stateOf(th))
	}
	// ps was 0, so the refund freed nothing in period 1
	if got := mustAllowance(t, th, 11); got != 400 {
		t.Fatalf("Allowance(11) = %d, want 400", got)
	}
	// period 2: s_2 = 300, d_2 = 800-300 = 500, A_2 = 400+min(500,800) = 900
	if got := mustAllowance(t, th, 20); got != 900 {
		t.Fatalf("Allowance(20) = %d, want 900", got)
	}
	// without the refund s_2 would be 400 and A_2 would be 800
}

// When the first accepted operation of a period is a refund, A_i is fixed
// with the spent value from before the refund.
func TestRefundFixesAllowanceWithPreRefundSpent(t *testing.T) {
	th := mustNew(t, 1200, 3, 10, []int64{1, 1, 1}, 200) // q = 400, cap = 800
	if err := th.Try(400, 0); err != nil {
		t.Fatalf("Try(400,0): %v", err)
	}
	if err := th.Refund(100, 10); err != nil {
		t.Fatalf("Refund(100,10): %v", err)
	}
	// A_1 was fixed with s_1 = 400 (d_1 = 0), not with 300 (d_1 = 100)
	if th.allow != 400 {
		t.Fatalf("A_1 = %d, want 400", th.allow)
	}
	if got := mustAllowance(t, th, 15); got != 400 {
		t.Fatalf("Allowance(15) = %d, want 400", got)
	}
}

// TryUpTo spends min(a, remaining allowance); a remaining of 0 is throttled.
func TestTryUpTo(t *testing.T) {
	th := mustNew(t, 1000, 2, 10, []int64{1, 1}, 0) // A_0 = 500
	if x, err := th.TryUpTo(300, 0); x != 300 || err != nil {
		t.Fatalf("TryUpTo(300,0) = (%d,%v), want (300,nil)", x, err)
	}
	if x, err := th.TryUpTo(999, 1); x != 200 || err != nil { // capped by remainder
		t.Fatalf("TryUpTo(999,1) = (%d,%v), want (200,nil)", x, err)
	}
	if x, err := th.TryUpTo(10, 2); x != 0 || !errors.Is(err, ErrThrottled) {
		t.Fatalf("TryUpTo(10,2) = (%d,%v), want (0,ErrThrottled)", x, err)
	}
	if th.spent != 500 {
		t.Fatalf("state: %s", stateOf(th))
	}
	// TryUpTo never reports budget exhaustion or oversize amounts
	th2 := mustNew(t, 100, 1, 10, []int64{1}, 0)
	if x, err := th2.TryUpTo(1_000_000_000_000, 0); x != 100 || err != nil {
		t.Fatalf("TryUpTo(1e12,0) = (%d,%v), want (100,nil)", x, err)
	}
}

// now = iL-1 belongs to period i-1; now = iL belongs to period i.
func TestPeriodBoundaries(t *testing.T) {
	th := mustNew(t, 100, 2, 10, []int64{1, 1}, 0) // q = 50 each
	if err := th.Try(50, 9); err != nil {          // last tick of period 0
		t.Fatalf("Try(50,9): %v", err)
	}
	if err := th.Try(1, 9); !errors.Is(err, ErrThrottled) {
		t.Fatalf("Try(1,9) = %v, want ErrThrottled", err)
	}
	if err := th.Try(1, 10); err != nil { // first tick of period 1
		t.Fatalf("Try(1,10): %v", err)
	}
}

// now at or beyond n*L, and negative now, are invalid parameters.
func TestNowOutOfRange(t *testing.T) {
	th := mustNew(t, 100, 2, 10, []int64{1, 1}, 0)
	if err := th.Try(1, 20); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("Try(1,20) = %v, want ErrInvalidParam", err)
	}
	if err := th.Try(1, -1); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("Try(1,-1) = %v, want ErrInvalidParam", err)
	}
	if err := th.Refund(1, 20); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("Refund(1,20) = %v, want ErrInvalidParam", err)
	}
	if _, err := th.Allowance(20); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("Allowance(20) = %v, want ErrInvalidParam", err)
	}
	if err := th.Try(1, 19); err != nil { // last valid tick
		t.Fatalf("Try(1,19): %v", err)
	}
}

type snapshot struct {
	spent, maxNow, allow, ps int64
	cur                      int
}

func snap(th *Throttler) snapshot {
	return snapshot{spent: th.spent, maxNow: th.maxNow, cur: th.cur, allow: th.allow, ps: th.ps}
}

func requireState(t *testing.T, th *Throttler, want snapshot) {
	t.Helper()
	got := snap(th)
	if got != want {
		t.Fatalf("state changed by rejected op: got %+v, want %+v", got, want)
	}
}

// Rejections are reported in a fixed priority order and never mutate state.
func TestRejectionPriorityAndNoStateChange(t *testing.T) {
	th := mustNew(t, 100, 2, 10, []int64{1, 1}, 0) // q = 50 each
	if err := th.Try(50, 5); err != nil {
		t.Fatalf("Try(50,5): %v", err)
	}
	if err := th.Try(50, 10); err != nil { // spent = 100 = B, maxNow = 10
		t.Fatalf("Try(100,5): %v", err)
	}

	// invalid parameter beats a backwards clock
	before := snap(th)
	if err := th.Try(0, 0); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("Try(0,0) = %v, want ErrInvalidParam", err)
	}
	requireState(t, th, before)

	// backwards clock beats budget exhaustion
	if err := th.Try(50, 9); !errors.Is(err, ErrClockBackwards) {
		t.Fatalf("Try(50,9) = %v, want ErrClockBackwards", err)
	}
	requireState(t, th, before)

	// budget exhaustion beats an oversize amount (a = 60 > A_1 = 50 too)
	if err := th.Try(60, 15); !errors.Is(err, ErrBudgetExhausted) {
		t.Fatalf("Try(60,15) = %v, want ErrBudgetExhausted", err)
	}
	requireState(t, th, before)

	// refund of more than spent
	if err := th.Refund(101, 15); !errors.Is(err, ErrRefundTooMuch) {
		t.Fatalf("Refund(101,15) = %v, want ErrRefundTooMuch", err)
	}
	requireState(t, th, before)

	// oversize amount beats throttling: fresh period, a > A but ps+a > A too
	th2 := mustNew(t, 1000, 2, 10, []int64{1, 1}, 0) // A_0 = 500
	before2 := snap(th2)
	if err := th2.Try(600, 0); !errors.Is(err, ErrAmountTooLarge) {
		t.Fatalf("Try(600,0) = %v, want ErrAmountTooLarge", err)
	}
	requireState(t, th2, before2)

	// invalid refund parameter beats a backwards clock
	if err := th.Refund(0, 0); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("Refund(0,0) = %v, want ErrInvalidParam", err)
	}
	requireState(t, th, before)

	// a rejected operation must not fix A_i of a new period either
	th3 := mustNew(t, 1000, 2, 10, []int64{1, 1}, 0)
	if err := th3.Try(600, 10); !errors.Is(err, ErrAmountTooLarge) {
		t.Fatalf("Try(600,10) = %v, want ErrAmountTooLarge", err)
	}
	if th3.cur != -1 {
		t.Fatalf("rejected op entered a period: %s", stateOf(th3))
	}
	if err := th3.Try(500, 10); err != nil { // A_1 = 500 fixed now
		t.Fatalf("Try(500,10): %v", err)
	}
}

// naive is an independent, deliberately simple implementation of the spec.
// It recomputes targets by iterating over all weights and uses big.Int for
// the 128-bit products.
type naive struct {
	budget int64
	n      int
	period int64
	w      []int64
	m      int64

	spent  int64
	maxNow int64
	cur    int
	allow  int64
	ps     int64
}

func newNaive(budget int64, n int, period int64, w []int64, m int64) *naive {
	return &naive{budget: budget, n: n, period: period, w: w, m: m, cur: -1}
}

func (nd *naive) tgt(i int) int64 {
	if i < 0 {
		return 0
	}
	var cum, tot int64
	for j := 0; j < nd.n; j++ {
		tot += nd.w[j]
		if j <= i {
			cum += nd.w[j]
		}
	}
	num := new(big.Int).Mul(big.NewInt(nd.budget), big.NewInt(cum))
	return new(big.Int).Div(num, big.NewInt(tot)).Int64()
}

func (nd *naive) allowanceOf(s int64, i int) int64 {
	prev := nd.tgt(i - 1)
	q := nd.tgt(i) - prev
	d := prev - s
	if d < 0 {
		d = 0
	}
	cap_ := q * nd.m / 100
	if d < cap_ {
		cap_ = d
	}
	a := q + cap_
	if rem := nd.budget - s; a > rem {
		a = rem
	}
	return a
}

func (nd *naive) prepare(now int64) (i int, allow, ps int64, newPeriod bool) {
	i = int(now / nd.period)
	if i == nd.cur {
		return i, nd.allow, nd.ps, false
	}
	return i, nd.allowanceOf(nd.spent, i), 0, true
}

func (nd *naive) enter(i int, allow int64) {
	nd.cur, nd.allow, nd.ps = i, allow, 0
}

func (nd *naive) valid(now int64) bool {
	return now >= 0 && now < int64(nd.n)*nd.period
}

func (nd *naive) try(a, now int64) (error, string) {
	if a < 1 || a > maxAmount || !nd.valid(now) {
		return ErrInvalidParam, "参数非法"
	}
	if now < nd.maxNow {
		return ErrClockBackwards, "时钟回退"
	}
	i, allow, ps, newPeriod := nd.prepare(now)
	if nd.spent+a > nd.budget {
		return ErrBudgetExhausted, fmt.Sprintf("日预算不足 spent+a=%d>B=%d", nd.spent+a, nd.budget)
	}
	if a > allow {
		return ErrAmountTooLarge, fmt.Sprintf("单笔过大 a=%d>A_%d=%d", a, i, allow)
	}
	if ps+a > allow {
		return ErrThrottled, fmt.Sprintf("被限速 ps+a=%d>A_%d=%d", ps+a, i, allow)
	}
	if newPeriod {
		nd.enter(i, allow)
	}
	nd.spent += a
	nd.ps += a
	nd.maxNow = now
	return nil, fmt.Sprintf("放行 ps=%d/%d spent=%d", nd.ps, allow, nd.spent)
}

func (nd *naive) tryUpTo(a, now int64) (int64, error, string) {
	if a < 1 || a > maxAmount || !nd.valid(now) {
		return 0, ErrInvalidParam, "参数非法"
	}
	if now < nd.maxNow {
		return 0, ErrClockBackwards, "时钟回退"
	}
	i, allow, ps, newPeriod := nd.prepare(now)
	x := a
	if rem := allow - ps; x > rem {
		x = rem
	}
	if x < 1 {
		return 0, ErrThrottled, fmt.Sprintf("被限速 剩余额度=%d", allow-ps)
	}
	if newPeriod {
		nd.enter(i, allow)
	}
	nd.spent += x
	nd.ps += x
	nd.maxNow = now
	return x, nil, fmt.Sprintf("放行 x=min(%d,%d)=%d spent=%d", a, allow-ps+x, x, nd.spent)
}

func (nd *naive) refund(a, now int64) (error, string) {
	if a < 1 || a > maxAmount || !nd.valid(now) {
		return ErrInvalidParam, "参数非法"
	}
	if now < nd.maxNow {
		return ErrClockBackwards, "时钟回退"
	}
	if a > nd.spent {
		return ErrRefundTooMuch, fmt.Sprintf("退款过多 a=%d>spent=%d", a, nd.spent)
	}
	i, allow, _, newPeriod := nd.prepare(now)
	if newPeriod {
		nd.enter(i, allow)
	}
	nd.spent -= a
	if a < nd.ps {
		nd.ps -= a
	} else {
		nd.ps = 0
	}
	nd.maxNow = now
	return nil, fmt.Sprintf("退款 spent=%d ps=%d A_%d=%d", nd.spent, nd.ps, i, allow)
}

func (nd *naive) allowance(now int64) (int64, error, string) {
	if !nd.valid(now) {
		return 0, ErrInvalidParam, "参数非法"
	}
	i := int(now / nd.period)
	if i < nd.cur {
		return 0, nil, "早于当前时段返回 0"
	}
	if i == nd.cur {
		return nd.allow - nd.ps, nil, fmt.Sprintf("当前时段 A-ps=%d", nd.allow-nd.ps)
	}
	a := nd.allowanceOf(nd.spent, i)
	return a, nil, fmt.Sprintf("未来时段按 spent=%d 现算 A_%d=%d", nd.spent, i, a)
}

type opKind int

const (
	opTry opKind = iota
	opTryUpTo
	opRefund
	opAllowance
)

type op struct {
	kind   opKind
	a, now int64
}

func (k opKind) String() string {
	return [...]string{"Try", "TryUpTo", "Refund", "Allowance"}[k]
}

func genSequence(rng *rand.Rand) (budget int64, n int, period int64, w []int64, m int64, ops []op) {
	n = 1 + rng.Intn(6)
	period = int64(1 + rng.Intn(3))
	w = make([]int64, n)
	for i := range w {
		if rng.Intn(10) == 0 {
			w[i] = int64(1 + rng.Intn(1_000_000))
		} else {
			w[i] = int64(1 + rng.Intn(4))
		}
	}
	switch rng.Intn(4) {
	case 0:
		budget = int64(1 + rng.Intn(50))
	case 1:
		budget = int64(1 + rng.Intn(2000))
	case 2:
		budget = int64(1 + rng.Intn(1_000_000))
	default:
		budget = int64(1 + rng.Int63n(1_000_000_000_000))
	}
	switch rng.Intn(3) {
	case 0:
		m = int64(rng.Intn(10_001))
	case 1:
		m = []int64{0, 1, 50, 100, 10_000}[rng.Intn(5)]
	default:
		m = int64(rng.Intn(201))
	}
	total := int64(n) * period
	var lastNow int64
	nOps := 15 + rng.Intn(10)
	ops = make([]op, 0, nOps)
	for k := 0; k < nOps; k++ {
		var o op
		o.kind = opKind(rng.Intn(4))
		switch r := rng.Intn(20); {
		case r < 13: // move forward, possibly skipping periods
			lastNow += int64(rng.Intn(int(2*period) + 1))
			if lastNow >= total { // re-enter the day to keep ops valid
				lastNow = rng.Int63n(total)
			}
			o.now = lastNow
		case r < 16: // stay
			o.now = lastNow
		case r < 19: // anywhere valid (may go backwards)
			o.now = rng.Int63n(total)
		default: // invalid
			o.now = []int64{-1, -5, total, total + 3}[rng.Intn(4)]
		}
		switch r := rng.Intn(20); {
		case r < 12:
			o.a = int64(1 + rng.Intn(60))
		case r < 17:
			o.a = int64(1 + rng.Int63n(max(budget*2, 2)))
		case r < 19:
			o.a = int64(1 + rng.Int63n(1_000_000_000_000))
		default:
			o.a = []int64{0, -3, 1_000_000_000_001}[rng.Intn(3)]
		}
		ops = append(ops, o)
	}
	return
}

func runSequence(t *testing.T, seq int, budget int64, n int, period int64, w []int64, m int64, ops []op) {
	th := mustNew(t, budget, n, period, w, m)
	nd := newNaive(budget, n, period, w, m)
	for k, o := range ops {
		var x int64
		var err error
		var why string
		switch o.kind {
		case opTry:
			var nerr error
			nerr, why = nd.try(o.a, o.now)
			err = th.Try(o.a, o.now)
			if (err == nil) != (nerr == nil) || (err != nil && !errors.Is(err, nerr)) {
				t.Fatalf("seq=%d op=%d %s(%d,%d): got %v, naive %v (%s)", seq, k, o.kind, o.a, o.now, err, nerr, why)
			}
		case opTryUpTo:
			var nx int64
			var nerr error
			nx, nerr, why = nd.tryUpTo(o.a, o.now)
			x, err = th.TryUpTo(o.a, o.now)
			if x != nx || (err == nil) != (nerr == nil) || (err != nil && !errors.Is(err, nerr)) {
				t.Fatalf("seq=%d op=%d %s(%d,%d): got (%d,%v), naive (%d,%v) (%s)", seq, k, o.kind, o.a, o.now, x, err, nx, nerr, why)
			}
		case opRefund:
			var nerr error
			nerr, why = nd.refund(o.a, o.now)
			err = th.Refund(o.a, o.now)
			if (err == nil) != (nerr == nil) || (err != nil && !errors.Is(err, nerr)) {
				t.Fatalf("seq=%d op=%d %s(%d,%d): got %v, naive %v (%s)", seq, k, o.kind, o.a, o.now, err, nerr, why)
			}
		case opAllowance:
			var nx int64
			var nerr error
			nx, nerr, why = nd.allowance(o.now)
			x, err = th.Allowance(o.now)
			if x != nx || (err == nil) != (nerr == nil) || (err != nil && !errors.Is(err, nerr)) {
				t.Fatalf("seq=%d op=%d %s(%d): got (%d,%v), naive (%d,%v) (%s)", seq, k, o.kind, o.now, x, err, nx, nerr, why)
			}
		}
		t.Logf("seq=%d op=%d %s(a=%d,now=%d) -> x=%d err=%v | %s", seq, k, o.kind, o.a, o.now, x, err, why)
		if th.spent != nd.spent || th.ps != nd.ps || th.allow != nd.allow ||
			th.cur != nd.cur || th.maxNow != nd.maxNow {
			t.Fatalf("seq=%d op=%d: state %s, naive spent=%d maxNow=%d cur=%d A=%d ps=%d",
				seq, k, stateOf(th), nd.spent, nd.maxNow, nd.cur, nd.allow, nd.ps)
		}
		if th.spent < 0 || th.spent > th.budget {
			t.Fatalf("seq=%d op=%d: spent=%d out of [0,%d]", seq, k, th.spent, th.budget)
		}
		if th.cur >= 0 {
			if th.ps < 0 || th.ps > th.allow {
				t.Fatalf("seq=%d op=%d: ps=%d out of [0,%d]", seq, k, th.ps, th.allow)
			}
			if th.spent > th.tgt(th.cur) {
				t.Fatalf("seq=%d op=%d: spent=%d > tgt_%d=%d", seq, k, th.spent, th.cur, th.tgt(th.cur))
			}
		}
	}
}

func TestRandomAgainstNaive(t *testing.T) {
	rng := rand.New(rand.NewSource(20261003))
	for seq := 0; seq < 2000; seq++ {
		budget, n, period, w, m, ops := genSequence(rng)
		t.Logf("seq=%d params B=%d n=%d L=%d w=%v m=%d", seq, budget, n, period, w, m)
		runSequence(t, seq, budget, n, period, w, m, ops)
	}
}

// Replaying the same operation sequence must reproduce identical results.
func TestReplayDeterminism(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	budget, n, period, w, m, ops := genSequence(rng)
	type outcome struct {
		x   int64
		err error
	}
	run := func() []outcome {
		th := mustNew(t, budget, n, period, w, m)
		out := make([]outcome, 0, len(ops))
		for _, o := range ops {
			var r outcome
			switch o.kind {
			case opTry:
				r.err = th.Try(o.a, o.now)
			case opTryUpTo:
				r.x, r.err = th.TryUpTo(o.a, o.now)
			case opRefund:
				r.err = th.Refund(o.a, o.now)
			case opAllowance:
				r.x, r.err = th.Allowance(o.now)
			}
			out = append(out, r)
		}
		return out
	}
	first, second := run(), run()
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("op %d diverged on replay: %+v vs %+v", i, first[i], second[i])
		}
	}
}

// tgt evaluations per operation must be a small constant independent of n.
func TestTgtEvaluationsConstant(t *testing.T) {
	for _, n := range []int{100, 1440} {
		w := make([]int64, n)
		for i := range w {
			w[i] = 1
		}
		th := mustNew(t, 1_000_000, n, 1, w, 50)
		for j := 0; j < 10; j++ {
			if err := th.Try(1, int64(j)); err != nil {
				t.Fatalf("n=%d Try(1,%d): %v", n, j, err)
			}
		}
		// each Try enters a new period and evaluates tgt(i) and tgt(i-1)
		// once; tgt(-1) is free, so 10 ops cost 19 evaluations for any n
		if th.tgtEvals != 19 {
			t.Fatalf("n=%d: tgtEvals=%d, want 19", n, th.tgtEvals)
		}
	}
}

// Concurrent operations must behave as some serial order: exactly A_0
// unit spends are admitted, and afterwards exactly that many unit refunds.
func TestConcurrentLinearizable(t *testing.T) {
	const total = 100_000
	th := mustNew(t, total, 1, 10, []int64{1}, 0)
	var wg sync.WaitGroup
	var accepted atomic.Int64
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for k := 0; k < total/4; k++ {
				if err := th.Try(1, 0); err == nil {
					accepted.Add(1)
				}
				if _, err := th.Allowance(0); err != nil {
					t.Errorf("Allowance: %v", err)
				}
			}
		}()
	}
	wg.Wait()
	if accepted.Load() != total || th.spent != total {
		t.Fatalf("accepted=%d spent=%d, want %d", accepted.Load(), th.spent, total)
	}
	var refunded atomic.Int64
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for k := 0; k < total/4; k++ {
				if err := th.Refund(1, 0); err == nil {
					refunded.Add(1)
				}
			}
		}()
	}
	wg.Wait()
	if refunded.Load() != total || th.spent != 0 {
		t.Fatalf("refunded=%d spent=%d, want %d/0", refunded.Load(), th.spent, total)
	}
}

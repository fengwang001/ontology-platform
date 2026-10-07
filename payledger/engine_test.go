package payledger

import (
	"errors"
	"testing"
)

func newEngine(t *testing.T, validityDays, toleranceBps int64) *Engine {
	t.Helper()
	e, err := NewEngine(validityDays, toleranceBps)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	return e
}

func mustOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func mustErr(t *testing.T, err error, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("want error %v, got %v", want, err)
	}
}

func mustAvailable(t *testing.T, e *Engine, accountID string, now, want int64) {
	t.Helper()
	got, err := e.Available(accountID, now)
	mustOK(t, err)
	if got != want {
		t.Fatalf("available(%q, %d) = %d, want %d", accountID, now, got, want)
	}
}

func mustAuthState(t *testing.T, e *Engine, authID string, now int64, want AuthView) {
	t.Helper()
	got, err := e.AuthState(authID, now)
	mustOK(t, err)
	if got != want {
		t.Fatalf("authState(%q, %d) = %+v, want %+v", authID, now, got, want)
	}
}

// 截止日恰等及差一天：截止日当天捕获/增量有效，次日起报已终结。
func TestExpiryBoundaryExactAndOneOff(t *testing.T) {
	e := newEngine(t, 5, 0)
	mustOK(t, e.CreateAccount("acct", 1000, 0))
	mustOK(t, e.Authorize("acct", "a1", 100, 0))

	// Day 5 == expiry day: capture is still valid.
	mustOK(t, e.Capture("a1", 40, false, 5))
	mustAuthState(t, e, "a1", 5, AuthView{
		ID: "a1", AccountID: "acct", Status: StatusActive,
		RemainingHold: 60, TotalAuthorized: 100, TotalCaptured: 40, ExpiryDay: 5,
	})

	// Day 6 == expiry day + 1: authorization is terminated (expired).
	mustErr(t, e.Capture("a1", 10, false, 6), ErrAuthTerminated)
	mustErr(t, e.Increment("a1", 10, 6), ErrAuthTerminated)
	mustErr(t, e.Reverse("a1", 6), ErrAuthTerminated)
	v, err := e.AuthState("a1", 6)
	mustOK(t, err)
	if v.Status != StatusExpired || v.RemainingHold != 0 {
		t.Fatalf("want expired with zero hold, got %+v", v)
	}

	// Increment on the last valid day resets validity to increment day + E.
	mustOK(t, e.Authorize("acct", "a2", 100, 6)) // expires day 11
	mustOK(t, e.Increment("a2", 50, 11))         // last valid day; resets to day 16
	mustErr(t, e.Capture("a2", 1, false, 17), ErrAuthTerminated)
}

// 增量失败不影响原授权及其有效期。
func TestFailedIncrementPreservesOriginal(t *testing.T) {
	e := newEngine(t, 2, 0)
	mustOK(t, e.CreateAccount("acct", 150, 0))
	mustOK(t, e.Authorize("acct", "a1", 100, 0)) // expires day 2, available 50

	// Increment beyond available credit fails...
	mustErr(t, e.Increment("a1", 100, 1), ErrInsufficientFunds)
	// ...and the original authorization is untouched: still 100, expiring day 2.
	mustAuthState(t, e, "a1", 1, AuthView{
		ID: "a1", AccountID: "acct", Status: StatusActive,
		RemainingHold: 100, TotalAuthorized: 100, ExpiryDay: 2,
	})
	mustAvailable(t, e, "acct", 1, 50)

	// Capture on the original expiry day still works; the day after it is dead.
	mustOK(t, e.Capture("a1", 100, false, 2))
	mustAvailable(t, e, "acct", 2, 50)

	// A successful increment on the last valid day resets the expiry.
	mustOK(t, e.Authorize("acct", "a2", 50, 2)) // available 0 now
	mustOK(t, e.AdjustCreditLimit("acct", 500, 2))
	mustOK(t, e.Increment("a2", 50, 4)) // a2 expires day 4; reset to day 6
	mustAuthState(t, e, "a2", 4, AuthView{
		ID: "a2", AccountID: "acct", Status: StatusActive,
		RemainingHold: 100, TotalAuthorized: 100, ExpiryDay: 6,
	})
}

// 超出剩余持有的捕获由可用额度覆盖，容差上浮部分向下取整。
func TestOverCaptureAndToleranceFloor(t *testing.T) {
	e := newEngine(t, 10, 50) // 50bps = 0.5%
	mustOK(t, e.CreateAccount("acct", 1000, 0))

	// 199 * 50 / 10000 = 0 (floor): cap is exactly 199.
	mustOK(t, e.Authorize("acct", "a1", 199, 0))
	mustErr(t, e.Capture("a1", 200, false, 0), ErrOverTolerance)
	mustOK(t, e.Capture("a1", 199, true, 0))

	// 200 * 50 / 10000 = 1: cap is 201.
	mustOK(t, e.Authorize("acct", "a2", 200, 0))
	mustOK(t, e.Capture("a2", 150, false, 0))                  // within hold
	mustOK(t, e.Capture("a2", 51, false, 0))                   // 50 from hold + 1 from available
	mustErr(t, e.Capture("a2", 1, false, 0), ErrOverTolerance) // 202 > 201
	mustAuthState(t, e, "a2", 0, AuthView{
		ID: "a2", AccountID: "acct", Status: StatusActive,
		RemainingHold: 0, TotalAuthorized: 200, TotalCaptured: 201, ExpiryDay: 10,
	})
	// posted = 199 + 201 = 400, holds = 0 -> available = 600.
	mustAvailable(t, e, "acct", 0, 600)

	// Remaining hold is zero: further captures come entirely from available
	// credit but stay capped by the tolerance.
	mustOK(t, e.AdjustCreditLimit("acct", 10000, 0))
	mustOK(t, e.Authorize("acct", "a3", 100, 0)) // cap 100 + 0 = 100
	mustOK(t, e.Capture("a3", 100, false, 0))
	mustErr(t, e.Capture("a3", 1, false, 0), ErrOverTolerance)

	// Excess over the remaining hold needs available credit.
	e2 := newEngine(t, 10, 10000) // 100% tolerance
	mustOK(t, e2.CreateAccount("acct", 300, 0))
	mustOK(t, e2.Authorize("acct", "a1", 100, 0)) // cap 200, available 200
	mustOK(t, e2.Authorize("acct", "a2", 150, 0)) // available 50
	// Capture 200 on a1: within cap (200), but excess 100 > available 50.
	mustErr(t, e2.Capture("a1", 200, false, 0), ErrInsufficientFunds)
	// Capture 120: excess 20 <= available 50, succeeds; hold of a1 zeroed.
	mustOK(t, e2.Capture("a1", 120, false, 0))
	mustAvailable(t, e2, "acct", 0, 30) // 300 - 120 posted - 150 held
}

// 终捕成功后剩余持有立即释放，授权终结。
func TestFinalCaptureReleasesHold(t *testing.T) {
	e := newEngine(t, 10, 0)
	mustOK(t, e.CreateAccount("acct", 1000, 0))
	mustOK(t, e.Authorize("acct", "a1", 100, 0))
	mustOK(t, e.Capture("a1", 30, true, 1)) // final: remaining 70 released
	mustAvailable(t, e, "acct", 1, 970)
	mustAuthState(t, e, "a1", 1, AuthView{
		ID: "a1", AccountID: "acct", Status: StatusFinalCaptured,
		RemainingHold: 0, TotalAuthorized: 100, TotalCaptured: 30, ExpiryDay: 10,
	})
	mustErr(t, e.Capture("a1", 1, false, 1), ErrAuthTerminated)
	mustErr(t, e.Increment("a1", 1, 1), ErrAuthTerminated)
	mustErr(t, e.Reverse("a1", 1), ErrAuthTerminated)
}

// 撤销释放剩余持有，已捕获部分不受影响；撤销后捕获被拒。
func TestReverseThenCaptureRejected(t *testing.T) {
	e := newEngine(t, 10, 0)
	mustOK(t, e.CreateAccount("acct", 1000, 0))
	mustOK(t, e.Authorize("acct", "a1", 100, 0))
	mustOK(t, e.Capture("a1", 40, false, 0))
	mustOK(t, e.Reverse("a1", 1))
	mustAvailable(t, e, "acct", 1, 960) // hold 60 released, posted 40 stays
	mustAuthState(t, e, "a1", 1, AuthView{
		ID: "a1", AccountID: "acct", Status: StatusReversed,
		RemainingHold: 0, TotalAuthorized: 100, TotalCaptured: 40, ExpiryDay: 10,
	})
	mustErr(t, e.Capture("a1", 10, false, 1), ErrAuthTerminated)
}

// 过期持有自动失效，不依赖任何操作触发，额度可再用于新授权。
func TestExpiredHoldAutoReleasedAndReusable(t *testing.T) {
	e := newEngine(t, 2, 0)
	mustOK(t, e.CreateAccount("acct", 100, 0))
	mustOK(t, e.Authorize("acct", "a1", 100, 0)) // expires day 2
	mustAvailable(t, e, "acct", 0, 0)
	// Day 3: pure query shows the hold is gone, no operation needed.
	mustAvailable(t, e, "acct", 3, 100)
	// The freed credit can be reused by a brand new authorization.
	mustOK(t, e.Authorize("acct", "a2", 100, 3))
	mustAvailable(t, e, "acct", 3, 0)
}

// 被拒绝的操作不得改变任何状态与时钟。
func TestRejectedOpsLeaveNoTrace(t *testing.T) {
	e := newEngine(t, 5, 0)
	mustOK(t, e.CreateAccount("acct", 500, 0))
	mustOK(t, e.Authorize("acct", "a1", 100, 1))
	mustOK(t, e.Capture("a1", 30, false, 2))

	snapshot := func() (int64, AuthView) {
		av, err := e.Available("acct", 2)
		mustOK(t, err)
		st, err := e.AuthState("a1", 2)
		mustOK(t, err)
		return av, st
	}
	beforeAvail, beforeState := snapshot()

	rejected := []error{
		e.Capture("a1", 0, false, 2),       // invalid param
		e.Capture("a1", -5, false, 2),      // invalid param
		e.Capture("a1", 10, false, 1),      // clock regression
		e.Capture("nope", 10, false, 2),    // unknown auth
		e.Capture("a1", 1000, false, 2),    // over tolerance
		e.Increment("a1", 1000, 2),         // insufficient funds
		e.Authorize("acct", "a1", 10, 2),   // duplicate id
		e.Authorize("acct", "a2", 1000, 2), // insufficient funds
		e.Authorize("ghost", "a3", 10, 2),  // unknown account
		e.Refund("a1", 100, 2),             // exceeds refundable
		e.Refund("a1", -1, 2),              // invalid param
		e.AdjustCreditLimit("acct", 50, 2), // would go negative
		e.CreateAccount("acct", 10, 2),     // duplicate account
		e.Reverse("ghost", 2),              // unknown auth
	}
	for i, err := range rejected {
		if err == nil {
			t.Fatalf("rejected op %d unexpectedly succeeded", i)
		}
	}
	afterAvail, afterState := snapshot()
	if beforeAvail != afterAvail || beforeState != afterState {
		t.Fatalf("rejected ops mutated state: (%d,%+v) -> (%d,%+v)",
			beforeAvail, beforeState, afterAvail, afterState)
	}
	// The clock did not move: an operation with the same now is still accepted.
	mustOK(t, e.Capture("a1", 10, false, 2))
	mustAvailable(t, e, "acct", 2, 400)
}

// 错误按优先级只报第一个：
// 参数非法 > 时钟回退 > 编号重复 > 授权不存在 > 授权已终结 > 超容差 > 额度不足。
func TestErrorPriority(t *testing.T) {
	e := newEngine(t, 2, 0)
	mustOK(t, e.CreateAccount("acct", 100, 0))
	mustOK(t, e.Authorize("acct", "a1", 100, 1))
	mustOK(t, e.Reverse("a1", 2))

	// invalid param beats clock regression
	mustErr(t, e.Capture("a1", 0, false, 0), ErrInvalidParam)
	// clock regression beats duplicate id / unknown auth / terminated
	mustErr(t, e.Authorize("acct", "a1", 10, 0), ErrClockRegression)
	mustErr(t, e.Capture("ghost", 10, false, 0), ErrClockRegression)
	mustErr(t, e.Capture("a1", 10, false, 0), ErrClockRegression)
	// duplicate id beats insufficient funds and unknown account
	mustErr(t, e.Authorize("acct", "a1", 1000, 2), ErrDuplicateAuthID)
	mustErr(t, e.Authorize("ghost", "a1", 10, 2), ErrDuplicateAuthID)
	// unknown auth beats terminated/over-tolerance/insufficient (n/a combined,
	// but unknown auth alone must be reported)
	mustErr(t, e.Capture("ghost", 10, false, 2), ErrAuthNotFound)
	// terminated beats over-tolerance and insufficient funds
	mustErr(t, e.Capture("a1", 1000, false, 2), ErrAuthTerminated)
	mustErr(t, e.Increment("a1", 1000, 2), ErrAuthTerminated)
	// over-tolerance beats insufficient funds
	mustOK(t, e.Authorize("acct", "a2", 100, 2)) // available 0
	mustErr(t, e.Capture("a2", 101, false, 2), ErrOverTolerance)
	// refund chain: invalid > clock > unknown > exceeds
	mustErr(t, e.Refund("a2", 0, 1), ErrInvalidParam)
	mustErr(t, e.Refund("a2", 1, 1), ErrClockRegression)
	mustErr(t, e.Refund("ghost", 1, 2), ErrAuthNotFound)
	mustErr(t, e.Refund("a2", 1, 2), ErrRefundExceeds)
}

// 时钟回退被拒绝且不改变时钟。
func TestClockRegression(t *testing.T) {
	e := newEngine(t, 5, 0)
	mustOK(t, e.CreateAccount("acct", 100, 3))
	mustErr(t, e.Authorize("acct", "a1", 10, 2), ErrClockRegression)
	// Clock untouched: day 3 still accepted.
	mustOK(t, e.Authorize("acct", "a1", 10, 3))
	// Queries also reject a regressed now.
	if _, err := e.Available("acct", 2); !errors.Is(err, ErrClockRegression) {
		t.Fatalf("want clock regression on query, got %v", err)
	}
	if _, err := e.AuthState("a1", 2); !errors.Is(err, ErrClockRegression) {
		t.Fatalf("want clock regression on query, got %v", err)
	}
	// Queries do not move the clock: same-day operation still accepted.
	mustOK(t, e.Capture("a1", 5, false, 3))
}

// 授权编号全局唯一：已终结的编号复用仍报重复；失败的授权不占用编号。
func TestDuplicateAuthID(t *testing.T) {
	e := newEngine(t, 5, 0)
	mustOK(t, e.CreateAccount("acct", 100, 0))
	mustOK(t, e.Authorize("acct", "a1", 50, 0))
	mustOK(t, e.Reverse("a1", 0))
	mustErr(t, e.Authorize("acct", "a1", 10, 0), ErrDuplicateAuthID)

	// A rejected authorize does not register the id.
	mustErr(t, e.Authorize("acct", "a2", 1000, 0), ErrInsufficientFunds)
	mustOK(t, e.AdjustCreditLimit("acct", 2000, 0))
	mustOK(t, e.Authorize("acct", "a2", 1000, 0))

	// Ids are global across accounts.
	mustOK(t, e.CreateAccount("other", 100, 0))
	mustErr(t, e.Authorize("other", "a2", 10, 0), ErrDuplicateAuthID)
}

// 退款只减少已入账余额，不恢复持有，不改变状态与容差；已终结授权可退款。
func TestRefundRules(t *testing.T) {
	e := newEngine(t, 2, 0)
	mustOK(t, e.CreateAccount("acct", 1000, 0))
	mustOK(t, e.Authorize("acct", "a1", 100, 0))
	mustOK(t, e.Capture("a1", 100, true, 0)) // final capture, terminated
	mustAvailable(t, e, "acct", 0, 900)

	// Refund on a terminated authorization works and only moves posted balance.
	mustOK(t, e.Refund("a1", 60, 1))
	mustAvailable(t, e, "acct", 1, 960)
	mustAuthState(t, e, "a1", 1, AuthView{
		ID: "a1", AccountID: "acct", Status: StatusFinalCaptured,
		RemainingHold: 0, TotalAuthorized: 100, TotalCaptured: 100,
		TotalRefunded: 60, ExpiryDay: 2,
	})
	// Cumulative refunds cannot exceed cumulative captures.
	mustErr(t, e.Refund("a1", 41, 1), ErrRefundExceeds)
	mustOK(t, e.Refund("a1", 40, 1))
	mustErr(t, e.Refund("a1", 1, 1), ErrRefundExceeds)
	mustAvailable(t, e, "acct", 1, 1000)

	// Refund does not revive or extend the authorization.
	mustErr(t, e.Capture("a1", 1, false, 1), ErrAuthTerminated)

	// Refund on a reversed authorization with partial captures.
	mustOK(t, e.Authorize("acct", "a2", 100, 1))
	mustOK(t, e.Capture("a2", 40, false, 1))
	mustOK(t, e.Reverse("a2", 1))
	mustOK(t, e.Refund("a2", 40, 2))
	mustErr(t, e.Refund("a2", 1, 2), ErrRefundExceeds)
}

// 信用额度调高立即生效；调低不得使可用额度为负，否则整体拒绝。
func TestAdjustCreditLimit(t *testing.T) {
	e := newEngine(t, 5, 0)
	mustOK(t, e.CreateAccount("acct", 1000, 0))
	mustOK(t, e.Authorize("acct", "a1", 400, 0))
	mustOK(t, e.Capture("a1", 100, false, 0)) // posted 100, hold 300

	// Lowering to 400 keeps available at exactly 0: accepted.
	mustOK(t, e.AdjustCreditLimit("acct", 400, 0))
	mustAvailable(t, e, "acct", 0, 0)
	// Lowering further would make it negative: rejected, nothing changes.
	mustErr(t, e.AdjustCreditLimit("acct", 399, 0), ErrInsufficientFunds)
	mustAvailable(t, e, "acct", 0, 0)
	// Raising takes effect immediately.
	mustOK(t, e.AdjustCreditLimit("acct", 5000, 0))
	mustAvailable(t, e, "acct", 0, 4600)
	mustErr(t, e.AdjustCreditLimit("ghost", 10, 0), ErrAccountNotFound)
	mustErr(t, e.AdjustCreditLimit("acct", -1, 0), ErrInvalidParam)
}

// 查询可用额度的开销不随历史授权总数增长，也不随账户总数增长。
// 通过 ScanSteps  instrumentation 可验证地证明。
func TestQueryCostIndependentOfHistoryAndAccounts(t *testing.T) {
	const daySpan = 10
	queryCost := func(e *Engine, acct string, now int64) int64 {
		before := e.ScanSteps()
		if _, err := e.Available(acct, now); err != nil {
			t.Fatalf("Available: %v", err)
		}
		return e.ScanSteps() - before
	}

	// History size N: N authorizations created and terminated in the past.
	costs := make([]int64, 0, 3)
	for _, n := range []int64{100, 1000, 10000} {
		e := newEngine(t, 1, 0)
		mustOK(t, e.CreateAccount("acct", 1<<40, 0))
		day := int64(0)
		for i := int64(0); i < n; i++ {
			id := string(rune('a'+i%26)) + "-" + string(rune('A'+i/26%26)) + "-" + itoa(i)
			mustOK(t, e.Authorize("acct", id, 1, day))
			mustOK(t, e.Reverse(id, day))
			day++ // advance time so expired buckets get swept
		}
		mustOK(t, e.CreateAccount("busy", 1, day)) // accepted op sweeps nothing new
		costs = append(costs, queryCost(e, "acct", day+daySpan))
	}
	if !(costs[0] == costs[1] && costs[1] == costs[2]) {
		t.Fatalf("query cost grows with history: %v", costs)
	}
	t.Logf("query scan steps with history 100/1000/10000: %v (constant)", costs)

	// Total account count M: querying one account never touches the others.
	e := newEngine(t, 1, 0)
	mustOK(t, e.CreateAccount("target", 100, 0))
	mustOK(t, e.Authorize("target", "keep", 10, 0))
	costOne := queryCost(e, "target", 5)
	for i := 0; i < 10000; i++ {
		mustOK(t, e.CreateAccount("acc-"+itoa(int64(i)), 1, 0))
	}
	costMany := queryCost(e, "target", 5)
	if costOne != costMany {
		t.Fatalf("query cost grows with account count: %d -> %d", costOne, costMany)
	}
	t.Logf("query scan steps with 1 vs 10001 accounts: %d vs %d", costOne, costMany)
}

// 摊还证明：整个运行期间 sweep+query 的总扫描步数与操作数成线性关系。
func TestSweepCostAmortizedConstant(t *testing.T) {
	e := newEngine(t, 3, 0)
	mustOK(t, e.CreateAccount("acct", 1<<40, 0))
	const ops = 20000
	day := int64(0)
	for i := 0; i < ops; i++ {
		id := "a-" + itoa(int64(i))
		mustOK(t, e.Authorize("acct", id, 1, day))
		if i%2 == 0 {
			mustOK(t, e.Reverse(id, day))
		}
		day += int64(i % 3) // irregular time advances, up to 2 days per op
	}
	steps := e.ScanSteps()
	// Each bucket is created once and swept once; day-driven scans are bounded
	// by total elapsed days (< 2*ops). A generous linear bound proves amortized
	// O(1) per operation.
	if limit := int64(20 * ops); steps > limit {
		t.Fatalf("scan steps %d exceed linear bound %d", steps, limit)
	}
	t.Logf("total scan steps after %d ops: %d (%.2f per op)", ops, steps, float64(steps)/ops)
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

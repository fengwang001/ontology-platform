package payledger

import "testing"

const (
	testE   = 7    // 有效期天数
	testBps = 1000 // 容差 10%
)

func newTestLedger() *Ledger {
	return NewLedger(Config{ExpiryDays: testE, ToleranceBps: testBps})
}

func mustOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("期望成功, 实际错误: %v", err)
	}
}

func mustErr(t *testing.T, err error, kind ErrKind) {
	t.Helper()
	if err == nil {
		t.Fatalf("期望错误类别 %d, 实际成功", kind)
	}
	e, ok := err.(*Error)
	if !ok {
		t.Fatalf("错误类型应为 *Error, 实际 %T: %v", err, err)
	}
	if e.Kind != kind {
		t.Fatalf("期望错误类别 %d, 实际 %d (%v)", kind, e.Kind, e)
	}
}

func mustAvail(t *testing.T, l *Ledger, acct string, now, want int64) {
	t.Helper()
	got, err := l.Available(acct, now)
	if err != nil {
		t.Fatalf("查询可用额度失败: %v", err)
	}
	if got != want {
		t.Fatalf("可用额度: 期望 %d, 实际 %d (acct=%s now=%d)", want, got, acct, now)
	}
}

func mustStatus(t *testing.T, l *Ledger, authID string, now int64, want AuthStatus) {
	t.Helper()
	snap, err := l.AuthSnapshot(authID, now)
	if err != nil {
		t.Fatalf("查询授权失败: %v", err)
	}
	if snap.Status != want {
		t.Fatalf("授权状态: 期望 %s, 实际 %s (auth=%s now=%d)", want, snap.Status, authID, now)
	}
}

// 授权占用持有；截止日当天仍占用，次日起自动释放（不依赖任何操作触发）。
func TestAuthorizeHoldAndAutoExpiry(t *testing.T) {
	l := newTestLedger()
	mustOK(t, l.CreateAccount("c1", 1000, 0))
	mustOK(t, l.Authorize("c1", "a1", 400, 10)) // 有效期 [10, 17]
	mustAvail(t, l, "c1", 10, 600)
	mustAvail(t, l, "c1", 17, 600)  // 截止日当天持有仍有效
	mustAvail(t, l, "c1", 18, 1000) // 次日起自动释放
	mustAvail(t, l, "c1", 100, 1000)
	mustStatus(t, l, "a1", 17, StatusActive)
	mustStatus(t, l, "a1", 18, StatusExpired)
}

// 截止日恰等及差一天：截止日当天捕获/增量有效，次日起报授权已终结。
func TestExpiryBoundary(t *testing.T) {
	l := newTestLedger()
	mustOK(t, l.CreateAccount("c1", 10000, 0))
	mustOK(t, l.Authorize("c1", "a1", 500, 10)) // 截止日 17
	mustOK(t, l.Capture("a1", 100, false, 17))  // 恰等截止日：有效
	mustErr(t, l.Capture("a1", 100, false, 18), ErrAuthTerminated)
	mustErr(t, l.Increment("a1", 100, 18), ErrAuthTerminated)
	mustErr(t, l.Void("a1", 18), ErrAuthTerminated)
}

// 增量授权：额外可用额度仅按增量金额计算；有效期重置； (+E)；
// 失败的增量不影响原授权及其有效期。
func TestIncrement(t *testing.T) {
	l := newTestLedger()
	mustOK(t, l.CreateAccount("c1", 600, 0))
	mustOK(t, l.Authorize("c1", "a1", 500, 0)) // 截止日 7

	// 可用只有 100，增量 200 失败；原授权不受影响。
	mustErr(t, l.Increment("a1", 200, 3), ErrInsufficientFunds)
	mustAvail(t, l, "c1", 3, 100)
	snap, _ := l.AuthSnapshot("a1", 3)
	if snap.ExpiryDay != 7 || snap.CumAuth != 500 || snap.RemainingHold != 500 {
		t.Fatalf("失败增量改变了原授权: %+v", snap)
	}

	// 增量 100 成功：有效期重置为 3+7=10，持有 600。
	mustOK(t, l.Increment("a1", 100, 3))
	mustAvail(t, l, "c1", 3, 0)
	mustAvail(t, l, "c1", 10, 0)   // 新截止日当天仍持有
	mustAvail(t, l, "c1", 11, 600) // 次日释放
	snap, _ = l.AuthSnapshot("a1", 3)
	if snap.ExpiryDay != 10 || snap.CumAuth != 600 {
		t.Fatalf("增量后快照不符: %+v", snap)
	}
}

// 增量发生在过期前最后一天，有效期照常重置。
func TestIncrementOnLastValidDay(t *testing.T) {
	l := newTestLedger()
	mustOK(t, l.CreateAccount("c1", 1000, 0))
	mustOK(t, l.Authorize("c1", "a1", 100, 0)) // 截止日 7
	mustOK(t, l.Increment("a1", 50, 7))        // 最后一天增量，重置为 14
	mustAvail(t, l, "c1", 14, 850)
	mustAvail(t, l, "c1", 15, 1000)
	mustOK(t, l.Capture("a1", 150, false, 14))
	mustErr(t, l.Capture("a1", 1, false, 15), ErrAuthTerminated)
}

// 超出剩余持有的捕获：超出部分由可用额度覆盖，持有清零；
// 后续捕获只能全部来自可用额度，且仍受累计容差上限约束。
func TestOverCaptureFromAvailable(t *testing.T) {
	l := newTestLedger() // 容差 10%
	mustOK(t, l.CreateAccount("c1", 1000, 0))
	mustOK(t, l.Authorize("c1", "a1", 100, 0)) // 上限 = 100 + 10 = 110

	mustOK(t, l.Capture("a1", 110, false, 1)) // 100 来自持有, 10 来自可用额度
	mustAvail(t, l, "c1", 1, 890)             // 1000 - 110
	snap, _ := l.AuthSnapshot("a1", 1)
	if snap.RemainingHold != 0 || snap.Captured != 110 {
		t.Fatalf("超额捕获后快照不符: %+v", snap)
	}

	// 持有已清零，再捕获只能来自可用额度，且受容差上限约束。
	mustErr(t, l.Capture("a1", 1, false, 2), ErrOverTolerance)

	// 增量提高累计授权额后，容差上限重新判定，可继续捕获。
	mustOK(t, l.Increment("a1", 100, 2)) // 新上限 = 200 + 20 = 220
	mustOK(t, l.Capture("a1", 110, false, 2))
	mustAvail(t, l, "c1", 2, 780) // 1000 - 220
	mustErr(t, l.Capture("a1", 1, false, 3), ErrOverTolerance)
}

// 容差上浮部分向下取整。
func TestToleranceFloor(t *testing.T) {
	l := NewLedger(Config{ExpiryDays: 30, ToleranceBps: 55}) // 0.55%
	mustOK(t, l.CreateAccount("c1", 100000, 0))
	// 累计授权 100：上浮 floor(100*55/10000)=0，上限 100。
	mustOK(t, l.Authorize("c1", "a1", 100, 0))
	mustErr(t, l.Capture("a1", 101, false, 1), ErrOverTolerance)
	mustOK(t, l.Capture("a1", 100, false, 1))
	// 累计授权 200：上浮 floor(200*55/10000)=1，上限 201。
	mustOK(t, l.Authorize("c1", "a2", 200, 1))
	mustErr(t, l.Capture("a2", 202, false, 2), ErrOverTolerance)
	mustOK(t, l.Capture("a2", 201, false, 2))
}

// 分次捕获：持有与已入账同步增减；终捕立即释放剩余持有并终结授权。
func TestPartialAndFinalCapture(t *testing.T) {
	l := newTestLedger()
	mustOK(t, l.CreateAccount("c1", 1000, 0))
	mustOK(t, l.Authorize("c1", "a1", 500, 0))
	mustOK(t, l.Capture("a1", 200, false, 1))
	mustAvail(t, l, "c1", 1, 500)            // 1000 - 200入账 - 300持有
	mustOK(t, l.Capture("a1", 100, true, 2)) // 终捕：释放剩余 200
	mustAvail(t, l, "c1", 2, 700)            // 1000 - 300入账
	mustStatus(t, l, "a1", 2, StatusFinalCaptured)
	mustErr(t, l.Capture("a1", 1, false, 3), ErrAuthTerminated)
	mustErr(t, l.Increment("a1", 1, 3), ErrAuthTerminated)
	mustErr(t, l.Void("a1", 3), ErrAuthTerminated)
}

// 撤销释放剩余持有，已捕获部分不受影响；撤销后捕获被拒。
func TestVoid(t *testing.T) {
	l := newTestLedger()
	mustOK(t, l.CreateAccount("c1", 1000, 0))
	mustOK(t, l.Authorize("c1", "a1", 500, 0))
	mustOK(t, l.Capture("a1", 200, false, 1))
	mustOK(t, l.Void("a1", 2))
	mustAvail(t, l, "c1", 2, 800) // 释放 300 持有, 保留 200 入账
	mustStatus(t, l, "a1", 2, StatusVoided)
	mustErr(t, l.Capture("a1", 1, false, 3), ErrAuthTerminated)
}

// 过期持有自动释放后额度可再用（无需任何操作触发释放）。
func TestExpiredHoldReusable(t *testing.T) {
	l := newTestLedger()
	mustOK(t, l.CreateAccount("c1", 500, 0))
	mustOK(t, l.Authorize("c1", "a1", 500, 0)) // 占满, 截止日 7
	mustErr(t, l.Authorize("c1", "a2", 1, 1), ErrInsufficientFunds)
	mustOK(t, l.Authorize("c1", "a2", 500, 8)) // a1 已过期, 额度可再用
	mustAvail(t, l, "c1", 8, 0)
}

// 时钟回退：now 小于上一次被接受操作的 now 报错；被拒绝操作不推进时钟。
func TestClockRollback(t *testing.T) {
	l := newTestLedger()
	mustOK(t, l.CreateAccount("c1", 1000, 10))
	mustErr(t, l.Authorize("c1", "a1", 100, 9), ErrClockRollback)
	// 被拒绝的操作不推进时钟：now=9 未被接受。
	mustOK(t, l.Authorize("c1", "a1", 100, 10))
	// 参数非法优先于时钟回退。
	mustErr(t, l.Authorize("c1", "a2", -5, 0), ErrInvalidParam)
	// 一个 now 很大但被拒绝（额度不足）的操作不推进时钟。
	mustErr(t, l.Authorize("c1", "a3", 99999, 1000), ErrInsufficientFunds)
	mustOK(t, l.Authorize("c1", "a3", 100, 11))
}

// 被拒绝的操作不留痕：状态、可用额度、时钟、编号占用均不变。
func TestRejectedOpLeavesNoTrace(t *testing.T) {
	l := newTestLedger()
	mustOK(t, l.CreateAccount("c1", 500, 0))
	mustOK(t, l.Authorize("c1", "a1", 200, 1))
	before, _ := l.Available("c1", 1)

	// 各种失败操作（携带很大的 now，若留痕会污染时钟）。
	mustErr(t, l.Authorize("c1", "a2", 9999, 100), ErrInsufficientFunds)
	mustErr(t, l.Capture("nope", 1, false, 100), ErrAuthNotFound)
	mustErr(t, l.Capture("a1", 99999, false, 100), ErrAuthTerminated) // 100 时 a1 已过期
	mustErr(t, l.Refund("a1", 1, 100), ErrRefundExceeds)
	mustErr(t, l.AdjustCredit("c1", 50, 2), ErrInsufficientFunds) // 持有 200, 调至 50 将为负

	after, _ := l.Available("c1", 1)
	if before != after {
		t.Fatalf("被拒绝操作改变了可用额度: %d -> %d", before, after)
	}
	// 时钟未被污染：now=2 仍被接受。
	mustOK(t, l.Capture("a1", 50, false, 2))
	// 失败的授权编号未被占用。
	mustOK(t, l.Authorize("c1", "a2", 100, 3))
}

// 授权编号全局唯一：复用已终结授权的编号也报编号重复；
// 其优先级位于时钟回退之后、其余检查之前。
func TestDuplicateAuthID(t *testing.T) {
	l := newTestLedger()
	mustOK(t, l.CreateAccount("c1", 1000, 0))
	mustOK(t, l.Authorize("c1", "a1", 100, 1))
	mustOK(t, l.Void("a1", 2))
	mustErr(t, l.Authorize("c1", "a1", 100, 3), ErrDuplicateAuthID)
	// 优先级：时钟回退 > 编号重复。
	mustErr(t, l.Authorize("c1", "a1", 100, 0), ErrClockRollback)
	// 优先级：编号重复 > 账户不存在 / 额度不足。
	mustErr(t, l.Authorize("ghost", "a1", 100, 4), ErrDuplicateAuthID)
	mustErr(t, l.Authorize("c1", "a1", 99999, 4), ErrDuplicateAuthID)
}

// 捕获错误优先级：参数非法 > 时钟回退 > 授权不存在 > 已终结 > 超容差 > 额度不足。
func TestCaptureErrorPriority(t *testing.T) {
	l := NewLedger(Config{ExpiryDays: 5, ToleranceBps: 0})
	mustOK(t, l.CreateAccount("c1", 100, 10))
	mustOK(t, l.Authorize("c1", "a1", 100, 10)) // 占满全部额度

	mustErr(t, l.Capture("a1", 0, false, 0), ErrInvalidParam)     // 非法+回退 -> 非法
	mustErr(t, l.Capture("ghost", 1, false, 9), ErrClockRollback) // 回退+不存在 -> 回退
	mustErr(t, l.Capture("ghost", 1, false, 11), ErrAuthNotFound)
	mustOK(t, l.Void("a1", 12))
	mustErr(t, l.Capture("a1", 999, false, 13), ErrAuthTerminated) // 终结+超容差 -> 终结

	mustOK(t, l.Authorize("c1", "a2", 100, 14))
	mustErr(t, l.Capture("a2", 101, false, 15), ErrOverTolerance) // 超容差+额度足 -> 超容差

	// 额度不足：授权 100 已全部占用, 另一授权超额捕获时可用为 0。
	l2 := NewLedger(Config{ExpiryDays: 5, ToleranceBps: 10000})
	mustOK(t, l2.CreateAccount("c1", 100, 0))
	mustOK(t, l2.Authorize("c1", "a1", 100, 0))
	mustErr(t, l2.Capture("a1", 101, false, 1), ErrInsufficientFunds) // 超出持有 1, 可用 0
}

// 退款：只减少已入账余额；不恢复持有、不改变状态与有效期；
// 合计不得超过累计已捕获额；已终结授权仍可退款。
func TestRefund(t *testing.T) {
	l := newTestLedger()
	mustOK(t, l.CreateAccount("c1", 1000, 0))
	mustOK(t, l.Authorize("c1", "a1", 500, 0))
	mustOK(t, l.Capture("a1", 200, false, 1))
	mustAvail(t, l, "c1", 1, 500)

	mustOK(t, l.Refund("a1", 50, 2))
	mustAvail(t, l, "c1", 2, 550) // 入账 200->150, 持有 300 不变
	mustErr(t, l.Refund("a1", 151, 3), ErrRefundExceeds)
	mustOK(t, l.Refund("a1", 150, 3)) // 累计退款 200 = 累计捕获
	mustErr(t, l.Refund("a1", 1, 4), ErrRefundExceeds)

	// 已终结（撤销）授权仍可退款；退款不改变状态。
	mustOK(t, l.Authorize("c1", "a2", 100, 5))
	mustOK(t, l.Capture("a2", 100, true, 6))
	mustOK(t, l.Refund("a2", 40, 7))
	mustStatus(t, l, "a2", 7, StatusFinalCaptured)
	snap, _ := l.AuthSnapshot("a2", 7)
	if snap.Refunded != 40 || snap.Captured != 100 {
		t.Fatalf("退款后快照不符: %+v", snap)
	}

	// 退款优先级：参数非法 > 时钟回退 > 授权不存在 > 超出可退额。
	mustErr(t, l.Refund("a1", 0, 0), ErrInvalidParam)
	mustErr(t, l.Refund("ghost", 1, 6), ErrClockRollback)
	mustErr(t, l.Refund("ghost", 1, 8), ErrAuthNotFound)
}

// 信用额度调整：调高立即生效；调低不得使可用额度为负。
func TestAdjustCredit(t *testing.T) {
	l := newTestLedger()
	mustOK(t, l.CreateAccount("c1", 1000, 0))
	mustOK(t, l.Authorize("c1", "a1", 400, 1))
	mustOK(t, l.Capture("a1", 100, false, 2)) // 入账 100, 持有 300

	mustErr(t, l.AdjustCredit("c1", 399, 3), ErrInsufficientFunds) // 可用将变为 -1
	mustOK(t, l.AdjustCredit("c1", 400, 3))                        // 可用恰好 0
	mustAvail(t, l, "c1", 3, 0)
	mustOK(t, l.AdjustCredit("c1", 2000, 4)) // 调高立即生效
	mustAvail(t, l, "c1", 4, 1600)
	mustErr(t, l.AdjustCredit("ghost", 1, 5), ErrAccountNotFound)
}

// 查询不修改状态、不触碰时钟；任意时刻（包括早于历史查询的时刻）查询结果
// 只取决于已接受的操作与查询 now。
func TestQueryIsPure(t *testing.T) {
	l := newTestLedger()
	mustOK(t, l.CreateAccount("c1", 1000, 10))
	mustOK(t, l.Authorize("c1", "a1", 400, 10)) // 截止日 17

	// 远期查询不推进时钟、不破坏后续操作的持有。
	mustAvail(t, l, "c1", 1000, 1000)
	mustOK(t, l.Capture("a1", 100, false, 15)) // now=15 < 1000 仍被接受
	mustAvail(t, l, "c1", 15, 600)             // 1000 - 100入账 - 300持有

	// 后向查询：now=12 时 a1 未过期且未捕获。
	mustAvail(t, l, "c1", 12, 600)
	// 前向查询：now=18 时持有已过期。
	mustAvail(t, l, "c1", 18, 900)
	// 再次后向查询结果一致（可复现）。
	mustAvail(t, l, "c1", 12, 600)
	mustAvail(t, l, "c1", 15, 600)
}

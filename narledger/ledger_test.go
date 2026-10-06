package narledger

import (
	"errors"
	"testing"
)

func mustOK(t *testing.T, err error, ctx string) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: 意外错误 %v", ctx, err)
	}
}

func codeOf(err error) ErrorCode {
	var e *OpError
	if errors.As(err, &e) {
		return e.Code
	}
	return 0
}

func grantPair(t *testing.T, l *Ledger, a, b string, start, end int64) {
	t.Helper()
	mustOK(t, l.RegisterGrant(a, start, end), "grant "+a)
	mustOK(t, l.RegisterGrant(b, start, end), "grant "+b)
}

// 效期恰等于 now：可发严格要求 now < expireAt。
func TestExpireBoundary(t *testing.T) {
	l := New()
	grantPair(t, l, "r1", "r2", 0, 1000)
	mustOK(t, l.Receive(0, "D", "L1", 10, 100), "receive")
	if err := l.Dispense(100, "o1", "dept", "doc", "D", 1, "r1", "r2"); codeOf(err) != ErrStock {
		t.Fatalf("now==expireAt 期望库存不足, got %v", err)
	}
	mustOK(t, l.Dispense(99, "o1", "dept", "doc", "D", 1, "r1", "r2"), "dispense at 99")
	bal, err := l.BatchBalance(99, "D", "L1")
	mustOK(t, err, "balance")
	if bal != 9 {
		t.Fatalf("余量应为 9, got %d", bal)
	}
}

// 授权失效边界恰取等：[start,end) 左闭右开。
func TestAuthBoundary(t *testing.T) {
	// 左闭：t=start 时有效
	l := New()
	mustOK(t, l.Receive(0, "D", "L1", 10, 1000), "receive")
	grantPair(t, l, "r1", "r2", 10, 20)
	mustOK(t, l.Dispense(10, "o1", "dept", "doc", "D", 1, "r1", "r2"), "t=10 生效")
	// 右开：t=end 时失效
	if err := l.Dispense(20, "o2", "dept", "doc", "D", 1, "r1", "r2"); codeOf(err) != ErrUnauthorized {
		t.Fatalf("t=20 期望授权无效, got %v", err)
	}
	// start-1 尚未生效的边界直接在授权名单上验证（操作时钟单调无法回放）
	ab := newAuthBook()
	ab.add("x", 10, 20)
	if ab.validAt("x", 9) || !ab.validAt("x", 10) || !ab.validAt("x", 19) || ab.validAt("x", 20) {
		t.Fatal("授权区间 [10,20) 的左闭右开边界错误")
	}
}

// 同一人两次复核 / 复核人为申请人本人。
func TestReviewerRules(t *testing.T) {
	l := New()
	mustOK(t, l.RegisterGrant("r1", 0, 1000), "grant")
	mustOK(t, l.Receive(0, "D", "L", 5, 1000), "receive")
	if err := l.Dispense(0, "o1", "d", "doc", "D", 1, "r1", "r1"); codeOf(err) != ErrReviewer {
		t.Fatalf("同一人复核期望 REVIEWER_INVALID, got %v", err)
	}
	if err := l.Dispense(0, "o1", "d", "r1", "D", 1, "r1", "r2"); codeOf(err) != ErrReviewer {
		t.Fatalf("申请人即复核人应优先报 REVIEWER_INVALID, got %v", err)
	}
}

// 跨批次领用的原子回滚：总量不足时不分出任何数量。
func TestCrossBatchAtomicRollback(t *testing.T) {
	l := New()
	grantPair(t, l, "r1", "r2", 0, 1000)
	mustOK(t, l.Receive(0, "D", "early", 4, 100), "r1")
	mustOK(t, l.Receive(0, "D", "later", 4, 200), "r2")
	if err := l.Dispense(0, "o1", "d", "doc", "D", 9, "r1", "r2"); codeOf(err) != ErrStock {
		t.Fatalf("期望库存不足, got %v", err)
	}
	for _, lot := range []string{"early", "later"} {
		bal, _ := l.BatchBalance(0, "D", lot)
		if bal != 4 {
			t.Fatalf("回滚失败：批次 %s 余量 %d != 4", lot, bal)
		}
	}
	mustOK(t, l.Dispense(0, "o2", "d", "doc", "D", 7, "r1", "r2"), "dispense 7")
	if b, _ := l.BatchBalance(0, "D", "early"); b != 0 {
		t.Fatalf("early 应耗尽, got %d", b)
	}
	if b, _ := l.BatchBalance(0, "D", "later"); b != 1 {
		t.Fatalf("later 应余 1, got %d", b)
	}
	v := l.Snapshot().Orders["o2"]
	if got := v.Lots[0] + "|" + v.Lots[1]; got != "early:4|later:3" {
		t.Fatalf("FEFO 分出记录错误: %v", v.Lots)
	}
}

// 结清期限：恰到期仍属期内；晚一秒逾期；逾期可补结。
func TestSettleDeadline(t *testing.T) {
	setup := func() *Ledger {
		l := New()
		grantPair(t, l, "r1", "r2", 0, 1e9)
		mustOK(t, l.Receive(0, "D", "L", 5, 1e9), "receive")
		return l
	}
	l := setup()
	mustOK(t, l.Dispense(0, "o1", "d", "doc", "D", 2, "r1", "r2"), "disp")
	if locked, _ := l.DeptLocked(86400, "d"); locked {
		t.Fatal("恰到期时刻不应锁定")
	}
	mustOK(t, l.Settle(86400, "o1", 2, 0, 0), "settle exactly")

	l = setup()
	mustOK(t, l.Dispense(0, "o1", "d", "doc", "D", 2, "r1", "r2"), "disp")
	locked, _ := l.DeptLocked(86401, "d")
	if !locked {
		t.Fatal("晚一秒应锁定")
	}
	if err := l.Dispense(86401, "o2", "d", "doc", "D", 1, "r1", "r2"); codeOf(err) != ErrDeptLocked {
		t.Fatalf("锁定期间应拒绝新领用, got %v", err)
	}
	mustOK(t, l.Settle(90000, "o1", 2, 0, 0), "逾期补结")
	if locked, _ = l.DeptLocked(90000, "d"); locked {
		t.Fatal("补结后应解锁")
	}
}

// 差额锁定与处理后解锁；处理差额不改变库存。
func TestDiscrepancyLockAndResolve(t *testing.T) {
	l := New()
	grantPair(t, l, "r1", "r2", 0, 1e9)
	mustOK(t, l.Receive(0, "D", "L", 10, 1e9), "receive")
	mustOK(t, l.Dispense(0, "o1", "d", "doc", "D", 5, "r1", "r2"), "disp")
	before, _ := l.BatchBalance(10, "D", "L")
	mustOK(t, l.Settle(10, "o1", 3, 0, 0), "short settle")
	if v := l.Snapshot().Orders["o1"]; v.Status != "DISCREPANCY" || v.Shortfall != 2 {
		t.Fatalf("应进入差额待处理, got %+v", v)
	}
	if locked, _ := l.DeptLocked(10, "d"); !locked {
		t.Fatal("差额待处理应锁定科室")
	}
	if err := l.Dispense(10, "o2", "d", "doc2", "D", 1, "r1", "r2"); codeOf(err) != ErrDeptLocked {
		t.Fatalf("期望科室锁定, got %v", err)
	}
	mustOK(t, l.ResolveDiscrepancy(20, "o1", "r1", "r2"), "resolve")
	if locked, _ := l.DeptLocked(20, "d"); locked {
		t.Fatal("差额处理后应解锁")
	}
	after, _ := l.BatchBalance(20, "D", "L")
	if before != after {
		t.Fatalf("处理差额不应改变库存: %d -> %d", before, after)
	}
	mustOK(t, l.CheckInvariant(), "invariant")
}

// 退回进入已过期批次：账面恢复但不可再发，可销毁。
func TestReturnIntoExpiredBatch(t *testing.T) {
	l := New()
	grantPair(t, l, "r1", "r2", 0, 1e9)
	mustOK(t, l.Receive(0, "D", "L", 5, 100), "receive")
	mustOK(t, l.Dispense(0, "o1", "d", "doc", "D", 3, "r1", "r2"), "disp")
	mustOK(t, l.Settle(100, "o1", 1, 2, 0), "settle with return")
	if bal, _ := l.BatchBalance(100, "D", "L"); bal != 4 {
		t.Fatalf("退回后期望账面 4, got %d", bal)
	}
	if err := l.Dispense(100, "o2", "d", "doc", "D", 1, "r1", "r2"); codeOf(err) != ErrStock {
		t.Fatalf("过期退回不应恢复可发, got %v", err)
	}
	mustOK(t, l.Destroy(100, "D", "L", 4, "r1", "r2"), "destroy")
	mustOK(t, l.CheckInvariant(), "invariant")
}

// 撤销授权后的后续操作；已完成领用不受影响。
func TestRevokeAuth(t *testing.T) {
	l := New()
	grantPair(t, l, "r1", "r2", 0, 1000)
	mustOK(t, l.Receive(0, "D", "L", 5, 1000), "receive")
	mustOK(t, l.Dispense(0, "o1", "d", "doc", "D", 1, "r1", "r2"), "disp before revoke")
	mustOK(t, l.RevokeGrant("r1", 50), "revoke")
	if err := l.Dispense(50, "o2", "d", "doc", "D", 1, "r1", "r2"); codeOf(err) != ErrUnauthorized {
		t.Fatalf("撤销后期望授权无效, got %v", err)
	}
	mustOK(t, l.RegisterGrant("r1", 60, 1000), "regrant")
	mustOK(t, l.Dispense(60, "o2", "d", "doc", "D", 1, "r1", "r2"), "disp after regrant")
}

// 错误优先级：参数非法 > 时钟回退 > 复核人 > 授权 > 对象不存在。
func TestErrorPriority(t *testing.T) {
	l := New()
	mustOK(t, l.RegisterGrant("r1", 0, 1000), "grant r1")
	mustOK(t, l.Receive(10, "D", "L", 5, 1000), "receive at 10")
	if err := l.Dispense(5, "", "d", "doc", "D", 1, "r1", "r1"); codeOf(err) != ErrInvalidParam {
		t.Fatalf("期望参数非法, got %v", err)
	}
	if err := l.Dispense(5, "oX", "d", "doc", "D", 1, "r1", "r1"); codeOf(err) != ErrClockRollback {
		t.Fatalf("期望时钟回退, got %v", err)
	}
	if err := l.Dispense(10, "oX", "d", "doc", "D", 1, "r1", "nobody"); codeOf(err) != ErrUnauthorized {
		t.Fatalf("期望授权无效, got %v", err)
	}
	mustOK(t, l.RegisterGrant("r2", 10, 1000), "grant r2")
	if err := l.Dispense(10, "oX", "d", "doc", "NOPE", 1, "r1", "r2"); codeOf(err) != ErrNotFound {
		t.Fatalf("期望对象不存在, got %v", err)
	}
	// 销毁超额报 ErrExceed；复核问题优先于数量超额
	if err := l.Destroy(10, "D", "L", 999, "r1", "r1"); codeOf(err) != ErrReviewer {
		t.Fatalf("同一人见证应优先 REVIEWER_INVALID, got %v", err)
	}
	if err := l.Destroy(10, "D", "L", 999, "r1", "r2"); codeOf(err) != ErrExceed {
		t.Fatalf("期望数量超出, got %v", err)
	}
}

// 未结清单据上限 3 张；被拒绝操作不推进时钟。
func TestOpenLimitAndRejectedNoClock(t *testing.T) {
	l := New()
	grantPair(t, l, "r1", "r2", 0, 1e9)
	mustOK(t, l.Receive(0, "D", "L", 100, 1e9), "receive")
	for i, id := range []string{"o1", "o2", "o3"} {
		mustOK(t, l.Dispense(int64(i), id, "d", "doc", "D", 1, "r1", "r2"), "disp "+id)
	}
	if err := l.Dispense(3, "o4", "d", "doc", "D", 1, "r1", "r2"); codeOf(err) != ErrOpenLimit {
		t.Fatalf("期望超过上限, got %v", err)
	}
	// 被拒绝的 now=3 不应推进时钟；此前最后接受时刻是 2，now=2 仍可用
	mustOK(t, l.Settle(2, "o1", 1, 0, 0), "settle o1 at t=2")
	mustOK(t, l.Dispense(2, "o4", "d", "doc", "D", 1, "r1", "r2"), "名额释放后可领用")
}

// 时钟回退被拒后状态完全不变。
func TestClockRollbackNoStateChange(t *testing.T) {
	l := New()
	grantPair(t, l, "r1", "r2", 0, 1e9)
	mustOK(t, l.Receive(100, "D", "L", 3, 1e9), "receive")
	err := l.Receive(50, "D", "L2", 3, 1e9)
	if codeOf(err) != ErrClockRollback {
		t.Fatalf("期望时钟回退, got %v", err)
	}
	if _, err := l.BatchBalance(100, "D", "L2"); codeOf(err) != ErrNotFound {
		t.Fatalf("回退操作不得登记批次, got %v", err)
	}
	if l.Snapshot().LastNow != 100 {
		t.Fatalf("时钟不应被回退操作改变, got %d", l.Snapshot().LastNow)
	}
}

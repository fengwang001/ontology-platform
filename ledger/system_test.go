package ledger

import (
	"reflect"
	"testing"
)

func mustOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("期望成功，实际失败: %v", err)
	}
}

func mustErr(t *testing.T, err error, kind ErrKind) {
	t.Helper()
	if err == nil {
		t.Fatalf("期望错误 %s，实际成功", kind)
	}
	e, ok := err.(*OpError)
	if !ok {
		t.Fatalf("错误类型不是 *OpError: %v", err)
	}
	if e.Kind != kind {
		t.Fatalf("期望错误 %s，实际 %s: %v", kind, e.Kind, e)
	}
}

func withdraw(s *System, now int64, id, dept, drug string, qty int) error {
	_, err := s.Withdraw(now, id, dept, "app1", drug, qty, []string{"r1", "r2"})
	return err
}

// 预置两名授权复核人 [0, 1e9]。
func newSysWithAuth(t *testing.T) *System {
	t.Helper()
	s := NewSystem()
	mustOK(t, s.GrantAuth(0, "r1", 0, 1_000_000_000))
	mustOK(t, s.GrantAuth(0, "r2", 0, 1_000_000_000))
	return s
}

func drugStat(t *testing.T, s *System, now int64, drug string) (book, avail int) {
	t.Helper()
	ds := s.Snapshot(now).Drugs[drug]
	return ds.BookTotal, ds.Available
}

// 效期恰等于 now 视为已过期；严格小于效期才可发。
func TestExpiryExactlyNow(t *testing.T) {
	s := newSysWithAuth(t)
	mustOK(t, s.Inbound(10, "d", "b1", 5, 100))
	// 恰等于效期：已过期，不可发；被拒绝不改变时钟（仍为 10）。
	mustErr(t, withdraw(s, 100, "s1", "k1", "d", 1), ErrStock)
	// now=99 合法（>=10），且批次未过期。
	mustOK(t, withdraw(s, 99, "s1", "k1", "d", 1))
	book, avail := drugStat(t, s, 99, "d")
	if book != 4 || avail != 4 {
		t.Fatalf("账面=%d 可用=%d，期望 4/4", book, avail)
	}
}

// 授权窗口左闭右开：生效时刻恰取等有效，失效时刻恰取等无效。
func TestAuthBoundary(t *testing.T) {
	s := NewSystem()
	mustOK(t, s.GrantAuth(0, "r1", 10, 20))
	mustOK(t, s.GrantAuth(0, "r2", 10, 20))
	mustOK(t, s.Inbound(10, "d", "b1", 10, 1_000_000_000))
	// now=10：生效时刻恰取等，左闭，有效。
	mustOK(t, withdraw(s, 10, "s1", "k1", "d", 1))
	// now=20：失效时刻恰取等，右开，无效。
	mustErr(t, withdraw(s, 20, "s2", "k1", "d", 1), ErrAuth)
	// now=19：仍有效。
	mustOK(t, withdraw(s, 19, "s2", "k1", "d", 1))
}

// 复核人人数不足、同一人、为申请人本人。
func TestReviewerRules(t *testing.T) {
	s := newSysWithAuth(t)
	mustOK(t, s.Inbound(0, "d", "b1", 10, 1_000_000_000))
	// 人数不足
	_, err := s.Withdraw(1, "s1", "k1", "a1", "d", 1, []string{"r1"})
	mustErr(t, err, ErrReviewer)
	// 同一人两次复核
	_, err = s.Withdraw(1, "s1", "k1", "a1", "d", 1, []string{"r1", "r1"})
	mustErr(t, err, ErrReviewer)
	// 复核人是申请人本人
	_, err = s.Withdraw(1, "s1", "k1", "r1", "d", 1, []string{"r1", "r2"})
	mustErr(t, err, ErrReviewer)
	// 被拒绝三次后时钟仍为 0，now=1 仍合法
	mustOK(t, withdraw(s, 1, "s1", "k1", "d", 1))
}

// 跨批次领用：效期最早优先，效期相同按入库先后；库存不足时全有或全无。
func TestCrossBatchAtomicity(t *testing.T) {
	s := newSysWithAuth(t)
	mustOK(t, s.Inbound(0, "d", "b1", 5, 200))
	mustOK(t, s.Inbound(0, "d", "b2", 5, 100)) // 效期更早，应先出
	mustOK(t, s.Inbound(0, "d", "b3", 5, 200)) // 与 b1 同效期，入库更晚
	lines, err := s.Withdraw(1, "s1", "k1", "app1", "d", 8, []string{"r1", "r2"})
	mustOK(t, err)
	want := []BatchLine{{BatchID: "b2", Qty: 5}, {BatchID: "b1", Qty: 3}}
	if len(lines) != 2 || lines[0] != want[0] || lines[1] != want[1] {
		t.Fatalf("分出明细=%v，期望 %v", lines, want)
	}
	// 剩余 7 支，领 8 支必失败且不得改变任何批次账面
	mustErr(t, withdraw(s, 2, "s2", "k1", "d", 8), ErrStock)
	snap := s.Snapshot(2)
	got := snap.Drugs["d"].Batches
	if got["b1"] != 2 || got["b2"] != 0 || got["b3"] != 5 {
		t.Fatalf("回滚后账面=%v，期望 b1=2 b2=0 b3=5", got)
	}
	if _, ok := snap.Slips["s2"]; ok {
		t.Fatal("被拒绝的领用不应留下单据")
	}
	if !s.InvariantOK() {
		t.Fatal("不变式被破坏")
	}
}

// 结清期限 24 小时：恰到期仍属期内，晚一秒即逾期并锁定科室。
func TestDeadlineBoundary(t *testing.T) {
	s := newSysWithAuth(t)
	mustOK(t, s.Inbound(0, "d", "b1", 100, 1_000_000_000))
	mustOK(t, withdraw(s, 1000, "s1", "k1", "d", 5))
	// 恰到期时刻结清，属期内
	mustOK(t, s.Settle(1000+settleWindow, "s1", 5, 0, 0))
	mustOK(t, withdraw(s, 1000+settleWindow, "s2", "k1", "d", 5))
	// 晚一秒未结清：科室锁定
	mustErr(t, withdraw(s, 1000+2*settleWindow+1, "s3", "k1", "d", 1), ErrDeptLocked)
	// 逾期单据仍可补结清，结清后锁定解除
	mustOK(t, s.Settle(1000+2*settleWindow+2, "s2", 5, 0, 0))
	mustOK(t, withdraw(s, 1000+2*settleWindow+2, "s3", "k1", "d", 1))
}

// 差额待处理锁定科室；两名复核人确认处理后解锁且库存不变。
func TestDiscrepancyLockAndResolve(t *testing.T) {
	s := newSysWithAuth(t)
	mustOK(t, s.Inbound(0, "d", "b1", 100, 1_000_000_000))
	mustOK(t, withdraw(s, 10, "s1", "k1", "d", 10))
	// 4+3+1=8 < 10：转差额待处理，差额 2
	mustOK(t, s.Settle(20, "s1", 4, 3, 1))
	sl := s.Snapshot(20).Slips["s1"]
	if sl.Status != SlipDiscrepancy || sl.Diff != 2 {
		t.Fatalf("单据状态=%s 差额=%d，期望差额待处理/2", sl.Status, sl.Diff)
	}
	mustErr(t, withdraw(s, 21, "s2", "k1", "d", 1), ErrDeptLocked)
	// 差额处理也须两名复核人
	mustErr(t, s.ResolveDiscrepancy(22, "s1", []string{"r1"}), ErrReviewer)
	bookBefore, _ := drugStat(t, s, 22, "d")
	mustOK(t, s.ResolveDiscrepancy(23, "s1", []string{"r1", "r2"}))
	bookAfter, _ := drugStat(t, s, 23, "d")
	if bookBefore != bookAfter {
		t.Fatal("差额处理不得改变库存")
	}
	if s.Snapshot(23).Slips["s1"].Status != SlipSettled {
		t.Fatal("处理后单据应视为已结清")
	}
	mustOK(t, withdraw(s, 24, "s2", "k1", "d", 1))
}

// 退回量退回原批次账面，即使该批次此时已过期；过期批次不计入可用库存。
func TestReturnToExpiredBatch(t *testing.T) {
	s := newSysWithAuth(t)
	mustOK(t, s.Inbound(0, "d", "b1", 10, 100))
	mustOK(t, withdraw(s, 50, "s1", "k1", "d", 6))
	// now=200 时批次已过期；退回 6 支仍回到账面
	mustOK(t, s.Settle(200, "s1", 0, 6, 0))
	book, avail := drugStat(t, s, 200, "d")
	if book != 10 || avail != 0 {
		t.Fatalf("账面=%d 可用=%d，期望 10/0", book, avail)
	}
	mustErr(t, withdraw(s, 201, "s2", "k1", "d", 1), ErrStock)
	// 过期批次可被销毁
	mustOK(t, s.Destroy(202, "d", "b1", 10, []string{"r1", "r2"}))
	book, _ = drugStat(t, s, 202, "d")
	if book != 0 {
		t.Fatalf("销毁后账面=%d，期望 0", book)
	}
	if !s.InvariantOK() {
		t.Fatal("不变式被破坏")
	}
}

// 撤销授权：撤销时刻起即失效；已完成的领用不受影响。
func TestRevokeAuth(t *testing.T) {
	s := NewSystem()
	mustOK(t, s.GrantAuth(0, "r1", 0, 100))
	mustOK(t, s.GrantAuth(0, "r2", 0, 100))
	mustOK(t, s.GrantAuth(0, "r3", 0, 100))
	mustOK(t, s.Inbound(10, "d", "b1", 10, 1_000_000_000))
	mustOK(t, withdraw(s, 20, "s1", "k1", "d", 5))
	mustOK(t, s.RevokeAuth(30, "r1"))
	// 撤销时刻起即失效
	_, err := s.Withdraw(30, "s2", "k1", "app1", "d", 1, []string{"r1", "r2"})
	mustErr(t, err, ErrAuth)
	// 已完成的领用可正常结清（结清不需要复核人）
	mustOK(t, s.Settle(31, "s1", 5, 0, 0))
	// 销毁见证也受授权约束
	mustErr(t, s.Destroy(32, "d", "b1", 1, []string{"r1", "r3"}), ErrAuth)
	mustOK(t, s.Destroy(32, "d", "b1", 1, []string{"r2", "r3"}))
	// 撤销不存在的人员
	mustErr(t, s.RevokeAuth(33, "nobody"), ErrNotFound)
}

// 未结清单据上限 3 张；差额待处理不计入。
func TestOpenSlipLimit(t *testing.T) {
	s := newSysWithAuth(t)
	mustOK(t, s.Inbound(0, "d", "b1", 100, 1_000_000_000))
	mustOK(t, withdraw(s, 1, "s1", "k1", "d", 1))
	mustOK(t, withdraw(s, 2, "s2", "k1", "d", 1))
	mustOK(t, withdraw(s, 3, "s3", "k1", "d", 1))
	// 第 4 张超出上限
	mustErr(t, withdraw(s, 4, "s4", "k1", "d", 1), ErrSlipLimit)
	// s1 结清为差额待处理：不再计入未结清数，但锁定科室
	mustOK(t, s.Settle(5, "s1", 0, 0, 0))
	mustErr(t, withdraw(s, 6, "s4", "k1", "d", 1), ErrDeptLocked)
	// 差额处理后解锁；此时未结清 2 张，可再领
	mustOK(t, s.ResolveDiscrepancy(7, "s1", []string{"r1", "r2"}))
	mustOK(t, withdraw(s, 8, "s4", "k1", "d", 1))
	// 又回到 3 张上限
	mustErr(t, withdraw(s, 9, "s5", "k1", "d", 1), ErrSlipLimit)
}

// 销毁：数量超出账面拒绝；未过期批次允许销毁；销毁不可撤回。
func TestDestroyRules(t *testing.T) {
	s := newSysWithAuth(t)
	mustOK(t, s.Inbound(0, "d", "b1", 10, 1_000_000_000))
	mustErr(t, s.Destroy(1, "d", "b1", 11, []string{"r1", "r2"}), ErrQuantity)
	mustErr(t, s.Destroy(1, "d", "bx", 1, []string{"r1", "r2"}), ErrNotFound)
	mustErr(t, s.Destroy(1, "dx", "b1", 1, []string{"r1", "r2"}), ErrNotFound)
	mustOK(t, s.Destroy(1, "d", "b1", 4, []string{"r1", "r2"}))
	book, avail := drugStat(t, s, 1, "d")
	if book != 6 || avail != 6 {
		t.Fatalf("销毁后账面=%d 可用=%d，期望 6/6", book, avail)
	}
	if !s.InvariantOK() {
		t.Fatal("不变式被破坏")
	}
}

// 被拒绝的操作不得改变状态与时钟。
func TestRejectedOpKeepsClockAndState(t *testing.T) {
	s := newSysWithAuth(t)
	mustOK(t, s.Inbound(100, "d", "b1", 10, 1_000_000_000))
	before := s.Snapshot(100)
	// 参数非法（数量为 0），now=500 不应被接受
	mustErr(t, withdraw(s, 500, "s1", "k1", "d", 0), ErrInvalidParam)
	// 时钟仍是 100：now=50 的回退判定以 100 为基准
	mustErr(t, withdraw(s, 50, "s1", "k1", "d", 1), ErrClockRollback)
	// now=100 合法且成功
	mustOK(t, withdraw(s, 100, "s1", "k1", "d", 1))
	after := s.Snapshot(100)
	if after.Drugs["d"].BookTotal != before.Drugs["d"].BookTotal-1 {
		t.Fatal("状态变化与预期不符")
	}
}

// 错误优先级：同一操作违反多项时只报优先级最高的一类。
func TestErrorPriority(t *testing.T) {
	s := NewSystem()
	mustOK(t, s.GrantAuth(0, "r1", 0, 1_000_000_000))
	mustOK(t, s.GrantAuth(0, "r2", 0, 1_000_000_000))
	mustOK(t, s.Inbound(10, "d", "b1", 10, 1_000_000_000))

	// 参数非法 + 时钟回退 → 参数非法
	_, err := s.Withdraw(5, "", "k1", "a1", "d", 1, []string{"r1", "r2"})
	mustErr(t, err, ErrInvalidParam)
	// 时钟回退 + 复核人同一人 → 时钟回退
	_, err = s.Withdraw(5, "x", "k1", "a1", "d", 1, []string{"r1", "r1"})
	mustErr(t, err, ErrClockRollback)
	// 复核人同一人 + 授权无效 + 药品不存在 → 复核人
	_, err = s.Withdraw(20, "x", "k1", "a1", "dx", 1, []string{"z1", "z1"})
	mustErr(t, err, ErrReviewer)
	// 授权无效 + 药品不存在 → 授权无效
	_, err = s.Withdraw(20, "x", "k1", "a1", "dx", 1, []string{"z1", "z2"})
	mustErr(t, err, ErrAuth)
	// 药品不存在 + 单据重复 → 对象不存在
	mustOK(t, withdraw(s, 20, "dup", "k1", "d", 1))
	_, err = s.Withdraw(21, "dup", "k1", "a1", "dx", 1, []string{"r1", "r2"})
	mustErr(t, err, ErrNotFound)
	// 单据重复 + 科室锁定 → 状态不符
	mustOK(t, withdraw(s, 22, "s1", "k2", "d", 1))
	mustOK(t, s.Settle(23, "s1", 0, 0, 0)) // k2 差额待处理 → 锁定
	_, err = s.Withdraw(24, "dup", "k2", "a1", "d", 1, []string{"r1", "r2"})
	mustErr(t, err, ErrState)
	// 科室锁定 + 超过上限 → 科室被锁定
	mustOK(t, s.ResolveDiscrepancy(25, "s1", []string{"r1", "r2"}))
	mustOK(t, withdraw(s, 26, "o1", "k3", "d", 1))
	mustOK(t, withdraw(s, 27, "o2", "k3", "d", 1))
	mustOK(t, withdraw(s, 28, "o3", "k3", "d", 1))
	mustOK(t, withdraw(s, 29, "o4", "k4", "d", 1))
	mustOK(t, s.Settle(30+settleWindow+1, "o4", 1, 0, 0)) // 已逾期后补结清
	mustOK(t, withdraw(s, 31+settleWindow+1, "o5", "k4", "d", 1))
	// k3 有 3 张未结清且 o1..o3 均已逾期 → 锁定优先于上限
	_, err = s.Withdraw(32+2*settleWindow, "o6", "k3", "a1", "d", 1, []string{"r1", "r2"})
	mustErr(t, err, ErrDeptLocked)
	// 超过上限 + 库存不足 → 超过上限（k3 解锁后仍 3 张未结清）
	mustOK(t, s.Settle(33+2*settleWindow, "o1", 1, 0, 0))
	mustOK(t, s.Settle(34+2*settleWindow, "o2", 1, 0, 0))
	mustOK(t, s.Settle(35+2*settleWindow, "o3", 1, 0, 0))
	mustOK(t, withdraw(s, 36+2*settleWindow, "p1", "k5", "d", 1))
	mustOK(t, withdraw(s, 37+2*settleWindow, "p2", "k5", "d", 1))
	mustOK(t, withdraw(s, 38+2*settleWindow, "p3", "k5", "d", 1))
	// 库存已不足（账面剩 0），但上限错误优先
	_, err = s.Withdraw(39+2*settleWindow, "p4", "k5", "a1", "d", 1, []string{"r1", "r2"})
	mustErr(t, err, ErrSlipLimit)
	// 库存不足 vs 数量超出：结清合计超出领出量 → 数量超出
	mustErr(t, s.Settle(40+2*settleWindow, "p1", 1, 1, 0), ErrQuantity)
	// 状态不符优先于数量超出：已结清单据再结清
	mustOK(t, s.Settle(41+2*settleWindow, "p1", 1, 0, 0))
	mustErr(t, s.Settle(42+2*settleWindow, "p1", 1, 1, 0), ErrState)
}

// 并发调用：结果等价于某个串行顺序，不变式始终成立。
func TestConcurrentOps(t *testing.T) {
	s := newSysWithAuth(t)
	const drugs = 8
	for i := 0; i < drugs; i++ {
		mustOK(t, s.Inbound(0, string(rune('a'+i)), "b1", 1000, 1_000_000_000))
	}
	done := make(chan struct{})
	for i := 0; i < drugs; i++ {
		i := i
		go func() {
			defer func() { done <- struct{}{} }()
			drug := string(rune('a' + i))
			for j := 0; j < 50; j++ {
				now := int64(j + 1)
				id := drug + "-" + string(rune('0'+j%10)) + "-" + string(rune('0'+j/10))
				if err := withdraw(s, now, id, "k1", drug, 1); err == nil {
					_ = s.Settle(now, id, 1, 0, 0)
				}
				_ = s.Destroy(now, drug, "b1", 1, []string{"r1", "r2"})
			}
		}()
	}
	for i := 0; i < drugs; i++ {
		<-done
	}
	if !s.InvariantOK() {
		t.Fatal("并发操作后不变式被破坏")
	}
	snap := s.Snapshot(100)
	for name, ds := range snap.Drugs {
		if ds.BookTotal < 0 || ds.Available < 0 {
			t.Fatalf("药品 %s 账面为负: %+v", name, ds)
		}
	}
}

// 相同操作序列重放得到完全相同的结果、账面与单据状态。
func TestReplayDeterminism(t *testing.T) {
	run := func() Snapshot {
		s := NewSystem()
		_ = s.GrantAuth(0, "r1", 0, 1_000_000_000)
		_ = s.GrantAuth(0, "r2", 0, 1_000_000_000)
		_ = s.Inbound(1, "d", "b1", 5, 100)
		_ = s.Inbound(2, "d", "b2", 5, 200)
		_ = s.Inbound(3, "d", "b3", 5, 100)
		_, _ = s.Withdraw(4, "s1", "k1", "a1", "d", 7, []string{"r1", "r2"})
		_, _ = s.Withdraw(5, "s2", "k1", "a1", "d", 2, []string{"r1", "r2"})
		_ = s.Settle(6, "s1", 3, 2, 1)
		_ = s.Settle(7, "s2", 0, 1, 0)
		_ = s.ResolveDiscrepancy(8, "s2", []string{"r1", "r2"})
		_ = s.Destroy(9, "d", "b1", 1, []string{"r1", "r2"})
		return s.Snapshot(9)
	}
	first := run()
	for i := 0; i < 20; i++ {
		got := run()
		if !reflect.DeepEqual(first, got) {
			t.Fatalf("第 %d 次重放结果不一致", i)
		}
	}
}

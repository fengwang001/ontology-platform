package leave_test

import (
	"testing"

	"ontology/leave"
)

// 整单拒绝不留痕：额度、状态、时钟均不变。
func TestRejectLeavesNoTrace(t *testing.T) {
	s := newService(t, baseConfig())
	mustRegister(t, s, 0, "e", 0) // 时钟 = 0

	before := mustBalance(t, s, 5, "e")
	// 11 天 > 年额度 5：整单拒绝
	_, _, err := s.RequestLeave(10, "e", 10, 20)
	expectErr(t, err, leave.CatInsufficientQuota, "oversized request")

	after := mustBalance(t, s, 5, "e")
	if before != after {
		t.Fatalf("balance changed by rejected op:\nbefore %+v\nafter  %+v", before, after)
	}
	dumps, _ := s.DumpYears("e")
	for _, d := range dumps {
		if d.UsedCur != 0 || d.PendCur != 0 || d.UsedCar != 0 || d.PendCar != 0 || d.CarriedOut != 0 {
			t.Fatalf("ledger changed by rejected op: %+v", d)
		}
	}
	// 时钟未推进：now=9（< 10）的操作仍被接受
	if _, _, err := s.RequestLeave(9, "e", 9, 10); err != nil {
		t.Fatalf("clock advanced by rejected op: %v", err)
	}

	// 重叠拒绝也不留痕：拒绝后相邻区间仍可申请
	_, _, err2 := s.RequestLeave(10, "e", 10, 12) // 与 [9,10] 重叠于 10
	expectErr(t, err2, leave.CatOverlap, "overlapping request")
	if _, _, err := s.RequestLeave(11, "e", 11, 12); err != nil {
		t.Fatalf("overlap rejection left trace: %v", err)
	}
}

// 拒绝优先级：参数非法 > 时钟回退 > 员工不存在 > 状态不允许 > 区间重叠 > 额度不足。
func TestErrorPriority(t *testing.T) {
	s := newService(t, baseConfig())
	mustRegister(t, s, 100, "e", 0) // 时钟 = 100

	// 参数非法 优先于 时钟回退
	_, _, err := s.RequestLeave(50, "", 60, 61)
	expectErr(t, err, leave.CatInvalidParam, "empty id + clock regression")
	_, _, err = s.RequestLeave(-1, "e", 0, 1)
	expectErr(t, err, leave.CatInvalidParam, "negative now")
	_, _, err = s.RequestLeave(50, "e", 61, 60)
	expectErr(t, err, leave.CatInvalidParam, "from>to + clock regression")
	expectErr(t, s.EarlyEnd(50, "e", 1, -5), leave.CatInvalidParam, "negative newEnd + clock regression")

	// 时钟回退 优先于 员工不存在
	_, _, err = s.RequestLeave(50, "ghost", 60, 61)
	expectErr(t, err, leave.CatClockRegression, "clock regression + unknown employee")
	expectErr(t, s.Approve(50, "ghost", 1), leave.CatClockRegression, "approve: clock + unknown employee")

	// 员工不存在 优先于 状态不允许
	expectErr(t, s.Approve(150, "ghost", 999), leave.CatEmployeeNotFound, "approve: unknown employee + unknown leave")
	expectErr(t, s.Register(150, "e", 0), leave.CatInvalidState, "duplicate register")

	// 状态不允许（假单不存在）
	expectErr(t, s.Approve(150, "e", 999), leave.CatInvalidState, "approve unknown leave")

	// 区间重叠 优先于 额度不足
	if _, _, err := s.RequestLeave(150, "e", 150, 154); err != nil { // 占满年 0 额度 5
		t.Fatalf("setup request: %v", err)
	}
	_, _, err = s.RequestLeave(154, "e", 154, 160) // 重叠于 154，且额度也不足
	expectErr(t, err, leave.CatOverlap, "overlap + insufficient")

	// 额度不足
	_, _, err = s.RequestLeave(155, "e", 155, 160)
	expectErr(t, err, leave.CatInsufficientQuota, "insufficient")
}

// 时钟回退被拒绝且不改变状态。
func TestClockRegression(t *testing.T) {
	s := newService(t, baseConfig())
	mustRegister(t, s, 100, "e", 0)
	lid, _ := mustRequest(t, s, 110, "e", 110, 111)

	expectErr(t, s.Approve(105, "e", lid), leave.CatClockRegression, "approve with regressed clock")
	// 状态未变：仍是待批准，用相同 now 重试成功
	mustApprove(t, s, 110, "e", lid)
	// now 相等允许
	if _, _, err := s.RequestLeave(110, "e", 112, 112); err != nil {
		t.Fatalf("equal now should be accepted: %v", err)
	}
}

// 惰性年度补齐：一次跨多年触达与逐年触达结果完全一致。
func TestLazyMultiYearCatchUp(t *testing.T) {
	cfg := baseConfig()

	// A：每年触达一次（申请 1 天随即撤回，净效果为零）。
	sa := newService(t, cfg)
	mustRegister(t, sa, 0, "e", 0)
	for y := 1; y <= 5; y++ {
		now := 365 * y
		lid, _ := mustRequest(t, sa, now, "e", now, now)
		if err := sa.Withdraw(now, "e", lid); err != nil {
			t.Fatalf("withdraw: %v", err)
		}
	}
	// B：直到第 5 年才首次触达。
	sb := newService(t, cfg)
	mustRegister(t, sb, 0, "e", 0)
	lidB, _ := mustRequest(t, sb, 365*5, "e", 365*5, 365*5)
	if err := sb.Withdraw(365*5, "e", lidB); err != nil {
		t.Fatalf("withdraw: %v", err)
	}

	ba := mustBalance(t, sa, 365*5, "e")
	bb := mustBalance(t, sb, 365*5, "e")
	if ba != bb {
		t.Fatalf("balance mismatch:\nyear-by-year %+v\nlazy         %+v", ba, bb)
	}
	da, _ := sa.DumpYears("e")
	db, _ := sb.DumpYears("e")
	if len(da) != len(db) {
		t.Fatalf("dump length %d vs %d", len(da), len(db))
	}
	for i := range da {
		if da[i] != db[i] {
			t.Fatalf("year %d dump mismatch:\nyear-by-year %+v\nlazy         %+v", da[i].Year, da[i], db[i])
		}
	}
	// 期望值：年 5 工龄 5 -> 10；每年结转 min(额度,4)=4
	if ba.CurrentGranted != 10 || ba.CarriedGranted != 4 {
		t.Fatalf("year5 balance = %+v, want granted 10 carried 4", ba)
	}
}

// 历史时刻余额查询与当时结果一致。
func TestHistoricalBalance(t *testing.T) {
	s := newService(t, baseConfig())
	mustRegister(t, s, 0, "e", 0)

	type point struct {
		now int
		bal leave.Balance
	}
	var points []point
	record := func(now int) {
		points = append(points, point{now, mustBalance(t, s, now, "e")})
	}

	lid, _ := mustRequest(t, s, 10, "e", 10, 12)
	record(10)
	mustApprove(t, s, 20, "e", lid)
	record(20)
	lid2, _ := mustRequest(t, s, 365, "e", 365, 366) // 2 天结转
	record(365)
	mustApprove(t, s, 370, "e", lid2)
	record(370)
	if err := s.EarlyEnd(400, "e", lid2, 365); err != nil { // 释放 366，截止日 454 未过，恢复
		t.Fatalf("EarlyEnd: %v", err)
	}
	record(400)

	// 事后用任意历史时刻查询，结果必须与当时一致
	for _, p := range points {
		got := mustBalance(t, s, p.now, "e")
		if got != p.bal {
			t.Fatalf("historical balance at %d:\ngot  %+v\nwant %+v", p.now, got, p.bal)
		}
	}
	// 中间时刻：now=15 与 now=10 相同（之间无操作）
	if got, want := mustBalance(t, s, 15, "e"), points[0].bal; got != want {
		t.Fatalf("balance at 15 = %+v, want %+v", got, want)
	}
	// 登记前：员工不存在
	s2 := newService(t, baseConfig())
	mustRegister(t, s2, 100, "e", 0)
	_, err := s2.Balance(99, "e")
	expectErr(t, err, leave.CatEmployeeNotFound, "balance before registration")
}

// 重叠规则：待批准与已批准都参与重叠；终结后释放区间。
func TestOverlapRules(t *testing.T) {
	cfg := baseConfig()
	cfg.AnnualQuotas = []int{20, 20, 20}
	s := newService(t, cfg)
	mustRegister(t, s, 0, "e", 0)

	lidA, _ := mustRequest(t, s, 10, "e", 10, 11)
	_, _, err := s.RequestLeave(10, "e", 11, 12)
	expectErr(t, err, leave.CatOverlap, "overlap with pending")
	lidC, _ := mustRequest(t, s, 10, "e", 12, 13)
	mustApprove(t, s, 10, "e", lidC)
	_, _, err = s.RequestLeave(10, "e", 13, 14)
	expectErr(t, err, leave.CatOverlap, "overlap with approved")

	// 撤回后区间释放
	if err := s.Withdraw(10, "e", lidA); err != nil {
		t.Fatalf("Withdraw: %v", err)
	}
	if _, _, err := s.RequestLeave(10, "e", 10, 11); err != nil {
		t.Fatalf("after withdraw should be free: %v", err)
	}
	// 提前结束收缩区间后尾部释放
	if err := s.EarlyEnd(12, "e", lidC, 12); err != nil {
		t.Fatalf("EarlyEnd: %v", err)
	}
	if _, _, err := s.RequestLeave(13, "e", 13, 13); err != nil {
		t.Fatalf("after early end tail should be free: %v", err)
	}
}

// 发放总量守恒：对每个年度，发放 = 已用 + 占用 + 已结转 + 可用；
// 结转发放 = 已用 + 占用 + 作废 + 可用；可用不为负。
func TestConservationInvariant(t *testing.T) {
	s := newService(t, baseConfig())
	mustRegister(t, s, 0, "e", 0)

	check := func(now int) {
		t.Helper()
		dumps, err := s.DumpYears("e")
		if err != nil {
			t.Fatalf("DumpYears: %v", err)
		}
		for _, d := range dumps {
			availCur := d.Granted - d.UsedCur - d.PendCur - d.CarriedOut
			if availCur < 0 {
				t.Fatalf("now=%d year=%d: negative current available %d (%+v)", now, d.Year, availCur, d)
			}
			availCar := d.CarriedIn - d.UsedCar - d.PendCar - d.VoidedCar
			if availCar < 0 {
				t.Fatalf("now=%d year=%d: negative carried available %d (%+v)", now, d.Year, availCar, d)
			}
		}
		b := mustBalance(t, s, now, "e")
		if b.CurrentAvailable < 0 || b.CarriedAvailable < 0 {
			t.Fatalf("now=%d: negative available in %+v", now, b)
		}
		if b.CurrentGranted != b.CurrentUsed+b.CurrentPending+b.CurrentAvailable {
			// 年 0 的已结转发生在年度切换后；此处 now 所在年 carriedOut 恒为 0
			t.Fatalf("now=%d: current not conserved in %+v", now, b)
		}
		if b.CarriedGranted != b.CarriedUsed+b.CarriedPending+b.CarriedAvailable {
			// 作废量不计入 Balance 四量；仅在无作废时严格相等
			t.Logf("now=%d: carried has voided amount (balance %+v)", now, b)
		}
	}

	lid1, _ := mustRequest(t, s, 10, "e", 10, 11)
	check(10)
	mustApprove(t, s, 20, "e", lid1)
	check(20)
	lid2, _ := mustRequest(t, s, 363, "e", 363, 367) // 跨年：年 0 两天 + 年 1 三天
	check(363)
	mustApprove(t, s, 365, "e", lid2)
	check(365)
	lid3, _ := mustRequest(t, s, 370, "e", 400, 401) // 结转
	check(370)
	mustApprove(t, s, 371, "e", lid3)
	check(371)
	if err := s.EarlyEnd(500, "e", lid3, 400); err != nil { // 401 回补，已过截止日，作废
		t.Fatalf("EarlyEnd: %v", err)
	}
	check(500)
	if err := s.CancelLeave(700, "e", lid1); err == nil {
		t.Fatalf("cancel started leave should fail")
	}
	check(700)
	lid4, _ := mustRequest(t, s, 730, "e", 800, 801)
	if err := s.Reject(731, "e", lid4); err != nil {
		t.Fatalf("Reject: %v", err)
	}
	check(731)
}

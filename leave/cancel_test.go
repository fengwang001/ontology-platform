package leave_test

import (
	"testing"

	"ontology/leave"
)

// 销假回补到已过期结转：截止日恰等 now 时恢复，晚一日作废。
func TestVoidedCarriedReturn(t *testing.T) {
	cfg := baseConfig()

	// 在截止日当天（365+89=454）提前结束：回补恢复。
	s1 := newService(t, cfg)
	mustRegister(t, s1, 0, "e", 0)
	lid, charges := mustRequest(t, s1, 365, "e", 400, 403) // doy 35..38，全部结转
	expectCharges(t, charges, []leave.Charge{
		ch(400, 1, leave.Carried), ch(401, 1, leave.Carried),
		ch(402, 1, leave.Carried), ch(403, 1, leave.Carried),
	})
	mustApprove(t, s1, 366, "e", lid)
	if err := s1.EarlyEnd(454, "e", lid, 401); err != nil { // 释放 402,403
		t.Fatalf("EarlyEnd: %v", err)
	}
	b := mustBalance(t, s1, 454, "e")
	if b.CarriedUsed != 2 || b.CarriedAvailable != 2 {
		t.Fatalf("restore case: %+v, want used 2 available 2", b)
	}

	// 晚一日（455）：结转截止日已早于 now，回补作废。
	s2 := newService(t, cfg)
	mustRegister(t, s2, 0, "e", 0)
	lid2, _ := mustRequest(t, s2, 365, "e", 400, 403)
	mustApprove(t, s2, 366, "e", lid2)
	if err := s2.EarlyEnd(455, "e", lid2, 401); err != nil {
		t.Fatalf("EarlyEnd: %v", err)
	}
	b2 := mustBalance(t, s2, 455, "e")
	if b2.CarriedUsed != 2 || b2.CarriedAvailable != 0 {
		t.Fatalf("void case: %+v, want used 2 available 0", b2)
	}
	dumps, err := s2.DumpYears("e")
	if err != nil {
		t.Fatalf("DumpYears: %v", err)
	}
	var y1 *leave.YearDump
	for i := range dumps {
		if dumps[i].Year == 1 {
			y1 = &dumps[i]
		}
	}
	if y1 == nil || y1.VoidedCar != 2 {
		t.Fatalf("void case: year1 dump = %+v, want voided 2", y1)
	}

	// 待批准假单撤回：同样适用作废规则。
	s3 := newService(t, cfg)
	mustRegister(t, s3, 0, "e", 0)
	lid3, _ := mustRequest(t, s3, 365, "e", 400, 401) // 2 天结转，占用中
	if err := s3.Withdraw(455, "e", lid3); err != nil {
		t.Fatalf("Withdraw: %v", err)
	}
	b3 := mustBalance(t, s3, 455, "e")
	if b3.CarriedPending != 0 || b3.CarriedAvailable != 2 {
		t.Fatalf("withdraw void case: %+v, want pending 0 available 2", b3)
	}
}

// 整单销假：仅限尚未开始的已批准请假，全额回补到来源。
func TestFullCancel(t *testing.T) {
	s := newService(t, baseConfig())
	mustRegister(t, s, 0, "e", 0)
	lid, _ := mustRequest(t, s, 10, "e", 100, 102)
	mustApprove(t, s, 20, "e", lid)

	// 待批准假单不能整单销假
	lid2, _ := mustRequest(t, s, 30, "e", 200, 201)
	expectErr(t, s.CancelLeave(40, "e", lid2), leave.CatInvalidState, "cancel pending")

	// 尚未开始：全额回补
	if err := s.CancelLeave(99, "e", lid); err != nil {
		t.Fatalf("CancelLeave: %v", err)
	}
	b := mustBalance(t, s, 99, "e")
	if b.CurrentUsed != 0 || b.CurrentPending != 2 || b.CurrentAvailable != 3 {
		t.Fatalf("after cancel: %+v, want used 0 pending 2 available 3", b)
	}

	// 已开始的不能整单销假
	lid3, _ := mustRequest(t, s, 100, "e", 100, 101)
	mustApprove(t, s, 100, "e", lid3)
	expectErr(t, s.CancelLeave(100, "e", lid3), leave.CatInvalidState, "cancel started")

	// 已终结的不能重复操作
	expectErr(t, s.CancelLeave(101, "e", lid), leave.CatInvalidState, "re-cancel")
	expectErr(t, s.Approve(101, "e", lid), leave.CatInvalidState, "approve cancelled")
}

// 提前结束：仅限已开始的已批准请假；(newEnd, to] 计扣日回补。
func TestEarlyEndBasics(t *testing.T) {
	s := newService(t, baseConfig())
	mustRegister(t, s, 0, "e", 0)
	lid, _ := mustRequest(t, s, 10, "e", 100, 104)
	mustApprove(t, s, 20, "e", lid)

	// 未开始不能提前结束
	expectErr(t, s.EarlyEnd(50, "e", lid, 102), leave.CatInvalidState, "early end not started")
	// newEnd 不能早于起始日、不能 >= 原结束日
	expectErr(t, s.EarlyEnd(100, "e", lid, 99), leave.CatInvalidParam, "newEnd before from")
	expectErr(t, s.EarlyEnd(100, "e", lid, 104), leave.CatInvalidParam, "newEnd == to")

	if err := s.EarlyEnd(102, "e", lid, 102); err != nil { // 释放 103,104
		t.Fatalf("EarlyEnd: %v", err)
	}
	b := mustBalance(t, s, 102, "e")
	if b.CurrentUsed != 3 || b.CurrentAvailable != 2 {
		t.Fatalf("after early end: %+v, want used 3 available 2", b)
	}
	// 区间收缩后，尾部日期可再申请
	if _, _, err := s.RequestLeave(103, "e", 103, 104); err != nil {
		t.Fatalf("request on freed days: %v", err)
	}
}

// 待批准占用计入已用；驳回/撤回释放；占用不计入结转。
func TestPendingOccupiesAndReleases(t *testing.T) {
	s := newService(t, baseConfig())
	mustRegister(t, s, 0, "e", 0)

	lid, _ := mustRequest(t, s, 360, "e", 360, 364) // 占满年 0 额度 5
	b := mustBalance(t, s, 360, "e")
	if b.CurrentPending != 5 || b.CurrentAvailable != 0 {
		t.Fatalf("pending: %+v, want pending 5 available 0", b)
	}
	// 占用计入已用 -> 年 0 无可结转
	_, charges := mustRequest(t, s, 365, "e", 365, 365)
	expectCharges(t, charges, []leave.Charge{ch(365, 1, leave.Current)})
	if b1 := mustBalance(t, s, 365, "e"); b1.CarriedGranted != 0 {
		t.Fatalf("carried = %d, want 0 (pending counts as used)", b1.CarriedGranted)
	}
	// 驳回释放占用，但结转已钉死为 0
	if err := s.Reject(366, "e", lid); err != nil {
		t.Fatalf("Reject: %v", err)
	}
	b2 := mustBalance(t, s, 366, "e")
	if b2.CarriedGranted != 0 {
		t.Fatalf("carried after reject = %d, want 0 (pinned)", b2.CarriedGranted)
	}
	dumps, _ := s.DumpYears("e")
	for _, d := range dumps {
		if d.Year == 0 && (d.PendCur != 0 || d.UsedCur != 0) {
			t.Fatalf("year0 after reject: %+v, want all released", d)
		}
	}
}

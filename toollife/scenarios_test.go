package toollife

import "testing"

// TestBrokenWithOpenReservation 破损刀上未结算预占：保持待结算，记账仍计入；换新被拒。
func TestBrokenWithOpenReservation(t *testing.T) {
	s := newTestService(t)
	mustAdd(t, s, "G", strictCfg(100, "T1", "T2"))
	if _, err := s.Apply("G", "R1", 30); err != nil {
		t.Fatal(err)
	}
	if err := s.ReportBroken("G", "T1"); err != nil {
		t.Fatal(err)
	}
	t.Logf("T1 破损；R1 的预占30 保持待结算，选刀应跳过 T1 选 T2")
	if r, err := s.Apply("G", "R2", 1); err != nil || r.ToolID != "T2" {
		t.Fatalf("破损刀不应被选中: %+v %v", r, err)
	}

	mustCode(t, s.Replace("G", "T1", "T1N"), ErrState, "有未结算预占时换新必须拒绝")
	v, _ := s.Query("G")
	if v.Tools[0].Status != StatusBroken || v.Tools[0].Reserved != 30 {
		t.Fatalf("破损与预占应保持不变: %+v", v.Tools[0])
	}

	res, err := s.Settle("R1", 30)
	if err != nil || res.ToolID != "T1" {
		t.Fatalf("破损刀记账仍应生效: %+v %v", res, err)
	}
	v, _ = s.Query("G")
	if v.Tools[0].Status != StatusBroken || v.Tools[0].Used != 30 {
		t.Fatalf("记账后状态保持破损，已用30: %+v", v.Tools[0])
	}
	if err := s.Replace("G", "T1", "T1N"); err != nil {
		t.Fatalf("结算后应可换新: %v", err)
	}
	v, _ = s.Query("G")
	if v.Tools[0].ID != "T1N" || v.Tools[0].Status != StatusAvailable ||
		v.Tools[0].Used != 0 || v.Tools[0].Reserved != 0 {
		t.Fatalf("换新后新刀占首位且归零: %+v", v.Tools[0])
	}
	t.Logf("换新成功: T1N 在位置0，used=0，重新具备预警资格，编号 T1 已移除")
	if _, err := s.Apply("G", "R3", 1); err != nil {
		t.Fatalf("换新后 T1N 应可选: %v", err)
	}
}

// TestReplacePreconditions 换新的拒绝条件。
func TestReplacePreconditions(t *testing.T) {
	s := newTestService(t)
	mustAdd(t, s, "G", strictCfg(100, "T1", "T2"))
	mustCode(t, s.Replace("G", "T1", "X"), ErrState, "可用刀不能换新")
	mustCode(t, s.Replace("G", "Missing", "X"), ErrNotFound, "刀具不存在")
	mustCode(t, s.Replace("Nope", "T1", "X"), ErrNotFound, "刀组不存在")
	if err := s.ReportBroken("G", "T1"); err != nil {
		t.Fatal(err)
	}
	mustCode(t, s.Replace("G", "T1", "T2"), ErrConflict, "新编号与现有刀冲突")
	mustCode(t, s.Replace("G", "T1", ""), ErrInvalidArgument, "新编号为空属参数非法")
}

// TestWarningCrossOnce 预警恰好跨线、只发一次、换新重新具备资格。
func TestWarningCrossOnce(t *testing.T) {
	s := newTestService(t)
	mustAdd(t, s, "G", GroupConfig{LifeLimit: 10, WarnPermille: 800, Mode: Strict, ToolIDs: []string{"T1"}})
	applySettle := func(req string, est, actual int) SettleResult {
		t.Helper()
		if _, err := s.Apply("G", req, est); err != nil {
			t.Fatalf("Apply %s: %v", req, err)
		}
		r, err := s.Settle(req, actual)
		if err != nil {
			t.Fatalf("Settle %s: %v", req, err)
		}
		return r
	}
	if r := applySettle("R1", 7, 7); r.Warned {
		t.Fatal("700‰ 不应预警")
	}
	if r := applySettle("R2", 1, 1); !r.Warned {
		t.Fatal("floor(8*1000/10)=800 恰好跨线应预警一次")
	}
	t.Logf("输入 used 7->8；输出 warned=true；依据: 700‰ < 800‰ <= 800‰，首次跨越")
	if r := applySettle("R3", 1, 1); r.Warned {
		t.Fatal("预警只发一次")
	}
	if err := s.ReportBroken("G", "T1"); err != nil {
		t.Fatal(err)
	}
	if err := s.Replace("G", "T1", "N1"); err != nil {
		t.Fatal(err)
	}
	r4 := applySettle("R4", 8, 8)
	if !r4.Warned {
		t.Fatal("换新后 warned 重置，跨线应重新预警")
	}
}

// TestNoMarginVsExhausted 暂无余量与耗尽的区分，以及预占释放后恢复。
func TestNoMarginVsExhausted(t *testing.T) {
	s := newTestService(t)
	mustAdd(t, s, "G", strictCfg(10, "T1", "T2"))
	if _, err := s.Apply("G", "R1", 10); err != nil { // T1 预占占满
		t.Fatal(err)
	}
	if _, err := s.Apply("G", "R2", 10); err != nil { // 顺延 T2
		t.Fatal(err)
	}
	_, err := s.Apply("G", "R3", 1)
	mustCode(t, err, ErrNoMargin, "两把可用刀全被预占占满 -> 暂无余量")

	if _, err := s.Settle("R2", 10); err != nil { // T2 耗尽
		t.Fatal(err)
	}
	_, err = s.Apply("G", "R4", 1)
	mustCode(t, err, ErrNoMargin, "T1 未耗尽但预占占满、T2 耗尽 -> 仍为暂无余量")

	if err := s.Cancel("R1"); err != nil { // 释放 T1
		t.Fatal(err)
	}
	r, err := s.Apply("G", "R5", 1)
	if err != nil || r.ToolID != "T1" {
		t.Fatalf("预占释放后应成功且选中 T1: %+v %v", r, err)
	}
	if _, err := s.Settle("R5", 10); err != nil {
		t.Fatal(err)
	}
	_, err = s.Apply("G", "R6", 1)
	mustCode(t, err, ErrNoTool, "T1、T2 均耗尽 -> 无刀可用（与暂无余量区分）")
	t.Logf("T1、T2 used=10 均耗尽，无未耗尽刀 -> ErrNoTool")
}

// TestIdempotentApplyAndSettle 重复申请回放、内容冲突、重复记账幂等。
func TestIdempotentApplyAndSettle(t *testing.T) {
	s := newTestService(t)
	mustAdd(t, s, "G", strictCfg(100, "T1"))
	r1, err := s.Apply("G", "REQ", 10)
	if err != nil {
		t.Fatal(err)
	}
	r1b, err := s.Apply("G", "REQ", 10)
	if err != nil || !r1b.Replayed || r1b.ToolID != "T1" || r1b.Reserved != 10 {
		t.Fatalf("重复申请应回放原结果: %+v %v", r1b, err)
	}
	v, _ := s.Query("G")
	if v.Tools[0].Reserved != 10 {
		t.Fatalf("重复申请不得重复预占, reserved=%d", v.Tools[0].Reserved)
	}
	mustCode(t, func() error { _, e := s.Apply("G", "REQ", 11); return e }(),
		ErrConflict, "同一编号换内容报冲突")

	st1, err := s.Settle("REQ", 7)
	if err != nil {
		t.Fatal(err)
	}
	st2, err := s.Settle("REQ", 7)
	if err != nil || st2.Actual != 7 {
		t.Fatalf("重复记账幂等: %+v %v", st2, err)
	}
	v, _ = s.Query("G")
	if v.Tools[0].Used != 7 || v.Tools[0].Reserved != 0 {
		t.Fatalf("重复记账不得重复累计: used=%d reserved=%d", v.Tools[0].Used, v.Tools[0].Reserved)
	}
	mustCode(t, func() error { _, e := s.Settle("REQ", 8); return e }(),
		ErrConflict, "重复记账实际值不一致报冲突")
	mustCode(t, s.Cancel("REQ"), ErrState, "已记账申请不能中止")
	_ = r1
	_ = st1
}

// TestLockUnlock 锁定不参与选刀、预占保留；解锁恢复；耗尽刀解锁不为可用。
func TestLockUnlock(t *testing.T) {
	s := newTestService(t)
	mustAdd(t, s, "G", strictCfg(100, "T1", "T2"))
	if _, err := s.Apply("G", "R1", 30); err != nil {
		t.Fatal(err)
	}
	if err := s.Lock("G", "T1"); err != nil {
		t.Fatal(err)
	}
	v, _ := s.Query("G")
	if v.Tools[0].Status != StatusLocked || v.Tools[0].Reserved != 30 || v.CurrentPick != "T2" {
		t.Fatalf("锁定后预占保留且跳过: %+v pick=%s", v.Tools[0], v.CurrentPick)
	}
	if r, err := s.Apply("G", "R2", 1); err != nil || r.ToolID != "T2" {
		t.Fatalf("锁定刀不应被选中: %+v %v", r, err)
	}
	if _, err := s.Settle("R1", 100); err != nil { // 锁定刀照常记账至达限
		t.Fatal(err)
	}
	if err := s.Unlock("G", "T1"); err != nil {
		t.Fatal(err)
	}
	v, _ = s.Query("G")
	if v.Tools[0].Status != StatusExhausted {
		t.Fatalf("已达寿命上限的刀不能解锁为可用: %s", v.Tools[0].Status)
	}
	mustCode(t, s.Unlock("G", "T2"), ErrState, "未锁定刀解锁报状态错误")
}

// TestErrorPriority 错误判定次序：参数非法 > 不存在 > 冲突 > 状态不允许。
func TestErrorPriority(t *testing.T) {
	s := newTestService(t)
	mustAdd(t, s, "G", strictCfg(100, "T1"))
	// 参数非法优先于刀组不存在
	_, e := s.Apply("", "R", 0)
	mustCode(t, e, ErrInvalidArgument, "空组名+非正消耗")
	_, e = s.Apply("Nope", "R", 0)
	mustCode(t, e, ErrInvalidArgument, "不存在组但参数非法优先")
	_, e = s.Apply("Nope", "R", 1)
	mustCode(t, e, ErrNotFound, "组不存在")
	_, e = s.Apply("G", "R", 0)
	mustCode(t, e, ErrInvalidArgument, "预计消耗非正")
	_, e = s.Settle("", 1)
	mustCode(t, e, ErrInvalidArgument, "空申请编号")
	mustCode(t, s.ReportBroken("G", ""), ErrInvalidArgument, "空刀具名优先")
	mustCode(t, s.ReportBroken("Nope", "X"), ErrNotFound, "刀组不存在")
	mustCode(t, s.ReportBroken("G", "X"), ErrNotFound, "刀具不存在")
}

// TestCancelReleasesReservation 中止释放预占且不计消耗，重复中止幂等。
func TestCancelReleasesReservation(t *testing.T) {
	s := newTestService(t)
	mustAdd(t, s, "G", strictCfg(10, "T1"))
	if _, err := s.Apply("G", "R1", 10); err != nil {
		t.Fatal(err)
	}
	if err := s.Cancel("R1"); err != nil {
		t.Fatal(err)
	}
	if err := s.Cancel("R1"); err != nil {
		t.Fatalf("重复中止应幂等: %v", err)
	}
	v, _ := s.Query("G")
	if v.Tools[0].Used != 0 || v.Tools[0].Reserved != 0 || v.Tools[0].Status != StatusAvailable {
		t.Fatalf("中止不计消耗且释放预占: %+v", v.Tools[0])
	}
	if _, err := s.Apply("G", "R2", 10); err != nil {
		t.Fatalf("释放后余量恢复: %v", err)
	}
}

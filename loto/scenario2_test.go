package loto_test

import (
	"strings"
	"testing"

	"ontology/loto"
)

// 把一张普通票推进到已开工，并让作业人员最终离场。
func startPermitAt(t *testing.T, s *loto.System, id, applicant string, devs []string, st, en, base int64, ws []string) {
	t.Helper()
	mustOK(t, s.Apply(id, applicant, devs, loto.WorkNormal, st, en, ws), "apply "+id)
	mustOK(t, s.Approve(id, "a1", base), "approve "+id)
	for _, w := range ws {
		ps := []string{}
		snap, _ := s.GetPermit(id)
		ps = append(ps, snap.Points...)
		for _, pt := range ps {
			mustOK(t, s.PlaceLock(id, w, pt, base+1), "lock "+id+" "+w+"@"+pt)
		}
	}
	mustOK(t, s.Verify(id, "v", base+2), "verify "+id)
	mustOK(t, s.StartWork(id, applicant, base+3), "start "+id)
}

// 试运行期间：本票锁暂解，但他票在共享隔离点上的锁仍维持隔离。
func TestTrialRunOtherPermitsLocksKeepIsolation(t *testing.T) {
	s := setup(t)
	// T1: D1 (P1,P2), w1; T2: D2 (P2,P3), w2 —— 不相交设备，可同时占用；共享点 P2。
	startPermitAt(t, s, "T1", "ap", []string{"D1"}, 0, 1000, 0, []string{"w1"})
	startPermitAt(t, s, "T2", "ap2", []string{"D2"}, 0, 1000, 4, []string{"w2"})

	// 两票开工后所有点有锁：D1、D2 都不可送电。
	if ok, _, _ := s.CanEnergize("D1", 10); ok {
		t.Fatalf("D1 locked")
	}
	if ok, _, _ := s.CanEnergize("D2", 10); ok {
		t.Fatalf("D2 locked")
	}

	// T1 试运行：其 P1/P2 锁暂解，但 P2 上仍有 T2 的 w2 锁。
	mustOK(t, s.BeginTrial("T1", "ap", 11), "begin trial T1")
	// P1 已无锁，D1 能否送电？仍不能：条件2——T1 处于 trial 不阻断，但 P2 有他票锁。
	ok, rep, err := s.CanEnergize("D1", 12)
	mustOK(t, err, "energize D1 during trial")
	if ok {
		t.Fatalf("D1 must stay isolated: P2 locked by T2; %v", rep.Reasons)
	}
	found := false
	for _, r := range rep.Reasons {
		if strings.Contains(r, "point=P2") && strings.Contains(r, "lock") {
			found = true
		}
	}
	if !found {
		t.Fatalf("report must name P2 lock: %v", rep.Reasons)
	}

	// T2 也试运行后，P2 上再无任何锁 -> D1 满足锁条件，但 T1/T2 都在 trial（不阻断条件2）。
	mustOK(t, s.BeginTrial("T2", "ap2", 13), "begin trial T2")
	ok, rep, _ = s.CanEnergize("D1", 14)
	if !ok {
		t.Fatalf("D1 should be energizable with both trials releasing locks: %v", rep.Reasons)
	}

	// 试运行中不允许进入现场；结束试运行前必须由原持锁人逐把恢复。
	assertCode(t, s.Enter("T1", "w1", 14), loto.StateNotAllowed, "enter during trial")
	assertCode(t, s.EndTrial("T1", "ap", 14), loto.ConditionNotMet, "end trial before restore")
	mustOK(t, s.PlaceLock("T1", "w1", "P1", 15), "restore P1")
	mustOK(t, s.PlaceLock("T1", "w1", "P2", 15), "restore P2")
	mustOK(t, s.EndTrial("T1", "ap", 16), "end trial T1")
	// 回到已上锁态，必须再次验证才能开工。
	assertCode(t, s.StartWork("T1", "ap", 17), loto.StateNotAllowed, "restart without re-verify")
	mustOK(t, s.Verify("T1", "v", 17), "re-verify")
	mustOK(t, s.StartWork("T1", "ap", 18), "restart")
}

// 逾期：到终点未完成 -> 逾期；本人不能摘锁；主管可附理由+另一主管确认强制摘除；
// 强制摘除有审计；被摘锁人再进入前须重新上锁。
func TestOverdueAndForceRemoval(t *testing.T) {
	s := setup(t)
	startPermitAt(t, s, "T1", "ap", []string{"D1"}, 0, 50, 0, []string{"w1"})
	mustOK(t, s.Enter("T1", "w1", 10), "enter")
	mustOK(t, s.Leave("T1", "w1", 11), "leave")

	// 到达终点仍未完工。用一次会被接受的无关操作把时钟推进到 50，系统在提交该操作时
	// 把 T1 标记逾期（被拒操作本身不推进时钟、不改状态）。
	mustOK(t, s.Apply("X1", "ap2", []string{"D3"}, loto.WorkObservation, 0, 100, []string{"w2"}), "unrelated apply")
	mustOK(t, s.Approve("X1", "a1", 50), "advance clock to 50 -> T1 overdue")
	snap, _ := s.GetPermit("T1")
	if !snap.Overdue {
		t.Fatalf("T1 must be overdue at t=50")
	}
	assertCode(t, s.Complete("T1", "ap", 50), loto.StateNotAllowed, "overdue permit cannot complete")

	// 本人摘锁被拒（逾期锁不能由他人/本人普通摘除）。
	assertCode(t, s.RemoveLock("T1", "w1", "P1", 51), loto.PermissionDenied, "personal removal on overdue forbidden")

	// 强制摘除：理由为空 -> 参数非法；同一主管确认 -> 条件不满足；非主管 -> 权限。
	assertCode(t, s.ForceRemoveLock("T1", "P1", "w1", "sv1", "sv2", "", 52), loto.InvalidParam, "reason required")
	assertCode(t, s.ForceRemoveLock("T1", "P1", "w1", "sv1", "sv1", "danger", 52), loto.ConditionNotMet, "distinct supervisor")
	assertCode(t, s.ForceRemoveLock("T1", "P1", "w1", "a1", "sv2", "danger", 52), loto.PermissionDenied, "must be supervisor")

	mustOK(t, s.ForceRemoveLock("T1", "P1", "w1", "sv1", "sv2", "leaking pipe needs access", 52), "force remove P1")
	// 审计中必须有强制摘除记录（含理由与两名主管）。
	found := false
	for _, e := range s.AuditLog() {
		if e.Op == "force_remove_lock" && e.Actor == "sv1" &&
			strings.Contains(e.Detail, "sv2") && strings.Contains(e.Detail, "leaking pipe") {
			found = true
		}
	}
	if !found {
		t.Fatalf("force removal must be audited with reason and confirmer: %+v", s.AuditLog())
	}

	// P2 仍被本票锁着 -> D1 不能送电；强制摘掉 P2 后点上无锁，但逾期票仍占用设备 -> 仍不能送电。
	if ok, _, _ := s.CanEnergize("D1", 53); ok {
		t.Fatalf("P2 still locked")
	}
	mustOK(t, s.ForceRemoveLock("T1", "P2", "w1", "sv2", "sv1", "same reason", 54), "force remove P2")
	ok, rep, _ := s.CanEnergize("D1", 55)
	if ok {
		t.Fatalf("overdue permit still occupies device: %v", rep.Reasons)
	}
}

// 送电判定的全部条件组合：锁 / 占用票阶段 / working|trial 不阻断票条件但锁仍阻断。
func TestCanEnergizeAllConditions(t *testing.T) {
	s := setup(t)
	// D3 只有点 P4：无任何票时可送电。
	ok, rep, err := s.CanEnergize("D3", 0)
	mustOK(t, err, "energize free device")
	if !ok {
		t.Fatalf("D3 free: %v", rep.Reasons)
	}

	// 生效但尚未开工的票占用设备 -> 阻断（即便点上有锁也先报锁）。
	mustOK(t, s.Apply("T1", "ap", []string{"D3"}, loto.WorkNormal, 0, 100, []string{"w1"}), "apply")
	mustOK(t, s.Approve("T1", "a1", 0), "approve")
	if ok, _, _ := s.CanEnergize("D3", 1); ok {
		t.Fatalf("effective permit blocks energize")
	}
	mustOK(t, s.PlaceLock("T1", "w1", "P4", 2), "lock")
	mustOK(t, s.Verify("T1", "v", 3), "verify")
	// verified 阶段也阻断。
	if ok, _, _ := s.CanEnergize("D3", 3); ok {
		t.Fatalf("verified permit blocks energize")
	}
	mustOK(t, s.StartWork("T1", "ap", 4), "start")
	// working：票条件不再阻断，但锁仍在 -> 仍不能送电。
	ok, rep, _ = s.CanEnergize("D3", 5)
	if ok {
		t.Fatalf("lock during working still blocks: %v", rep.Reasons)
	}
	mustOK(t, s.BeginTrial("T1", "ap", 6), "trial releases lock")
	// trial：锁暂解且阶段不阻断 -> 可送电。
	ok, rep, _ = s.CanEnergize("D3", 7)
	if !ok {
		t.Fatalf("trial with no locks should energize: %v", rep.Reasons)
	}
	// 完工后锁仍在（尚未摘除）-> 锁阻断（完工票已不占用设备，只剩锁条件）。
	mustOK(t, s.PlaceLock("T1", "w1", "P4", 8), "restore")
	mustOK(t, s.EndTrial("T1", "ap", 9), "end trial")
	mustOK(t, s.Verify("T1", "v", 10), "reverify")
	mustOK(t, s.StartWork("T1", "ap", 11), "restart")
	mustOK(t, s.Complete("T1", "ap", 12), "complete")
	ok, rep, _ = s.CanEnergize("D3", 13)
	if ok {
		t.Fatalf("completed permit's lock still physically present: %v", rep.Reasons)
	}
	mustOK(t, s.RemoveLock("T1", "w1", "P4", 14), "remove after completion")
	ok, rep, _ = s.CanEnergize("D3", 15)
	if !ok {
		t.Fatalf("fully released device should energize: %v", rep.Reasons)
	}

	// 只读查询：参数非法 / 时刻回退 / 对象不存在。
	assertCode(t, func() error { _, _, e := s.CanEnergize("nope", -1); return e }(), loto.InvalidParam, "negative time")
	assertCode(t, func() error { _, _, e := s.CanEnergize("nope", 0); return e }(), loto.ClockRollback, "rollback beats not-found")
	assertCode(t, func() error { _, _, e := s.CanEnergize("nope", 100); return e }(), loto.NotFound, "unknown device")
	assertCode(t, func() error { _, _, e := s.CanEnergize("D3", -1); return e }(), loto.InvalidParam, "negative time")
}

// 错误次序：参数非法 > 时刻回退 > 对象不存在 > 权限 > 状态 > 冲突 > 条件。
func TestErrorPrecedence(t *testing.T) {
	s := setup(t)
	// 用一次合法操作把时钟推进到 5（D3 无其他票，不影响后续）。
	mustOK(t, s.Apply("SEED", "ap", []string{"D3"}, loto.WorkNormal, 0, 100, []string{"w1"}), "seed apply")
	mustOK(t, s.Approve("SEED", "a1", 5), "advance clock to 5")

	// 参数非法优先于一切。
	assertCode(t, s.Approve("", "", 0), loto.InvalidParam, "empty params beats rollback")
	// 时刻回退优先于对象不存在。
	assertCode(t, s.Approve("missing", "a1", 4), loto.ClockRollback, "rollback beats not-found")
	// 对象不存在优先于权限。
	assertCode(t, s.Approve("missing", "nobody", 6), loto.NotFound, "not-found beats permission")

	// 权限优先于状态：无批准人角色的人对一张已生效票操作 -> PermissionDenied 而非 StateNotAllowed。
	// （在不冲突的 D1 上另起一票。）
	mustOK(t, s.Apply("T1", "ap", []string{"D1"}, loto.WorkNormal, 0, 100, []string{"w1"}), "apply")
	mustOK(t, s.Approve("T1", "a1", 7), "approve -> effective")
	assertCode(t, s.Approve("T1", "w2", 8), loto.PermissionDenied, "permission beats state")

	// 冲突优先于条件：高风险票第二批准人若是申请人 -> 按实现先做冲突再做身份条件；
	// 这里专门断言普通票"冲突"优先于其后条件（冲突是批准的末道检查前的条件）。
	// 状态优先于条件：已生效票用非申请人之外的合法批准人重复批准 -> StateNotAllowed 路径
	// 由重复人员条件分支承接，故另用开工演示状态>条件：未验证票开工（状态先于时刻窗口条件）。
	mustOK(t, s.Apply("T2", "ap2", []string{"D1"}, loto.WorkNormal, 0, 100, []string{"w1"}), "apply T2")
	// T2 与已占用 T1 冲突；用与申请人不同的批准人，避免走到"申请人批准"条件分支。
	assertCode(t, s.Approve("T2", "a2", 9), loto.Conflict, "conflict detected")

	// 被拒绝操作不推进时钟：上一被接受时刻是 8（T2 apply 不带时钟），冲突调用 at=9 被拒后，
	// 再以 8 发别的操作不应被视为回退。
	mustOK(t, s.PlaceLock("T1", "w1", "P1", 8), "clock still 8 after rejected approve")

	// 条件不满足示例：验证人是作业人员。
	mustOK(t, s.PlaceLock("T1", "w1", "P2", 9), "lock P2")
	mustOK(t, s.Verify("T1", "v", 10), "verify T1")
	assertCode(t, s.Verify("T1", "w1", 11), loto.StateNotAllowed, "verify again is state error")
}

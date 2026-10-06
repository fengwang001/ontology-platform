package loto_test

import (
	"strings"
	"testing"

	"ontology/loto"
)

func mustOK(t *testing.T, err error, ctx string) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: unexpected error: %v", ctx, err)
	}
}

func errCode(t *testing.T, err error, ctx string) loto.Code {
	t.Helper()
	if err == nil {
		t.Fatalf("%s: expected error, got nil", ctx)
	}
	oe, ok := err.(*loto.OpError)
	if !ok {
		t.Fatalf("%s: expected *OpError, got %T", ctx, err)
	}
	return oe.Code
}

func assertCode(t *testing.T, err error, want loto.Code, ctx string) {
	t.Helper()
	if got := errCode(t, err, ctx); got != want {
		t.Fatalf("%s: code=%s want=%s (%v)", ctx, got, want, err)
	}
}

func setup(t *testing.T) *loto.System {
	t.Helper()
	s := loto.New()
	// 人员：ap=申请人，a1/a2/a3=批准人，w1/w2/w3=作业人员，sv1/sv2=主管，v=纯验证人
	mustOK(t, s.RegisterPerson("ap", loto.RoleApplicant), "register ap")
	mustOK(t, s.RegisterPerson("ap2", loto.RoleApplicant), "register ap2")
	mustOK(t, s.RegisterPerson("ap", loto.RoleApprover), "ap also approver (for applicant-approval test)")
	mustOK(t, s.RegisterPerson("a1", loto.RoleApprover), "a1")
	mustOK(t, s.RegisterPerson("a2", loto.RoleApprover), "a2")
	mustOK(t, s.RegisterPerson("a3", loto.RoleApprover), "a3")
	mustOK(t, s.RegisterPerson("w1", loto.RoleWorker), "w1")
	mustOK(t, s.RegisterPerson("w2", loto.RoleWorker), "w2")
	mustOK(t, s.RegisterPerson("w3", loto.RoleWorker), "w3")
	mustOK(t, s.RegisterPerson("sv1", loto.RoleSupervisor), "sv1")
	mustOK(t, s.RegisterPerson("sv2", loto.RoleSupervisor), "sv2")
	mustOK(t, s.RegisterPerson("v"), "verifier")
	// ap 兼作业人员；w1 兼批准人（验证角色可叠加）
	mustOK(t, s.RegisterPerson("ap", loto.RoleWorker), "ap also worker")
	mustOK(t, s.RegisterPerson("w1", loto.RoleApprover), "w1 also approver")
	// 设备：D1 依赖 P1,P2；D2 依赖 P2,P3（P2 共享）
	mustOK(t, s.RegisterDevice("D1", []string{"P1", "P2"}), "D1")
	mustOK(t, s.RegisterDevice("D2", []string{"P2", "P3"}), "D2")
	mustOK(t, s.RegisterDevice("D3", []string{"P4"}), "D3")
	return s
}

// 时段左闭右开：首尾相接 [0,10) 与 [10,20) 不冲突。
func TestAdjacentWindowsDoNotConflict(t *testing.T) {
	s := setup(t)
	mustOK(t, s.Apply("T1", "ap", []string{"D1"}, loto.WorkNormal, 0, 10, []string{"w1"}), "apply T1")
	mustOK(t, s.Apply("T2", "ap", []string{"D1"}, loto.WorkNormal, 10, 20, []string{"w1"}), "apply T2")
	mustOK(t, s.Approve("T1", "a1", 0), "approve T1")
	// T1 在 5 完工后 T2 才能在 10 开工，但批准时两者仅首尾相接，不冲突。
	mustOK(t, s.Approve("T2", "a1", 0), "approve T2 at boundary")

	// 真正重叠（共享起点）必须冲突。
	mustOK(t, s.Apply("T3", "ap", []string{"D1"}, loto.WorkNormal, 5, 15, []string{"w1"}), "apply T3")
	assertCode(t, s.Approve("T3", "a1", 1), loto.Conflict, "overlapping T3")
}

// 只读观察之间可共存；观察票与普通票不豁免。
func TestObservationCoexistence(t *testing.T) {
	s := setup(t)
	mustOK(t, s.Apply("O1", "ap", []string{"D1"}, loto.WorkObservation, 0, 100, []string{"w1"}), "O1")
	mustOK(t, s.Apply("O2", "ap", []string{"D1"}, loto.WorkObservation, 0, 100, []string{"w1"}), "O2")
	mustOK(t, s.Approve("O1", "a1", 0), "approve O1")
	mustOK(t, s.Approve("O2", "a1", 0), "approve O2 (both observation)")

	mustOK(t, s.Apply("N1", "ap2", []string{"D1"}, loto.WorkNormal, 0, 100, []string{"w1"}), "N1")
	assertCode(t, s.Approve("N1", "a1", 0), loto.Conflict, "normal vs observation still conflicts")
}

// 高风险票：两个互不相同的批准人；申请人不能批；重复不占名额。
func TestHighRiskTwoDistinctApprovers(t *testing.T) {
	s := setup(t)
	mustOK(t, s.Apply("H1", "ap", []string{"D1"}, loto.WorkHighRisk, 0, 100, []string{"w1"}), "H1")
	// 申请人批准被拒（条件不满足）。
	assertCode(t, s.Approve("H1", "ap", 0), loto.ConditionNotMet, "applicant cannot approve")
	// 第一次批准不生效。
	mustOK(t, s.Approve("H1", "a1", 0), "first approver")
	if ph, ok := s.GetPermit("H1"); !ok || ph.Phase != loto.PhasePending {
		t.Fatalf("H1 should stay pending after one approval, got %+v", ph)
	}
	// 同一人重复批准被拒，且不消耗名额（再来 a2 仍生效）。
	assertCode(t, s.Approve("H1", "a1", 1), loto.ConditionNotMet, "duplicate approver")
	mustOK(t, s.Approve("H1", "a2", 2), "second distinct approver")
	if ph, _ := s.GetPermit("H1"); ph.Phase != loto.PhaseEffective {
		t.Fatalf("H1 should be effective, got %s", ph.Phase)
	}
}

// 完整正向流程：多人多锁 -> 验证 -> 开工 -> 进出 -> 完工 -> 逐把摘锁（最后一把解除隔离）。
func TestFullWorkflowAndLastLock(t *testing.T) {
	s := setup(t)
	mustOK(t, s.Apply("T1", "ap", []string{"D1"}, loto.WorkNormal, 0, 100, []string{"w1", "w2"}), "apply")
	mustOK(t, s.Approve("T1", "a1", 0), "approve")

	// 未全部上锁不能验证。
	mustOK(t, s.PlaceLock("T1", "w1", "P1", 1), "w1 P1")
	mustOK(t, s.PlaceLock("T1", "w1", "P2", 1), "w1 P2")
	assertCode(t, s.Verify("T1", "v", 1), loto.ConditionNotMet, "verify before all locked")

	mustOK(t, s.PlaceLock("T1", "w2", "P1", 2), "w2 P1")
	mustOK(t, s.PlaceLock("T1", "w2", "P2", 2), "w2 P2")

	// 验证人不得是作业人员。
	assertCode(t, s.Verify("T1", "w1", 2), loto.ConditionNotMet, "worker cannot verify")
	mustOK(t, s.Verify("T1", "v", 2), "verify by outsider")

	// 开工时刻落在计划时段终点（左闭右开）：已到终点，按逾期/超期的状态错误拒绝。
	assertCode(t, s.StartWork("T1", "ap", 100), loto.StateNotAllowed, "start at end (exclusive, overdue)")
	mustOK(t, s.StartWork("T1", "ap", 3), "start in window")

	// 未完成自己全部上锁的人不能进入（这里都已上锁，可进入）。
	mustOK(t, s.Enter("T1", "w1", 4), "w1 enter")
	assertCode(t, s.Enter("T1", "w1", 4), loto.ConditionNotMet, "double enter")
	mustOK(t, s.Leave("T1", "w1", 5), "w1 leave")
	mustOK(t, s.Enter("T1", "w1", 6), "w1 re-enter")
	mustOK(t, s.Enter("T1", "w2", 6), "w2 enter")

	// 有人在场不能完工。
	assertCode(t, s.Complete("T1", "ap", 7), loto.ConditionNotMet, "complete while inside")
	mustOK(t, s.Leave("T1", "w1", 8), "w1 leave 2")
	mustOK(t, s.Leave("T1", "w2", 8), "w2 leave")
	mustOK(t, s.Complete("T1", "ap", 9), "complete")

	// 完工前摘锁不允许。已完工。摘自己的锁；他人不能代摘。
	// 最后一把锁（P1/P2 上跨票统计）解除隔离：
	// 这里 D1 依赖 P1、P2，摘到 P1 与 P2 均无锁时才可送电。
	energizableBefore, _, err := s.CanEnergize("D1", 9)
	mustOK(t, err, "energize before removal")
	if energizableBefore {
		t.Fatalf("D1 must not be energizable with locks present")
	}
	// 摘锁错误次序：v 名下没有该锁 -> NotFound（先于权限）；非本票作业人员同理。
	assertCode(t, s.RemoveLock("T1", "v", "P1", 10), loto.NotFound, "no such lock (not-found beats role)")
	mustOK(t, s.RemoveLock("T1", "w1", "P1", 10), "w1 removes own P1")
	// P2 上仍有锁、P1 上仍有 w2 的锁 -> D1 不能送电。
	ok2, rep, err := s.CanEnergize("D1", 10)
	mustOK(t, err, "energize mid-removal")
	if ok2 {
		t.Fatalf("D1 still isolated; report=%v", rep.Reasons)
	}
	mustOK(t, s.RemoveLock("T1", "w2", "P1", 11), "w2 P1")
	// P2 上 w1/w2 两把锁仍在 -> 不能送电。
	ok3, _, _ := s.CanEnergize("D1", 11)
	if ok3 {
		t.Fatalf("P2 still locked")
	}
	mustOK(t, s.RemoveLock("T1", "w1", "P2", 12), "w1 P2")
	// P2 上还剩 w2 一把锁 -> 点仍隔离。
	ok4, _, _ := s.CanEnergize("D1", 12)
	if ok4 {
		t.Fatalf("last lock on P2 still present")
	}
	mustOK(t, s.RemoveLock("T1", "w2", "P2", 13), "w2 P2 (LAST)")
	ok5, rep5, _ := s.CanEnergize("D1", 13)
	if !ok5 {
		t.Fatalf("D1 must be energizable after last lock removed: %v", rep5.Reasons)
	}
	if !strings.Contains(strings.Join(rep5.Reasons, "\n"), "LAST") &&
		!strings.Contains(strings.Join(rep5.Reasons, "\n"), "no lock") {
		t.Fatalf("report should explain no-lock decision: %v", rep5.Reasons)
	}
}

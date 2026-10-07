package ontology

import "testing"

// 调度模型（确定性）：
//   - 动作在锁内读取一致快照 (version, highWater, attrs) 后，
//     释放锁并停在“读同步点”；放行后重新取锁执行 CAS，随后停在
//     “判定同步点”。
//   - script 按 (opID, kind) 精确放行；判定点不影响状态，自动收尾。
//
// 因此一次尝试的判定基线，等于它的读点被放行那一刻的实例状态。

// 场景 1：同权限多方竞争——同权限永远不得以权限抢占；
// 输家只能普通冲突重试或预算耗尽。
func TestSamePrivilegeNoPreemption(t *testing.T) {
	ops := []Op{
		counterOp(1, 2, 1, 1),
		counterOp(2, 2, 2, 1),
		counterOp(3, 2, 3, 4),
	}
	// 读闸门允许多个动作持有同一旧基线在途：1、2、3 都读 v0。
	// CAS 顺序：1 生效 v1；2 同权限冲突、预算 1 => 耗尽（不得抢占）；
	// 3 同权限冲突，重试读 v1 后生效 v2。
	steps := cat(reads(1, 2, 3), commits(1, 2, 3), reads(3), commits(3))
	results, sched := Run(ops, steps)

	if results[1].Status != StatusCommitted || results[1].CommittedAt != 1 {
		t.Fatalf("op1 want committed@v1, got %+v", results[1])
	}
	if results[2].Status != StatusExhausted {
		t.Fatalf("op2 want exhausted (same privilege must not preempt), got %s", results[2].Status)
	}
	if results[3].Status != StatusCommitted || results[3].CommittedAt != 2 {
		t.Fatalf("op3 want committed@v2 after retry, got %+v", results[3])
	}
	for _, r := range results {
		for _, a := range r.Attempts {
			if a.Verdict == "preempted" {
				t.Fatalf("same-privilege must never preempt: op=%d %+v", a.OpID, a)
			}
		}
	}
	ver, _, attrs := sched.executor.FinalState(testInstance)
	if ver != 2 || attrs["x"].(int) != 4 { // 1 生效(1)，3 重试后生效(3)
		t.Fatalf("unexpected final state: v=%d attrs=%v", ver, attrs)
	}
	compareWithReference(t, ops, results, sched)
}

// 场景 2：不同权限交替抢占。高权限生效后，基于被推进基线的
// 低权限在途尝试立即被判定抢占，不消耗预算。
func TestAlternatingPreemption(t *testing.T) {
	ops := []Op{
		counterOp(10, 1, 10, 1), // 先导低权限写入 v1
		counterOp(1, 1, 1, 5),   // 低权限：先冲突，再被高权限抢占
		counterOp(2, 5, 2, 5),   // 高权限：生效 v2(hw5)
		counterOp(3, 1, 3, 5),   // 低权限：读 v2 即被抢占
	}
	// 10 生效 v1；1 读 v1 冲突（预算保留）；2 读 v1 生效 v2(hw5)；
	// 1 重试读 v2：基线 v2 被 hw5 推进 => 抢占；3 读 v2 => 抢占。
	steps := cat(
		reads(10, 1), commits(10, 1),
		reads(2, 1, 3), commits(2, 1, 3),
	)
	results, sched := Run(ops, steps)

	if results[10].Status != StatusCommitted {
		t.Fatalf("op10 want committed, got %s", results[10].Status)
	}
	if results[2].Status != StatusCommitted || results[2].CommittedAt != 2 {
		t.Fatalf("op2 want committed@v2, got %+v", results[2])
	}
	for _, id := range []int{1, 3} {
		if results[id].Status != StatusPreempted {
			t.Fatalf("op%d want preempted, got %s attempts=%d",
				id, results[id].Status, len(results[id].Attempts))
		}
	}
	// 抢占是最后一次尝试的判定；op1 先冲突一次（预算未被抢占消耗）。
	if len(results[1].Attempts) != 2 ||
		results[1].Attempts[0].Verdict != "conflict" ||
		results[1].Attempts[1].Verdict != "preempted" {
		t.Fatalf("op1 trajectory want [conflict preempted], got %+v", results[1].Attempts)
	}
	if len(results[3].Attempts) != 1 || results[3].Attempts[0].Verdict != "preempted" {
		t.Fatalf("op3 want single immediate preempted attempt, got %+v", results[3].Attempts)
	}
	ver, hw, attrs := sched.executor.FinalState(testInstance)
	// 仅 10 与 2 两次生效：v1: 10；v2: 10+2=12。
	if ver != 2 || hw != 5 || attrs["x"].(int) != 12 {
		t.Fatalf("preemption must add no state change: v=%d hw=%d attrs=%v", ver, hw, attrs)
	}
	compareWithReference(t, ops, results, sched)
}

// 场景 3：抢占判定先于预算耗尽（预算恰为 1）。
func TestPreemptionBeforeBudgetExhaustion(t *testing.T) {
	ops := []Op{
		counterOp(10, 9, 10, 1),
		counterOp(1, 1, 1, 1), // 预算 1；撞上 hw9 必须是抢占而非耗尽
	}
	steps := cat(reads(10, 1), commits(10), commits(1))
	results, sched := Run(ops, steps)
	if results[1].Status != StatusPreempted {
		t.Fatalf("want preempted even on the only budget, got %s", results[1].Status)
	}
	if len(results[1].Attempts) != 1 || results[1].Attempts[0].Verdict != "preempted" {
		t.Fatalf("want single preempted attempt, got %+v", results[1].Attempts)
	}
	compareWithReference(t, ops, results, sched)
}

// 场景 4：高权限动作自身也发生同权限冲突重试；其重试生效后抢占低权限。
func TestHighPrivilegeActionRetriesToo(t *testing.T) {
	ops := []Op{
		counterOp(2, 5, 20, 4), // 同权限 B 先生效 v1
		counterOp(1, 5, 10, 4), // 同权限 A：冲突后重试生效 v2
		counterOp(3, 1, 30, 4), // 低权限：读 v2 被 A 抢占
	}
	steps := cat(reads(2, 1), commits(2, 1), reads(1, 3), commits(1, 3))
	results, sched := Run(ops, steps)

	if results[2].Status != StatusCommitted || results[2].CommittedAt != 1 {
		t.Fatalf("op2 want committed@v1, got %+v", results[2])
	}
	if results[1].Status != StatusCommitted || results[1].CommittedAt != 2 {
		t.Fatalf("op1 want committed@v2 after conflict retry, got %+v", results[1])
	}
	if results[1].Attempts[0].Verdict != "conflict" {
		t.Fatalf("op1 first attempt want conflict (same privilege), got %s",
			results[1].Attempts[0].Verdict)
	}
	if results[3].Status != StatusPreempted {
		t.Fatalf("op3 want preempted by retried high op1, got %s", results[3].Status)
	}
	ver, _, attrs := sched.executor.FinalState(testInstance)
	if ver != 2 || attrs["x"].(int) != 30 { // v1:20 v2:30
		t.Fatalf("unexpected final state v=%d attrs=%v", ver, attrs)
	}
	compareWithReference(t, ops, results, sched)
}

// 场景 5：高水位单调——中权限(2) 也被高水位(9) 抢占，低写不复位水位。
func TestHighWaterMonotonic(t *testing.T) {
	ops := []Op{
		counterOp(1, 1, 1, 4),
		counterOp(3, 9, 3, 4),
		counterOp(4, 1, 4, 4),
		counterOp(5, 2, 5, 4), // 权限 2 < 9，仍被抢占
	}
	steps := cat(reads(1, 3), commits(1), commits(3), reads(3), commits(3), reads(4, 5), commits(4), commits(5))
	results, sched := Run(ops, steps)
	want := map[int]Status{
		1: StatusCommitted, 3: StatusCommitted,
		4: StatusPreempted, 5: StatusPreempted,
	}
	for id, status := range want {
		if results[id].Status != status {
			t.Fatalf("op%d want %s got %s", id, status, results[id].Status)
		}
	}
	ver, hw, _ := sched.executor.FinalState(testInstance)
	if ver != 2 || hw != 9 {
		t.Fatalf("want v2 hw9 (monotonic high water), got v=%d hw=%d", ver, hw)
	}
	compareWithReference(t, ops, results, sched)
}

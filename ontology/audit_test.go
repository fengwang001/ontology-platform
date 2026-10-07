package ontology

import (
	"testing"
)

// TestCommitVsRollback 覆盖提交与回退记录在审计序列中的区分：
// 提交 Before!=After 并改变状态；回退占用序号但 Before==After、状态不变。
func TestCommitVsRollback(t *testing.T) {
	L := testLogger{t}
	store, exec := newSeeded(t, "A", "B")

	r1, err := exec.ExecuteAction("act-commit", map[string]string{"A": "a1", "B": "b1"}, false)
	if err != nil {
		t.Fatal(err)
	}
	before := store.initialCopy()
	L.log("输入1: 提交动作 act-commit 写 A=a1,B=b1")
	L.log("输出1: seq=%d outcome=%s changes=%+v", r1.Seq, r1.Outcome, r1.Changes)
	if r1.Seq != 1 || r1.Outcome != Committed {
		t.Fatalf("期望 seq=1 COMMITTED")
	}

	r2, err := exec.ExecuteAction("act-rollback", map[string]string{"A": "SHOULD_NOT_APPLY"}, true)
	if err != nil {
		t.Fatal(err)
	}
	L.log("输入2: 回退动作 act-rollback 试图写 A=SHOULD_NOT_APPLY")
	L.log("输出2: seq=%d outcome=%s changes=%+v", r2.Seq, r2.Outcome, r2.Changes)

	// 判定依据：序号连续无空洞（1,2）；回退记录 Before==After；状态停留在提交后。
	if r2.Seq != 2 {
		t.Fatalf("回退也须占用连续序号，得到 %d", r2.Seq)
	}
	if r2.Outcome != RolledBack {
		t.Fatalf("期望 ROLLED_BACK")
	}
	for _, c := range r2.Changes {
		if c.Before != c.After {
			t.Fatalf("回退记录 Before/After 必须相同，得到 %+v", c)
		}
	}
	if store.Len() != 2 {
		t.Fatalf("两条记录都应存在，Len=%d", store.Len())
	}
	want := map[string]string{"A": "a1", "B": "b1"}
	got := NewReplayer(store).StateAt(2)
	L.log("依据: 回退后 seq=2 状态应为 %s，实际 %s（初始=%s）", stateString(want), stateString(got), stateString(before))
	mustState(t, got, want, "rollback 不改变状态")

	// 重放区间：回退只作为「尝试事件」出现，不产生状态变更。
	ev, err := NewReplayer(store).ReplayRange(0, 2)
	if err != nil {
		t.Fatal(err)
	}
	L.log("依据: 重放(0,2] 事件=%s（提交有 Action 变更，回退 Action=nil）", eventsString(ev))
	if len(ev) != 3 { // 提交 2 个实例变更 + 1 条回退尝试
		t.Fatalf("期望 3 个事件，得到 %d: %s", len(ev), eventsString(ev))
	}
	if ev[2].Action != nil || ev[2].Outcome != RolledBack {
		t.Fatalf("回退事件不得携带状态变更: %+v", ev[2])
	}
}

// TestAuditFailureRollsBack 审计写入失败时：状态不变、序号不占用、无空洞。
func TestAuditFailureRollsBack(t *testing.T) {
	L := testLogger{t}
	store, exec := newSeeded(t, "A")

	ok, err := exec.ExecuteAction("a0", map[string]string{"A": "a1"}, false)
	if err != nil {
		t.Fatal(err)
	}
	L.log("输入: 先成功 seq=%d；随后注入 1 次审计写入失败", ok.Seq)

	store.InjectAppendFailures(1)
	_, failErr := exec.ExecuteAction("a-bad", map[string]string{"A": "SHOULD_NEVER_VISIBLE"}, false)
	if failErr == nil || !IsAuditWriteFailure(failErr) {
		t.Fatalf("期望 AuditWriteError，得到 %v", failErr)
	}
	L.log("输出: 审计失败错误=%v（类型可区分=%v）", failErr, IsAuditWriteFailure(failErr))

	retry, err := exec.ExecuteAction("a-good", map[string]string{"A": "a2"}, false)
	if err != nil {
		t.Fatal(err)
	}
	// 判定依据：失败不占序号，重试成功得到 seq=2，无空洞；状态自洽。
	L.log("依据: 失败后重试 seq=%d（应为2，无空洞），Len=%d", retry.Seq, store.Len())
	if retry.Seq != 2 || store.Len() != 2 {
		t.Fatalf("失败不得占用序号，seq=%d len=%d", retry.Seq, store.Len())
	}
	mustState(t, NewReplayer(store).StateAt(2), map[string]string{"A": "a2"}, "审计失败状态回退")

	// 回退动作 + 审计失败同样不留痕迹。
	store.InjectAppendFailures(1)
	_, err = exec.ExecuteAction("rb-bad", map[string]string{"A": "x"}, true)
	if !IsAuditWriteFailure(err) {
		t.Fatalf("期望审计失败，得到 %v", err)
	}
	if store.Len() != 2 {
		t.Fatalf("失败回退不得留记录，Len=%d", store.Len())
	}
	L.log("依据: 回退动作审计失败后 Len 仍=%d，状态=%s", store.Len(), stateString(NewReplayer(store).StateAt(store.Len())))
}

// TestErrorOrdering 参数非法优先于审计写入失败，且两类错误可区分。
func TestErrorOrdering(t *testing.T) {
	L := testLogger{t}
	store, exec := newSeeded(t, "A")
	store.InjectAppendFailures(5) // 即使审计被配置为失败

	_, err := exec.ExecuteAction("missing", map[string]string{"NOPE": "x"}, false)
	L.log("输入: 目标实例不存在且审计处于故障；输出 err=%v", err)
	if !IsInvalidInput(err) {
		t.Fatalf("参数非法须先报，得到 %v", err)
	}

	_, err = exec.Correct("c-missing", 99, map[string]string{"A": "x"})
	L.log("输入: 订正不存在序号；输出 err=%v", err)
	if !IsInvalidInput(err) {
		t.Fatalf("订正不存在序号须为参数非法，得到 %v", err)
	}

	store.ResetFaults()
	r, err := exec.ExecuteAction("base", map[string]string{"A": "a1"}, false)
	if err != nil {
		t.Fatal(err)
	}
	corr, err := exec.Correct("corr", r.Seq, map[string]string{"A": "a1-fixed"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = exec.Correct("corr-of-corr", corr.Seq, map[string]string{"A": "a2"})
	L.log("输入: 订正指向另一条订正 seq=%d；输出 err=%v", corr.Seq, err)
	if !IsInvalidInput(err) {
		t.Fatalf("订正不得指向订正，须为参数非法，得到 %v", err)
	}
}

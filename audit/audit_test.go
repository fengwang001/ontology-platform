package audit

import (
	"fmt"
	"testing"
)

// TestCommitVsRollbackDistinction 覆盖：提交与回退记录在审计序列中
// 的区分；回退也占序号、无空洞；重放跳过回退。
func TestCommitVsRollbackDistinction(t *testing.T) {
	tl := &testLogger{}
	defer tl.flush(t)

	store, log, exec, replayer, _ := newSystem(4)
	const typ = "Order"
	if _, err := exec.RegisterInstance(typ, "o1", "v0"); err != nil {
		t.Fatal(err)
	}

	r1, err := exec.Execute(Action{ActionID: "A1", TypeName: typ, Writes: []Write{{"o1", "v1"}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	tl.logf("输入: 动作 A1 提交 o1:v0->v1 | 实际输出: %s | 依据: ACTION 且 Before!=After", r1)

	r2, err := exec.Execute(Action{ActionID: "A2", TypeName: typ, Writes: []Write{{"o1", "v2"}}},
		func(*Txn) error { return ErrActionRolledBack })
	if err != nil {
		t.Fatal(err)
	}
	tl.logf("输入: 动作 A2 业务回退 o1:v1->v2 | 实际输出: %s | 依据: ROLLBACK 且 Before==After=v1", r2)

	r3, err := exec.Execute(Action{ActionID: "A3", TypeName: typ, Writes: []Write{{"o1", "v3"}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	tl.logf("输入: 动作 A3 提交 o1->v3 | 实际输出: %s | 依据: seq 连续 1,2,3", r3)

	if r1.Kind != KindAction || r1.Seq != 2 || r1.Changes[0].Before != "v0" || r1.Changes[0].After != "v1" {
		t.Fatalf("r1 = %v", r1)
	}
	if r2.Kind != KindRollback || r2.Seq != 3 ||
		r2.Changes[0].Before != "v1" || r2.Changes[0].After != "v1" {
		t.Fatalf("rollback record must have Before==After, got %v", r2)
	}
	if r3.Seq != 4 {
		t.Fatalf("seqs = %d,%d want 3,4", r2.Seq, r3.Seq)
	}

	state, err := replayer.StateAt(typ, 4)
	if err != nil {
		t.Fatal(err)
	}
	assertState(t, state, map[string]string{"o1": "v3"}, "rollback skipped in replay")
	tl.logf("重放至 seq=3: state=%v | 依据: v3（回退的 v2 未生效）", state)

	if err := log.VerifyChain(typ); err != nil {
		t.Fatalf("hash chain: %v", err)
	}
	if v, _ := store.Get(typ, "o1"); v != "v3" {
		t.Fatalf("live state=%q want v3", v)
	}
}

// TestAuditFailureRollsBackAtomically 覆盖：审计写入失败导致状态与
// 审计一致回退（状态不变、无序号占用），并与参数非法相互区分、
// 参数非法优先报告。
func TestAuditFailureRollsBackAtomically(t *testing.T) {
	tl := &testLogger{}
	defer tl.flush(t)

	store, log, exec, replayer, _ := newSystem(4)
	const typ = "Order"
	if _, err := exec.RegisterInstance(typ, "o1", "v0"); err != nil {
		t.Fatal(err)
	}

	_, err := exec.Execute(Action{ActionID: "A-bad", TypeName: typ, Writes: []Write{{"ghost", "x"}}}, nil)
	tl.logf("输入: 写不存在实例 ghost | 实际输出: %v | 依据: IllegalRequestError 最先报告", err)
	if !isIllegal(err) {
		t.Fatalf("want IllegalRequestError, got %v", err)
	}

	log.InjectNextAppendFailure(fmt.Errorf("disk full"))
	_, err = exec.Execute(Action{ActionID: "A-fail", TypeName: typ, Writes: []Write{{"o1", "v1"}}}, nil)
	tl.logf("输入: 提交 o1->v1 但审计写入失败 | 实际输出: %v | 依据: AuditWriteError，状态仍 v0", err)
	if !isAuditWrite(err) {
		t.Fatalf("want AuditWriteError, got %v", err)
	}
	if isIllegal(err) {
		t.Fatalf("audit write failure must not be classified as illegal")
	}
	if v, _ := store.Get(typ, "o1"); v != "v0" {
		t.Fatalf("state changed despite audit failure: %q", v)
	}
	if log.LastSeq(typ) != 1 {
		t.Fatalf("failed append must not reserve a seq, last=%d", log.LastSeq(typ))
	}

	rec, err := exec.Execute(Action{ActionID: "A-ok", TypeName: typ, Writes: []Write{{"o1", "v1"}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	tl.logf("随后成功动作 | 实际输出: %s | 依据: seq=1（失败未留洞）状态 v1", rec)
	if rec.Seq != 2 {
		t.Fatalf("seq=%d want 2", rec.Seq)
	}
	state, _ := replayer.StateAt(typ, 2)
	assertState(t, state, map[string]string{"o1": "v1"}, "post-failure replay")
}

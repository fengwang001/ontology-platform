package runner_test

import (
	"errors"
	"testing"

	"ontology/flow"
	"ontology/grants"
	"ontology/runner"
)

func newKit(t *testing.T, T int64) (*runner.Runner, *grants.Registry, *flow.Catalog) {
	t.Helper()
	g := grants.New()
	c := flow.NewCatalog()
	r, err := runner.New(g, c, T)
	if err != nil {
		t.Fatal(err)
	}
	return r, g, c
}

func requireErrIs(t *testing.T, err error, target error, why string) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("%s: err = %v, 期望 %v", why, err, target)
	}
	t.Logf("输入 %s → 输出 %v；判定依据 %s", why, err, target)
}

// 题目给出的完整例子：快照封顶 → Deny 挂起 → Grant 不扩大 eff →
// 审批资格（自批先于无权）→ Override 只放一步 → 后续仍按 eff 判定。
func TestWorkedExample(t *testing.T) {
	r, g, c := newKit(t, 10)
	if err := g.Grant([]byte("p"), 0b0111); err != nil {
		t.Fatal(err)
	}
	if err := c.Define([]byte("d"), 0b0110, []uint64{0b0010, 0b0100}); err != nil {
		t.Fatal(err)
	}
	if err := r.Launch([]byte("i"), []byte("d"), []byte("p"), 0); err != nil {
		t.Fatal(err)
	}
	if err := g.Revoke([]byte("p"), 0b0010); err != nil {
		t.Fatal(err)
	}
	// StartStep：eff = 0b0101 & 0b0110 = 0b0100；req0=0b0010 缺失 → Deny。
	if err := r.StartStep([]byte("i"), 1); err != nil {
		t.Fatal(err)
	}
	st, err := r.Status([]byte("i"), 1)
	if err != nil {
		t.Fatal(err)
	}
	if !st.Suspended || st.Miss != 0b0010 || st.Deadline != 11 {
		t.Fatalf("Deny 后状态 = %+v, 期望 Suspended miss=0b0010 deadline=11", st)
	}
	t.Logf("StartStep@1 → Suspended miss=%#b deadline=%d；eff=0b0100 缺位 0b0010", st.Miss, st.Deadline)
	evs, _ := r.AuditLog([]byte("i"))
	if evs[0].Kind.String() != "Deny" || evs[0].Seq != 1 {
		t.Fatalf("审计[0] = %+v", evs[0])
	}
	// 启动后 Grant 位 3：E 不含位 3，eff 不扩大，仍 Suspended。
	if err := g.Grant([]byte("p"), 0b1000); err != nil {
		t.Fatal(err)
	}
	if err := r.StartStep([]byte("i"), 2); err != nil {
		t.Fatal(err)
	}
	st, _ = r.Status([]byte("i"), 2)
	if !st.Suspended || st.Miss != 0b0010 {
		t.Fatalf("Grant 后重试应仍 Deny: %+v", st)
	}
	t.Logf("Grant(0b1000) 后 StartStep@2 → 仍 Suspended miss=%#b；快照 E=0b0110 封顶", st.Miss)
	// 自批先于无权；无权第三人。
	requireErrIs(t, r.Approve([]byte("i"), []byte("p"), 3), runner.ErrSelf, "自批")
	requireErrIs(t, r.Approve([]byte("i"), []byte("a0"), 3), runner.ErrNoAuthority, "a 无位63")
	// 被拒绝操作不写审计。
	evs, _ = r.AuditLog([]byte("i"))
	if len(evs) != 2 {
		t.Fatalf("被拒绝后审计长度 = %d, 期望 2", len(evs))
	}
	if err := g.Grant([]byte("a"), grants.ApproverBit); err != nil {
		t.Fatal(err)
	}
	if err := r.Approve([]byte("i"), []byte("a"), 3); err != nil {
		t.Fatal(err)
	}
	st, _ = r.Status([]byte("i"), 3)
	if st.Phase != runner.RunningPhase || st.Steps[0] != runner.StepRunning {
		t.Fatalf("Override 后 = %+v, 期望步骤0 Running", st)
	}
	t.Logf("Approve@3 → 步骤0 Running（Override 不受 miss 约束）")
	// Finish 不复查权限；步骤1 按 eff=0b0100 满足 req1=0b0100 → Allow。
	if err := r.FinishStep([]byte("i"), 4); err != nil {
		t.Fatal(err)
	}
	if err := r.StartStep([]byte("i"), 5); err != nil {
		t.Fatal(err)
	}
	st, _ = r.Status([]byte("i"), 5)
	if st.Phase != runner.RunningPhase || st.Steps[1] != runner.StepRunning {
		t.Fatalf("步骤1 应 Running: %+v", st)
	}
	t.Logf("Finish@4, StartStep@5 → 步骤1 Running；req1=0b0100 是 eff=0b0100 子集")
	if err := r.FinishStep([]byte("i"), 6); err != nil {
		t.Fatal(err)
	}
	st, _ = r.Status([]byte("i"), 6)
	if st.Phase != runner.CompletedPhase || st.Outcome != runner.Completed {
		t.Fatalf("终局 = %+v, 期望 Completed", st)
	}
	evs, _ = r.AuditLog([]byte("i"))
	want := []string{"Deny", "Deny", "Override", "Allow"}
	if len(evs) != len(want) {
		t.Fatalf("审计 = %v, 期望 %v", kinds(evs), want)
	}
	for i, k := range want {
		if evs[i].Kind.String() != k || evs[i].Seq != i+1 {
			t.Fatalf("审计[%d] = %+v, 期望 %s seq=%d", i, evs[i], k, i+1)
		}
	}
	t.Logf("审计序列 = %v；序号 1..4 连续", kinds(evs))
}

func kinds(evs []runner.Event) []string {
	out := make([]string, len(evs))
	for i, ev := range evs {
		out[i] = ev.Kind.String()
	}
	return out
}

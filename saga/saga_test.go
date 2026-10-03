package saga_test

import (
	"testing"

	"ontology/effect"
	"ontology/journal"
	"ontology/saga"
)

func newSaga() (*saga.Saga, *journal.Journal, *effect.Table) {
	jr := journal.New()
	eff := effect.New()
	return saga.New(jr, eff), jr, eff
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("意外错误: %v", err)
	}
}

func wantPlan(t *testing.T, s *saga.Saga, id []byte, kind saga.PlanKind, step int, why string) saga.Plan {
	t.Helper()
	got, err := s.Recover(id)
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	want := saga.Plan{Kind: kind, Step: step}
	if got != want {
		t.Fatalf("计划不符: 得到 %v，期望 %v；判定依据: %s", got, want, why)
	}
	t.Logf("输出 %v；判定依据: %s", got, why)
	return got
}

// 规格例：n=5，p=2，B=1。
func TestExamples(t *testing.T) {
	// 例 1：[B,I0,D0,I1] → 补偿自 1（顺序 1、0），fwd/comp 各新增 2。
	s, _, eff := newSaga()
	id := []byte("ex1")
	must(t, s.Begin(id, 5, 2, 1))
	must(t, s.Intent(id, 0))
	must(t, s.Done(id, 0))
	must(t, s.Intent(id, 1))
	t.Logf("输入: [B,I0,D0,I1]，n=5 p=2 B=1")
	wantPlan(t, s, id, saga.Compensate, 1, "I(1) 且 1<p=2，结果未知，补偿自 1（含 1，可能已生效）")
	must(t, s.CompIntent(id, 1))
	must(t, s.CompDone(id, 1))
	must(t, s.CompIntent(id, 0))
	must(t, s.CompDone(id, 0))
	wantPlan(t, s, id, saga.Compensated, -1, "CD(0) 后为已补偿")
	if a, _ := eff.Stats("ex1", 1, effect.Fwd); a != 1 {
		t.Fatalf("fwd(1) 新增应为 1，实际 %d", a)
	}
	if fa, _ := eff.Totals(); fa != 4 {
		t.Fatalf("fwd 新增 2（步骤 0、1）+ comp 新增 2，总新增应为 4，实际 %d", fa)
	}
	t.Logf("副作用账: fwd 新增 2（步骤 0、1），comp 新增 2；步骤 1 结果未知，空补偿也照常登记")

	// 例 2：[B,I0,D0,I1,D1,I2] → 探测；Probe(true) → 前滚 3。
	s2, _, _ := newSaga()
	id2 := []byte("ex2")
	must(t, s2.Begin(id2, 5, 2, 1))
	for i := 0; i < 2; i++ {
		must(t, s2.Intent(id2, i))
		must(t, s2.Done(id2, i))
	}
	must(t, s2.Intent(id2, 2))
	t.Logf("输入: [B,I0,D0,I1,D1,I2]，n=5 p=2 B=1")
	wantPlan(t, s2, id2, saga.ProbePivot, -1, "I(2) 且 2==p，枢轴结果未知，必须探测")
	must(t, s2.Probe(id2, true))
	wantPlan(t, s2, id2, saga.Forward, 3, "Probe(true) 写 D(2)，D 非末步则前滚 3")

	// 例 2b：同前缀 Probe(false) → 补偿自 1 并最终已补偿。
	s2b, _, _ := newSaga()
	id2b := []byte("ex2b")
	must(t, s2b.Begin(id2b, 5, 2, 1))
	for i := 0; i < 2; i++ {
		must(t, s2b.Intent(id2b, i))
		must(t, s2b.Done(id2b, i))
	}
	must(t, s2b.Intent(id2b, 2))
	wantPlan(t, s2b, id2b, saga.ProbePivot, -1, "I(2) 且 2==p，枢轴结果未知，必须探测")
	must(t, s2b.Probe(id2b, false))
	wantPlan(t, s2b, id2b, saga.Compensate, 1, "Probe(false) 写 F(2,永久)，终败且 2≤p，补偿自 1")
	must(t, s2b.CompIntent(id2b, 1))
	must(t, s2b.CompDone(id2b, 1))
	must(t, s2b.CompIntent(id2b, 0))
	must(t, s2b.CompDone(id2b, 0))
	wantPlan(t, s2b, id2b, saga.Compensated, -1, "补偿到 CD(0) 结束")

	// 例 3：[B,I0,D0,I1,D1,I2,F2瞬时] → 前滚 2；再 I2、F2瞬时 → c=2>1 → 补偿自 1。
	s3, _, _ := newSaga()
	id3 := []byte("ex3")
	must(t, s3.Begin(id3, 5, 2, 1))
	for i := 0; i < 2; i++ {
		must(t, s3.Intent(id3, i))
		must(t, s3.Done(id3, i))
	}
	must(t, s3.Intent(id3, 2))
	must(t, s3.Fail(id3, 2, journal.Transient))
	wantPlan(t, s3, id3, saga.Forward, 2, "F(2,瞬时) c=1≤B=1，非终败，前滚 2")
	must(t, s3.Intent(id3, 2))
	must(t, s3.Fail(id3, 2, journal.Transient))
	wantPlan(t, s3, id3, saga.Compensate, 1, "F(2,瞬时) c=2>B=1 且 2≤p，终败，补偿自 1")

	// 例 4：[…,D2,I3,F3永久] → 转人工。
	s4, _, _ := newSaga()
	id4 := []byte("ex4")
	must(t, s4.Begin(id4, 5, 2, 1))
	for i := 0; i < 3; i++ {
		must(t, s4.Intent(id4, i))
		must(t, s4.Done(id4, i))
	}
	must(t, s4.Intent(id4, 3))
	must(t, s4.Fail(id4, 3, journal.Permanent))
	wantPlan(t, s4, id4, saga.Manual, -1, "F(3,永久) 终败且 3>p，不能前滚也不能回退，转人工")

	// 例 5：B=0 时 i≤p 的首个瞬时失败即终败。
	s5, _, _ := newSaga()
	id5 := []byte("ex5")
	must(t, s5.Begin(id5, 3, 1, 0))
	must(t, s5.Intent(id5, 0))
	must(t, s5.Fail(id5, 0, journal.Transient))
	wantPlan(t, s5, id5, saga.Compensated, -1, "B=0，c=1>0 终败，i=0 为已补偿")

	// 例 6：I3 后崩溃重做 Intent(3)，fwd(3) 新增 1、重复 1。
	s6, _, eff6 := newSaga()
	id6 := []byte("ex6")
	must(t, s6.Begin(id6, 5, 2, 1))
	for i := 0; i < 3; i++ {
		must(t, s6.Intent(id6, i))
		must(t, s6.Done(id6, i))
	}
	must(t, s6.Intent(id6, 3))
	wantPlan(t, s6, id6, saga.Forward, 3, "I(3) 且 3>p，重做，副作用幂等")
	must(t, s6.Intent(id6, 3))
	if a, d := eff6.Stats("ex6", 3, effect.Fwd); a != 1 || d != 1 {
		t.Fatalf("fwd(3) 应新增 1、重复 1，实际 %d、%d", a, d)
	}
	t.Logf("输出: fwd(3) 新增 1 次、重复 1 次；判定依据: 幂等表按 (实例,步骤,阶段) 去重")
}

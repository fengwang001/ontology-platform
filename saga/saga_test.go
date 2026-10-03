package saga_test

import (
	"fmt"
	"testing"

	"ontology/effect"
	"ontology/journal"
	"ontology/saga"
)

func begin(t *testing.T, m *saga.Manager, id string, n, p, b int) {
	t.Helper()
	if err := m.Begin(id, n, p, b); err != nil {
		t.Fatalf("Begin(%s,%d,%d,%d): %v", id, n, p, b, err)
	}
}

func recoverPlan(t *testing.T, m *saga.Manager, id string) saga.Plan {
	t.Helper()
	pl, err := m.Recover(id)
	if err != nil {
		t.Fatalf("Recover(%s): %v", id, err)
	}
	return pl
}

// TestExamples 覆盖设计文档中的全部例子（n=5, p=2, B=1）。
func TestExamples(t *testing.T) {
	jr, ef := journal.New(), effect.New()
	m := saga.New(jr, ef)
	begin(t, m, "s", 5, 2, 1)
	must(t, m.Intent("s", 0))
	must(t, m.Done("s", 0))
	must(t, m.Intent("s", 1)) // 日志 [B,I0,D0,I1]，此处崩溃
	pl := recoverPlan(t, m, "s")
	t.Logf("输入 [B,I0,D0,I1]；输出 %v；依据: I(1) 结果未知且 1<p=2，直接补偿", pl)
	if pl != (saga.Plan{Kind: saga.PlanCompensate, Step: 1}) {
		t.Fatalf("plan=%v", pl)
	}
	must(t, m.CompIntent("s", 1))
	must(t, m.CompDone("s", 1))
	must(t, m.CompIntent("s", 0))
	must(t, m.CompDone("s", 0))
	if pl := recoverPlan(t, m, "s"); pl.Kind != saga.PlanCompensated {
		t.Fatalf("补偿链走完应为已补偿, got %v", pl)
	}
	fa, _ := ef.Totals("s", effect.Fwd)
	ca, _ := ef.Totals("s", effect.Comp)
	t.Logf("副作用账: fwd 新增 %d（步骤 0、1），comp 新增 %d（步骤 1、0）", fa, ca)
	if fa != 2 || ca != 2 {
		t.Fatalf("fwd=%d comp=%d, 期望 2/2", fa, ca)
	}

	// 枢轴在途 → 探测；两分支各用一个实例。
	for _, committed := range []bool{true, false} {
		id := fmt.Sprintf("probe-%v", committed)
		begin(t, m, id, 5, 2, 1)
		for i := 0; i <= 1; i++ {
			must(t, m.Intent(id, i))
			must(t, m.Done(id, i))
		}
		must(t, m.Intent(id, 2)) // [B,I0,D0,I1,D1,I2]
		if pl := recoverPlan(t, m, id); pl.Kind != saga.PlanProbe {
			t.Fatalf("I(p) 在途应探测, got %v", pl)
		}
		must(t, m.Probe(id, committed))
		pl := recoverPlan(t, m, id)
		t.Logf("Probe(%v) 后计划 %v", committed, pl)
		want := saga.Plan{Kind: saga.PlanForward, Step: 3}
		if !committed {
			want = saga.Plan{Kind: saga.PlanCompensate, Step: 1}
		}
		if pl != want {
			t.Fatalf("Probe(%v) 后 got %v want %v", committed, pl, want)
		}
	}

	// 枢轴瞬时失败可重试；耗尽预算后终败补偿。
	begin(t, m, "retry", 5, 2, 1)
	for i := 0; i <= 2; i++ {
		must(t, m.Intent("retry", i))
		if i < 2 {
			must(t, m.Done("retry", i))
		}
	}
	must(t, m.Fail("retry", 2, journal.Transient)) // […,I2,F2瞬时]
	if pl := recoverPlan(t, m, "retry"); pl != (saga.Plan{Kind: saga.PlanForward, Step: 2}) {
		t.Fatalf("c=1≤B 应前滚 2, got %v", pl)
	}
	must(t, m.Intent("retry", 2))
	must(t, m.Fail("retry", 2, journal.Transient)) // c=2>B=1
	if pl := recoverPlan(t, m, "retry"); pl != (saga.Plan{Kind: saga.PlanCompensate, Step: 1}) {
		t.Fatalf("c=2>B=1 应补偿自 1, got %v", pl)
	}

	// 枢轴后永久失败 → 转人工。
	begin(t, m, "manual", 5, 2, 1)
	for i := 0; i <= 2; i++ {
		must(t, m.Intent("manual", i))
		must(t, m.Done("manual", i))
	}
	must(t, m.Intent("manual", 3))
	must(t, m.Fail("manual", 3, journal.Permanent)) // […,D2,I3,F3永久]
	if pl := recoverPlan(t, m, "manual"); pl.Kind != saga.PlanManual {
		t.Fatalf("i>p 永久失败应转人工, got %v", pl)
	}

	// 规格例（n=5, p=2, B=1）：[…,I2,F2瞬时] 得前滚 2；
	// 再 I2、F2瞬时 则 c=2>1 得补偿自 1。
	begin(t, m, "budget", 5, 2, 1)
	for i := 0; i <= 2; i++ {
		must(t, m.Intent("budget", i))
		if i < 2 {
			must(t, m.Done("budget", i))
		}
	}
	must(t, m.Fail("budget", 2, journal.Transient)) // [B,I0,D0,I1,D1,I2,F2t]
	pl = recoverPlan(t, m, "budget")
	t.Logf("输入 […,I2,F2瞬时]；输出 %v；依据: c=1≤B=1 未终败，前滚重做", pl)
	if pl != (saga.Plan{Kind: saga.PlanForward, Step: 2}) {
		t.Fatalf("plan=%v", pl)
	}
	must(t, m.Intent("budget", 2))
	must(t, m.Fail("budget", 2, journal.Transient)) // c=2>1 终败
	pl = recoverPlan(t, m, "budget")
	t.Logf("输入 […,I2,F2t,I2,F2t]；输出 %v；依据: c=2>B=1 终败，补偿自 i-1=1", pl)
	if pl != (saga.Plan{Kind: saga.PlanCompensate, Step: 1}) {
		t.Fatalf("plan=%v", pl)
	}

	// 规格例：[…,D2,I3,F3永久] 得转人工（i=3>p=2）。
	begin(t, m, "perm", 5, 2, 1)
	for i := 0; i <= 3; i++ {
		must(t, m.Intent("perm", i))
		if i < 3 {
			must(t, m.Done("perm", i))
		}
	}
	must(t, m.Fail("perm", 3, journal.Permanent))
	if pl := recoverPlan(t, m, "perm"); pl.Kind != saga.PlanManual {
		t.Fatalf("i>p 永久失败应转人工, got %v", pl)
	}

	// 规格例：探测 false 得补偿自 1 并最终已补偿。
	begin(t, m, "probef", 5, 2, 1)
	for i := 0; i <= 2; i++ {
		must(t, m.Intent("probef", i))
		if i < 2 {
			must(t, m.Done("probef", i))
		}
	} // [B,I0,D0,I1,D1,I2]
	if pl := recoverPlan(t, m, "probef"); pl.Kind != saga.PlanProbe {
		t.Fatalf("I(p) 在途崩溃应探测, got %v", pl)
	}
	must(t, m.Probe("probef", false)) // 补写 F(p,永久)
	pl = recoverPlan(t, m, "probef")
	t.Logf("Probe(false) 后输出 %v；依据: F(p,永久) 终败，补偿自 p-1=1", pl)
	if pl != (saga.Plan{Kind: saga.PlanCompensate, Step: 1}) {
		t.Fatalf("plan=%v", pl)
	}
	for j := 1; j >= 0; j-- {
		must(t, m.CompIntent("probef", j))
		must(t, m.CompDone("probef", j))
	}
	if pl := recoverPlan(t, m, "probef"); pl.Kind != saga.PlanCompensated {
		t.Fatalf("补偿到 0 应为已补偿, got %v", pl)
	}

	// B=0 时 i≤p 的首个瞬时失败即终败。
	begin(t, m, "b0", 5, 2, 0)
	must(t, m.Intent("b0", 0))
	must(t, m.Fail("b0", 0, journal.Transient))
	if pl := recoverPlan(t, m, "b0"); pl.Kind != saga.PlanCompensated {
		t.Fatalf("B=0 且 i=0 终败应为已补偿, got %v", pl)
	}

	// 崩溃后重做 Intent(3)：fwd(3) 新增 1、重复 1。
	begin(t, m, "dup", 5, 2, 1)
	for i := 0; i <= 3; i++ {
		must(t, m.Intent("dup", i))
		if i < 3 {
			must(t, m.Done("dup", i))
		}
	} // [B,I0,D0,…,D2,I3]，I3 后崩溃
	if pl := recoverPlan(t, m, "dup"); pl != (saga.Plan{Kind: saga.PlanForward, Step: 3}) {
		t.Fatalf("i>p 结果未知应重做, got %v", pl)
	}
	must(t, m.Intent("dup", 3))
	a, r := ef.Stats(effect.Key{ID: "dup", Step: 3, Phase: effect.Fwd})
	t.Logf("fwd(3): 新增 %d 重复 %d", a, r)
	if a != 1 || r != 1 {
		t.Fatalf("fwd(3) 新增=%d 重复=%d, 期望 1/1", a, r)
	}
}

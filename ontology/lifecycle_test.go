package ontology

import (
	"errors"
	"testing"
)

// 运行期间输入被改写：物化结果一落地即过期，并向更下游传播。
func TestStaleAtBirth(t *testing.T) {
	p, _ := basicGraph(t)
	if err := p.ExternalWrite("S", 1); err != nil {
		t.Fatal(err)
	}

	runA := mustStart(t, p, "A", 1)
	if err := p.ExternalWrite("S", 1); err != nil {
		t.Fatal(err)
	}
	if err := p.Complete(runA, true); err != nil {
		t.Fatal(err)
	}

	plan, err := p.Plan([]PartitionRef{{Asset: "A", Partition: 1}})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan) != 1 || plan[0] != (PartitionRef{Asset: "A", Partition: 1}) {
		t.Fatalf("A#1 should be stale at birth, plan=%v", plan)
	}

	// 修复 A，再物化 B；之后改写 S#1，A#1 与 B#1 应传递过期。
	runA2 := mustStart(t, p, "A", 1)
	if err := p.Complete(runA2, true); err != nil {
		t.Fatal(err)
	}
	runB := mustStart(t, p, "B", 1)
	if err := p.Complete(runB, true); err != nil {
		t.Fatal(err)
	}

	impact, err := p.Impact("S", 1)
	if err != nil {
		t.Fatal(err)
	}
	want := []PartitionRef{{Asset: "A", Partition: 1}, {Asset: "B", Partition: 1}}
	if len(impact) != 2 || impact[0] != want[0] || impact[1] != want[1] {
		t.Fatalf("impact=%v want=%v", impact, want)
	}
}

// 失败完成不改变任何状态。
func TestFailedRunChangesNothing(t *testing.T) {
	p, logBuf := basicGraph(t)
	if err := p.ExternalWrite("S", 2); err != nil {
		t.Fatal(err)
	}
	run := mustStart(t, p, "A", 2)
	if err := p.Complete(run, false); err != nil {
		t.Fatal(err)
	}
	plan, _ := p.Plan([]PartitionRef{{Asset: "A", Partition: 2}})
	if len(plan) != 1 || !plan[0].eq(PartitionRef{Asset: "A", Partition: 2}) {
		t.Fatalf("failed run must leave A#2 missing, plan=%v", plan)
	}
	// 失败后同一分区可以重新开始；运行号标记为已结束。
	_ = mustStart(t, p, "A", 2)
	if err := p.Complete(run, true); reasonOf(err) != ReasonRunFinished {
		t.Fatalf("recompleting finished run: %v (log=%s)", err, logBuf.String())
	}
}

func (r PartitionRef) eq(o PartitionRef) bool {
	return r.Asset == o.Asset && r.Partition == o.Partition
}

// 输入未就绪时给出全部缺失/过期输入。
func TestStartRejectedWhenInputsNotReady(t *testing.T) {
	p, _ := newTestPlanner(t,
		[]AssetSpec{{"S1", 0, 9}, {"S2", 0, 9}, {"D", 0, 9}},
		[]EdgeSpec{{Up: "S1", Down: "D", Lo: 0, Hi: 0}, {Up: "S2", Down: "D", Lo: -1, Hi: 1}})

	if err := p.ExternalWrite("S1", 1); err != nil {
		t.Fatal(err)
	}
	if err := p.ExternalWrite("S2", 0); err != nil {
		t.Fatal(err)
	}
	_, err := p.Start("D", 1)
	if reasonOf(err) != ReasonInputNotReady {
		t.Fatalf("want input_not_ready, got %v", err)
	}
	var e *Error
	errors.As(err, &e)
	xs, _ := e.Details["inputs"].([]NotReadyInput)
	// S1#1 已物化；实际读取 S2#0,S2#1,S2#2，其中 S2#1、S2#2 缺失。
	if len(xs) != 2 {
		t.Fatalf("want 2 missing inputs, got %v", xs)
	}
	for _, x := range xs {
		if x.Asset != "S2" || !x.Missing {
			t.Fatalf("unexpected not-ready input %+v", x)
		}
	}

	// 补齐后物化 D#1。注意：源 S1#1 被外部改写只让 D#1 自身过期，仍可重算；
	// 真正阻塞开始的是“自身过期”的中间层输入（见 TestStaleIntermediateBlocksStart）。
	if err := p.ExternalWrite("S2", 1); err != nil {
		t.Fatal(err)
	}
	if err := p.ExternalWrite("S2", 2); err != nil {
		t.Fatal(err)
	}
	run := mustStart(t, p, "D", 1)
	if err := p.Complete(run, true); err != nil {
		t.Fatal(err)
	}
	if err := p.ExternalWrite("S1", 1); err != nil {
		t.Fatal(err)
	}
	plan, _ := p.Plan([]PartitionRef{{Asset: "D", Partition: 1}})
	if len(plan) != 1 || plan[0] != (PartitionRef{Asset: "D", Partition: 1}) {
		t.Fatalf("version-mismatched source makes D stale and replannable, plan=%v", plan)
	}
	// 过期目标可以直接重算，消费源的最新版本。
	run2 := mustStart(t, p, "D", 1)
	if err := p.Complete(run2, true); err != nil {
		t.Fatal(err)
	}
	if plan, _ := p.Plan([]PartitionRef{{Asset: "D", Partition: 1}}); len(plan) != 0 {
		t.Fatalf("D#1 must be fresh after recompute, plan=%v", plan)
	}
}

// 中间输入自身过期时，下游不可开始，并在未就绪列表中列出它。
func TestStaleIntermediateBlocksStart(t *testing.T) {
	// S -> M -> D；M#1 物化后 S#1 被改写使 M#1 过期。
	p, _ := newTestPlanner(t,
		[]AssetSpec{{"S", 0, 9}, {"M", 0, 9}, {"D", 0, 9}},
		[]EdgeSpec{{"S", "M", 0, 0}, {"M", "D", 0, 0}})

	if err := p.ExternalWrite("S", 1); err != nil {
		t.Fatal(err)
	}
	mRun := mustStart(t, p, "M", 1)
	if err := p.Complete(mRun, true); err != nil {
		t.Fatal(err)
	}
	if err := p.ExternalWrite("S", 1); err != nil {
		t.Fatal(err)
	}

	_, err := p.Start("D", 1)
	if reasonOf(err) != ReasonInputNotReady {
		t.Fatalf("stale intermediate must block D start, got %v", err)
	}
	var e *Error
	errors.As(err, &e)
	xs, _ := e.Details["inputs"].([]NotReadyInput)
	if len(xs) != 1 || xs[0].Asset != "M" || xs[0].Partition != 1 || xs[0].Missing {
		t.Fatalf("want one stale M#1, got %v", xs)
	}

	// M 重算变新鲜后，D 可开始。
	mRun2 := mustStart(t, p, "M", 1)
	if err := p.Complete(mRun2, true); err != nil {
		t.Fatal(err)
	}
	dRun := mustStart(t, p, "D", 1)
	if err := p.Complete(dRun, true); err != nil {
		t.Fatal(err)
	}
}

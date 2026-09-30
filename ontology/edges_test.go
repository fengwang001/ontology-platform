package ontology

import "testing"

// 负偏移与跨度大于一时，影响面沿真实读取方向传播，范围外分区被裁剪。
func TestImpactDirectionWithNegativeAndWideOffsets(t *testing.T) {
	p, _ := newTestPlanner(t,
		[]AssetSpec{{"S", 0, 9}, {"A", 0, 9}},
		[]EdgeSpec{{Up: "S", Down: "A", Lo: -1, Hi: 1}})

	for _, sp := range []int{0, 1, 2} {
		if err := p.ExternalWrite("S", sp); err != nil {
			t.Fatal(err)
		}
	}
	run := mustStart(t, p, "A", 1)
	if err := p.Complete(run, true); err != nil {
		t.Fatal(err)
	}

	imp, err := p.Impact("S", 0)
	if err != nil || len(imp) != 1 || imp[0] != (PartitionRef{Asset: "A", Partition: 1}) {
		t.Fatalf("S#0 impact=%v err=%v", imp, err)
	}
	imp, _ = p.Impact("S", 2)
	if len(imp) != 1 || imp[0] != (PartitionRef{Asset: "A", Partition: 1}) {
		t.Fatalf("S#2 impact=%v want A#1", imp)
	}
	imp, _ = p.Impact("S", 5)
	if len(imp) != 0 {
		t.Fatalf("S#5 impact=%v want empty", imp)
	}

	// A#0 读取 [-1..1] 与 [0..9] 的交集 = S#0,S#1。
	if err := p.ExternalWrite("S", 1); err != nil {
		t.Fatal(err)
	}
	run0 := mustStart(t, p, "A", 0)
	if err := p.Complete(run0, true); err != nil {
		t.Fatal(err)
	}
	imp, _ = p.Impact("S", 1)
	found := map[PartitionRef]bool{}
	for _, r := range imp {
		found[r] = true
	}
	if !found[PartitionRef{Asset: "A", Partition: 0}] ||
		!found[PartitionRef{Asset: "A", Partition: 1}] {
		t.Fatalf("S#1 impact=%v want A#0 and A#1", imp)
	}
}

// 规划不含新鲜分区；结果按 (层深, 资产名, 分区号) 升序。
func TestPlanExcludesFreshAndOrdersByDepth(t *testing.T) {
	p, _ := newTestPlanner(t,
		[]AssetSpec{{"S", 0, 9}, {"Z", 0, 9}, {"M", 0, 9}, {"N", 0, 9}},
		[]EdgeSpec{
			{Up: "S", Down: "M", Lo: 0, Hi: 0},
			{Up: "S", Down: "N", Lo: 0, Hi: 0},
			{Up: "M", Down: "Z", Lo: 0, Hi: 0},
			{Up: "N", Down: "Z", Lo: 0, Hi: 0},
		})

	if err := p.ExternalWrite("S", 2); err != nil {
		t.Fatal(err)
	}
	for _, a := range []string{"M", "N", "Z"} {
		r := mustStart(t, p, a, 2)
		if err := p.Complete(r, true); err != nil {
			t.Fatal(err)
		}
	}
	if plan, _ := p.Plan([]PartitionRef{{"Z", 2}}); len(plan) != 0 {
		t.Fatalf("fresh pipeline plan must be empty, got %v", plan)
	}

	if err := p.ExternalWrite("S", 2); err != nil {
		t.Fatal(err)
	}
	plan, _ := p.Plan([]PartitionRef{{"Z", 2}})
	// S#2 是新鲜源，不进结果；只有传递过期的 M#2、N#2、Z#2 需要回填。
	want := []PartitionRef{{"M", 2}, {"N", 2}, {"Z", 2}}
	if len(plan) != len(want) {
		t.Fatalf("plan=%v", plan)
	}
	for i := range want {
		if plan[i] != want[i] {
			t.Fatalf("order mismatch: plan=%v want=%v", plan, want)
		}
	}

	// 全新分区：S#3 缺失 -> M#3,N#3,Z#3 全部缺失。
	plan, _ = p.Plan([]PartitionRef{{"Z", 3}})
	if len(plan) != 4 {
		t.Fatalf("missing closure plan=%v", plan)
	}
}

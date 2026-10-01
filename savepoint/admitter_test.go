package savepoint

import (
	"errors"
	"reflect"
	"sync"
	"testing"
)

func strPtr(s string) *string { return &s }
func intPtr(n int) *int       { return &n }

// baseSavepoint 含两个算子：source（value:int、list:long）与 extra（map:string）。
func baseSavepoint() Savepoint {
	return Savepoint{
		ID: "sp1",
		Operators: []SavepointOperator{
			{
				ID:             "source",
				MaxParallelism: 64,
				States: []StateItem{
					{Name: "count", Kind: KindValue, Type: TypeInt},
					{Name: "events", Kind: KindList, Type: TypeLong},
				},
			},
			{
				ID:             "extra",
				MaxParallelism: 32,
				States: []StateItem{
					{Name: "index", Kind: KindMap, Type: TypeString},
				},
			},
		},
	}
}

func mustRegister(t *testing.T, a *Admitter, sp Savepoint) {
	t.Helper()
	if err := a.RegisterSavepoint(sp); err != nil {
		t.Fatalf("register savepoint: %v", err)
	}
}

func logPlan(t *testing.T, plan *Plan) {
	t.Helper()
	for _, op := range plan.Operators {
		t.Logf("  plan op=%s action=%s source=%q maxPar=%d items=%v",
			op.OperatorID, op.Action, op.Source, op.MaxParallelism, op.ItemActions)
	}
	t.Logf("  dropped operators=%v dropped items=%v", plan.DroppedOperators, plan.DroppedStateItems)
}

func mustReject(t *testing.T, err error) *RejectError {
	t.Helper()
	var re *RejectError
	if !errors.As(err, &re) {
		t.Fatalf("expected RejectError, got %v", err)
	}
	t.Logf("  rejected reason=%s objects=%v", re.Reason, re.Objects)
	return re
}

// 来源映射改名后承接旧状态：renamed 通过 SourceID 承接 source。
func TestRestoreRenameViaSourceMapping(t *testing.T) {
	a := NewAdmitter()
	mustRegister(t, a, baseSavepoint())

	graph := JobGraph{Operators: []JobOperator{
		{
			ID:          "renamed",
			Parallelism: 8,
			SourceID:    strPtr("source"),
			States: []StateItem{
				{Name: "count", Kind: KindValue, Type: TypeInt},
				{Name: "events", Kind: KindList, Type: TypeLong},
			},
		},
		{
			ID:          "extra",
			Parallelism: 4,
			States: []StateItem{
				{Name: "index", Kind: KindMap, Type: TypeString},
			},
		},
	}}
	t.Logf("input: job=job1 sp=sp1 allowDiscard=false graph=%+v", graph)
	plan, err := a.Restore("job1", "sp1", false, graph)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	logPlan(t, plan)

	if len(plan.Operators) != 2 {
		t.Fatalf("want 2 operator plans, got %d", len(plan.Operators))
	}
	renamed := plan.Operators[1]
	if renamed.OperatorID != "renamed" || renamed.Source != "source" {
		t.Fatalf("renamed op should claim source, got %+v", renamed)
	}
	if renamed.Action != ActionRestoreDirect {
		t.Fatalf("want direct restore, got %s", renamed.Action)
	}
	if renamed.MaxParallelism != 64 {
		t.Fatalf("max parallelism should inherit 64, got %d", renamed.MaxParallelism)
	}
	if len(plan.DroppedOperators) != 0 || len(plan.DroppedStateItems) != 0 {
		t.Fatalf("nothing should be dropped: %+v", plan)
	}
}

// 旧标识同时被同名算子与来源映射承接：source 被同名算子和 renamed 同时承接。
func TestDuplicateClaimConflict(t *testing.T) {
	a := NewAdmitter()
	mustRegister(t, a, baseSavepoint())

	graph := JobGraph{Operators: []JobOperator{
		{ID: "source", Parallelism: 4},
		{ID: "renamed", Parallelism: 4, SourceID: strPtr("source")},
		{ID: "extra", Parallelism: 4, States: []StateItem{
			{Name: "index", Kind: KindMap, Type: TypeString},
		}},
	}}
	t.Logf("input: job=job1 sp=sp1 allowDiscard=false graph=%+v", graph)
	_, err := a.Restore("job1", "sp1", false, graph)
	re := mustReject(t, err)
	if re.Reason != ReasonDuplicateClaim {
		t.Fatalf("want %s, got %s", ReasonDuplicateClaim, re.Reason)
	}
	if !reflect.DeepEqual(re.Objects, []string{"source"}) {
		t.Fatalf("want [source], got %v", re.Objects)
	}
	// 被拒绝不得占用作业标识。
	if _, ok := a.jobs["job1"]; ok {
		t.Fatal("rejected restore must not occupy job id")
	}
}

// int 变宽为 long 兼容（变宽迁移），long 变窄为 int 拒绝。
func TestWidenAndNarrow(t *testing.T) {
	a := NewAdmitter()
	mustRegister(t, a, baseSavepoint())

	widenGraph := JobGraph{Operators: []JobOperator{
		{
			ID:          "source",
			Parallelism: 4,
			States: []StateItem{
				{Name: "count", Kind: KindValue, Type: TypeLong},
				{Name: "events", Kind: KindList, Type: TypeLong},
			},
		},
		{ID: "extra", Parallelism: 4, States: []StateItem{
			{Name: "index", Kind: KindMap, Type: TypeString},
		}},
	}}
	t.Logf("input: job=widen sp=sp1 allowDiscard=false, count int->long")
	plan, err := a.Restore("widen", "sp1", false, widenGraph)
	if err != nil {
		t.Fatalf("widen restore: %v", err)
	}
	logPlan(t, plan)
	src := plan.Operators[1]
	if src.Action != ActionRestoreWiden {
		t.Fatalf("want widen action, got %s", src.Action)
	}
	if src.ItemActions["count"] != ActionRestoreWiden || src.ItemActions["events"] != ActionRestoreDirect {
		t.Fatalf("unexpected item actions: %v", src.ItemActions)
	}
	if err := a.Stop("widen"); err != nil {
		t.Fatalf("stop: %v", err)
	}

	narrowGraph := JobGraph{Operators: []JobOperator{
		{
			ID:          "source",
			Parallelism: 4,
			States: []StateItem{
				{Name: "count", Kind: KindValue, Type: TypeInt},
				{Name: "events", Kind: KindList, Type: TypeInt},
			},
		},
		{ID: "extra", Parallelism: 4, States: []StateItem{
			{Name: "index", Kind: KindMap, Type: TypeString},
		}},
	}}
	t.Logf("input: job=narrow sp=sp1 allowDiscard=false, events long->int")
	_, err = a.Restore("narrow", "sp1", false, narrowGraph)
	re := mustReject(t, err)
	if re.Reason != ReasonIncompatibleStateItems {
		t.Fatalf("want %s, got %s", ReasonIncompatibleStateItems, re.Reason)
	}
	if !reflect.DeepEqual(re.Objects, []string{"source.events"}) {
		t.Fatalf("want [source.events], got %v", re.Objects)
	}
}

// 允许丢弃开关：未承接算子与缺失状态项两类缺失，关闭时拒绝、开启时恢复并记入丢弃清单。
func TestAllowDiscardSwitch(t *testing.T) {
	newAdmitter := func() *Admitter {
		a := NewAdmitter()
		mustRegister(t, a, baseSavepoint())
		return a
	}
	// source 缺失状态项 events，extra 完全无人承接。
	graph := JobGraph{Operators: []JobOperator{
		{
			ID:          "source",
			Parallelism: 4,
			States: []StateItem{
				{Name: "count", Kind: KindValue, Type: TypeInt},
			},
		},
	}}

	a := newAdmitter()
	t.Logf("input: allowDiscard=false, missing item source.events, unclaimed operator extra")
	_, err := a.Restore("job1", "sp1", false, graph)
	re := mustReject(t, err)
	// 未承接算子（第 6 类）先于缺失状态项（第 8 类）报告。
	if re.Reason != ReasonUnclaimedOperators {
		t.Fatalf("want %s, got %s", ReasonUnclaimedOperators, re.Reason)
	}
	if !reflect.DeepEqual(re.Objects, []string{"extra"}) {
		t.Fatalf("want [extra], got %v", re.Objects)
	}

	a = newAdmitter()
	t.Logf("input: allowDiscard=true, same graph")
	plan, err := a.Restore("job1", "sp1", true, graph)
	if err != nil {
		t.Fatalf("restore with discard: %v", err)
	}
	logPlan(t, plan)
	if !reflect.DeepEqual(plan.DroppedOperators, []string{"extra"}) {
		t.Fatalf("dropped operators: %v", plan.DroppedOperators)
	}
	if !reflect.DeepEqual(plan.DroppedStateItems, []string{"source.events"}) {
		t.Fatalf("dropped items: %v", plan.DroppedStateItems)
	}

	// 仅缺失状态项时，关闭开关报 missing-state-items。
	a = newAdmitter()
	full := JobGraph{Operators: []JobOperator{
		{ID: "source", Parallelism: 4, States: []StateItem{
			{Name: "count", Kind: KindValue, Type: TypeInt},
		}},
		{ID: "extra", Parallelism: 4, States: []StateItem{
			{Name: "index", Kind: KindMap, Type: TypeString},
		}},
	}}
	t.Logf("input: allowDiscard=false, only missing item source.events")
	_, err = a.Restore("job1", "sp1", false, full)
	re = mustReject(t, err)
	if re.Reason != ReasonMissingStateItems {
		t.Fatalf("want %s, got %s", ReasonMissingStateItems, re.Reason)
	}
	if !reflect.DeepEqual(re.Objects, []string{"source.events"}) {
		t.Fatalf("want [source.events], got %v", re.Objects)
	}
}

// 多因并存只报第一类：保存点不存在 > 作业标识占用 > 图非法 > 重复承接 > 最大并行度不一致。
func TestRejectOrdering(t *testing.T) {
	a := NewAdmitter()
	mustRegister(t, a, baseSavepoint())

	// 保存点不存在，即使作业图同时非法。
	bad := JobGraph{Operators: []JobOperator{{ID: "", Parallelism: -1}}}
	_, err := a.Restore("job1", "nope", false, bad)
	if re := mustReject(t, err); re.Reason != ReasonSavepointNotFound {
		t.Fatalf("want %s, got %s", ReasonSavepointNotFound, re.Reason)
	}

	// 先成功恢复占用 job1。
	okGraph := JobGraph{Operators: []JobOperator{
		{ID: "source", Parallelism: 4, States: []StateItem{
			{Name: "count", Kind: KindValue, Type: TypeInt},
			{Name: "events", Kind: KindList, Type: TypeLong},
		}},
		{ID: "extra", Parallelism: 4, States: []StateItem{
			{Name: "index", Kind: KindMap, Type: TypeString},
		}},
	}}
	if _, err := a.Restore("job1", "sp1", false, okGraph); err != nil {
		t.Fatalf("restore: %v", err)
	}

	// 作业标识占用优先于图非法。
	_, err = a.Restore("job1", "sp1", false, bad)
	if re := mustReject(t, err); re.Reason != ReasonJobIDInUse {
		t.Fatalf("want %s, got %s", ReasonJobIDInUse, re.Reason)
	}

	// 图非法（多个问题同类全部升序列出）优先于重复承接。
	dup := JobGraph{Operators: []JobOperator{
		{ID: "source", Parallelism: 4},
		{ID: "source", Parallelism: 4},
		{ID: "ghost", Parallelism: 4, SourceID: strPtr("missing")},
	}}
	_, err = a.Restore("job2", "sp1", false, dup)
	re := mustReject(t, err)
	if re.Reason != ReasonInvalidGraph {
		t.Fatalf("want %s, got %s", ReasonInvalidGraph, re.Reason)
	}
	if len(re.Objects) != 2 || re.Objects[0] > re.Objects[1] {
		t.Fatalf("invalid objects should be sorted, got %v", re.Objects)
	}

	// 重复承接优先于最大并行度不一致。
	conflict := JobGraph{Operators: []JobOperator{
		{ID: "source", Parallelism: 4, MaxParallelism: intPtr(16)},
		{ID: "renamed", Parallelism: 4, SourceID: strPtr("source")},
	}}
	_, err = a.Restore("job2", "sp1", false, conflict)
	if re := mustReject(t, err); re.Reason != ReasonDuplicateClaim {
		t.Fatalf("want %s, got %s", ReasonDuplicateClaim, re.Reason)
	}

	// 最大并行度不一致优先于未承接算子。
	mismatch := JobGraph{Operators: []JobOperator{
		{ID: "source", Parallelism: 4, MaxParallelism: intPtr(16), States: []StateItem{
			{Name: "count", Kind: KindValue, Type: TypeInt},
			{Name: "events", Kind: KindList, Type: TypeLong},
		}},
	}}
	_, err = a.Restore("job2", "sp1", false, mismatch)
	re = mustReject(t, err)
	if re.Reason != ReasonMaxParallelismMismatch {
		t.Fatalf("want %s, got %s", ReasonMaxParallelismMismatch, re.Reason)
	}
	t.Logf("ordering verified: not-found > id-in-use > invalid-graph > duplicate-claim > maxpar-mismatch > unclaimed")
}

// 引用中的保存点不能删除，作业停止后才可删。
func TestDeleteReferencedSavepoint(t *testing.T) {
	a := NewAdmitter()
	mustRegister(t, a, baseSavepoint())

	graph := JobGraph{Operators: []JobOperator{
		{ID: "source", Parallelism: 4, States: []StateItem{
			{Name: "count", Kind: KindValue, Type: TypeInt},
			{Name: "events", Kind: KindList, Type: TypeLong},
		}},
		{ID: "extra", Parallelism: 4, States: []StateItem{
			{Name: "index", Kind: KindMap, Type: TypeString},
		}},
	}}
	if _, err := a.Restore("job1", "sp1", false, graph); err != nil {
		t.Fatalf("restore: %v", err)
	}
	t.Logf("input: delete sp1 while referenced by live job1")
	if err := a.DeleteSavepoint("sp1"); err == nil {
		t.Fatal("delete referenced savepoint should fail")
	} else {
		t.Logf("  delete rejected: %v", err)
	}
	if err := a.Stop("job1"); err != nil {
		t.Fatalf("stop: %v", err)
	}
	t.Logf("input: delete sp1 after job1 stopped")
	if err := a.DeleteSavepoint("sp1"); err != nil {
		t.Fatalf("delete after stop: %v", err)
	}
	// 删除后恢复应报保存点不存在。
	_, err := a.Restore("job2", "sp1", false, graph)
	if re := mustReject(t, err); re.Reason != ReasonSavepointNotFound {
		t.Fatalf("want %s, got %s", ReasonSavepointNotFound, re.Reason)
	}
}

// 并发：同一作业标识并发恢复只成功一个；被引用的保存点不会被并发删除。
func TestConcurrency(t *testing.T) {
	a := NewAdmitter()
	mustRegister(t, a, baseSavepoint())

	graph := JobGraph{Operators: []JobOperator{
		{ID: "source", Parallelism: 4, States: []StateItem{
			{Name: "count", Kind: KindValue, Type: TypeInt},
			{Name: "events", Kind: KindList, Type: TypeLong},
		}},
		{ID: "extra", Parallelism: 4, States: []StateItem{
			{Name: "index", Kind: KindMap, Type: TypeString},
		}},
	}}

	const n = 32
	var wg sync.WaitGroup
	var mu sync.Mutex
	successes := 0
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := a.Restore("job1", "sp1", false, graph); err == nil {
				mu.Lock()
				successes++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	t.Logf("concurrent restore same job id: successes=%d (want 1)", successes)
	if successes != 1 {
		t.Fatalf("want exactly 1 success, got %d", successes)
	}

	// 并发删除被引用的保存点：全部失败。
	var delOK int32
	var delMu sync.Mutex
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := a.DeleteSavepoint("sp1"); err == nil {
				delMu.Lock()
				delOK++
				delMu.Unlock()
			}
		}()
	}
	wg.Wait()
	t.Logf("concurrent delete referenced savepoint: successes=%d (want 0)", delOK)
	if delOK != 0 {
		t.Fatalf("referenced savepoint must not be deleted, got %d successes", delOK)
	}
	if err := a.Stop("job1"); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if err := a.DeleteSavepoint("sp1"); err != nil {
		t.Fatalf("delete after stop: %v", err)
	}
}

// 相同输入得到完全相同的计划与清单。
func TestDeterministicPlan(t *testing.T) {
	build := func() *Admitter {
		a := NewAdmitter()
		mustRegister(t, a, baseSavepoint())
		return a
	}
	graph := JobGraph{Operators: []JobOperator{
		{ID: "renamed", Parallelism: 8, SourceID: strPtr("source"), States: []StateItem{
			{Name: "count", Kind: KindValue, Type: TypeLong},
			{Name: "fresh", Kind: KindMap, Type: TypeString},
		}},
		{ID: "brand-new", Parallelism: 2},
	}}
	var first *Plan
	for i := 0; i < 5; i++ {
		a := build()
		plan, err := a.Restore("job1", "sp1", true, graph)
		if err != nil {
			t.Fatalf("restore: %v", err)
		}
		if first == nil {
			first = plan
			logPlan(t, plan)
			continue
		}
		if !reflect.DeepEqual(first, plan) {
			t.Fatalf("plan not deterministic:\nfirst=%+v\ngot=%+v", first, plan)
		}
	}
	// 新增算子空启动、缺省最大并行度 128；新增状态项空启动。
	var brandNew, renamed OperatorPlan
	for _, op := range first.Operators {
		switch op.OperatorID {
		case "brand-new":
			brandNew = op
		case "renamed":
			renamed = op
		}
	}
	if brandNew.Action != ActionStartEmpty || brandNew.MaxParallelism != DefaultMaxParallelism {
		t.Fatalf("new operator should start empty with maxPar 128, got %+v", brandNew)
	}
	if renamed.Action != ActionRestoreWiden {
		t.Fatalf("want widen, got %s", renamed.Action)
	}
	if renamed.ItemActions["fresh"] != ActionStartEmpty {
		t.Fatalf("new state item should start empty, got %v", renamed.ItemActions)
	}
	if !reflect.DeepEqual(first.DroppedOperators, []string{"extra"}) {
		t.Fatalf("dropped operators: %v", first.DroppedOperators)
	}
	if !reflect.DeepEqual(first.DroppedStateItems, []string{"renamed.events"}) {
		t.Fatalf("dropped items: %v", first.DroppedStateItems)
	}
}

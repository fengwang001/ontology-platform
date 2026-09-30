package savepoint

import (
	"log"
	"reflect"
	"strings"
	"sync"
	"testing"
)

type testWriter struct{ t *testing.T }

func (w testWriter) Write(p []byte) (int, error) {
	w.t.Log(strings.TrimRight(string(p), "\n"))
	return len(p), nil
}

func newGatekeeper(t *testing.T) *Gatekeeper {
	t.Helper()
	return NewGatekeeper(log.New(testWriter{t}, "", 0))
}

func strPtr(s string) *string { return &s }

func intPtr(n int) *int { return &n }

func mustReject(t *testing.T, rej *Rejection, reason Reason, objects ...string) {
	t.Helper()
	if rej == nil {
		t.Fatalf("expected rejection %s, got success", reason)
	}
	if rej.Reason != reason {
		t.Fatalf("expected reason %s, got %s (objects=%v)", reason, rej.Reason, rej.Objects)
	}
	if !reflect.DeepEqual(rej.Objects, objects) {
		t.Fatalf("expected objects %v, got %v", objects, rej.Objects)
	}
}

func mustAccept(t *testing.T, plan *RestorePlan, rej *Rejection) *RestorePlan {
	t.Helper()
	if rej != nil {
		t.Fatalf("expected success, got rejection %s objects=%v", rej.Reason, rej.Objects)
	}
	return plan
}

func restoreOK(t *testing.T, g *Gatekeeper, jobID, spID string, allowDiscard bool, graph JobGraph) *RestorePlan {
	t.Helper()
	plan, rej := g.Restore(jobID, spID, allowDiscard, graph)
	return mustAccept(t, plan, rej)
}

// 同名承接：直接恢复。
func TestRestoreBySameName(t *testing.T) {
	g := newGatekeeper(t)
	g.RegisterSavepoint(Savepoint{
		ID: "sp1",
		Operators: []SavepointOperator{
			{ID: "map", MaxParallelism: 16, Items: []StateItem{{Name: "cnt", Kind: KindValue, Value: TypeLong}}},
		},
	})
	plan := restoreOK(t, g, "job1", "sp1", false, JobGraph{Operators: []JobOperator{
		{ID: "map", Parallelism: 4, Items: []StateItem{{Name: "cnt", Kind: KindValue, Value: TypeLong}}},
	}})
	want := []OperatorPlan{{OperatorID: "map", Action: ActionRestoreDirect}}
	if !reflect.DeepEqual(plan.Operators, want) {
		t.Fatalf("plan.Operators = %+v, want %+v", plan.Operators, want)
	}
	if len(plan.DroppedOperators) != 0 || len(plan.DroppedStateItems) != 0 {
		t.Fatalf("unexpected drops: %+v", plan)
	}
}

// 来源映射改名后承接旧状态：新算子通过来源标识承接保存点中不同名的算子。
func TestRestoreBySourceMappingRename(t *testing.T) {
	g := newGatekeeper(t)
	g.RegisterSavepoint(Savepoint{
		ID: "sp1",
		Operators: []SavepointOperator{
			{ID: "old-name", MaxParallelism: 8, Items: []StateItem{{Name: "buf", Kind: KindList, Value: TypeString}}},
		},
	})
	plan := restoreOK(t, g, "job1", "sp1", false, JobGraph{Operators: []JobOperator{
		{ID: "new-name", Parallelism: 2, SourceID: strPtr("old-name"),
			Items: []StateItem{{Name: "buf", Kind: KindList, Value: TypeString}}},
	}})
	want := []OperatorPlan{{OperatorID: "new-name", Action: ActionRestoreDirect}}
	if !reflect.DeepEqual(plan.Operators, want) {
		t.Fatalf("plan.Operators = %+v, want %+v", plan.Operators, want)
	}
}

// 旧标识同时被同名算子与来源映射承接：同一保存点算子被多个新算子承接，拒绝。
func TestDuplicateClaimByNameAndSource(t *testing.T) {
	g := newGatekeeper(t)
	g.RegisterSavepoint(Savepoint{
		ID: "sp1",
		Operators: []SavepointOperator{
			{ID: "a", MaxParallelism: 8, Items: []StateItem{{Name: "s", Kind: KindValue, Value: TypeInt}}},
		},
	})
	_, rej := g.Restore("job1", "sp1", false, JobGraph{Operators: []JobOperator{
		{ID: "a", Parallelism: 1, Items: []StateItem{{Name: "s", Kind: KindValue, Value: TypeInt}}},
		{ID: "b", Parallelism: 1, SourceID: strPtr("a"), Items: []StateItem{{Name: "s", Kind: KindValue, Value: TypeInt}}},
	}})
	mustReject(t, rej, ReasonDuplicateClaim, "a")
}

// int 变宽为 long：兼容，动作是变宽迁移后恢复。
func TestWidenIntToLong(t *testing.T) {
	g := newGatekeeper(t)
	g.RegisterSavepoint(Savepoint{
		ID: "sp1",
		Operators: []SavepointOperator{
			{ID: "op", MaxParallelism: 4, Items: []StateItem{
				{Name: "a", Kind: KindValue, Value: TypeInt},
				{Name: "b", Kind: KindMap, Value: TypeInt},
				{Name: "c", Kind: KindList, Value: TypeString},
			}},
		},
	})
	plan := restoreOK(t, g, "job1", "sp1", false, JobGraph{Operators: []JobOperator{
		{ID: "op", Parallelism: 1, Items: []StateItem{
			{Name: "a", Kind: KindValue, Value: TypeLong},
			{Name: "b", Kind: KindMap, Value: TypeLong},
			{Name: "c", Kind: KindList, Value: TypeString},
		}},
	}})
	want := []OperatorPlan{{OperatorID: "op", Action: ActionRestoreWiden, WidenedItems: []string{"a", "b"}}}
	if !reflect.DeepEqual(plan.Operators, want) {
		t.Fatalf("plan.Operators = %+v, want %+v", plan.Operators, want)
	}
}

// long 变窄为 int 与种类变化：不兼容，拒绝且同类对象升序列出。
func TestNarrowAndKindChangeIncompatible(t *testing.T) {
	g := newGatekeeper(t)
	g.RegisterSavepoint(Savepoint{
		ID: "sp1",
		Operators: []SavepointOperator{
			{ID: "op", MaxParallelism: 4, Items: []StateItem{
				{Name: "narrow", Kind: KindValue, Value: TypeLong},
				{Name: "rekind", Kind: KindList, Value: TypeString},
				{Name: "tostr", Kind: KindValue, Value: TypeInt},
			}},
		},
	})
	_, rej := g.Restore("job1", "sp1", false, JobGraph{Operators: []JobOperator{
		{ID: "op", Parallelism: 1, Items: []StateItem{
			{Name: "narrow", Kind: KindValue, Value: TypeInt},
			{Name: "rekind", Kind: KindMap, Value: TypeString},
			{Name: "tostr", Kind: KindValue, Value: TypeString},
		}},
	}})
	mustReject(t, rej, ReasonIncompatibleStateItems, "op.narrow", "op.rekind", "op.tostr")
}

// 允许丢弃开关：未承接算子与缺失状态项两类缺失，只在允许丢弃时可恢复并记入丢弃清单。
func TestAllowDiscardForBothMissingKinds(t *testing.T) {
	newGate := func() *Gatekeeper {
		g := newGatekeeper(t)
		g.RegisterSavepoint(Savepoint{
			ID: "sp1",
			Operators: []SavepointOperator{
				{ID: "kept", MaxParallelism: 4, Items: []StateItem{
					{Name: "alive", Kind: KindValue, Value: TypeInt},
					{Name: "gone", Kind: KindValue, Value: TypeLong},
				}},
				{ID: "orphan", MaxParallelism: 2, Items: []StateItem{
					{Name: "x", Kind: KindValue, Value: TypeInt},
				}},
			},
		})
		return g
	}
	graph := JobGraph{Operators: []JobOperator{
		{ID: "kept", Parallelism: 1, Items: []StateItem{{Name: "alive", Kind: KindValue, Value: TypeInt}}},
	}}

	// 不允许丢弃：先报未承接算子（次序靠前）。
	g := newGate()
	_, rej := g.Restore("job1", "sp1", false, graph)
	mustReject(t, rej, ReasonUnclaimedOperators, "orphan")

	// 不允许丢弃：承接了全部算子但缺失状态项，报缺失状态项。
	g = newGate()
	_, rej = g.Restore("job1", "sp1", false, JobGraph{Operators: []JobOperator{
		{ID: "kept", Parallelism: 1, Items: []StateItem{{Name: "alive", Kind: KindValue, Value: TypeInt}}},
		{ID: "orphan", Parallelism: 1, Items: []StateItem{{Name: "x", Kind: KindValue, Value: TypeInt}}},
	}})
	mustReject(t, rej, ReasonMissingStateItems, "kept.gone")

	// 允许丢弃：两类缺失都记入丢弃清单，恢复成功。
	g = newGate()
	plan := restoreOK(t, g, "job1", "sp1", true, graph)
	if !reflect.DeepEqual(plan.DroppedOperators, []string{"orphan"}) {
		t.Fatalf("DroppedOperators = %v", plan.DroppedOperators)
	}
	if !reflect.DeepEqual(plan.DroppedStateItems, []string{"kept.gone"}) {
		t.Fatalf("DroppedStateItems = %v", plan.DroppedStateItems)
	}
}

// 多因并存时只报次序最靠前的一类。
func TestRejectionOrdering(t *testing.T) {
	newGate := func() *Gatekeeper {
		g := newGatekeeper(t)
		g.RegisterSavepoint(Savepoint{
			ID: "sp1",
			Operators: []SavepointOperator{
				{ID: "a", MaxParallelism: 4, Items: []StateItem{{Name: "s", Kind: KindValue, Value: TypeLong}}},
			},
		})
		return g
	}

	// 保存点不存在优先于作业标识占用。
	g := newGate()
	restoreOK(t, g, "job1", "sp1", false, JobGraph{Operators: []JobOperator{
		{ID: "a", Parallelism: 1, Items: []StateItem{{Name: "s", Kind: KindValue, Value: TypeLong}}},
	}})
	_, rej := g.Restore("job1", "no-such-sp", false, JobGraph{})
	mustReject(t, rej, ReasonSavepointNotFound, "no-such-sp")

	// 作业标识占用优先于作业图非法。
	_, rej = g.Restore("job1", "sp1", false, JobGraph{Operators: []JobOperator{{ID: "", Parallelism: 0}}})
	mustReject(t, rej, ReasonJobIDOccupied, "job1")

	// 作业图非法优先于重复承接与最大并行度不一致。
	g = newGate()
	_, rej = g.Restore("job2", "sp1", false, JobGraph{Operators: []JobOperator{
		{ID: "a", Parallelism: 1, MaxParallelism: intPtr(9)},
		{ID: "b", Parallelism: 1, SourceID: strPtr("a")},
		{ID: "b", Parallelism: 1, SourceID: strPtr("a")},
	}})
	mustReject(t, rej, ReasonInvalidJobGraph, "b")

	// 重复承接优先于最大并行度不一致。
	g = newGate()
	_, rej = g.Restore("job2", "sp1", false, JobGraph{Operators: []JobOperator{
		{ID: "a", Parallelism: 1, MaxParallelism: intPtr(9)},
		{ID: "b", Parallelism: 1, SourceID: strPtr("a")},
	}})
	mustReject(t, rej, ReasonDuplicateClaim, "a")

	// 最大并行度不一致优先于未承接算子。
	g = newGate()
	g.RegisterSavepoint(Savepoint{ID: "sp2", Operators: []SavepointOperator{
		{ID: "a", MaxParallelism: 4}, {ID: "orphan", MaxParallelism: 1},
	}})
	_, rej = g.Restore("job2", "sp2", false, JobGraph{Operators: []JobOperator{
		{ID: "a", Parallelism: 1, MaxParallelism: intPtr(9)},
	}})
	mustReject(t, rej, ReasonMaxParallelismMismatch, "a")

	// 不兼容状态项优先于缺失状态项。
	g = newGate()
	_, rej = g.Restore("job2", "sp1", false, JobGraph{Operators: []JobOperator{
		{ID: "a", Parallelism: 1, Items: []StateItem{{Name: "s", Kind: KindValue, Value: TypeInt}}},
	}})
	mustReject(t, rej, ReasonIncompatibleStateItems, "a.s")
}

// 作业图非法的各种形态：空标识、重复标识、p 非正、p 超最大并行度、来源不存在。
func TestInvalidJobGraph(t *testing.T) {
	newGate := func() *Gatekeeper {
		g := newGatekeeper(t)
		g.RegisterSavepoint(Savepoint{ID: "sp1", Operators: []SavepointOperator{
			{ID: "a", MaxParallelism: 4},
		}})
		return g
	}

	// 空标识与重复标识，同类对象升序。
	g := newGate()
	_, rej := g.Restore("j", "sp1", false, JobGraph{Operators: []JobOperator{
		{ID: "z", Parallelism: 1},
		{ID: "z", Parallelism: 1},
		{ID: "", Parallelism: 1},
	}})
	mustReject(t, rej, ReasonInvalidJobGraph, "", "z")

	// p 非正。
	g = newGate()
	_, rej = g.Restore("j", "sp1", false, JobGraph{Operators: []JobOperator{{ID: "a", Parallelism: 0}}})
	mustReject(t, rej, ReasonInvalidJobGraph, "a")

	// p 大于承接保存点的最大并行度（缺省继承 m=4）。
	g = newGate()
	_, rej = g.Restore("j", "sp1", false, JobGraph{Operators: []JobOperator{{ID: "a", Parallelism: 5}}})
	mustReject(t, rej, ReasonInvalidJobGraph, "a")

	// 新增算子缺省最大并行度 128：p=128 合法，p=129 非法。
	g = newGate()
	restoreOK(t, g, "j", "sp1", true, JobGraph{Operators: []JobOperator{{ID: "fresh", Parallelism: 128}}})
	g = newGate()
	_, rej = g.Restore("j", "sp1", true, JobGraph{Operators: []JobOperator{{ID: "fresh", Parallelism: 129}}})
	mustReject(t, rej, ReasonInvalidJobGraph, "fresh")

	// 声明的来源不在保存点内。
	g = newGate()
	_, rej = g.Restore("j", "sp1", false, JobGraph{Operators: []JobOperator{
		{ID: "b", Parallelism: 1, SourceID: strPtr("ghost")},
	}})
	mustReject(t, rej, ReasonInvalidJobGraph, "b")
}

// 新增算子空启动，新增状态项不影响承接算子的直接恢复。
func TestNewOperatorStartsEmpty(t *testing.T) {
	g := newGatekeeper(t)
	g.RegisterSavepoint(Savepoint{ID: "sp1", Operators: []SavepointOperator{
		{ID: "a", MaxParallelism: 4, Items: []StateItem{{Name: "s", Kind: KindValue, Value: TypeInt}}},
	}})
	plan := restoreOK(t, g, "job1", "sp1", false, JobGraph{Operators: []JobOperator{
		{ID: "a", Parallelism: 1, Items: []StateItem{
			{Name: "s", Kind: KindValue, Value: TypeInt},
			{Name: "brand-new", Kind: KindMap, Value: TypeString},
		}},
		{ID: "added", Parallelism: 3},
	}})
	want := []OperatorPlan{
		{OperatorID: "a", Action: ActionRestoreDirect},
		{OperatorID: "added", Action: ActionStartEmpty},
	}
	if !reflect.DeepEqual(plan.Operators, want) {
		t.Fatalf("plan.Operators = %+v, want %+v", plan.Operators, want)
	}
}

// 保存点被存活作业引用时不能删除，作业停止后才可删。
func TestDeleteSavepointWhileReferenced(t *testing.T) {
	g := newGatekeeper(t)
	g.RegisterSavepoint(Savepoint{ID: "sp1", Operators: []SavepointOperator{
		{ID: "a", MaxParallelism: 4},
	}})
	restoreOK(t, g, "job1", "sp1", false, JobGraph{Operators: []JobOperator{{ID: "a", Parallelism: 1}}})

	rej := g.DeleteSavepoint("sp1")
	mustReject(t, rej, ReasonJobIDOccupied, "job1")

	// 被拒绝的删除无副作用：保存点仍可用于新作业恢复。
	restoreOK(t, g, "job2", "sp1", false, JobGraph{Operators: []JobOperator{{ID: "a", Parallelism: 1}}})

	if !g.Stop("job1") || !g.Stop("job2") {
		t.Fatal("stop should succeed for live jobs")
	}
	if rej := g.DeleteSavepoint("sp1"); rej != nil {
		t.Fatalf("delete after stop should succeed, got %v", rej)
	}
	// 删除后保存点不存在。
	_, rej = g.Restore("job3", "sp1", false, JobGraph{})
	mustReject(t, rej, ReasonSavepointNotFound, "sp1")
	mustReject(t, g.DeleteSavepoint("sp1"), ReasonSavepointNotFound, "sp1")
}

// 被拒绝的恢复不得改变任何状态：作业标识不占用、保存点不引用。
func TestRejectedRestoreHasNoSideEffects(t *testing.T) {
	g := newGatekeeper(t)
	g.RegisterSavepoint(Savepoint{ID: "sp1", Operators: []SavepointOperator{
		{ID: "a", MaxParallelism: 4},
	}})
	_, rej := g.Restore("job1", "sp1", false, JobGraph{Operators: []JobOperator{{ID: "a", Parallelism: 0}}})
	mustReject(t, rej, ReasonInvalidJobGraph, "a")

	// 同一作业标识可再次用于合法恢复。
	restoreOK(t, g, "job1", "sp1", false, JobGraph{Operators: []JobOperator{{ID: "a", Parallelism: 1}}})
}

// 相同输入得到完全相同的计划与清单。
func TestDeterministicPlan(t *testing.T) {
	newGate := func() *Gatekeeper {
		g := newGatekeeper(t)
		g.RegisterSavepoint(Savepoint{ID: "sp1", Operators: []SavepointOperator{
			{ID: "b", MaxParallelism: 4, Items: []StateItem{
				{Name: "w", Kind: KindValue, Value: TypeInt},
				{Name: "drop-me", Kind: KindValue, Value: TypeLong},
			}},
			{ID: "a", MaxParallelism: 2},
			{ID: "orphan", MaxParallelism: 1},
		}})
		return g
	}
	graph := JobGraph{Operators: []JobOperator{
		{ID: "b", Parallelism: 1, Items: []StateItem{{Name: "w", Kind: KindValue, Value: TypeLong}}},
		{ID: "renamed", Parallelism: 1, SourceID: strPtr("a")},
	}}
	p1 := restoreOK(t, newGate(), "job1", "sp1", true, graph)
	p2 := restoreOK(t, newGate(), "job1", "sp1", true, graph)
	if !reflect.DeepEqual(p1, p2) {
		t.Fatalf("plans differ:\n%+v\n%+v", p1, p2)
	}
	wantOps := []OperatorPlan{
		{OperatorID: "b", Action: ActionRestoreWiden, WidenedItems: []string{"w"}},
		{OperatorID: "renamed", Action: ActionRestoreDirect},
	}
	if !reflect.DeepEqual(p1.Operators, wantOps) {
		t.Fatalf("Operators = %+v, want %+v", p1.Operators, wantOps)
	}
	if !reflect.DeepEqual(p1.DroppedOperators, []string{"orphan"}) ||
		!reflect.DeepEqual(p1.DroppedStateItems, []string{"b.drop-me"}) {
		t.Fatalf("drops = %v / %v", p1.DroppedOperators, p1.DroppedStateItems)
	}
}

// 并发：同一作业标识并发恢复只成功一个；被引用的保存点不会被并发删除。
func TestConcurrentRestoreAndDelete(t *testing.T) {
	g := newGatekeeper(t)
	g.RegisterSavepoint(Savepoint{ID: "sp1", Operators: []SavepointOperator{
		{ID: "a", MaxParallelism: 64},
	}})
	graph := JobGraph{Operators: []JobOperator{{ID: "a", Parallelism: 1}}}

	const n = 32
	var wg sync.WaitGroup
	wins := make(chan string, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			// 一半goroutine抢同一个作业标识，另一半抢同一保存点的删除。
			if i%2 == 0 {
				if _, rej := g.Restore("hot-job", "sp1", false, graph); rej == nil {
					wins <- "restore"
				}
			} else {
				if rej := g.DeleteSavepoint("sp1"); rej == nil {
					wins <- "delete"
				}
			}
		}(i)
	}
	wg.Wait()
	close(wins)

	var restores, deletes int
	for w := range wins {
		switch w {
		case "restore":
			restores++
		case "delete":
			deletes++
		}
	}
	if restores > 1 {
		t.Fatalf("same job id restored %d times, want at most 1", restores)
	}
	// 若恢复先成功，删除必须全部被拒；若删除先成功，恢复必须全部被拒。
	if restores == 1 && deletes != 0 {
		t.Fatalf("savepoint deleted while referenced by live job")
	}
	if deletes == 1 && restores != 0 {
		t.Fatalf("restore succeeded after savepoint deletion")
	}
}

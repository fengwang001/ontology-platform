package vanload

import (
	"strings"
	"testing"
)

func TestBatchLoadAllOrNothing(t *testing.T) {
	sys, _ := New([]Compartment{{MaxWeight: 100, MaxVolume: 100}})

	// 第三件超重，整批无变化。
	cargos := []Cargo{
		{ID: 1, Weight: 10, Volume: 10, Stop: 1},
		{ID: 2, Weight: 10, Volume: 10, Stop: 1},
		{ID: 3, Weight: 90, Volume: 10, Stop: 1},
	}
	_, err := sys.BatchLoad(cargos)
	r := mustReject(t, err)
	if r.Kind != KindOverweight || r.FailedIndex != 2 {
		t.Fatalf("期望下标2超重, 实际 kind=%v idx=%d", r.Kind, r.FailedIndex)
	}
	if len(sys.state.items) != 0 {
		t.Fatalf("失败批不得留痕, 车上仍有 %d 件", len(sys.state.items))
	}
	caps := sys.RemainingCapacities()
	if caps[0].RemainWeight != 100 || caps[0].RemainVolume != 100 {
		t.Fatalf("失败批不得改变容量, 实际 %+v", caps[0])
	}

	// 全部成功。
	ok := []Cargo{
		{ID: 10, Weight: 30, Volume: 30, Stop: 2},
		{ID: 11, Weight: 30, Volume: 30, Stop: 2},
	}
	res, err := sys.BatchLoad(ok)
	if err != nil {
		t.Fatalf("批量装货意外失败: %v", err)
	}
	if res.Placements[10] != 1 || res.Placements[11] != 1 {
		t.Fatalf("两件都应在分区1, 实际 %v", res.Placements)
	}

	// 批内编号重复（第二、四件）报参数非法，取下标最小的重复件。
	dup := []Cargo{
		{ID: 20, Weight: 1, Volume: 1, Stop: 1},
		{ID: 20, Weight: 1, Volume: 1, Stop: 1},
	}
	_, err = sys.BatchLoad(dup)
	r = mustReject(t, err)
	if r.Kind != KindDuplicateInBatch || r.FailedIndex != 1 {
		t.Fatalf("期望下标1批内重复, 实际 kind=%v idx=%d", r.Kind, r.FailedIndex)
	}

	// 与车上编号重复也报参数非法。
	_, err = sys.BatchLoad([]Cargo{{ID: 10, Weight: 1, Volume: 1, Stop: 1}})
	if mustReject(t, err).Kind != KindDuplicateInBatch {
		t.Fatalf("期望与车上重复")
	}

	// 批量看到前面各件放入后的状态：第二件因第一件占容量而去不了分区1。
	sys2, _ := New([]Compartment{
		{MaxWeight: 10, MaxVolume: 10},
		{MaxWeight: 100, MaxVolume: 100},
	})
	res2, err := sys2.BatchLoad([]Cargo{
		{ID: 30, Weight: 8, Volume: 8, Stop: 1},
		{ID: 31, Weight: 8, Volume: 8, Stop: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res2.Placements[30] != 1 || res2.Placements[31] != 2 {
		t.Fatalf("期望 30→1,31→2, 实际 %v", res2.Placements)
	}
}

func TestUnloadSequence(t *testing.T) {
	sys, _ := New([]Compartment{{MaxWeight: 1_000_000, MaxVolume: 1_000_000}})
	_, _ = sys.BatchLoad([]Cargo{
		{ID: 1, Weight: 1, Volume: 1, Stop: 1},
		{ID: 2, Weight: 1, Volume: 1, Stop: 1},
		{ID: 3, Weight: 1, Volume: 1, Stop: 3},
	})

	// 未到 1 先卸 3：顺序错误且状态不变。
	if _, err := sys.Unload(3); mustReject(t, err).Kind != KindUnloadOrder {
		t.Fatalf("期望卸货顺序错误")
	}
	if sys.ArrivedStop() != 0 {
		t.Fatalf("被拒绝卸货不得推进进度")
	}

	// 卸 1：卸下 1、2。
	res, err := sys.Unload(1)
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != UnloadOK || len(res.Removed) != 2 || res.Removed[0] != 1 || res.Removed[1] != 2 {
		t.Fatalf("卸货结果不符: %+v", res)
	}
	if _, ok := sys.Locate(1); ok {
		t.Fatalf("货物1应已卸下")
	}

	// 重复卸 1：已处理，无变化。
	res2, err := sys.Unload(1)
	if err != nil || res2.Status != UnloadAlreadyProcessed {
		t.Fatalf("期望已处理, 实际 %+v err=%v", res2, err)
	}

	// 卸空停靠点 2：成功推进但无货物。
	res3, err := sys.Unload(2)
	if err != nil || res3.Status != UnloadEmpty || sys.ArrivedStop() != 2 {
		t.Fatalf("期望空停靠点推进, 实际 %+v err=%v", res3, err)
	}

	// 再卸 1：仍是已处理。
	if r, _ := sys.Unload(1); r.Status != UnloadAlreadyProcessed {
		t.Fatalf("期望已处理")
	}

	// 卸 3 成功。
	res4, err := sys.Unload(3)
	if err != nil || res4.Status != UnloadOK || len(res4.Removed) != 1 {
		t.Fatalf("期望卸下货物3, 实际 %+v err=%v", res4, err)
	}

	// 非法序号。
	if _, err := sys.Unload(0); mustReject(t, err).Kind != KindInvalidArgument {
		t.Fatalf("期望参数非法")
	}
}

func TestMidwayLoadStopPassed(t *testing.T) {
	sys, _ := New([]Compartment{{MaxWeight: 1_000_000, MaxVolume: 1_000_000}})
	mustLoad(t, sys, Cargo{ID: 1, Weight: 1, Volume: 1, Stop: 1})
	mustLoad(t, sys, Cargo{ID: 5, Weight: 1, Volume: 1, Stop: 3})

	// 开始卸货：卸停靠点1，再到达空停靠点2。
	if _, err := sys.Unload(1); err != nil {
		t.Fatal(err)
	}
	if res, err := sys.Unload(2); err != nil || res.Status != UnloadEmpty {
		t.Fatalf("期望空停靠点2推进, 实际 %+v err=%v", res, err)
	}
	if sys.ArrivedStop() != 2 {
		t.Fatalf("期望已到达停靠点2")
	}

	// 新装货物停靠点 2：已过，即使四类约束都满足也拒绝，且先于四类约束判定。
	_, err := sys.Load(Cargo{ID: 2, Weight: 1, Volume: 1, Stop: 2})
	if mustReject(t, err).Kind != KindStopPassed {
		t.Fatalf("期望停靠点已过")
	}
	// 卸完停靠点3，使进度推进到 3。
	if _, err := sys.Unload(3); err != nil {
		t.Fatal(err)
	}
	// 停靠点 3（等于已到达）也算已过。
	_, err = sys.Load(Cargo{ID: 3, Weight: 1, Volume: 1, Stop: 3})
	if mustReject(t, err).Kind != KindStopPassed {
		t.Fatalf("等于已到达序号期望已过")
	}
	// 停靠点 4：允许（即使车上有 stop3 的货，同区混装没问题）。
	if k := mustLoad(t, sys, Cargo{ID: 4, Weight: 1, Volume: 1, Stop: 4}); k != 1 {
		t.Fatalf("停靠点4应能装入分区1, 实际 %d", k)
	}

	// 拒绝优先级：编号重复先于停靠点已过。
	_, err = sys.Load(Cargo{ID: 4, Weight: 1, Volume: 1, Stop: 1})
	if mustReject(t, err).Kind != KindDuplicateID {
		t.Fatalf("期望编号重复优先, 实际 %v", err)
	}
}

func TestRejectPriority(t *testing.T) {
	// 单分区容量为 2；车上有一件 stop5 货物占 1，卸货进度推进到 2。
	sys, _ := New([]Compartment{{MaxWeight: 2, MaxVolume: 2}})
	mustLoad(t, sys, Cargo{ID: 2, Weight: 1, Volume: 1, Stop: 2})
	mustLoad(t, sys, Cargo{ID: 1, Weight: 1, Volume: 1, Stop: 5})
	if _, err := sys.Unload(2); err != nil {
		t.Fatal(err)
	}

	// 参数非法 > 编号重复 > 停靠点已过 > 四类约束。
	_, err := sys.Load(Cargo{ID: 0, Weight: 1, Volume: 1, Stop: 1})
	if mustReject(t, err).Kind != KindInvalidArgument {
		t.Fatalf("参数非法应最优先")
	}
	_, err = sys.Load(Cargo{ID: 1, Weight: 1, Volume: 1, Stop: 1})
	if mustReject(t, err).Kind != KindDuplicateID {
		t.Fatalf("编号重复应先于停靠点已过")
	}
	_, err = sys.Load(Cargo{ID: 9, Weight: 1, Volume: 1, Stop: 1})
	if mustReject(t, err).Kind != KindStopPassed {
		t.Fatalf("停靠点已过应先于四类约束")
	}
	// 停靠点 5 未过但超重（车上剩 1 容量，货重 2）。
	_, err = sys.Load(Cargo{ID: 9, Weight: 2, Volume: 1, Stop: 5})
	if got := mustReject(t, err).Kind; got != KindOverweight {
		t.Fatalf("通过前三道检查后才判定四类约束, 实际 %v", got)
	}
}

type captureLogger struct{ lines []string }

func (l *captureLogger) Log(e Event) {
	l.lines = append(l.lines, "["+e.Op+"] "+e.Input+" => "+e.Output+" | "+e.Reason)
}

func TestLoggingInputOutputBasis(t *testing.T) {
	logger := &captureLogger{}
	sys, _ := New([]Compartment{{MaxWeight: 100, MaxVolume: 100}}, WithLogger(logger))

	_, err := sys.Load(Cargo{ID: 7, Weight: 50, Volume: 60, Stop: 2, Category: CategoryFood})
	if err != nil {
		t.Fatal(err)
	}
	_, err = sys.Load(Cargo{ID: 8, Weight: 60, Volume: 60, Stop: 1, Category: CategoryOxidizing})
	_ = err // 同区食品隔离冲突

	if len(logger.lines) != 2 {
		t.Fatalf("期望两条日志, 实际 %d", len(logger.lines))
	}
	if !strings.Contains(logger.lines[0], "id=7") ||
		!strings.Contains(logger.lines[0], "食品") ||
		!strings.Contains(logger.lines[0], "成功:分区=1") {
		t.Fatalf("成功日志内容不符: %s", logger.lines[0])
	}
	if !strings.Contains(logger.lines[1], "拒绝:隔离冲突") ||
		!strings.Contains(logger.lines[1], "分区1:隔离冲突") {
		t.Fatalf("失败日志应含判定依据: %s", logger.lines[1])
	}
}

func mustReject(t *testing.T, err error) *Reject {
	t.Helper()
	r := rejectKindErr(t, err)
	return r
}

func rejectKindErr(t *testing.T, err error) *Reject {
	t.Helper()
	if err == nil {
		t.Fatal("期望拒绝错误, 实际成功")
	}
	r, ok := err.(*Reject)
	if !ok {
		t.Fatalf("期望 *Reject, 实际 %T: %v", err, err)
	}
	return r
}

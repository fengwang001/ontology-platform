package ontology

import (
	"testing"
)

// TestInsertAndSnapshot 覆盖插入后聚合视图正确，以及
// “求和为零但计数为正的组必须保留”这一关键语义。
func TestInsertAndSnapshot(t *testing.T) {
	agg := New(0)

	batch := 0
	logOps(t, batch, []Op{
		{Kind: OpInsert, RowID: "a", Group: "g1", Value: 5},
		{Kind: OpInsert, RowID: "b", Group: "g1", Value: -5}, // 与 a 求和抵消
		{Kind: OpInsert, RowID: "c", Group: "g1", Value: 0},  // 零值行仍计数
		{Kind: OpInsert, RowID: "d", Group: "g2", Value: 7},
	})
	entries, err := agg.Apply([]Op{
		{Kind: OpInsert, RowID: "a", Group: "g1", Value: 5},
		{Kind: OpInsert, RowID: "b", Group: "g1", Value: -5},
		{Kind: OpInsert, RowID: "c", Group: "g1", Value: 0},
		{Kind: OpInsert, RowID: "d", Group: "g2", Value: 7},
	})
	if err != nil {
		t.Fatalf("插入失败: %v", err)
	}
	logEntries(t, entries)
	logReason(t, "g1 含 3 行且求和 0，计数为正必须保留；g2 求和 7 计数 1")

	view := agg.Snapshot()
	if len(view) != 2 {
		t.Fatalf("应有 2 个组, got=%d (%v)", len(view), view)
	}
	if view[0] != (GroupView{Group: "g1", Sum: 0, Count: 3}) {
		t.Fatalf("g1 求和为零但应保留计数 3, got=%+v", view[0])
	}
	if view[1] != (GroupView{Group: "g2", Sum: 7, Count: 1}) {
		t.Fatalf("g2 状态错误, got=%+v", view[1])
	}
	assertConsistent(t, agg)
}

// TestSameGroupUpdate 覆盖同组内更新：净变化为先撤回旧值再写入新值，
// 计数先 -1 后 +1，组始终存活。
func TestSameGroupUpdate(t *testing.T) {
	agg := New(0)
	agg.Apply([]Op{{Kind: OpInsert, RowID: "a", Group: "g1", Value: 10}})

	ops := []Op{{Kind: OpUpdate, RowID: "a", Group: "g1", Value: 3}}
	logOps(t, 1, ops)
	entries, err := agg.Apply(ops)
	if err != nil {
		t.Fatalf("更新失败: %v", err)
	}
	logEntries(t, entries)
	logReason(t, "同组更新：先撤回 -10/count-1，再写入 +3/count+1；净求和变化 -7")

	if len(entries) != 2 {
		t.Fatalf("同组更新应产生 2 条日志, got=%d", len(entries))
	}
	if entries[0].Kind != EntryRetract || entries[0].Value != -10 || entries[0].Count != -1 {
		t.Fatalf("首条应为撤回旧值 -10/-1, got=%+v", entries[0])
	}
	if entries[0].CountAfter != 0 || entries[0].SumAfter != 0 {
		t.Fatalf("撤回后应为瞬时 0/0, got=%+v", entries[0])
	}
	if entries[1].Kind != EntryAdd || entries[1].Value != 3 || entries[1].Count != 1 {
		t.Fatalf("次条应为写入新值 3/+1, got=%+v", entries[1])
	}
	if view := agg.Snapshot(); view[0] != (GroupView{Group: "g1", Sum: 3, Count: 1}) {
		t.Fatalf("更新后 g1 错误, got=%+v", view)
	}
	assertConsistent(t, agg)
}

// TestGroupKeyChange 覆盖改分组键：必须先输出旧组撤回、再输出新组加入；
// 旧组计数归零后从视图消失，新组出现。
func TestGroupKeyChange(t *testing.T) {
	agg := New(0)
	agg.Apply([]Op{
		{Kind: OpInsert, RowID: "a", Group: "old", Value: 4},
		{Kind: OpInsert, RowID: "b", Group: "old", Value: 6},
		{Kind: OpInsert, RowID: "c", Group: "keep", Value: 9},
	})

	ops := []Op{{Kind: OpUpdate, RowID: "a", Group: "new", Value: 4}}
	logOps(t, 1, ops)
	entries, err := agg.Apply(ops)
	if err != nil {
		t.Fatalf("改键失败: %v", err)
	}
	logEntries(t, entries)
	logReason(t, "改分组键：第 1 条作用于旧组 old（撤回），第 2 条作用于新组 new（加入）")

	if len(entries) != 2 {
		t.Fatalf("改键应产生 2 条日志, got=%d", len(entries))
	}
	if entries[0].Group != "old" || entries[0].Kind != EntryRetract {
		t.Fatalf("必须先输出旧组撤回, got=%+v", entries[0])
	}
	if entries[1].Group != "new" || entries[1].Kind != EntryAdd {
		t.Fatalf("必须后输出新组加入, got=%+v", entries[1])
	}
	view := agg.Snapshot()
	if len(view) != 3 {
		t.Fatalf("old(剩 b)、new、keep 三组都应存在, got=%v", view)
	}
	want := map[string]GroupView{
		"old":  {Group: "old", Sum: 6, Count: 1},
		"new":  {Group: "new", Sum: 4, Count: 1},
		"keep": {Group: "keep", Sum: 9, Count: 1},
	}
	for _, v := range view {
		if v != want[v.Group] {
			t.Fatalf("组 %s 状态错误: got=%+v want=%+v", v.Group, v, want[v.Group])
		}
	}
	assertConsistent(t, agg)
}

// TestGroupKeyChangeToEmpty 覆盖把组里最后一行改走：旧组应彻底从视图消失，
// 且日志中撤回后计数为 0。
func TestGroupKeyChangeToEmpty(t *testing.T) {
	agg := New(0)
	agg.Apply([]Op{{Kind: OpInsert, RowID: "a", Group: "old", Value: 4}})

	entries, err := agg.Apply([]Op{{Kind: OpUpdate, RowID: "a", Group: "new", Value: 5}})
	if err != nil {
		t.Fatalf("改键失败: %v", err)
	}
	logEntries(t, entries)
	if entries[0].Group != "old" || entries[0].CountAfter != 0 {
		t.Fatalf("旧组撤回后计数应为 0, got=%+v", entries[0])
	}
	for _, v := range agg.Snapshot() {
		if v.Group == "old" {
			t.Fatalf("计数归零的旧组不得出现在视图中: %+v", v)
		}
	}
	assertConsistent(t, agg)
}

// TestDelete 覆盖删除：撤回后组计数归零即消失。
func TestDelete(t *testing.T) {
	agg := New(0)
	agg.Apply([]Op{
		{Kind: OpInsert, RowID: "a", Group: "g1", Value: 2},
		{Kind: OpInsert, RowID: "b", Group: "g1", Value: 3},
	})
	entries, err := agg.Apply([]Op{{Kind: OpDelete, RowID: "a"}})
	if err != nil {
		t.Fatalf("删除失败: %v", err)
	}
	logEntries(t, entries)
	if entries[0].Kind != EntryRetract || entries[0].Value != -2 || entries[0].Count != -1 {
		t.Fatalf("删除日志错误: %+v", entries[0])
	}
	if view := agg.Snapshot(); view[0] != (GroupView{Group: "g1", Sum: 3, Count: 1}) {
		t.Fatalf("删除后 g1 错误: %+v", view)
	}

	// 删除最后一行，组消失。
	entries, _ = agg.Apply([]Op{{Kind: OpDelete, RowID: "b"}})
	logEntries(t, entries)
	if len(agg.Snapshot()) != 0 {
		t.Fatalf("组清空后视图应为空, got=%v", agg.Snapshot())
	}
	assertConsistent(t, agg)
}

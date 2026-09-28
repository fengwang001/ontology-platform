package ontology

import (
	"errors"
	"testing"
)

// TestRejectDuplicateInsert 重复插入（含同一批次内先插再插）必须拒绝。
func TestRejectDuplicateInsert(t *testing.T) {
	agg := New(0)
	agg.Apply([]Op{{Kind: OpInsert, RowID: "a", Group: "g1", Value: 1}})

	ops := []Op{{Kind: OpInsert, RowID: "a", Group: "g2", Value: 2}}
	logOps(t, 1, ops)
	logEntries(t, nil)
	assertReject(t, agg, ops, 0, ErrDuplicateRow)

	// 同批次内重复插入也必须拒绝。
	ops = []Op{
		{Kind: OpInsert, RowID: "x", Group: "g1", Value: 1},
		{Kind: OpInsert, RowID: "x", Group: "g1", Value: 2},
	}
	logOps(t, 2, ops)
	assertReject(t, agg, ops, 1, ErrDuplicateRow)
	assertConsistent(t, agg)
}

// TestRejectUpdateMissing 更新不存在的行必须拒绝。
func TestRejectUpdateMissing(t *testing.T) {
	agg := New(0)
	ops := []Op{{Kind: OpUpdate, RowID: "ghost", Group: "g1", Value: 1}}
	logOps(t, 0, ops)
	assertReject(t, agg, ops, 0, ErrRowNotFound)
}

// TestRejectDeleteMissing 删除不存在的行必须拒绝。
func TestRejectDeleteMissing(t *testing.T) {
	agg := New(0)
	agg.Apply([]Op{{Kind: OpInsert, RowID: "a", Group: "g1", Value: 1}})
	agg.Apply([]Op{{Kind: OpDelete, RowID: "a"}})

	// 删除已删除的行属于“删除不存在的行”。
	ops := []Op{{Kind: OpDelete, RowID: "a"}}
	logOps(t, 1, ops)
	assertReject(t, agg, ops, 0, ErrRowNotFound)
}

// TestRejectEmptyKeys 空行 ID 与空分组键必须以不同原因拒绝。
func TestRejectEmptyKeys(t *testing.T) {
	agg := New(0)

	ops := []Op{{Kind: OpInsert, RowID: "", Group: "g1", Value: 1}}
	logOps(t, 0, ops)
	assertReject(t, agg, ops, 0, ErrEmptyRowKey)

	ops = []Op{{Kind: OpInsert, RowID: "a", Group: "", Value: 1}}
	logOps(t, 1, ops)
	assertReject(t, agg, ops, 0, ErrEmptyGroupKey)

	// 更新到空分组键同样拒绝。
	agg.Apply([]Op{{Kind: OpInsert, RowID: "a", Group: "g1", Value: 1}})
	ops = []Op{{Kind: OpUpdate, RowID: "a", Group: "", Value: 2}}
	logOps(t, 2, ops)
	assertReject(t, agg, ops, 0, ErrEmptyGroupKey)

	// 删除时分组键字段被忽略，空分组键不构成错误。
	ops = []Op{{Kind: OpDelete, RowID: "a", Group: "", Value: 0}}
	logOps(t, 3, ops)
	if _, err := agg.Apply(ops); err != nil {
		t.Fatalf("删除不应校验分组键: %v", err)
	}
	logReason(t, "删除只凭行 ID 定位，空分组键字段被忽略——合法")
}

// TestRejectUnknownKind 未知操作类型必须拒绝。
func TestRejectUnknownKind(t *testing.T) {
	agg := New(0)
	ops := []Op{{Kind: OpKind(99), RowID: "a", Group: "g1", Value: 1}}
	logOps(t, 0, ops)
	assertReject(t, agg, ops, 0, ErrInvalidOp)
}

// TestRejectTooManyGroups 组数超限必须拒绝；注意计数归零的组不计入组数。
func TestRejectTooManyGroups(t *testing.T) {
	agg := New(2) // 最多 2 个活跃组

	ops := []Op{
		{Kind: OpInsert, RowID: "a", Group: "g1", Value: 1},
		{Kind: OpInsert, RowID: "b", Group: "g2", Value: 1},
		{Kind: OpInsert, RowID: "c", Group: "g3", Value: 1}, // 触发第 3 组
	}
	logOps(t, 0, ops)
	assertReject(t, agg, ops, 2, ErrTooManyGroups)
	assertConsistent(t, agg)

	// 改键到一个会产生第 3 个活跃组的新组，也必须拒绝。
	// g1 中放两行，使 a 改走后 g1 仍存活，最终活跃组为 g1/g2/g3 共 3 个。
	agg2 := New(2)
	agg2.Apply([]Op{
		{Kind: OpInsert, RowID: "a", Group: "g1", Value: 1},
		{Kind: OpInsert, RowID: "a2", Group: "g1", Value: 1},
		{Kind: OpInsert, RowID: "b", Group: "g2", Value: 1},
	})
	move := []Op{{Kind: OpUpdate, RowID: "a", Group: "g3", Value: 1}}
	logOps(t, 1, move)
	assertReject(t, agg2, move, 0, ErrTooManyGroups)

	// 但把某组最后一行改走到已存在的组不新增组数，应合法。
	move = []Op{{Kind: OpUpdate, RowID: "a", Group: "g2", Value: 1}}
	logOps(t, 2, move)
	if _, err := agg2.Apply(move); err != nil {
		t.Fatalf("并入已存在组不应触发上限: %v", err)
	}
	logReason(t, "旧组在撤回阶段已消失、目标组已存在，活跃组数未增加——合法")
	assertConsistent(t, agg2)

	// 先删空一个组再新建组，组数未超限，应合法。
	agg3 := New(1)
	agg3.Apply([]Op{{Kind: OpInsert, RowID: "a", Group: "g1", Value: 1}})
	agg3.Apply([]Op{{Kind: OpDelete, RowID: "a"}})
	if _, err := agg3.Apply([]Op{{Kind: OpInsert, RowID: "b", Group: "g2", Value: 1}}); err != nil {
		t.Fatalf("旧组计数归零已释放名额, 应允许新组: %v", err)
	}

	// 边界：唯一一组中仅有的一行做“同组更新”，撤回阶段组被瞬时删除、
	// 随后重新加入，最终仍是 1 组，不得误判为超限。
	agg4 := New(1)
	agg4.Apply([]Op{{Kind: OpInsert, RowID: "a", Group: "g1", Value: 1}})
	if _, err := agg4.Apply([]Op{{Kind: OpUpdate, RowID: "a", Group: "g1", Value: 2}}); err != nil {
		t.Fatalf("同组更新（组瞬时删后重建）不应触发上限: %v", err)
	}
	if view := agg4.Snapshot(); len(view) != 1 || view[0] != (GroupView{Group: "g1", Sum: 2, Count: 1}) {
		t.Fatalf("同组更新后状态错误: %v", view)
	}
}

// TestReasonsAreDistinct 所有拒绝原因必须可被 errors.Is 区分。
func TestReasonsAreDistinct(t *testing.T) {
	causes := []error{
		ErrDuplicateRow, ErrRowNotFound, ErrEmptyRowKey,
		ErrEmptyGroupKey, ErrTooManyGroups, ErrInvalidOp,
	}
	for i := range causes {
		for j := range causes {
			if i != j && errors.Is(causes[i], causes[j]) {
				t.Fatalf("哨兵错误 %v 与 %v 不应互相满足 errors.Is", causes[i], causes[j])
			}
		}
	}
	logReason(t, "六个拒绝原因两两不同，errors.Is 可精确定位")
}

// TestBatchAtomicity 批次中部出现非法操作时，前面已模拟的条目必须全部回滚，
// 行表、聚合、日志三者都保持批前状态。
func TestBatchAtomicity(t *testing.T) {
	agg := New(0)
	agg.Apply([]Op{
		{Kind: OpInsert, RowID: "a", Group: "g1", Value: 1},
		{Kind: OpInsert, RowID: "b", Group: "g2", Value: 2},
	})

	ops := []Op{
		{Kind: OpInsert, RowID: "c", Group: "g1", Value: 3},   // 模拟阶段合法
		{Kind: OpUpdate, RowID: "a", Group: "g3", Value: 9},   // 模拟阶段合法
		{Kind: OpDelete, RowID: "missing"},                    // 非法：批次在此中止
		{Kind: OpInsert, RowID: "d", Group: "g9", Value: 100}, // 不应被触及
	}
	logOps(t, 1, ops)
	entries, err := agg.Apply(ops)
	if err == nil {
		t.Fatalf("批次应被拒绝")
	}
	if len(entries) != 0 {
		t.Fatalf("被拒绝的批不得返回任何日志条目, got=%d", len(entries))
	}
	logReason(t, "第 2 条删除不存在的行，整批回滚，前两条模拟结果不落盘")

	// 行表仍是批前两行。
	if len(agg.Rows()) != 2 {
		t.Fatalf("行表应回滚为 2 行, got=%d", len(agg.Rows()))
	}
	// 日志仍是批前两条插入共 2 条。
	if l := agg.Log(); len(l) != 2 {
		t.Fatalf("日志应回滚为 2 条, got=%d", len(l))
	}
	assertConsistent(t, agg)
}

// TestEmptyBatch 空批次是合法 no-op，不产生日志也不改变状态。
func TestEmptyBatch(t *testing.T) {
	agg := New(0)
	entries, err := agg.Apply(nil)
	if err != nil || len(entries) != 0 {
		t.Fatalf("空批次应为 no-op, entries=%d err=%v", len(entries), err)
	}
	logReason(t, "空批次合法，无输入即无输出，状态不变")
}

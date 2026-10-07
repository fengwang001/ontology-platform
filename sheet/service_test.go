package sheet_test

import (
	"reflect"
	"testing"

	"ontology/sheet"
)

func newSvc(t *testing.T, depths map[string]int) *sheet.Service {
	t.Helper()
	s, err := sheet.New(depths)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s
}

func wantOKValue(t *testing.T, r sheet.Result) {
	t.Helper()
	if !r.OK {
		t.Fatalf("期望成功, 实际 %v (cell=%q)", r.Code, r.Cell)
	}
}

func wantReject(t *testing.T, r sheet.Result, code sheet.ErrCode) {
	t.Helper()
	if r.OK || r.Code != code {
		t.Fatalf("期望拒绝 %v, 实际 ok=%v code=%v", code, r.OK, r.Code)
	}
}

func wantCell(t *testing.T, s *sheet.Service, key string, value int64, empty bool, version int64) {
	t.Helper()
	got := s.Cell(key)
	want := sheet.CellState{Value: value, Empty: empty, Version: version}
	if got != want {
		t.Fatalf("Cell(%q) = %+v, 期望 %+v", key, got, want)
	}
}

func wantHistory(t *testing.T, s *sheet.Service, user string, undoCount, redoCount int, undoTop, redoTop []string) {
	t.Helper()
	info, ok := s.History(user)
	if !ok {
		t.Fatalf("History(%q) 用户不存在", user)
	}
	if info.UndoCount != undoCount || info.RedoCount != redoCount {
		t.Fatalf("History(%q) 计数 = (%d, %d), 期望 (%d, %d)",
			user, info.UndoCount, info.RedoCount, undoCount, redoCount)
	}
	if !reflect.DeepEqual(info.UndoTop, undoTop) || !reflect.DeepEqual(info.RedoTop, redoTop) {
		t.Fatalf("History(%q) 栈顶 = (%v, %v), 期望 (%v, %v)",
			user, info.UndoTop, info.RedoTop, undoTop, redoTop)
	}
}

// 基础：Apply 生效、修订号与版本推进、Cell/History 查询。
func TestApplyBasic(t *testing.T) {
	s := newSvc(t, map[string]int{"alice": 3, "bob": 3})

	r := s.Apply("alice", []sheet.Edit{
		{Key: "b2", Value: 7},
		{Key: "a1", Value: 5},
	}, 10)
	wantOKValue(t, r)
	if r.Revision != 1 {
		t.Fatalf("Revision = %d, 期望 1", r.Revision)
	}
	wantCell(t, s, "a1", 5, false, 1)
	wantCell(t, s, "b2", 7, false, 1)
	wantCell(t, s, "c3", 0, true, 0) // 从未写入
	wantHistory(t, s, "alice", 1, 0, []string{"a1", "b2"}, nil)
	wantHistory(t, s, "bob", 0, 0, nil, nil)

	// 清除已有值：版本仍推进。
	r = s.Apply("alice", []sheet.Edit{{Key: "a1", Clear: true}}, 11)
	wantOKValue(t, r)
	wantCell(t, s, "a1", 0, true, 2)
	wantHistory(t, s, "alice", 2, 0, []string{"a1"}, nil)
}

// 无效编辑（写入同值、清除空单元格）被剔除，不进入历史；
// 全部无效时整个操作被拒且无变化。
func TestInvalidEditsFiltered(t *testing.T) {
	s := newSvc(t, map[string]int{"alice": 3})
	wantOKValue(t, s.Apply("alice", []sheet.Edit{{Key: "k1", Value: 3}}, 1))

	// k2 写入同值(无效) + k3 清除空单元格(无效) + k4 新写入(有效)。
	r := s.Apply("alice", []sheet.Edit{
		{Key: "k1", Value: 3},    // 同值写入, 无效
		{Key: "k2", Clear: true}, // 清除空单元格, 无效
		{Key: "k3", Value: 9},    // 有效
	}, 2)
	wantOKValue(t, r)
	if r.Revision != 2 {
		t.Fatalf("Revision = %d, 期望 2", r.Revision)
	}
	// 历史记录只含有效编辑的单元格。
	wantHistory(t, s, "alice", 2, 0, []string{"k3"}, nil)
	wantCell(t, s, "k1", 3, false, 1) // 未被触碰, 版本保持 1

	// 全部无效：被拒, 无变化。
	revBefore := s.Revision()
	r = s.Apply("alice", []sheet.Edit{
		{Key: "k1", Value: 3},
		{Key: "k2", Clear: true},
	}, 3)
	wantReject(t, r, sheet.CodeNoChange)
	if r.Dropped {
		t.Fatalf("无变化拒绝不应标明 Dropped")
	}
	if s.Revision() != revBefore {
		t.Fatalf("被拒后修订号改变: %d -> %d", revBefore, s.Revision())
	}
	wantHistory(t, s, "alice", 2, 0, []string{"k3"}, nil)
}

// 他人穿插编辑导致被覆盖：记录弹出丢弃、不进入重做栈、
// 返回标明 Dropped 且给出按键排序第一个不符单元格。
func TestUndoOverwrittenByOther(t *testing.T) {
	s := newSvc(t, map[string]int{"alice": 3, "bob": 3})
	wantOKValue(t, s.Apply("alice", []sheet.Edit{
		{Key: "a", Value: 1},
		{Key: "b", Value: 2},
	}, 1))
	// bob 穿插覆盖 b（a 保持版本 1）。
	wantOKValue(t, s.Apply("bob", []sheet.Edit{{Key: "b", Value: 20}}, 2))

	revBefore := s.Revision()
	r := s.Undo("alice", 3)
	wantReject(t, r, sheet.CodeOverwritten)
	if r.Cell != "b" {
		t.Fatalf("冲突单元格 = %q, 期望 b", r.Cell)
	}
	if !r.Dropped {
		t.Fatalf("被覆盖例外必须标明 Dropped")
	}
	// 表格与修订号不变。
	if s.Revision() != revBefore {
		t.Fatalf("被覆盖拒绝改变了修订号")
	}
	wantCell(t, s, "a", 1, false, 1)
	wantCell(t, s, "b", 20, false, 2)
	// 记录被丢弃：撤销栈变空, 重做栈不增加。
	wantHistory(t, s, "alice", 0, 0, nil, nil)
	// 再次撤销报栈空（而不是再次报被覆盖）。
	wantReject(t, s.Undo("alice", 4), sheet.CodeEmptyStack)
}

// 多单元格记录中, 报告按键排序的第一个不符单元格。
func TestOverwrittenFirstSortedCell(t *testing.T) {
	s := newSvc(t, map[string]int{"alice": 3, "bob": 3})
	wantOKValue(t, s.Apply("alice", []sheet.Edit{
		{Key: "m", Value: 1},
		{Key: "a", Value: 2},
		{Key: "z", Value: 3},
	}, 1))
	// bob 覆盖 m 与 z, 不覆盖 a。
	wantOKValue(t, s.Apply("bob", []sheet.Edit{
		{Key: "m", Value: 10},
		{Key: "z", Value: 30},
	}, 2))
	r := s.Undo("alice", 3)
	wantReject(t, r, sheet.CodeOverwritten)
	if r.Cell != "m" {
		t.Fatalf("冲突单元格 = %q, 期望按键序第一个 m", r.Cell)
	}
}

// 当前值恰好与原值相同但版本不同, 也算被覆盖。
func TestSameValueDifferentVersionIsOverwritten(t *testing.T) {
	s := newSvc(t, map[string]int{"alice": 3, "bob": 3})
	// alice 写入 a=5（原值为空, 版本 1）。
	wantOKValue(t, s.Apply("alice", []sheet.Edit{{Key: "a", Value: 5}}, 1))
	// bob 清除 a（值回到空, 版本 2）。
	wantOKValue(t, s.Apply("bob", []sheet.Edit{{Key: "a", Clear: true}}, 2))
	wantCell(t, s, "a", 0, true, 2)

	// alice 撤销：当前值为空 == 记录原值(空), 但版本 2 != 1, 仍算被覆盖。
	r := s.Undo("alice", 3)
	wantReject(t, r, sheet.CodeOverwritten)
	if r.Cell != "a" || !r.Dropped {
		t.Fatalf("cell=%q dropped=%v, 期望 a/true", r.Cell, r.Dropped)
	}
	wantCell(t, s, "a", 0, true, 2)
}

// Undo/Redo 成功路径：值往返恢复, 版本与修订号持续推进。
func TestUndoRedoRoundTrip(t *testing.T) {
	s := newSvc(t, map[string]int{"alice": 3})
	wantOKValue(t, s.Apply("alice", []sheet.Edit{{Key: "a", Value: 1}}, 1))
	wantOKValue(t, s.Apply("alice", []sheet.Edit{{Key: "b", Value: 2}}, 2))

	// 撤销第二次：b 清空。
	r := s.Undo("alice", 3)
	wantOKValue(t, r)
	if r.Revision != 3 {
		t.Fatalf("Revision = %d, 期望 3", r.Revision)
	}
	wantCell(t, s, "b", 0, true, 3)
	wantHistory(t, s, "alice", 1, 1, []string{"a"}, []string{"b"})

	// 撤销第一次：a 清空。
	wantOKValue(t, s.Undo("alice", 4))
	wantCell(t, s, "a", 0, true, 4)
	wantHistory(t, s, "alice", 0, 2, nil, []string{"a"})

	// 重做第一次：a=1。
	wantOKValue(t, s.Redo("alice", 5))
	wantCell(t, s, "a", 1, false, 5)
	wantHistory(t, s, "alice", 1, 1, []string{"a"}, []string{"b"})

	// 重做第二次：b=2。
	wantOKValue(t, s.Redo("alice", 6))
	wantCell(t, s, "b", 2, false, 6)
	wantHistory(t, s, "alice", 2, 0, []string{"b"}, nil)
}

// 撤销后他人保护该单元格, 重做报受保护且两个栈不变。
func TestRedoBlockedByOthersProtection(t *testing.T) {
	s := newSvc(t, map[string]int{"alice": 3, "bob": 3})
	wantOKValue(t, s.Apply("alice", []sheet.Edit{{Key: "a", Value: 1}}, 1))
	wantOKValue(t, s.Undo("alice", 2))
	wantOKValue(t, s.Protect("bob", "a", 3))

	revBefore := s.Revision()
	r := s.Redo("alice", 4)
	wantReject(t, r, sheet.CodeProtected)
	if r.Cell != "a" {
		t.Fatalf("冲突单元格 = %q, 期望 a", r.Cell)
	}
	if r.Dropped {
		t.Fatalf("受保护拒绝不应弹出记录")
	}
	if s.Revision() != revBefore {
		t.Fatalf("受保护拒绝改变了修订号")
	}
	// 两个栈不变。
	wantHistory(t, s, "alice", 0, 1, nil, []string{"a"})
	// 解除保护后重做成功。
	wantOKValue(t, s.Unprotect("bob", "a", 5))
	wantOKValue(t, s.Redo("alice", 6))
	wantCell(t, s, "a", 1, false, 3) // 修订号只随 Apply/Undo/Redo 推进
}

// 自己撤销之后他人覆盖, 导致重做失败并丢弃记录。
func TestRedoOverwrittenByOther(t *testing.T) {
	s := newSvc(t, map[string]int{"alice": 3, "bob": 3})
	wantOKValue(t, s.Apply("alice", []sheet.Edit{{Key: "a", Value: 5}}, 1))
	wantOKValue(t, s.Undo("alice", 2)) // a 清空, 版本 2
	wantOKValue(t, s.Apply("bob", []sheet.Edit{{Key: "a", Value: 9}}, 3))

	r := s.Redo("alice", 4)
	wantReject(t, r, sheet.CodeOverwritten)
	if !r.Dropped || r.Cell != "a" {
		t.Fatalf("cell=%q dropped=%v, 期望 a/true", r.Cell, r.Dropped)
	}
	wantHistory(t, s, "alice", 0, 0, nil, nil)
	wantCell(t, s, "a", 9, false, 3)
}

// Undo 时记录涉及他人保护的单元格：报受保护, 两个栈不变。
func TestUndoBlockedByOthersProtection(t *testing.T) {
	s := newSvc(t, map[string]int{"alice": 3, "bob": 3})
	wantOKValue(t, s.Apply("alice", []sheet.Edit{{Key: "a", Value: 1}, {Key: "b", Value: 2}}, 1))
	wantOKValue(t, s.Protect("bob", "b", 2))

	r := s.Undo("alice", 3)
	wantReject(t, r, sheet.CodeProtected)
	if r.Cell != "b" {
		t.Fatalf("冲突单元格 = %q, 期望 b", r.Cell)
	}
	wantHistory(t, s, "alice", 1, 0, []string{"a", "b"}, nil)
	wantCell(t, s, "a", 1, false, 1)
	wantCell(t, s, "b", 2, false, 1)
}

// 深度限制：撤销栈超过 D 时丢弃最旧记录。
func TestDepthLimitDropsOldest(t *testing.T) {
	s := newSvc(t, map[string]int{"alice": 2})
	wantOKValue(t, s.Apply("alice", []sheet.Edit{{Key: "a", Value: 1}}, 1))
	wantOKValue(t, s.Apply("alice", []sheet.Edit{{Key: "b", Value: 2}}, 2))
	wantOKValue(t, s.Apply("alice", []sheet.Edit{{Key: "c", Value: 3}}, 3))

	// 栈深 2：最早写入 a 的记录已被丢弃。
	wantHistory(t, s, "alice", 2, 0, []string{"c"}, nil)
	wantOKValue(t, s.Undo("alice", 4)) // 撤销 c
	wantOKValue(t, s.Undo("alice", 5)) // 撤销 b
	wantCell(t, s, "c", 0, true, 4)
	wantCell(t, s, "b", 0, true, 5)
	wantCell(t, s, "a", 1, false, 1) // a 的记录已丢弃, 保持原样
	wantReject(t, s.Undo("alice", 6), sheet.CodeEmptyStack)
}

// 新的编辑清空自己的重做栈, 但不影响他人的栈。
func TestNewEditClearsOwnRedoOnly(t *testing.T) {
	s := newSvc(t, map[string]int{"alice": 3, "bob": 3})
	wantOKValue(t, s.Apply("alice", []sheet.Edit{{Key: "a", Value: 1}}, 1))
	wantOKValue(t, s.Undo("alice", 2)) // alice 重做栈 = 1
	wantOKValue(t, s.Apply("bob", []sheet.Edit{{Key: "b", Value: 2}}, 3))
	wantOKValue(t, s.Undo("bob", 4)) // bob 重做栈 = 1

	// alice 新编辑：清空自己的重做栈。
	wantOKValue(t, s.Apply("alice", []sheet.Edit{{Key: "c", Value: 3}}, 5))
	wantHistory(t, s, "alice", 1, 0, []string{"c"}, nil)
	// bob 的重做栈不受影响。
	wantHistory(t, s, "bob", 0, 1, nil, []string{"b"})
	wantOKValue(t, s.Redo("bob", 6))
	wantCell(t, s, "b", 2, false, 6)
}

// 保护语义：重复自我保护成功无变化；他人保护/解除被拒；
// 保护与解除不改变修订号、版本与历史；自己可写自己保护的单元格。
func TestProtectSemantics(t *testing.T) {
	s := newSvc(t, map[string]int{"alice": 3, "bob": 3})
	wantOKValue(t, s.Apply("alice", []sheet.Edit{{Key: "a", Value: 1}}, 1))

	wantOKValue(t, s.Protect("alice", "a", 2))
	if owner, ok := s.Protector("a"); !ok || owner != "alice" {
		t.Fatalf("Protector(a) = %q,%v", owner, ok)
	}
	// 重复自我保护：成功且无变化。
	wantOKValue(t, s.Protect("alice", "a", 3))
	// 他人保护同一单元格：被拒。
	wantReject(t, s.Protect("bob", "a", 4), sheet.CodeProtected)
	// 他人解除：被拒。
	wantReject(t, s.Unprotect("bob", "a", 5), sheet.CodeProtected)
	// 保护不改变修订号、版本与历史。
	if s.Revision() != 1 {
		t.Fatalf("保护改变了修订号: %d", s.Revision())
	}
	wantCell(t, s, "a", 1, false, 1)
	wantHistory(t, s, "alice", 1, 0, []string{"a"}, nil)

	// 自己可写自己保护的单元格（Apply 与 Undo 均适用）。
	wantOKValue(t, s.Apply("alice", []sheet.Edit{{Key: "a", Value: 2}}, 6))
	wantOKValue(t, s.Undo("alice", 7))
	wantCell(t, s, "a", 1, false, 3) // 第 3 个修订号
	// 他人不可写。
	wantReject(t, s.Apply("bob", []sheet.Edit{{Key: "a", Value: 3}}, 8), sheet.CodeProtected)

	// 保护者解除后他人可写。
	wantOKValue(t, s.Unprotect("alice", "a", 9))
	if _, ok := s.Protector("a"); ok {
		t.Fatalf("解除后仍有保护者")
	}
	wantOKValue(t, s.Apply("bob", []sheet.Edit{{Key: "a", Value: 3}}, 10))
}

// Apply 的保护检查覆盖无效编辑：对受保护的空单元的清除（无效编辑）
// 也导致整个操作报受保护, 而不是无变化。
func TestApplyProtectionCoversInvalidEdits(t *testing.T) {
	s := newSvc(t, map[string]int{"alice": 3, "bob": 3})
	wantOKValue(t, s.Protect("bob", "x", 1))
	// x 为空, 清除本属无效编辑; 但因被 bob 保护, 报受保护。
	r := s.Apply("alice", []sheet.Edit{{Key: "x", Clear: true}}, 2)
	wantReject(t, r, sheet.CodeProtected)
	if r.Cell != "x" {
		t.Fatalf("冲突单元格 = %q, 期望 x", r.Cell)
	}
}

// 时钟：被接受的操作推进时钟; 被拒绝的操作不改变时钟。
func TestClockAdvanceAndRollback(t *testing.T) {
	s := newSvc(t, map[string]int{"alice": 3, "bob": 3})
	wantOKValue(t, s.Apply("alice", []sheet.Edit{{Key: "a", Value: 1}}, 5))

	// 时钟回退被拒。
	wantReject(t, s.Apply("alice", []sheet.Edit{{Key: "b", Value: 1}}, 4), sheet.CodeClockRollback)
	// 被拒绝的操作不改变时钟：now=5 仍被接受。
	wantOKValue(t, s.Apply("alice", []sheet.Edit{{Key: "b", Value: 1}}, 5))

	// 被接受的 Protect 也推进时钟。
	wantOKValue(t, s.Protect("alice", "c", 7))
	wantReject(t, s.Apply("alice", []sheet.Edit{{Key: "d", Value: 1}}, 6), sheet.CodeClockRollback)
	wantOKValue(t, s.Apply("alice", []sheet.Edit{{Key: "d", Value: 1}}, 7))

	// Undo/Redo 同样受时钟约束。
	wantReject(t, s.Undo("alice", 6), sheet.CodeClockRollback)
	wantOKValue(t, s.Undo("alice", 8))
	wantReject(t, s.Redo("alice", 7), sheet.CodeClockRollback)
	wantOKValue(t, s.Redo("alice", 8))
}

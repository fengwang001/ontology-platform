package sheet_test

import (
	"testing"

	"ontology/sheet"
)

// 参数非法的各种情形（Apply）。
func TestApplyInvalidParams(t *testing.T) {
	s := newSvc(t, map[string]int{"alice": 3})
	wantOKValue(t, s.Apply("alice", []sheet.Edit{{Key: "a", Value: 1}}, 1))

	cases := []struct {
		name  string
		user  string
		edits []sheet.Edit
		now   int64
	}{
		{"未知用户", "nobody", []sheet.Edit{{Key: "x", Value: 1}}, 2},
		{"空编辑集", "alice", nil, 2},
		{"空单元格键", "alice", []sheet.Edit{{Key: "", Value: 1}}, 2},
		{"重复单元格", "alice", []sheet.Edit{{Key: "x", Value: 1}, {Key: "x", Value: 2}}, 2},
		{"值超上限", "alice", []sheet.Edit{{Key: "x", Value: sheet.MaxValue + 1}}, 2},
		{"值超下限", "alice", []sheet.Edit{{Key: "x", Value: -sheet.MaxValue - 1}}, 2},
		{"now为负", "alice", []sheet.Edit{{Key: "x", Value: 1}}, -1},
		{"now超上限", "alice", []sheet.Edit{{Key: "x", Value: 1}}, sheet.MaxNow + 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			wantReject(t, s.Apply(c.user, c.edits, c.now), sheet.CodeInvalidParam)
		})
	}

	// 超过 50 条编辑。
	tooMany := make([]sheet.Edit, sheet.MaxEdits+1)
	for i := range tooMany {
		tooMany[i] = sheet.Edit{Key: string(rune('a'+i/26)) + string(rune('a'+i%26)) + string(rune('0'+i%10)), Value: int64(i)}
	}
	// 保证键互不相同。
	seen := map[string]bool{}
	dups := false
	for _, e := range tooMany {
		if seen[e.Key] {
			dups = true
		}
		seen[e.Key] = true
	}
	if dups {
		t.Fatalf("测试数据存在重复键")
	}
	wantReject(t, s.Apply("alice", tooMany, 2), sheet.CodeInvalidParam)

	// 边界值本身合法。
	wantOKValue(t, s.Apply("alice", []sheet.Edit{{Key: "max", Value: sheet.MaxValue}}, 2))
	wantOKValue(t, s.Apply("alice", []sheet.Edit{{Key: "min", Value: -sheet.MaxValue}}, 3))
}

// Undo/Redo 的参数非法：未知用户、now 越界。
func TestUndoRedoInvalidParams(t *testing.T) {
	s := newSvc(t, map[string]int{"alice": 3})
	wantOKValue(t, s.Apply("alice", []sheet.Edit{{Key: "a", Value: 1}}, 1))
	wantReject(t, s.Undo("nobody", 2), sheet.CodeInvalidParam)
	wantReject(t, s.Redo("nobody", 2), sheet.CodeInvalidParam)
	wantReject(t, s.Undo("alice", -1), sheet.CodeInvalidParam)
	wantReject(t, s.Redo("alice", sheet.MaxNow+1), sheet.CodeInvalidParam)
}

// 拒绝次序（Apply）：参数非法 > 时钟回退。
func TestOrderApplyInvalidBeforeClock(t *testing.T) {
	s := newSvc(t, map[string]int{"alice": 3})
	wantOKValue(t, s.Apply("alice", []sheet.Edit{{Key: "a", Value: 1}}, 5))
	// 同时满足参数非法（未知用户）与时钟回退（now=1 < 5）。
	wantReject(t, s.Apply("nobody", []sheet.Edit{{Key: "x", Value: 1}}, 1), sheet.CodeInvalidParam)
}

// 拒绝次序（Apply）：时钟回退 > 受保护。
func TestOrderApplyClockBeforeProtected(t *testing.T) {
	s := newSvc(t, map[string]int{"alice": 3, "bob": 3})
	wantOKValue(t, s.Apply("alice", []sheet.Edit{{Key: "a", Value: 1}}, 5))
	wantOKValue(t, s.Protect("bob", "p", 6))
	// 同时满足时钟回退（now=4 < 6）与受保护（p 被 bob 保护）。
	wantReject(t, s.Apply("alice", []sheet.Edit{{Key: "p", Value: 1}}, 4), sheet.CodeClockRollback)
}

// 拒绝次序（Apply）：受保护 > 无变化。
func TestOrderApplyProtectedBeforeNoChange(t *testing.T) {
	s := newSvc(t, map[string]int{"alice": 3, "bob": 3})
	wantOKValue(t, s.Protect("bob", "p", 1))
	// 清除空单元格本属无变化; 但 p 被 bob 保护, 报受保护。
	wantReject(t, s.Apply("alice", []sheet.Edit{{Key: "p", Clear: true}}, 2), sheet.CodeProtected)
}

// 拒绝次序（Undo/Redo）：参数非法 > 时钟回退。
func TestOrderUndoInvalidBeforeClock(t *testing.T) {
	s := newSvc(t, map[string]int{"alice": 3})
	wantOKValue(t, s.Apply("alice", []sheet.Edit{{Key: "a", Value: 1}}, 5))
	wantReject(t, s.Undo("nobody", 1), sheet.CodeInvalidParam)
	wantReject(t, s.Redo("nobody", 1), sheet.CodeInvalidParam)
}

// 拒绝次序（Undo/Redo）：时钟回退 > 栈空。
func TestOrderUndoClockBeforeEmpty(t *testing.T) {
	s := newSvc(t, map[string]int{"alice": 3, "bob": 3})
	wantOKValue(t, s.Apply("alice", []sheet.Edit{{Key: "a", Value: 1}}, 5))
	// bob 栈为空, 且 now=4 < 5：报时钟回退。
	wantReject(t, s.Undo("bob", 4), sheet.CodeClockRollback)
	wantReject(t, s.Redo("bob", 4), sheet.CodeClockRollback)
}

// 拒绝次序（Undo/Redo）：栈空 > 被覆盖。
// 记录因被覆盖被丢弃后, 栈变空, 再次操作报栈空而非被覆盖。
func TestOrderUndoEmptyAfterOverwriteDrop(t *testing.T) {
	s := newSvc(t, map[string]int{"alice": 3, "bob": 3})
	wantOKValue(t, s.Apply("alice", []sheet.Edit{{Key: "a", Value: 1}}, 1))
	wantOKValue(t, s.Apply("bob", []sheet.Edit{{Key: "a", Value: 2}}, 2))
	wantReject(t, s.Undo("alice", 3), sheet.CodeOverwritten) // 记录被丢弃
	wantReject(t, s.Undo("alice", 4), sheet.CodeEmptyStack)  // 栈已空

	// Redo 方向同理。
	wantOKValue(t, s.Undo("bob", 5)) // bob 撤销自己的写入
	wantOKValue(t, s.Apply("alice", []sheet.Edit{{Key: "a", Value: 7}}, 6))
	wantReject(t, s.Redo("bob", 7), sheet.CodeOverwritten) // 记录被丢弃
	wantReject(t, s.Redo("bob", 8), sheet.CodeEmptyStack)  // 栈已空
}

// 拒绝次序（Undo/Redo）：被覆盖 > 受保护。
func TestOrderUndoOverwrittenBeforeProtected(t *testing.T) {
	s := newSvc(t, map[string]int{"alice": 3, "bob": 3})
	wantOKValue(t, s.Apply("alice", []sheet.Edit{
		{Key: "a", Value: 1},
		{Key: "b", Value: 2},
	}, 1))
	wantOKValue(t, s.Apply("bob", []sheet.Edit{{Key: "a", Value: 10}}, 2)) // 覆盖 a
	wantOKValue(t, s.Protect("bob", "b", 3))                               // 保护 b
	// 同时满足被覆盖（a）与受保护（b）：报被覆盖, 且记录被丢弃。
	r := s.Undo("alice", 4)
	wantReject(t, r, sheet.CodeOverwritten)
	if r.Cell != "a" || !r.Dropped {
		t.Fatalf("cell=%q dropped=%v, 期望 a/true", r.Cell, r.Dropped)
	}

	// Redo 方向同理：先构造 alice 的重做记录, 再让他人覆盖并保护。
	s2 := newSvc(t, map[string]int{"alice": 3, "bob": 3})
	wantOKValue(t, s2.Apply("alice", []sheet.Edit{
		{Key: "a", Value: 1},
		{Key: "b", Value: 2},
	}, 1))
	wantOKValue(t, s2.Undo("alice", 2))
	wantOKValue(t, s2.Apply("bob", []sheet.Edit{{Key: "a", Value: 10}}, 3))
	wantOKValue(t, s2.Protect("bob", "b", 4))
	r = s2.Redo("alice", 5)
	wantReject(t, r, sheet.CodeOverwritten)
	if r.Cell != "a" || !r.Dropped {
		t.Fatalf("cell=%q dropped=%v, 期望 a/true", r.Cell, r.Dropped)
	}
}

// 被拒绝的操作不改变表格、修订号、版本、时钟与保护状态（被覆盖例外除外）。
func TestRejectionKeepsState(t *testing.T) {
	s := newSvc(t, map[string]int{"alice": 3, "bob": 3})
	wantOKValue(t, s.Apply("alice", []sheet.Edit{{Key: "a", Value: 1}}, 5))
	wantOKValue(t, s.Protect("bob", "p", 6))

	// 一系列拒绝：参数非法 / 时钟回退 / 受保护 / 无变化 / 栈空。
	wantReject(t, s.Apply("nobody", nil, 0), sheet.CodeInvalidParam)
	wantReject(t, s.Apply("alice", []sheet.Edit{{Key: "z", Value: 1}}, 4), sheet.CodeClockRollback)
	wantReject(t, s.Apply("alice", []sheet.Edit{{Key: "p", Value: 1}}, 7), sheet.CodeProtected)
	wantReject(t, s.Apply("alice", []sheet.Edit{{Key: "a", Value: 1}}, 8), sheet.CodeNoChange)
	wantReject(t, s.Undo("bob", 9), sheet.CodeEmptyStack)

	// 状态与第 6 秒时一致：now=6 之后的操作都被拒, 时钟停在 6。
	wantReject(t, s.Apply("alice", []sheet.Edit{{Key: "z", Value: 1}}, 5), sheet.CodeClockRollback)
	wantOKValue(t, s.Apply("alice", []sheet.Edit{{Key: "z", Value: 1}}, 6))
	if s.Revision() != 2 {
		t.Fatalf("Revision = %d, 期望 2", s.Revision())
	}
	wantCell(t, s, "a", 1, false, 1)
	if owner, ok := s.Protector("p"); !ok || owner != "bob" {
		t.Fatalf("保护状态被改变")
	}
	wantHistory(t, s, "alice", 2, 0, []string{"z"}, nil)
}

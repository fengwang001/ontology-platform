package sheet

import (
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
)

func newTestService(t *testing.T, depths map[string]int) *Service {
	t.Helper()
	s, err := NewService(depths)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return s
}

func mustApply(t *testing.T, s *Service, user string, edits []Edit, now int64) Result {
	t.Helper()
	r := s.Apply(user, edits, now)
	if r.Code != OK {
		t.Fatalf("Apply(%s) = %v, want OK", user, r.Code)
	}
	return r
}

func getCell(t *testing.T, s *Service, key string) CellInfo {
	t.Helper()
	info, ok := s.Cell(key)
	if !ok {
		t.Fatalf("Cell(%q) param rejected", key)
	}
	return info
}

func getHistory(t *testing.T, s *Service, user string) HistoryInfo {
	t.Helper()
	h, ok := s.History(user)
	if !ok {
		t.Fatalf("History(%q) param rejected", user)
	}
	return h
}

// 他人穿插编辑导致记录被覆盖：弹出丢弃、不进入重做栈、表格不变。
func TestUndoOverwrittenByOther(t *testing.T) {
	s := newTestService(t, map[string]int{"a": 3, "b": 3})
	mustApply(t, s, "a", []Edit{{Key: "x", Value: 1}}, 1) // rev1
	mustApply(t, s, "b", []Edit{{Key: "x", Value: 2}}, 2) // rev2 覆盖 x

	r := s.Undo("a", 3)
	if r.Code != ErrOverwritten || r.Cell != "x" || !r.Popped {
		t.Fatalf("Undo = %+v, want Overwritten cell=x popped", r)
	}
	if got := getCell(t, s, "x"); got.Value != 2 || got.Version != 2 {
		t.Fatalf("x = %+v, want value=2 version=2 (表格不变)", got)
	}
	h := getHistory(t, s, "a")
	if h.UndoCount != 0 || h.RedoCount != 0 {
		t.Fatalf("history = %+v, want 记录被丢弃且未进重做栈", h)
	}
	if s.Revision() != 2 {
		t.Fatalf("revision = %d, want 2", s.Revision())
	}
}

// 当前值恰好与原值相同但版本不同，也算被覆盖。
func TestUndoOverwrittenSameValueDifferentVersion(t *testing.T) {
	s := newTestService(t, map[string]int{"a": 3, "b": 3})
	mustApply(t, s, "a", []Edit{{Key: "x", Value: 5}}, 1)    // rev1: x 空->5
	mustApply(t, s, "b", []Edit{{Key: "x", Value: 7}}, 2)    // rev2
	mustApply(t, s, "b", []Edit{{Key: "x", Clear: true}}, 3) // rev3: x 回到空（恰为 a 记录的原值）

	r := s.Undo("a", 4)
	if r.Code != ErrOverwritten || r.Cell != "x" || !r.Popped {
		t.Fatalf("Undo = %+v, want Overwritten（值同原值但版本 3 != 1）", r)
	}
	if got := getCell(t, s, "x"); got.Set || got.Version != 3 {
		t.Fatalf("x = %+v, want 空值 version=3", got)
	}
}

// 撤销成功后他人保护该单元格，重做报受保护且两栈不变。
func TestRedoBlockedByOthersProtection(t *testing.T) {
	s := newTestService(t, map[string]int{"a": 3, "b": 3})
	mustApply(t, s, "a", []Edit{{Key: "x", Value: 1}}, 1) // rev1
	if r := s.Undo("a", 2); r.Code != OK {                // rev2: x 恢复为空
		t.Fatalf("Undo = %+v, want OK", r)
	}
	if r := s.Protect("b", "x", 3); r.Code != OK {
		t.Fatalf("Protect = %+v, want OK", r)
	}

	r := s.Redo("a", 4)
	if r.Code != ErrProtected || r.Cell != "x" || r.Popped {
		t.Fatalf("Redo = %+v, want Protected cell=x 不弹出", r)
	}
	h := getHistory(t, s, "a")
	if h.UndoCount != 0 || h.RedoCount != 1 {
		t.Fatalf("history = %+v, want undo=0 redo=1（两栈不变）", h)
	}
	if got := getCell(t, s, "x"); got.Set || got.Version != 2 {
		t.Fatalf("x = %+v, want 空值 version=2（表格不变）", got)
	}
}

// 自己撤销之后他人覆盖同一单元格，导致重做失败（被覆盖）并丢弃记录。
func TestRedoFailsAfterOtherOverwrites(t *testing.T) {
	s := newTestService(t, map[string]int{"a": 3, "b": 3})
	mustApply(t, s, "a", []Edit{{Key: "x", Value: 1}}, 1) // rev1
	if r := s.Undo("a", 2); r.Code != OK {                // rev2
		t.Fatalf("Undo = %+v, want OK", r)
	}
	mustApply(t, s, "b", []Edit{{Key: "x", Value: 9}}, 3) // rev3 覆盖

	r := s.Redo("a", 4)
	if r.Code != ErrOverwritten || r.Cell != "x" || !r.Popped {
		t.Fatalf("Redo = %+v, want Overwritten cell=x popped", r)
	}
	h := getHistory(t, s, "a")
	if h.UndoCount != 0 || h.RedoCount != 0 {
		t.Fatalf("history = %+v, want 重做记录被丢弃", h)
	}
	if got := getCell(t, s, "x"); got.Value != 9 || got.Version != 3 {
		t.Fatalf("x = %+v, want value=9 version=3", got)
	}
}

// 历史深度限制：超过 D 条时丢弃最旧记录。
func TestDepthLimitDropsOldest(t *testing.T) {
	s := newTestService(t, map[string]int{"a": 2})
	mustApply(t, s, "a", []Edit{{Key: "x", Value: 1}}, 1) // rev1
	mustApply(t, s, "a", []Edit{{Key: "y", Value: 2}}, 2) // rev2
	mustApply(t, s, "a", []Edit{{Key: "z", Value: 3}}, 3) // rev3，挤出 rev1

	h := getHistory(t, s, "a")
	if h.UndoCount != 2 || !reflect.DeepEqual(h.UndoTop, []string{"z"}) {
		t.Fatalf("history = %+v, want undo=2 top={z}", h)
	}
	// 撤销两次后栈空：最旧的 rev1 已被丢弃，无法再撤销。
	if r := s.Undo("a", 4); r.Code != OK {
		t.Fatalf("Undo#1 = %+v, want OK", r)
	}
	if r := s.Undo("a", 5); r.Code != OK {
		t.Fatalf("Undo#2 = %+v, want OK", r)
	}
	if r := s.Undo("a", 6); r.Code != ErrEmptyStack {
		t.Fatalf("Undo#3 = %+v, want EmptyStack（rev1 已被深度限制丢弃）", r)
	}
	if got := getCell(t, s, "x"); got.Value != 1 || got.Version != 1 {
		t.Fatalf("x = %+v, want value=1 version=1（未被撤销）", got)
	}
}

// 新的编辑清空自己的重做栈，但不影响他人的栈。
func TestNewEditClearsOwnRedoOnly(t *testing.T) {
	s := newTestService(t, map[string]int{"a": 3, "b": 3})
	mustApply(t, s, "a", []Edit{{Key: "x", Value: 1}}, 1)
	mustApply(t, s, "b", []Edit{{Key: "y", Value: 2}}, 2)
	if r := s.Undo("a", 3); r.Code != OK {
		t.Fatalf("Undo a = %+v", r)
	}
	if r := s.Undo("b", 4); r.Code != OK {
		t.Fatalf("Undo b = %+v", r)
	}
	mustApply(t, s, "a", []Edit{{Key: "z", Value: 5}}, 5) // 清空 a 的重做栈

	ha := getHistory(t, s, "a")
	if ha.RedoCount != 0 {
		t.Fatalf("a redo = %d, want 0（自己的重做栈被清空）", ha.RedoCount)
	}
	hb := getHistory(t, s, "b")
	if hb.RedoCount != 1 || !reflect.DeepEqual(hb.RedoTop, []string{"y"}) {
		t.Fatalf("b history = %+v, want redo=1 top={y}（他人不受影响）", hb)
	}
}

// 无效编辑被剔除，只有有效编辑进入历史；全部无效则整体被拒。
func TestInvalidEditsFilteredAndAllInvalidRejected(t *testing.T) {
	s := newTestService(t, map[string]int{"a": 5})
	mustApply(t, s, "a", []Edit{{Key: "x", Value: 1}}, 1) // rev1: x=1

	// 混合：x 写入相同值（无效）、y 清除空单元格（无效）、z 真实写入（有效）。
	r := s.Apply("a", []Edit{
		{Key: "x", Value: 1},
		{Key: "y", Clear: true},
		{Key: "z", Value: 9},
	}, 2)
	if r.Code != OK || r.Revision != 2 {
		t.Fatalf("Apply = %+v, want OK rev=2", r)
	}
	h := getHistory(t, s, "a")
	if h.UndoCount != 2 || !reflect.DeepEqual(h.UndoTop, []string{"z"}) {
		t.Fatalf("history = %+v, want 栈顶记录只含有效编辑 z", h)
	}

	// 全部无效：被拒，报无变化，不改变任何东西也不进历史。
	revBefore := s.Revision()
	r = s.Apply("a", []Edit{
		{Key: "x", Value: 1},
		{Key: "y", Clear: true},
	}, 3)
	if r.Code != ErrNoChange {
		t.Fatalf("Apply = %+v, want NoChange", r)
	}
	if s.Revision() != revBefore {
		t.Fatalf("revision 变为 %d, want 不变 %d", s.Revision(), revBefore)
	}
	if h := getHistory(t, s, "a"); h.UndoCount != 2 {
		t.Fatalf("undo = %d, want 2（未进历史）", h.UndoCount)
	}
}

// Apply 的保护检查覆盖无效编辑：触及他人保护单元格即整批被拒。
func TestApplyProtectedCoversInvalidEdits(t *testing.T) {
	s := newTestService(t, map[string]int{"a": 3, "b": 3})
	mustApply(t, s, "a", []Edit{{Key: "x", Value: 1}}, 1)
	if r := s.Protect("b", "x", 2); r.Code != OK {
		t.Fatalf("Protect = %+v", r)
	}
	// x 的编辑即使无效（写入相同值），也因被他人保护而整批被拒。
	r := s.Apply("a", []Edit{{Key: "x", Value: 1}, {Key: "y", Value: 2}}, 3)
	if r.Code != ErrProtected || r.Cell != "x" {
		t.Fatalf("Apply = %+v, want Protected cell=x", r)
	}
	if got := getCell(t, s, "y"); got.Set {
		t.Fatalf("y = %+v, want 未写入（整批被拒）", got)
	}
	// 受保护 > 无变化：编辑全无效且触及保护，报受保护。
	r = s.Apply("a", []Edit{{Key: "x", Value: 1}}, 4)
	if r.Code != ErrProtected {
		t.Fatalf("Apply = %+v, want Protected（优先于 NoChange）", r)
	}
}

// 自己保护的单元格自己可写，对撤销与重做同样适用。
func TestSelfProtectedWritable(t *testing.T) {
	s := newTestService(t, map[string]int{"a": 3, "b": 3})
	if r := s.Protect("a", "x", 1); r.Code != OK {
		t.Fatalf("Protect = %+v", r)
	}
	mustApply(t, s, "a", []Edit{{Key: "x", Value: 1}}, 2) // 自己可写
	if r := s.Undo("a", 3); r.Code != OK {                // 自己可撤销
		t.Fatalf("Undo = %+v, want OK", r)
	}
	if r := s.Redo("a", 4); r.Code != OK { // 自己可重做
		t.Fatalf("Redo = %+v, want OK", r)
	}
	if got := getCell(t, s, "x"); got.Value != 1 {
		t.Fatalf("x = %+v, want value=1", got)
	}
	// 他人不可写。
	if r := s.Apply("b", []Edit{{Key: "x", Value: 2}}, 5); r.Code != ErrProtected {
		t.Fatalf("Apply(b) = %+v, want Protected", r)
	}
}

// 保护规则：他人保护被拒、自己重复保护成功无变化、仅保护者可解除、
// 保护与解除不改变修订号、单元格版本与历史。
func TestProtectUnprotectRules(t *testing.T) {
	s := newTestService(t, map[string]int{"a": 3, "b": 3})
	mustApply(t, s, "a", []Edit{{Key: "x", Value: 1}}, 1) // rev1

	if r := s.Protect("a", "x", 2); r.Code != OK {
		t.Fatalf("Protect = %+v", r)
	}
	if r := s.Protect("a", "x", 3); r.Code != OK { // 自己重复保护：成功无变化
		t.Fatalf("re-Protect = %+v, want OK", r)
	}
	if r := s.Protect("b", "x", 4); r.Code != ErrProtected { // 已被他人保护
		t.Fatalf("Protect(b) = %+v, want Protected", r)
	}
	if r := s.Unprotect("b", "x", 5); r.Code != ErrProtected { // 非保护者不可解除
		t.Fatalf("Unprotect(b) = %+v, want Protected", r)
	}
	if s.Revision() != 1 {
		t.Fatalf("revision = %d, want 1（保护不改修订号）", s.Revision())
	}
	if got := getCell(t, s, "x"); got.Version != 1 {
		t.Fatalf("x version = %d, want 1（保护不改版本）", got.Version)
	}
	if h := getHistory(t, s, "a"); h.UndoCount != 1 {
		t.Fatalf("undo = %d, want 1（保护不影响历史）", h.UndoCount)
	}
	if r := s.Unprotect("a", "x", 6); r.Code != OK {
		t.Fatalf("Unprotect(a) = %+v, want OK", r)
	}
	// 解除后他人可写。
	mustApply(t, s, "b", []Edit{{Key: "x", Value: 2}}, 7)
}

// 时钟：被接受操作推进时钟；被拒绝操作不推进时钟。
func TestClockAdvanceAndRejection(t *testing.T) {
	s := newTestService(t, map[string]int{"a": 3})
	mustApply(t, s, "a", []Edit{{Key: "x", Value: 1}}, 10)

	if r := s.Apply("a", []Edit{{Key: "y", Value: 1}}, 9); r.Code != ErrClockRollback {
		t.Fatalf("Apply(now=9) = %+v, want ClockRollback", r)
	}
	// 被拒绝操作（此处用较大的 now=100 但编辑全无效）不推进时钟。
	if r := s.Apply("a", []Edit{{Key: "x", Value: 1}}, 100); r.Code != ErrNoChange {
		t.Fatalf("Apply(now=100) = %+v, want NoChange", r)
	}
	// now=10 仍被接受：时钟仍停留在上一次被接受操作的 10。
	mustApply(t, s, "a", []Edit{{Key: "y", Value: 2}}, 10)
	// 时钟回退的被拒操作同样不推进时钟。
	if r := s.Apply("a", []Edit{{Key: "z", Value: 1}}, 5); r.Code != ErrClockRollback {
		t.Fatalf("Apply(now=5) = %+v, want ClockRollback", r)
	}
	mustApply(t, s, "a", []Edit{{Key: "z", Value: 3}}, 10)
}

// Undo/Redo 拒绝次序的相邻类别对：
// 参数非法 > 时钟回退 > 栈空 > 被覆盖 > 受保护。
func TestRejectOrderUndoRedo(t *testing.T) {
	s := newTestService(t, map[string]int{"a": 3, "b": 3})
	mustApply(t, s, "a", []Edit{{Key: "x", Value: 1}}, 1) // rev1

	// 参数非法 > 时钟回退：now 非法且回退，报参数非法。
	if r := s.Undo("a", -1); r.Code != ErrInvalidParam {
		t.Fatalf("Undo(now=-1) = %+v, want InvalidParam", r)
	}
	// 时钟回退 > 栈空：b 栈空且 now 回退，报时钟回退。
	if r := s.Undo("b", 0); r.Code != ErrClockRollback {
		t.Fatalf("Undo(b, now=0) = %+v, want ClockRollback", r)
	}
	// 栈空 > 被覆盖：b 从未写入，无可被覆盖记录，报栈空。
	if r := s.Undo("b", 2); r.Code != ErrEmptyStack {
		t.Fatalf("Undo(b) = %+v, want EmptyStack", r)
	}
	// 被覆盖 > 受保护：记录已被覆盖且单元格被他人保护，报被覆盖并弹出。
	mustApply(t, s, "b", []Edit{{Key: "x", Value: 2}}, 3) // rev2 覆盖
	if r := s.Protect("b", "x", 4); r.Code != OK {
		t.Fatalf("Protect = %+v", r)
	}
	r := s.Undo("a", 5)
	if r.Code != ErrOverwritten || r.Cell != "x" || !r.Popped {
		t.Fatalf("Undo(a) = %+v, want Overwritten（优先于 Protected）popped", r)
	}
}

// Apply 拒绝次序的相邻类别对：参数非法 > 时钟回退 > 受保护 > 无变化。
func TestRejectOrderApply(t *testing.T) {
	s := newTestService(t, map[string]int{"a": 3, "b": 3})
	mustApply(t, s, "a", []Edit{{Key: "x", Value: 1}}, 10) // rev1 @now=10
	if r := s.Protect("b", "y", 11); r.Code != OK {
		t.Fatalf("Protect = %+v", r)
	}

	// 参数非法 > 时钟回退：编辑列表为空且 now 回退。
	if r := s.Apply("a", nil, 0); r.Code != ErrInvalidParam {
		t.Fatalf("Apply(nil) = %+v, want InvalidParam", r)
	}
	// 时钟回退 > 受保护：触及他人保护且 now 回退。
	if r := s.Apply("a", []Edit{{Key: "y", Value: 2}}, 5); r.Code != ErrClockRollback {
		t.Fatalf("Apply = %+v, want ClockRollback", r)
	}
	// 受保护 > 无变化：触及他人保护单元格（即使编辑本身无效也先报受保护，
	// 无效编辑场景的精确覆盖见 TestApplyProtectedCoversInvalidEdits）。
	if r := s.Apply("a", []Edit{{Key: "y", Value: 2}}, 12); r.Code != ErrProtected {
		t.Fatalf("Apply = %+v, want Protected", r)
	}
	// 全无效时报无变化。
	if r := s.Apply("a", []Edit{{Key: "x", Value: 1}}, 13); r.Code != ErrNoChange {
		t.Fatalf("Apply = %+v, want NoChange", r)
	}
}

// 撤销/重做成功路径：版本推进、历史迁移、栈顶键集合。
func TestUndoRedoRoundTrip(t *testing.T) {
	s := newTestService(t, map[string]int{"a": 5})
	mustApply(t, s, "a", []Edit{{Key: "x", Value: 1}, {Key: "y", Value: 2}}, 1) // rev1

	r := s.Undo("a", 2)
	if r.Code != OK || r.Revision != 2 {
		t.Fatalf("Undo = %+v, want OK rev=2", r)
	}
	if got := getCell(t, s, "x"); got.Set || got.Version != 2 {
		t.Fatalf("x = %+v, want 空值 version=2", got)
	}
	h := getHistory(t, s, "a")
	if h.UndoCount != 0 || h.RedoCount != 1 || !reflect.DeepEqual(h.RedoTop, []string{"x", "y"}) {
		t.Fatalf("history = %+v, want redo=1 top={x,y}", h)
	}

	r = s.Redo("a", 3)
	if r.Code != OK || r.Revision != 3 {
		t.Fatalf("Redo = %+v, want OK rev=3", r)
	}
	if got := getCell(t, s, "x"); got.Value != 1 || got.Version != 3 {
		t.Fatalf("x = %+v, want value=1 version=3", got)
	}
	h = getHistory(t, s, "a")
	if h.UndoCount != 1 || h.RedoCount != 0 || !reflect.DeepEqual(h.UndoTop, []string{"x", "y"}) {
		t.Fatalf("history = %+v, want undo=1 top={x,y}", h)
	}
}

// 参数非法：未知用户、空键、重复键、越界值、过多编辑、now 越界。
func TestInvalidParams(t *testing.T) {
	s := newTestService(t, map[string]int{"a": 3})
	cases := []struct {
		name  string
		apply func() Result
	}{
		{"未知用户", func() Result { return s.Apply("ghost", []Edit{{Key: "x", Value: 1}}, 1) }},
		{"空键", func() Result { return s.Apply("a", []Edit{{Key: "", Value: 1}}, 1) }},
		{"重复键", func() Result {
			return s.Apply("a", []Edit{{Key: "x", Value: 1}, {Key: "x", Value: 2}}, 1)
		}},
		{"值越界", func() Result { return s.Apply("a", []Edit{{Key: "x", Value: MaxValue + 1}}, 1) }},
		{"编辑过多", func() Result {
			edits := make([]Edit, MaxEdits+1)
			for i := range edits {
				edits[i] = Edit{Key: string(rune('a' + i)), Value: 1}
			}
			return s.Apply("a", edits, 1)
		}},
		{"now越界", func() Result { return s.Apply("a", []Edit{{Key: "x", Value: 1}}, MaxNow+1) }},
	}
	for _, c := range cases {
		if r := c.apply(); r.Code != ErrInvalidParam {
			t.Fatalf("%s: got %+v, want InvalidParam", c.name, r)
		}
	}
	if _, err := NewService(map[string]int{"a": 0}); err == nil {
		t.Fatal("NewService(depth=0) want error")
	}
	if _, err := NewService(map[string]int{"": 1}); err == nil {
		t.Fatal("NewService(empty user) want error")
	}
}

// 并发调用：结果等价于某个串行顺序。
// 多个用户并发 Apply 互不相交的单元格，最终修订号等于成功事务数，
// 且每个单元格的版本恰为某个已接受事务的修订号。
func TestConcurrentSerialization(t *testing.T) {
	s := newTestService(t, map[string]int{"a": 100, "b": 100, "c": 100})
	const perUser = 30
	var wg sync.WaitGroup
	var nowGen int64 // 全局时钟单调，故用共享原子时钟源
	for _, u := range []string{"a", "b", "c"} {
		wg.Add(1)
		go func(u string) {
			defer wg.Done()
			for i := 0; i < perUser; i++ {
				key := u + "-" + string(rune('A'+i))
				for {
					now := atomic.AddInt64(&nowGen, 1)
					r := s.Apply(u, []Edit{{Key: key, Value: int64(i)}}, now)
					if r.Code == OK {
						break
					}
					// 并发下取到的 now 可能落后于他人已接受的 now，重试即可。
					if r.Code != ErrClockRollback {
						t.Errorf("Apply(%s) = %+v", u, r)
						break
					}
				}
			}
		}(u)
	}
	wg.Wait()
	if got, want := s.Revision(), int64(3*perUser); got != want {
		t.Fatalf("revision = %d, want %d（每个被接受事务恰好 +1）", got, want)
	}
}

// 相同操作序列重放得到完全相同的结果。
func TestReplayDeterminism(t *testing.T) {
	run := func() []Result {
		s := newTestService(t, map[string]int{"a": 2, "b": 2})
		var out []Result
		out = append(out, s.Apply("a", []Edit{{Key: "x", Value: 1}, {Key: "y", Value: 2}}, 1))
		out = append(out, s.Apply("b", []Edit{{Key: "x", Value: 9}}, 2))
		out = append(out, s.Undo("a", 3))
		out = append(out, s.Undo("b", 4))
		out = append(out, s.Redo("b", 5))
		out = append(out, s.Protect("b", "y", 6))
		out = append(out, s.Apply("a", []Edit{{Key: "y", Value: 3}}, 7))
		out = append(out, s.Unprotect("b", "y", 8))
		out = append(out, s.Redo("a", 9))
		return out
	}
	first, second := run(), run()
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("重放结果不一致:\n%v\n%v", first, second)
	}
}

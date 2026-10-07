package scheduler

import (
	"fmt"
	"reflect"
	"sync"
	"testing"
)

func setupSched(t *testing.T, ngroups int) *Scheduler {
	t.Helper()
	s := NewScheduler(10)
	mustOK(t, s.AddRegion("R"))
	for i := 1; i <= ngroups; i++ {
		mustOK(t, s.AddGroup("R", fmt.Sprintf("g%d", i)))
	}
	return s
}

func mustOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func mustKind(t *testing.T, err error, k Kind) {
	t.Helper()
	e, ok := err.(*Error)
	if !ok {
		t.Fatalf("want *Error kind %d, got %v", k, err)
	}
	if e.Kind != k {
		t.Fatalf("want kind %d, got %d (%v)", k, e.Kind, e)
	}
}

func checkHistory(t *testing.T, s *Scheduler, want []SlotRecord) {
	t.Helper()
	if got := s.History(); !reflect.DeepEqual(got, want) {
		t.Fatalf("history mismatch:\n got: %+v\nwant: %+v", got, want)
	}
}

func checkAccum(t *testing.T, s *Scheduler, want map[string]int64) {
	t.Helper()
	for g, w := range want {
		if got, _ := s.Accum(g); got != w {
			t.Fatalf("accum[%s] = %d, want %d", g, got, w)
		}
	}
}

// 累计时长相同按组编号小者优先。
func TestTieBreakByGroupID(t *testing.T) {
	s := setupSched(t, 3)
	mustOK(t, s.Issue("i1", "R", 1, 10, 40))
	mustOK(t, s.AdvanceTo(40))
	checkHistory(t, s, []SlotRecord{
		{Start: 10, Level: 1, Groups: []string{"g1"}},
		{Start: 20, Level: 1, Groups: []string{"g2"}},
		{Start: 30, Level: 1, Groups: []string{"g3"}},
		{Start: 40, Level: 0, Groups: nil},
	})
	checkAccum(t, s, map[string]int64{"g1": 10, "g2": 10, "g3": 10})
}

// 等级等于组数时全选。
func TestLevelEqualsGroupCount(t *testing.T) {
	s := setupSched(t, 3)
	mustOK(t, s.Issue("i1", "R", 3, 10, 30))
	mustOK(t, s.AdvanceTo(30))
	checkHistory(t, s, []SlotRecord{
		{Start: 10, Level: 3, Groups: []string{"g1", "g2", "g3"}},
		{Start: 20, Level: 3, Groups: []string{"g1", "g2", "g3"}},
		{Start: 30, Level: 0, Groups: nil},
	})
	checkAccum(t, s, map[string]int64{"g1": 20, "g2": 20, "g3": 20})
}

// 改级恰在时段边界：对当前时段立即生效。
func TestModifyExactlyAtBoundary(t *testing.T) {
	s := setupSched(t, 3)
	mustOK(t, s.Issue("i1", "R", 1, 10, 50))
	mustOK(t, s.AdvanceTo(20))
	// 当前时段 [20,30) 原选 g2；恰在边界 20 改级为 2，当前时段立即重选。
	mustOK(t, s.Modify("i1", 2))
	checkHistory(t, s, []SlotRecord{
		{Start: 10, Level: 1, Groups: []string{"g1"}},
		{Start: 20, Level: 2, Groups: []string{"g2", "g3"}},
	})
	if got := s.CurrentSelection(); !reflect.DeepEqual(got, []string{"g2", "g3"}) {
		t.Fatalf("current selection = %v", got)
	}
}

// 改级在边界之后一刻：当前时段不变，自下一时段起生效。
func TestModifyOneTickAfterBoundary(t *testing.T) {
	s := setupSched(t, 3)
	mustOK(t, s.Issue("i1", "R", 1, 10, 50))
	mustOK(t, s.AdvanceTo(21))
	mustOK(t, s.Modify("i1", 2))
	if got := s.CurrentSelection(); !reflect.DeepEqual(got, []string{"g2"}) {
		t.Fatalf("current selection changed: %v", got)
	}
	mustOK(t, s.AdvanceTo(40))
	checkHistory(t, s, []SlotRecord{
		{Start: 10, Level: 1, Groups: []string{"g1"}},
		{Start: 20, Level: 1, Groups: []string{"g2"}},
		{Start: 30, Level: 2, Groups: []string{"g3", "g1"}},
		{Start: 40, Level: 2, Groups: []string{"g2", "g3"}},
	})
}

// 免控用户不入通知名单；保底用户通知中携带保底功率；普通用户限到零。
func TestNotificationContent(t *testing.T) {
	s := setupSched(t, 3)
	mustOK(t, s.AddUser("u1", "g1", CatExempt, 0))
	mustOK(t, s.AddUser("u2", "g1", CatGuaranteed, 500))
	mustOK(t, s.AddUser("u3", "g1", CatNormal, 0))
	mustOK(t, s.AddUser("u4", "g2", CatNormal, 0))
	mustOK(t, s.Issue("i1", "R", 1, 10, 20))
	got := s.Notifications(10)
	want := []Notification{
		{Slot: 10, User: "u2", Group: "g1", BasePower: 500, HasBasePower: true, State: NotifPending},
		{Slot: 10, User: "u3", Group: "g1", State: NotifPending},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("notifications:\n got: %+v\nwant: %+v", got, want)
	}
}

// 用户须在时段开始前确认；恰在时段开始时刻确认被拒（已过截止）；
// 时段开始时未确认者记考核。
func TestConfirmDeadline(t *testing.T) {
	s := setupSched(t, 3)
	mustOK(t, s.AddUser("u2", "g1", CatGuaranteed, 500))
	mustOK(t, s.AddUser("u3", "g1", CatNormal, 0))
	mustOK(t, s.AddUser("u4", "g1", CatNormal, 0))
	mustOK(t, s.Issue("i1", "R", 1, 10, 20))
	mustOK(t, s.AdvanceTo(9))
	mustOK(t, s.Confirm("u2", 10))
	mustOK(t, s.Confirm("u3", 10))
	mustOK(t, s.AdvanceTo(10))
	// u4 未确认 → 考核
	if got := s.Assessments(); !reflect.DeepEqual(got, []Assessment{{Slot: 10, User: "u4", Group: "g1"}}) {
		t.Fatalf("assessments = %+v", got)
	}
	// 恰在时段开始时刻确认被拒
	mustKind(t, s.Confirm("u3", 10), KindDeadline)
	mustKind(t, s.Confirm("u4", 10), KindDeadline)
}

// 换组与改类别在用户所在组被限时段内被拒（时段进行中）。
func TestMoveGroupBusy(t *testing.T) {
	s := setupSched(t, 3)
	mustOK(t, s.AddUser("u3", "g1", CatNormal, 0))
	mustOK(t, s.Issue("i1", "R", 1, 10, 20))
	mustOK(t, s.AdvanceTo(15))
	mustKind(t, s.MoveGroup("u3", "g2"), KindBusy)
	mustKind(t, s.SetCategory("u3", CatExempt, 0), KindBusy)
	mustOK(t, s.AdvanceTo(20))
	mustOK(t, s.MoveGroup("u3", "g2"))
	mustOK(t, s.SetCategory("u3", CatGuaranteed, 300))
}

// 一次推进跨越多个边界时逐段结算。
func TestMultiBoundaryAdvance(t *testing.T) {
	s := setupSched(t, 3)
	mustOK(t, s.Issue("i1", "R", 1, 10, 40))
	mustOK(t, s.AdvanceTo(45))
	checkHistory(t, s, []SlotRecord{
		{Start: 10, Level: 1, Groups: []string{"g1"}},
		{Start: 20, Level: 1, Groups: []string{"g2"}},
		{Start: 30, Level: 1, Groups: []string{"g3"}},
		{Start: 40, Level: 0, Groups: nil},
	})
	checkAccum(t, s, map[string]int64{"g1": 10, "g2": 10, "g3": 10})
	if s.Now() != 45 {
		t.Fatalf("now = %d", s.Now())
	}
}

// 公平性不变量：连续限电结束后各组累计被限时长极差不超过一个时段长度。
func TestFairnessInvariant(t *testing.T) {
	s := setupSched(t, 4)
	mustOK(t, s.Issue("i1", "R", 2, 10, 2010))
	mustOK(t, s.AdvanceTo(2010))
	var minA, maxA int64 = -1, 0
	for _, g := range []string{"g1", "g2", "g3", "g4"} {
		a, _ := s.Accum(g)
		if minA < 0 || a < minA {
			minA = a
		}
		if a > maxA {
			maxA = a
		}
	}
	if maxA-minA > 10 {
		t.Fatalf("fairness violated: max=%d min=%d", maxA, minA)
	}
	// 200 个时段 × 2 组 / 4 组 = 每组恰被限 100 个时段
	checkAccum(t, s, map[string]int64{"g1": 1000, "g2": 1000, "g3": 1000, "g4": 1000})
}

// 拒绝次序固定，且被拒绝的操作不改变任何状态。
func TestRejectionOrder(t *testing.T) {
	s := setupSched(t, 2)
	mustOK(t, s.AddUser("u1", "g1", CatNormal, 0))
	mustOK(t, s.Issue("i1", "R", 1, 10, 20))
	mustOK(t, s.AdvanceTo(15)) // 时段 [10,20) 进行中，g1 被限
	mustOK(t, s.Cancel("i1"))  // 生效边界 20

	snap := func() any {
		return []any{s.History(), s.Assessments(), s.AllNotifications(), s.Now()}
	}
	before := snap()
	reject := func(err error, k Kind) {
		t.Helper()
		mustKind(t, err, k)
		if after := snap(); !reflect.DeepEqual(before, after) {
			t.Fatalf("rejected op mutated state")
		}
	}
	// 参数非法 > 时段进行中：用户不存在且组被限 → 参数非法
	reject(s.MoveGroup("nouser", "g2"), KindParam)
	// 参数非法 > 指令不存在：等级为 0 且指令已取消 → 参数非法
	reject(s.Modify("i1", 0), KindParam)
	// 指令不存在或已取消
	reject(s.Modify("i1", 1), KindInstr)
	reject(s.Cancel("i1"), KindInstr)
	reject(s.Cancel("ghost"), KindInstr)
	// 时段进行中
	reject(s.MoveGroup("u1", "g2"), KindBusy)
	reject(s.SetCategory("u1", CatExempt, 0), KindBusy)
	// 参数非法 > 已过截止：用户不存在且时段已过 → 参数非法
	reject(s.Confirm("nouser", 10), KindParam)
	// 已过截止 > 通知不存在：时段已过且无通知 → 已过截止
	reject(s.Confirm("u1", 10), KindDeadline)
	// 通知不存在：时段未到但无通知
	reject(s.Confirm("u1", 1000), KindNotif)
	// 时钟回退
	reject(s.AdvanceTo(14), KindClock)
}

// 指令发布参数校验。
func TestIssueValidation(t *testing.T) {
	s := setupSched(t, 2)
	mustKind(t, s.Issue("a", "noregion", 1, 10, 20), KindParam)
	mustKind(t, s.Issue("a", "R", 0, 10, 20), KindParam)
	mustKind(t, s.Issue("a", "R", 3, 10, 20), KindParam) // 等级超过组数
	mustKind(t, s.Issue("a", "R", 1, 15, 20), KindParam) // 未对齐
	mustKind(t, s.Issue("a", "R", 1, 10, 15), KindParam) // 未对齐
	mustKind(t, s.Issue("a", "R", 1, 20, 20), KindParam) // 空窗口
	mustKind(t, s.Issue("a", "R", 1, 0, 20), KindParam)  // 起点早于下一时段起点
	mustOK(t, s.Issue("a", "R", 1, 10, 20))
	mustKind(t, s.Issue("a", "R", 1, 30, 40), KindParam) // ID 重复
	mustOK(t, s.AdvanceTo(25))
	mustKind(t, s.Issue("b", "R", 1, 20, 40), KindParam) // 时段已开始
}

// 改级使组不再被选时撤回通知；已确认的撤回不计考核。
func TestWithdrawOnModify(t *testing.T) {
	s := setupSched(t, 3)
	mustOK(t, s.AddUser("a1", "g1", CatNormal, 0))
	mustOK(t, s.AddUser("b1", "g2", CatNormal, 0))
	mustOK(t, s.AddUser("c1", "g3", CatNormal, 0))
	mustOK(t, s.Issue("i1", "R", 2, 10, 40))
	// 预选：10→{g1,g2} 20→{g3,g1} 30→{g2,g3}
	mustOK(t, s.Confirm("b1", 10)) // b1 确认其在时段 10 的通知
	mustOK(t, s.Modify("i1", 1))   // 恰在边界 0 改级为 1
	// 新预选：10→{g1} 20→{g2} 30→{g3}；b1@10 被撤回（已确认，不计考核）
	for _, n := range s.Notifications(10) {
		if n.User == "b1" && n.State != NotifWithdrawn {
			t.Fatalf("b1@10 should be withdrawn, got %+v", n)
		}
	}
	mustOK(t, s.AdvanceTo(40))
	want := []Assessment{
		{Slot: 10, User: "a1", Group: "g1"},
		{Slot: 20, User: "b1", Group: "g2"},
		{Slot: 30, User: "c1", Group: "g3"},
	}
	if got := s.Assessments(); !reflect.DeepEqual(got, want) {
		t.Fatalf("assessments:\n got: %+v\nwant: %+v", got, want)
	}
}

// 窗口尚未开始的指令取消后视同从未存在。
func TestCancelBeforeStartNeverExisted(t *testing.T) {
	s := setupSched(t, 2)
	mustOK(t, s.AddUser("u1", "g1", CatNormal, 0))
	mustOK(t, s.Issue("i1", "R", 1, 10, 30))
	mustOK(t, s.Cancel("i1"))
	mustOK(t, s.AdvanceTo(50))
	for _, r := range s.History() {
		if r.Level != 0 {
			t.Fatalf("level = %d at %d", r.Level, r.Start)
		}
	}
	for _, n := range s.AllNotifications() {
		if n.State != NotifWithdrawn {
			t.Fatalf("notification should be withdrawn: %+v", n)
		}
	}
	if got := s.Assessments(); len(got) != 0 {
		t.Fatalf("assessments = %+v", got)
	}
	checkAccum(t, s, map[string]int64{"g1": 0, "g2": 0})
	if st := s.Stats(); st.EventsProcessed != 0 {
		t.Fatalf("events processed = %d, want 0", st.EventsProcessed)
	}
}

// 相同操作序列重放得到完全相同的选组序列与考核记录。
func TestReplayDeterminism(t *testing.T) {
	run := func() ([]SlotRecord, []Assessment, []Notification) {
		s := setupSched(t, 3)
		mustOK(t, s.AddUser("u1", "g1", CatNormal, 0))
		mustOK(t, s.AddUser("u2", "g2", CatGuaranteed, 100))
		mustOK(t, s.Issue("i1", "R", 2, 10, 50))
		mustOK(t, s.Issue("i2", "R", 1, 20, 60))
		mustOK(t, s.AdvanceTo(25))
		mustOK(t, s.Modify("i2", 3))
		mustOK(t, s.AdvanceTo(40))
		mustOK(t, s.Cancel("i1"))
		mustOK(t, s.AdvanceTo(70))
		return s.History(), s.Assessments(), s.AllNotifications()
	}
	h1, a1, n1 := run()
	h2, a2, n2 := run()
	if !reflect.DeepEqual(h1, h2) || !reflect.DeepEqual(a1, a2) || !reflect.DeepEqual(n1, n2) {
		t.Fatalf("replay mismatch")
	}
}

// 所有操作可并发调用（配合 -race 验证），结果等价于某个串行顺序。
func TestConcurrent(t *testing.T) {
	s := setupSched(t, 3)
	for i := 0; i < 6; i++ {
		mustOK(t, s.AddUser(fmt.Sprintf("u%d", i), fmt.Sprintf("g%d", i%3+1), CatNormal, 0))
	}
	var wg sync.WaitGroup
	wg.Add(4)
	go func() { // 单调推进时钟
		defer wg.Done()
		for ts := int64(10); ts <= 500; ts += 10 {
			_ = s.AdvanceTo(ts)
		}
	}()
	go func() { // 发布/改级/取消
		defer wg.Done()
		for i := 0; i < 40; i++ {
			id := fmt.Sprintf("i%d", i)
			if err := s.Issue(id, "R", 1+i%3, int64(10*(i+1)), int64(10*(i+3))); err == nil {
				_ = s.Modify(id, 1)
				if i%2 == 0 {
					_ = s.Cancel(id)
				}
			}
		}
	}()
	go func() { // 确认
		defer wg.Done()
		for i := 0; i < 200; i++ {
			_ = s.Confirm(fmt.Sprintf("u%d", i%6), int64(10*(i%50)))
		}
	}()
	go func() { // 查询
		defer wg.Done()
		for i := 0; i < 200; i++ {
			_ = s.History()
			_ = s.Assessments()
			_, _ = s.Accum("g1")
		}
	}()
	wg.Wait()
	// 串行不变量仍然成立：被限组数等于有效等级、累计为时段长度的倍数。
	var maxA int64
	for _, g := range []string{"g1", "g2", "g3"} {
		a, _ := s.Accum(g)
		if a%10 != 0 {
			t.Fatalf("accum[%s]=%d not multiple of slotLen", g, a)
		}
		if a > maxA {
			maxA = a
		}
	}
	for _, r := range s.History() {
		if len(r.Groups) != r.Level {
			t.Fatalf("slot %d: level %d but %d groups", r.Start, r.Level, len(r.Groups))
		}
	}
}

// 取消恰在时段边界：当前时段立即按剩余指令重选。
func TestCancelExactlyAtBoundary(t *testing.T) {
	s := setupSched(t, 3)
	mustOK(t, s.Issue("i1", "R", 2, 10, 50))
	mustOK(t, s.Issue("i2", "R", 1, 10, 50))
	mustOK(t, s.AdvanceTo(20))
	mustOK(t, s.Cancel("i1"))
	checkHistory(t, s, []SlotRecord{
		{Start: 10, Level: 2, Groups: []string{"g1", "g2"}},
		{Start: 20, Level: 1, Groups: []string{"g3"}},
	})
}

// 取消在边界之后一刻：当前时段不变，自下一时段起回落。
func TestCancelOneTickAfterBoundary(t *testing.T) {
	s := setupSched(t, 3)
	mustOK(t, s.Issue("i1", "R", 2, 10, 50))
	mustOK(t, s.Issue("i2", "R", 1, 10, 50))
	mustOK(t, s.AdvanceTo(21))
	mustOK(t, s.Cancel("i1"))
	if got := s.CurrentSelection(); !reflect.DeepEqual(got, []string{"g3", "g1"}) {
		t.Fatalf("current selection changed: %v", got)
	}
	mustOK(t, s.AdvanceTo(40))
	checkHistory(t, s, []SlotRecord{
		{Start: 10, Level: 2, Groups: []string{"g1", "g2"}},
		{Start: 20, Level: 2, Groups: []string{"g3", "g1"}},
		{Start: 30, Level: 1, Groups: []string{"g2"}},
		{Start: 40, Level: 1, Groups: []string{"g3"}},
	})
}

// 叠加指令取最大等级；取消其中一条后回落。
func TestOverlayMaxAndFallback(t *testing.T) {
	newSched := func(t *testing.T) *Scheduler {
		s := setupSched(t, 4)
		mustOK(t, s.Issue("i1", "R", 1, 10, 50))
		mustOK(t, s.Issue("i2", "R", 3, 20, 40))
		return s
	}
	t.Run("叠加取最大", func(t *testing.T) {
		s := newSched(t)
		mustOK(t, s.AdvanceTo(50))
		var levels []int
		for _, r := range s.History() {
			levels = append(levels, r.Level)
		}
		if !reflect.DeepEqual(levels, []int{1, 3, 3, 1, 0}) {
			t.Fatalf("levels = %v", levels)
		}
	})
	t.Run("取消后回落", func(t *testing.T) {
		s := newSched(t)
		mustOK(t, s.AdvanceTo(25))
		mustOK(t, s.Cancel("i2"))
		mustOK(t, s.AdvanceTo(50))
		var levels []int
		for _, r := range s.History() {
			levels = append(levels, r.Level)
		}
		if !reflect.DeepEqual(levels, []int{1, 3, 1, 1, 0}) {
			t.Fatalf("levels = %v", levels)
		}
	})
}

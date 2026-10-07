package scheduler

// 性能不变量的可验证证明：通过内部开销计数器（Stats）断言各项开销
// 只依赖于规格允许的量，而不随被排除的量增长。

import (
	"fmt"
	"testing"
)

// 判定某时段有效等级的开销不随已取消或已结束指令的数量增长：
// 大量「窗口尚未开始即取消」的指令不留下任何待处理事件。
func TestCancelledInstructionsCostNothing(t *testing.T) {
	s := NewScheduler(10)
	mustOK(t, s.AddRegion("R"))
	for i := 1; i <= 3; i++ {
		mustOK(t, s.AddGroup("R", fmt.Sprintf("g%d", i)))
	}
	const n = 2000
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("i%d", i)
		mustOK(t, s.Issue(id, "R", 1, 10, 20))
		mustOK(t, s.Cancel(id)) // 窗口尚未开始：视同从未存在
	}
	mustOK(t, s.AdvanceTo(1000))
	if st := s.Stats(); st.EventsProcessed != 0 {
		t.Fatalf("已取消指令留下 %d 个待处理事件，期望 0", st.EventsProcessed)
	}
	// 一条真实指令：恰好处理激活、失效两个事件。
	mustOK(t, s.Issue("real", "R", 1, 1010, 1050))
	mustOK(t, s.AdvanceTo(1100))
	st := s.Stats()
	if st.EventsProcessed != 2 {
		t.Fatalf("EventsProcessed = %d, want 2", st.EventsProcessed)
	}
	if st.LevelHeapOps > 4 {
		t.Fatalf("LevelHeapOps = %d, 随已取消指令数量退化", st.LevelHeapOps)
	}
}

// 生成通知的开销只与被选组内用户数相关，不随区域总用户数增长；
// 选组开销只与组数相关，不随用户数增长。
func TestNotificationAndSelectionCost(t *testing.T) {
	build := func(usersPerBigGroup int) *Scheduler {
		s := NewScheduler(10)
		mustOK(t, s.AddRegion("R"))
		for i := 1; i <= 30; i++ {
			g := fmt.Sprintf("g%02d", i)
			mustOK(t, s.AddGroup("R", g))
		}
		// g01、g02 各 5 个用户（将被选中），其余组各 usersPerBigGroup 个用户。
		for i := 0; i < 5; i++ {
			mustOK(t, s.AddUser(fmt.Sprintf("s1-%d", i), "g01", CatNormal, 0))
			mustOK(t, s.AddUser(fmt.Sprintf("s2-%d", i), "g02", CatNormal, 0))
		}
		for g := 3; g <= 30; g++ {
			for i := 0; i < usersPerBigGroup; i++ {
				mustOK(t, s.AddUser(fmt.Sprintf("b%d-%d", g, i), fmt.Sprintf("g%02d", g), CatNormal, 0))
			}
		}
		return s
	}
	run := func(s *Scheduler) Stats {
		mustOK(t, s.Issue("i1", "R", 2, 10, 20)) // 仅一个时段，选中 g01、g02
		mustOK(t, s.AdvanceTo(20))
		return s.Stats()
	}
	// 区域总用户 11210，但被选中组内只有 10 个用户。
	st := run(build(400))
	if st.UserVisits > 100 {
		t.Fatalf("UserVisits = %d，随区域总用户数（11210）增长", st.UserVisits)
	}
	// 用户规模放大 20 倍，选组堆开销必须完全不变。
	small := run(build(5))
	big := run(build(100))
	if small.GroupHeapOps != big.GroupHeapOps {
		t.Fatalf("GroupHeapOps 随用户数变化: %d vs %d", small.GroupHeapOps, big.GroupHeapOps)
	}
}

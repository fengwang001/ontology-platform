package signal

import (
	"fmt"
	"sync"
	"testing"
)

func dup2(pair [3]int) []Phase {
	return []Phase{
		{MinGreen: pair[0], MaxGreen: pair[1], Clear: pair[2]},
		{MinGreen: pair[0], MaxGreen: pair[1], Clear: pair[2]},
	}
}

func dup3(pair [3]int) []Phase {
	return []Phase{
		{MinGreen: pair[0], MaxGreen: pair[1], Clear: pair[2]},
		{MinGreen: pair[0], MaxGreen: pair[1], Clear: pair[2]},
		{MinGreen: pair[0], MaxGreen: pair[1], Clear: pair[2]},
	}
}

func mustNew(t *testing.T, ph []Phase, p Plan) *Controller {
	t.Helper()
	c, err := NewController(ph, p)
	if err != nil {
		t.Fatalf("NewController: %v", err)
	}
	return c
}

func mustQuery(t *testing.T, c *Controller, tm int64) QueryResult {
	t.Helper()
	q, err := c.Query(tm)
	if err != nil {
		t.Fatalf("Query(%d): %v", tm, err)
	}
	return q
}

func wantErrKind(t *testing.T, op string, err error, kind ErrKind) {
	t.Helper()
	if KindOf(err) != kind {
		t.Fatalf("%s: got %v, want kind %s", op, err, kind)
	}
}

// 设定绿时恰等于最小绿或最大绿均合法；越界与相位差不合法则不可行。
func TestBoundaryGreens(t *testing.T) {
	ph := dup2([3]int{5, 15, 1})
	if _, err := NewController(ph, Plan{ID: "p", Greens: []int{5, 15}, Offset: 0, MaxAdjust: 1}); err != nil {
		t.Fatalf("min/max boundary greens must be feasible: %v", err)
	}
	if _, err := NewController(ph, Plan{ID: "p", Greens: []int{4, 10}, Offset: 0, MaxAdjust: 1}); KindOf(err) != ErrInfeasiblePlan {
		t.Fatalf("below min: got %v", err)
	}
	if _, err := NewController(ph, Plan{ID: "p", Greens: []int{10, 16}, Offset: 0, MaxAdjust: 1}); KindOf(err) != ErrInfeasiblePlan {
		t.Fatalf("above max: got %v", err)
	}
	// 周期 = 10+1+10+1 = 22；相位差必须 < 周期。
	if _, err := NewController(ph, Plan{ID: "p", Greens: []int{10, 10}, Offset: 22, MaxAdjust: 1}); KindOf(err) != ErrInfeasiblePlan {
		t.Fatalf("offset == cycle: got %v", err)
	}
	if _, err := NewController(ph, Plan{ID: "p", Greens: []int{10, 10}, Offset: 21, MaxAdjust: 1}); err != nil {
		t.Fatalf("offset == cycle-1 must be feasible: %v", err)
	}
	// 方案变更同样接受边界值。
	c := mustNew(t, ph, Plan{ID: "A", Greens: []int{10, 10}, Offset: 0, MaxAdjust: 1})
	if err := c.ChangePlan(Plan{ID: "B", Greens: []int{5, 15}, Offset: 0, MaxAdjust: 1}, 1); err != nil {
		t.Fatalf("boundary plan change: %v", err)
	}
}

// 方案变更只在循环结束瞬间生效；同循环多次变更以最后接受的为准，
// 被替换方案可查询；与循环结束同一时刻的查询按新方案回答。
func TestPlanChangeAtCycleEnd(t *testing.T) {
	ph := dup2([3]int{5, 15, 1})
	c := mustNew(t, ph, Plan{ID: "A", Greens: []int{10, 10}, Offset: 0, MaxAdjust: 2})
	// 周期 22：相位0 [0,10) 清空 [10,11)，相位1 [11,21) 清空 [21,22)，22 回绕。
	if err := c.ChangePlan(Plan{ID: "B", Greens: []int{12, 8}, Offset: 0, MaxAdjust: 2}, 5); err != nil {
		t.Fatal(err)
	}
	if err := c.ChangePlan(Plan{ID: "C", Greens: []int{6, 14}, Offset: 0, MaxAdjust: 1}, 8); err != nil {
		t.Fatal(err)
	}
	if s, _ := c.PlanStatus("B"); s != PlanReplaced {
		t.Fatalf("B should be Replaced, got %s", s)
	}
	if s, _ := c.PlanStatus("C"); s != PlanPending {
		t.Fatalf("C should be Pending, got %s", s)
	}
	// 生效前按旧方案回答。
	if q := mustQuery(t, c, 21); q.Phase != 1 || q.Remaining != 1 {
		t.Fatalf("t=21: got %+v", q)
	}
	if q := mustQuery(t, c, 10); q.Phase != 0 || !q.InClearance || q.Remaining != 1 {
		t.Fatalf("t=10: got %+v", q)
	}
	// 循环结束同一时刻（t=22）按新方案回答：相位0 绿 6 + 清空 1。
	if q := mustQuery(t, c, 22); q.Phase != 0 || q.Elapsed != 0 || q.Remaining != 7 {
		t.Fatalf("t=22 should use plan C: got %+v", q)
	}
	if s, _ := c.PlanStatus("C"); s != PlanActive {
		t.Fatalf("C should be Active, got %s", s)
	}
	if s, _ := c.PlanStatus("A"); s != PlanReplaced {
		t.Fatalf("A should be Replaced, got %s", s)
	}
}

// 紧急请求目标恰为当前相位：延长到最大绿；通过确认提前结束保持（不早于最小绿）。
func TestEmergencyTargetIsCurrent(t *testing.T) {
	ph := dup2([3]int{5, 15, 1})
	c := mustNew(t, ph, Plan{ID: "A", Greens: []int{10, 10}, Offset: 0, MaxAdjust: 2})
	if err := c.RequestEmergency("E1", 0, 3); err != nil {
		t.Fatal(err)
	}
	if q := mustQuery(t, c, 3); q.Phase != 0 || q.Elapsed != 3 || q.Remaining != 13 {
		t.Fatalf("extend to max green: got %+v", q) // 绿15+清空1-已3
	}
	if s, _ := c.EmergencyStatus("E1"); s != EmActive {
		t.Fatalf("E1 should be Active, got %s", s)
	}
	if err := c.ConfirmPass("E1", 12); err != nil {
		t.Fatal(err)
	}
	if q := mustQuery(t, c, 12); !q.InClearance || q.Remaining != 1 {
		t.Fatalf("confirm ends hold at t=12: got %+v", q)
	}
	if q := mustQuery(t, c, 13); q.Phase != 1 || q.Elapsed != 0 {
		t.Fatalf("t=13 next phase: got %+v", q)
	}
	if s, _ := c.EmergencyStatus("E1"); s != EmCompleted {
		t.Fatalf("E1 should be Completed, got %s", s)
	}
	// 服务造成周期偏移 2：下一循环缩短回归，再下一循环归零。
	if q := mustQuery(t, c, 30); q.Deviation != 2 {
		t.Fatalf("deviation after preemption: got %+v", q)
	}
	if q := mustQuery(t, c, 50); q.Deviation != 0 {
		t.Fatalf("deviation should resync to 0: got %+v", q)
	}
}

// 跳相后的下一循环再次请求跳同一相位被拒绝，且不留痕。
func TestConsecutiveSkipRejected(t *testing.T) {
	ph := []Phase{{5, 12, 1}, {5, 12, 1}, {5, 12, 1}}
	c := mustNew(t, ph, Plan{ID: "A", Greens: []int{8, 8, 8}, Offset: 0, MaxAdjust: 2})
	if err := c.RequestEmergency("E1", 2, 2); err != nil {
		t.Fatal(err)
	}
	// 相位0跑满最小绿5，清空1，t=6 跳到相位2（相位1在循环0被跳过）。
	if q := mustQuery(t, c, 6); q.Phase != 2 || q.Elapsed != 0 {
		t.Fatalf("jump to target at t=6: got %+v", q)
	}
	before := mustQuery(t, c, 20)
	err := c.RequestEmergency("E2", 2, 20)
	wantErrKind(t, "E2 skip phase1 again in cycle1", err, ErrConsecutiveSkip)
	if _, ok := c.EmergencyStatus("E2"); ok {
		t.Fatal("rejected request must leave no trace")
	}
	if after := mustQuery(t, c, 20); after != before {
		t.Fatalf("rejected request changed state: %+v -> %+v", before, after)
	}
	// 再下一个循环（t=48 回绕后）同一跳相被允许。
	if err := c.RequestEmergency("E3", 2, 50); err != nil {
		t.Fatalf("skip allowed two cycles later: %v", err)
	}
	if q := mustQuery(t, c, 55); q.Phase != 2 || q.Elapsed != 1 {
		t.Fatalf("E3 jump at t=54: got %+v", q)
	}
}

// 公交与紧急同时有效时紧急优先，公交被抢占（不报错，状态可查）。
func TestBusPreemptedByEmergency(t *testing.T) {
	ph := dup2([3]int{5, 15, 1})
	c := mustNew(t, ph, Plan{ID: "A", Greens: []int{10, 10}, Offset: 0, MaxAdjust: 2})
	if err := c.RequestEmergency("E1", 1, 4); err != nil {
		t.Fatal(err)
	}
	if err := c.RequestBus("B1", BusExtend, 3, 4); err != nil {
		t.Fatalf("preempted bus must not error: %v", err)
	}
	if s, _ := c.BusStatus("B1"); s != BusPreempted {
		t.Fatalf("B1 should be Preempted, got %s", s)
	}
	// 无紧急时公交延长生效。
	if err := c.RequestBus("B2", BusExtend, 3, 30); err != nil {
		t.Fatal(err)
	}
	if s, _ := c.BusStatus("B2"); s != BusCompleted {
		t.Fatalf("B2 should be Completed, got %s", s)
	}
	if q := mustQuery(t, c, 30); q.Remaining != 6 {
		t.Fatalf("B2 extended green to 13: got %+v", q)
	}
}

// 偏离恰为半个周期时取延长方向回归。
func TestHalfCycleDeviationExtends(t *testing.T) {
	ph := dup2([3]int{5, 21, 1})
	c := mustNew(t, ph, Plan{ID: "A", Greens: []int{10, 10}, Offset: 0, MaxAdjust: 4})
	// 周期22；紧急延长相位0到最大绿21，下一循环起点推迟到33，偏离 +11 = 半个周期。
	if err := c.RequestEmergency("E1", 0, 2); err != nil {
		t.Fatal(err)
	}
	if err := c.ConfirmPass("E1", 33); err != nil { // 推进引擎越过回绕点（幂等空操作）
		t.Fatal(err)
	}
	if got := c.eng.cycGreen; got[0] != 14 || got[1] != 10 {
		t.Fatalf("half-cycle deviation must extend: cycGreen=%v", got)
	}
	if q := mustQuery(t, c, 33); q.Remaining != 15 {
		t.Fatalf("phase0 green extended to 14: got %+v", q)
	}
}

// 回归中途再次被优先服务打断，偏离量按打断后的实际状态重新确定。
func TestResyncInterrupted(t *testing.T) {
	ph := dup2([3]int{5, 21, 1})
	c := mustNew(t, ph, Plan{ID: "A", Greens: []int{10, 10}, Offset: 0, MaxAdjust: 2})
	if err := c.RequestEmergency("E1", 0, 2); err != nil { // 偏离 +11，延长方向回归中
		t.Fatal(err)
	}
	if q := mustQuery(t, c, 60); q.Deviation != -9 {
		t.Fatalf("mid-resync deviation: got %+v", q)
	}
	if err := c.RequestEmergency("E2", 1, 90); err != nil { // 打断：跳向相位1
		t.Fatal(err)
	}
	// E2 服务于 t=113 结束并回绕，偏离按实际状态重定为 +3（缩短方向）。
	if q := mustQuery(t, c, 113); q.Phase != 0 || q.Deviation != 3 {
		t.Fatalf("deviation recomputed after interruption: got %+v", q)
	}
	if q := mustQuery(t, c, 200); q.Deviation != 0 {
		t.Fatalf("resync converges after interruption: got %+v", q)
	}
}

// 时钟回退与各类拒绝不改变任何状态、排队次序与时钟。
func TestRejectionsLeaveNoTrace(t *testing.T) {
	ph := dup2([3]int{5, 15, 1})
	c := mustNew(t, ph, Plan{ID: "A", Greens: []int{10, 10}, Offset: 0, MaxAdjust: 2})
	if err := c.RequestBus("B1", BusExtend, 2, 9); err != nil {
		t.Fatal(err)
	}
	wantErrKind(t, "bus rollback", c.RequestBus("B2", BusExtend, 1, 8), ErrClockRollback)
	if err := c.RequestEmergency("E1", 1, 10); err != nil {
		t.Fatal(err)
	}
	wantErrKind(t, "duplicate", c.RequestEmergency("E1", 0, 10), ErrDuplicateRequest)
	wantErrKind(t, "occupied", c.RequestEmergency("E2", 1, 10), ErrTargetOccupied)
	wantErrKind(t, "no phase", c.RequestEmergency("E3", 5, 10), ErrPhaseNotExist)
	wantErrKind(t, "infeasible", c.ChangePlan(Plan{ID: "X", Greens: []int{1, 1}}, 10), ErrInfeasiblePlan)
	wantErrKind(t, "query rollback", func() error { _, e := c.Query(9); return e }(), ErrClockRollback)
	for _, id := range []string{"B2", "E2", "E3"} {
		if _, ok := c.EmergencyStatus(id); ok {
			t.Fatalf("rejected %s left trace", id)
		}
		if _, ok := c.BusStatus(id); ok {
			t.Fatalf("rejected %s left trace", id)
		}
	}
	if _, ok := c.PlanStatus("X"); ok {
		t.Fatal("rejected plan left trace")
	}
	// 与只包含被接受操作的全新控制器逐刻对比，证明拒绝不留痕。
	c2 := mustNew(t, ph, Plan{ID: "A", Greens: []int{10, 10}, Offset: 0, MaxAdjust: 2})
	if err := c2.RequestBus("B1", BusExtend, 2, 9); err != nil {
		t.Fatal(err)
	}
	if err := c2.RequestEmergency("E1", 1, 10); err != nil {
		t.Fatal(err)
	}
	for tm := int64(10); tm <= 60; tm += 7 {
		q1, q2 := mustQuery(t, c, tm), mustQuery(t, c2, tm)
		if q1 != q2 {
			t.Fatalf("t=%d diverged: %+v vs %+v", tm, q1, q2)
		}
	}
}

// 错误只报次序最靠前的一类。
func TestErrorPrecedence(t *testing.T) {
	ph := dup2([3]int{5, 15, 1})
	c := mustNew(t, ph, Plan{ID: "A", Greens: []int{10, 10}, Offset: 0, MaxAdjust: 2})
	if err := c.RequestEmergency("E1", 0, 5); err != nil {
		t.Fatal(err)
	}
	// 参数非法 < 时钟回退：空标识且时刻回退，报参数非法。
	wantErrKind(t, "param before clock", c.RequestEmergency("", 0, 1), ErrInvalidParam)
	// 时钟回退 < 重复请求：已用标识且时刻回退，报时钟回退。
	wantErrKind(t, "clock before dup", c.RequestEmergency("E1", 0, 1), ErrClockRollback)
	// 时钟回退 < 方案不可行：绿时越界且时刻回退，报时钟回退。
	wantErrKind(t, "clock before infeasible",
		c.ChangePlan(Plan{ID: "Z", Greens: []int{1, 1}}, 1), ErrClockRollback)
	// 相位不存在 < 重复请求：已用标识且目标不存在，报相位不存在。
	wantErrKind(t, "phase before dup", c.RequestEmergency("E1", 9, 6), ErrPhaseNotExist)
	// 重复请求 < 目标占用：已用标识且目标被占用，报重复请求。
	if err := c.RequestEmergency("E2", 1, 6); err != nil {
		t.Fatal(err)
	}
	wantErrKind(t, "dup before occupied", c.RequestEmergency("E2", 1, 6), ErrDuplicateRequest)
}

// 查询开销不随已过去循环数增长：稳态下超大时刻查询只触发有界个事件。
func TestQueryComplexityBound(t *testing.T) {
	ph := dup2([3]int{5, 15, 1})
	c := mustNew(t, ph, Plan{ID: "A", Greens: []int{10, 10}, Offset: 0, MaxAdjust: 2})
	if err := c.RequestEmergency("E1", 0, 3); err != nil {
		t.Fatal(err)
	}
	if err := c.ConfirmPass("E1", 12); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Query(200); err != nil { // 让回归完成、进入稳态
		t.Fatal(err)
	}
	before := c.eng.events
	const far = int64(1) << 40
	q1, err := c.Query(far)
	if err != nil {
		t.Fatal(err)
	}
	if got := c.eng.events - before; got > 100 {
		t.Fatalf("query at t=2^40 processed %d events, want bounded", got)
	}
	// 稳态纯周期：同一周期相位处的查询结果一致。
	q2, err := c.Query(far - 1000*22)
	if err != nil {
		t.Fatal(err)
	}
	if q1.Phase != q2.Phase || q1.Elapsed != q2.Elapsed || q1.Remaining != q2.Remaining ||
		q1.Deviation != q2.Deviation || q1.InClearance != q2.InClearance {
		t.Fatalf("periodic mismatch: %+v vs %+v", q1, q2)
	}
}

// 多个紧急请求按 (请求时刻, 标识字典序) 依次服务。
func TestEmergencyQueueOrder(t *testing.T) {
	ph := []Phase{{5, 12, 1}, {5, 12, 1}, {5, 12, 1}}
	c := mustNew(t, ph, Plan{ID: "A", Greens: []int{8, 8, 8}, Offset: 0, MaxAdjust: 2})
	if err := c.RequestEmergency("b", 2, 1); err != nil {
		t.Fatal(err)
	}
	if err := c.RequestEmergency("d", 1, 2); err != nil {
		t.Fatal(err)
	}
	if err := c.RequestEmergency("c", 0, 2); err != nil { // 同时刻，字典序小者先服务
		t.Fatal(err)
	}
	// b：跳向相位2，t=6 起保持12，t=19 完成；随后 c（目标0）先于 d（目标1）。
	if q := mustQuery(t, c, 20); q.Phase != 0 {
		t.Fatalf("c served before d: got %+v", q)
	}
	if s, _ := c.EmergencyStatus("c"); s != EmActive {
		t.Fatalf("c should be Active at t=20 view, got %s", s)
	}
	if s, _ := c.EmergencyStatus("d"); s != EmQueued {
		t.Fatalf("d should be Queued, got %s", s)
	}
	if q := mustQuery(t, c, 40); q.Phase != 1 {
		t.Fatalf("d served after c completes: got %+v", q)
	}
	if s, _ := c.EmergencyStatus("c"); s != EmCompleted {
		t.Fatalf("c should be Completed, got %s", s)
	}
	if q := mustQuery(t, c, 60); q.Phase == 1 {
		if s, _ := c.EmergencyStatus("d"); s != EmActive && s != EmCompleted {
			t.Fatalf("d unexpected status %s", s)
		}
	}
}

// 相同操作序列重放得到完全相同的相位历史与各请求终态。
func TestReplayDeterministic(t *testing.T) {
	ph := []Phase{{5, 12, 1}, {5, 12, 1}, {5, 12, 1}}
	plan := Plan{ID: "A", Greens: []int{8, 8, 8}, Offset: 3, MaxAdjust: 2}
	ops := func(c *Controller) {
		_ = c.RequestEmergency("E1", 2, 2)
		_ = c.RequestBus("B1", BusExtend, 2, 4)
		_ = c.ChangePlan(Plan{ID: "P2", Greens: []int{6, 10, 8}, Offset: 1, MaxAdjust: 3}, 9)
		_ = c.RequestEmergency("E2", 0, 20)
		_ = c.ConfirmPass("E1", 21)
		_ = c.RequestBus("B2", BusShorten, 3, 40)
	}
	c1 := mustNew(t, ph, plan)
	c2 := mustNew(t, ph, plan)
	ops(c1)
	ops(c2)
	for tm := int64(40); tm <= 300; tm++ {
		q1, q2 := mustQuery(t, c1, tm), mustQuery(t, c2, tm)
		if q1 != q2 {
			t.Fatalf("t=%d replay diverged: %+v vs %+v", tm, q1, q2)
		}
	}
	for _, id := range []string{"E1", "E2"} {
		s1, _ := c1.EmergencyStatus(id)
		s2, _ := c2.EmergencyStatus(id)
		if s1 != s2 {
			t.Fatalf("final status of %s diverged: %s vs %s", id, s1, s2)
		}
	}
}

// 并发调用等价于某个串行顺序（-race 下验证无数据竞争、无恐慌）。
func TestConcurrentAccess(t *testing.T) {
	ph := dup2([3]int{5, 15, 1})
	c := mustNew(t, ph, Plan{ID: "A", Greens: []int{10, 10}, Offset: 0, MaxAdjust: 2})
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				tm := int64(g*200 + i)
				switch i % 5 {
				case 0:
					_, _ = c.Query(tm)
				case 1:
					_ = c.RequestEmergency(fmt.Sprintf("e%d-%d", g, i), (g+i)%2, tm)
				case 2:
					_ = c.RequestBus(fmt.Sprintf("b%d-%d", g, i), BusExtend, 1, tm)
				case 3:
					_ = c.ConfirmPass(fmt.Sprintf("e%d-%d", g, i-1), tm)
				default:
					_, _ = c.EmergencyStatus("e1-1")
				}
			}
		}(g)
	}
	wg.Wait()
	if _, err := c.Query(1600); err != nil {
		t.Fatal(err)
	}
}

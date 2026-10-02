package scheduler

import (
	"errors"
	"reflect"
	"testing"
)

func mustNew(t *testing.T, g, pod, bud int64) *Scheduler {
	t.Helper()
	s, err := New(g, pod, bud)
	if err != nil {
		t.Fatalf("New(%d,%d,%d) error: %v", g, pod, bud, err)
	}
	return s
}

func eq(t *testing.T, name string, got, want any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s = %v, want %v", name, got, want)
	}
}

// TestSpecExample 复现题目给出的完整示例。
func TestSpecExample(t *testing.T) {
	s := mustNew(t, 100, 2, 600)
	if err := s.AddNode(1, Spot, 5); err != nil {
		t.Fatal(err)
	}
	tasks := []struct{ id, w, iv, ck int64 }{
		{1, 500, 100, 40},
		{2, 400, 50, 45},
		{3, 300, 30, 20},
		{4, 900, 200, 30},
		{5, 250, 50, 15},
	}
	for _, x := range tasks {
		if err := s.AddTask(x.id, 0, x.w, x.iv, x.ck); err != nil {
			t.Fatal(err)
		}
	}
	s.Place()
	for _, id := range []int64{1, 2, 3, 4, 5} {
		if s.tasks[id].NodeID() != 1 {
			t.Fatalf("task %d not on S1", id)
		}
	}

	reports := []struct{ id, p int64 }{
		{1, 330}, {2, 240}, {3, 250}, {4, 700}, {5, 100},
	}
	for _, r := range reports {
		if err := s.Report(r.id, r.p); err != nil {
			t.Fatal(err)
		}
	}
	wantCP := map[int64]int64{1: 300, 2: 200, 3: 240, 4: 600, 5: 100}
	for id, cp := range wantCP {
		eq(t, "cp", s.tasks[id].cp, cp)
	}

	res, err := s.Notice(1, 0)
	if err != nil {
		t.Fatal(err)
	}
	eq(t, "completed", res.Completed, []int64{3})
	eq(t, "saved", res.Saved, []int64{2, 4, 5})
	eq(t, "discarded", res.Discarded, []int64{1})

	if err := s.Report(3, 300); err != nil {
		t.Fatal(err)
	}
	eq(t, "task3 state", s.tasks[3].st, Done)

	items, err := s.Expire(1, 100)
	if err != nil {
		t.Fatal(err)
	}
	eq(t, "expire items", items, []ExpireItem{{1, 30}, {2, 0}, {4, 0}, {5, 0}})

	wantP := map[int64]int64{1: 300, 2: 240, 4: 700, 5: 100}
	for id, p := range wantP {
		eq(t, "p after expire", s.tasks[id].p, p)
		eq(t, "state after expire", s.tasks[id].st, Pending)
	}

	if err := s.AddNode(2, Spot, 2); err != nil {
		t.Fatal(err)
	}
	if err := s.AddNode(3, OnDemand, 5); err != nil {
		t.Fatal(err)
	}
	s.Place()
	eq(t, "1 on S2", s.tasks[1].NodeID(), int64(2))
	eq(t, "2 on S2", s.tasks[2].NodeID(), int64(2))
	eq(t, "4 on O1", s.tasks[4].NodeID(), int64(3))
	eq(t, "5 pending", s.tasks[5].st, Pending)
	eq(t, "budget", s.Budget(), int64(200))
}

// TestGraceBoundary 剩余恰等于 G 自然完成，大于 G 才尝试检查点。
func TestGraceBoundary(t *testing.T) {
	s := mustNew(t, 100, 2, 0)
	_ = s.AddNode(1, Spot, 2)
	_ = s.AddTask(1, 0, 200, 1000, 1)
	_ = s.AddTask(2, 0, 201, 1000, 1)
	s.Place()
	_ = s.Report(1, 100)
	_ = s.Report(2, 100)
	res, err := s.Notice(1, 0)
	if err != nil {
		t.Fatal(err)
	}
	eq(t, "completed", res.Completed, []int64{1})
	eq(t, "saved", res.Saved, []int64{2})
	eq(t, "discarded", res.Discarded, []int64(nil))
}

// TestZeroUnsavedFree u=0 的任务不耗时、不参与累计排序。
func TestZeroUnsavedFree(t *testing.T) {
	s := mustNew(t, 10, 1, 0)
	_ = s.AddNode(1, Spot, 3)
	_ = s.AddTask(1, 0, 500, 100, 100) // p=cp=100, u=0
	_ = s.AddTask(2, 0, 500, 100, 8)   // u=6
	_ = s.AddTask(3, 0, 500, 100, 9)   // u=9，先排
	s.Place()
	_ = s.Report(1, 100)
	_ = s.Report(2, 106)
	_ = s.Report(3, 109)
	res, err := s.Notice(1, 0)
	if err != nil {
		t.Fatal(err)
	}
	eq(t, "saved", res.Saved, []int64{1, 3})
	eq(t, "discarded", res.Discarded, []int64{2})
}

// TestCumulativeEqualAndStop 累计恰等于 G 保存，大 1 丢弃并停止后面全部。
func TestCumulativeEqualAndStop(t *testing.T) {
	s := mustNew(t, 10, 1, 0)
	_ = s.AddNode(1, Spot, 4)
	_ = s.AddTask(1, 0, 1000, 10000, 6)
	_ = s.AddTask(2, 0, 1000, 10000, 4)
	_ = s.AddTask(3, 0, 1000, 10000, 1)
	_ = s.AddTask(4, 0, 1000, 10000, 1)
	s.Place()
	_ = s.Report(1, 10)
	_ = s.Report(2, 9)
	_ = s.Report(3, 8)
	_ = s.Report(4, 1)
	res, err := s.Notice(1, 0)
	if err != nil {
		t.Fatal(err)
	}
	eq(t, "saved", res.Saved, []int64{1, 2})
	eq(t, "discarded", res.Discarded, []int64{3, 4})

	s2 := mustNew(t, 10, 1, 0)
	_ = s2.AddNode(1, Spot, 4)
	_ = s2.AddTask(1, 0, 1000, 10000, 6)
	_ = s2.AddTask(2, 0, 1000, 10000, 5) // 累计 11 > 10
	_ = s2.AddTask(3, 0, 1000, 10000, 1)
	_ = s2.AddTask(4, 0, 1000, 10000, 1)
	s2.Place()
	_ = s2.Report(1, 10)
	_ = s2.Report(2, 9)
	_ = s2.Report(3, 8)
	_ = s2.Report(4, 1)
	res2, err := s2.Notice(1, 0)
	if err != nil {
		t.Fatal(err)
	}
	eq(t, "saved2", res2.Saved, []int64{1})
	eq(t, "discarded2", res2.Discarded, []int64{2, 3, 4})
}

// TestUTieByID u 并列时按 id 升序累计。
func TestUTieByID(t *testing.T) {
	s := mustNew(t, 10, 1, 0)
	_ = s.AddNode(1, Spot, 2)
	_ = s.AddTask(2, 0, 1000, 10000, 6)
	_ = s.AddTask(1, 0, 1000, 10000, 6)
	s.Place()
	_ = s.Report(2, 5)
	_ = s.Report(1, 5)
	res, err := s.Notice(1, 0)
	if err != nil {
		t.Fatal(err)
	}
	eq(t, "saved", res.Saved, []int64{1})
	eq(t, "discarded", res.Discarded, []int64{2})
}

// TestCheckpointFloorMonotonic cp 取 floor 且只增不减。
func TestCheckpointFloorMonotonic(t *testing.T) {
	s := mustNew(t, 100, 1, 1_000_000)
	_ = s.AddNode(1, OnDemand, 1)
	_ = s.AddTask(1, 0, 10000, 30, 1)
	s.Place()
	_ = s.Report(1, 29)
	eq(t, "cp 29", s.tasks[1].cp, int64(0))
	_ = s.Report(1, 59)
	eq(t, "cp 59", s.tasks[1].cp, int64(30))
	if err := s.Report(1, 50); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("regress report err = %v", err)
	}
	eq(t, "cp stays", s.tasks[1].cp, int64(30))
}

// TestExpireRewindAndRework 回退到 cp，返工量 p-cp；自然完成未上报也回退。
func TestExpireRewindAndRework(t *testing.T) {
	s := mustNew(t, 100, 1, 0)
	_ = s.AddNode(1, Spot, 2)
	_ = s.AddTask(1, 0, 500, 100, 1)
	_ = s.AddTask(2, 0, 500, 100, 1)
	s.Place()
	_ = s.Report(1, 450) // 剩 50 归自然完成
	_ = s.Report(2, 330) // cp=300, u=30
	res, err := s.Notice(1, 0)
	if err != nil {
		t.Fatal(err)
	}
	eq(t, "completed list", res.Completed, []int64{1})
	eq(t, "saved list", res.Saved, []int64{2})
	items, err := s.Expire(1, 100)
	if err != nil {
		t.Fatal(err)
	}
	eq(t, "items", items, []ExpireItem{{1, 50}, {2, 0}})
	eq(t, "t1 p==cp", s.tasks[1].p, s.tasks[1].cp)
	eq(t, "t1 state", s.tasks[1].st, Pending)
	eq(t, "t2 p", s.tasks[2].p, int64(330))
	eq(t, "rs", s.tasks[1].rs, int64(1))
}

// TestSpotSelection 候选取空闲槽最多，并列取 id 小。
func TestSpotSelection(t *testing.T) {
	s := mustNew(t, 100, 1, 0)
	_ = s.AddNode(3, Spot, 5)
	_ = s.AddNode(1, Spot, 5)
	_ = s.AddNode(2, Spot, 8)
	for i := int64(1); i <= 4; i++ {
		_ = s.AddTask(i, 0, 100, 1000, 1)
	}
	s.Place()
	eq(t, "t1 most free", s.tasks[1].NodeID(), int64(2))
	eq(t, "t2 most free", s.tasks[2].NodeID(), int64(2))
	eq(t, "t3 most free", s.tasks[3].NodeID(), int64(2))
	// 放 3 个后 node2 free=5 与 node1、3 并列，取 id 最小
	eq(t, "t4 tie small id", s.tasks[4].NodeID(), int64(1))
}

// TestNotifiedNodeRejectsPlace 已通知节点不再接收放置。
func TestNotifiedNodeRejectsPlace(t *testing.T) {
	s := mustNew(t, 100, 1, 0)
	_ = s.AddNode(1, Spot, 1)
	_ = s.AddTask(1, 0, 100, 1000, 1)
	s.Place()
	if _, err := s.Notice(1, 0); err != nil {
		t.Fatal(err)
	}
	_ = s.AddTask(2, 0, 100, 1000, 1)
	s.Place()
	eq(t, "2 stays pending", s.tasks[2].st, Pending)
	eq(t, "node removed only on expire", s.nodes[1] != nil, true)
}

// TestOnDemandCostAndBudget 成本按放置时 w-cp 计，不足不阻塞后续。
func TestOnDemandCostAndBudget(t *testing.T) {
	s := mustNew(t, 100, 3, 300)
	_ = s.AddNode(1, OnDemand, 5)
	_ = s.AddTask(1, 0, 200, 1000, 1) // 成本 600，预算不足
	_ = s.AddTask(2, 0, 100, 1000, 1) // 成本 300，放得下
	_ = s.AddTask(3, 0, 50, 1000, 1)  // 之后预算 0，放不下
	s.Place()
	eq(t, "1 pending", s.tasks[1].st, Pending)
	eq(t, "2 running", s.tasks[2].st, Running)
	eq(t, "2 on O1", s.tasks[2].NodeID(), int64(1))
	eq(t, "3 pending", s.tasks[3].st, Pending)
	eq(t, "budget", s.Budget(), int64(0))

	// cp=100 后重放，1 号成本 (200-100)*3=300 恰好可放
	_ = s.Report(2, 100) // 2 完成（w=100），释放槽
	s.tasks[1].st = Running
	s.tasks[1].node = 1
	s.nodes[1].used++
	s.tasks[1].p, s.tasks[1].cp = 100, 100
	// 模拟节点失效后回待放置
	s.tasks[1].st = Pending
	s.tasks[1].node = 0
	s.nodes[1].used--
	s.bud = 300
	s.Place()
	eq(t, "1 placed with cp cost", s.tasks[1].st, Running)
	eq(t, "budget after", s.Budget(), int64(0))
}

// TestRestartTwoSkipsSpot rs 达 2 后只放 OnDemand。
func TestRestartTwoSkipsSpot(t *testing.T) {
	s := mustNew(t, 100, 2, 1000)
	_ = s.AddNode(1, Spot, 5)
	_ = s.AddNode(2, OnDemand, 5)
	_ = s.AddTask(1, 0, 500, 1000, 1)
	s.tasks[1].rs = 2
	s.tasks[1].cp = 300
	s.Place()
	eq(t, "on O1 not Spot", s.tasks[1].NodeID(), int64(2))
	eq(t, "cost deducted", s.Budget(), int64(600))
}

// TestRejectedOpsNoState 被拒绝操作不得改变任何状态。
func TestRejectedOpsNoState(t *testing.T) {
	if _, err := New(0, 1, 0); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("grace low: %v", err)
	}
	if _, err := New(1, 0, 0); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("pod low: %v", err)
	}
	if _, err := New(1, 1, -1); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("bud neg: %v", err)
	}

	s := mustNew(t, 100, 2, 600)
	_ = s.AddNode(1, Spot, 2)
	_ = s.AddNode(2, OnDemand, 1)
	_ = s.AddTask(1, 0, 100, 50, 10)

	// AddNode：参数非法优先于已存在
	if err := s.AddNode(1, Spot, 0); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("addnode invalid first: %v", err)
	}
	if err := s.AddNode(1, Spot, 2); !errors.Is(err, ErrExists) {
		t.Fatalf("addnode exists: %v", err)
	}
	if err := s.AddNode(3, NodeKind(9), 1); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("bad kind: %v", err)
	}
	// AddTask：非法优先于已存在
	if err := s.AddTask(1, 0, 0, 50, 10); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("addtask invalid first: %v", err)
	}
	if err := s.AddTask(1, 256, 100, 50, 10); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("prio high: %v", err)
	}
	if err := s.AddTask(1, 0, 100, 50, 10); !errors.Is(err, ErrExists) {
		t.Fatalf("addtask exists: %v", err)
	}

	// Report：不存在 -> 未运行 -> 进度非法
	if err := s.Report(99, 0); !errors.Is(err, ErrNotFound) {
		t.Fatalf("report missing: %v", err)
	}
	if err := s.Report(1, 1); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("report pending: %v", err)
	}
	s.Place()
	if err := s.Report(1, 101); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("report over w: %v", err)
	}

	// Notice 错误序：参数非法 -> 不存在 -> 非 Spot -> 已通知
	if _, err := s.Notice(1, -1); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("notice neg now: %v", err)
	}
	if _, err := s.Notice(99, 0); !errors.Is(err, ErrNotFound) {
		t.Fatalf("notice missing: %v", err)
	}
	if _, err := s.Notice(2, 0); !errors.Is(err, ErrNotSpot) {
		t.Fatalf("notice ond: %v", err)
	}
	if _, err := s.Notice(1, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Notice(1, 0); !errors.Is(err, ErrNotified) {
		t.Fatalf("notice twice: %v", err)
	}

	// Expire 错误序：参数非法 -> 不存在 -> 未通知 -> 未到期
	s2 := mustNew(t, 100, 2, 0)
	_ = s2.AddNode(1, Spot, 1)
	if _, err := s2.Expire(1, -1); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("expire neg: %v", err)
	}
	if _, err := s2.Expire(99, 0); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expire missing: %v", err)
	}
	if _, err := s2.Expire(1, 0); !errors.Is(err, ErrNotNotified) {
		t.Fatalf("expire unnotified: %v", err)
	}
	_, _ = s2.Notice(1, 0)
	if _, err := s2.Expire(1, 99); !errors.Is(err, ErrNotDue) {
		t.Fatalf("expire early: %v", err)
	}

	// 拒绝后状态保持：S1 仍在、仍 notified、预算不变、任务仍运行于 1
	eq(t, "budget unchanged", s.Budget(), int64(600))
	eq(t, "task still running", s.tasks[1].st, Running)
	eq(t, "node still present", s.nodes[1] != nil, true)
	if _, err := s.Expire(1, 100); err != nil {
		t.Fatal(err)
	}
}

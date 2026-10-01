package reassign

import (
	"errors"
	"reflect"
	"sync"
	"testing"
)

func newTestController(t *testing.T, maxInflight int, nodes ...string) *Controller {
	t.Helper()
	c := NewController(maxInflight)
	for _, n := range nodes {
		c.AddNode(n)
	}
	t.Logf("输入: NewController(maxInflight=%d), AddNode%v", maxInflight, nodes)
	return c
}

func mustState(t *testing.T, c *Controller, id string) PartitionState {
	t.Helper()
	st, ok := c.GetPartition(id)
	if !ok {
		t.Fatalf("分区 %s 不存在", id)
	}
	return st
}

func assertState(t *testing.T, c *Controller, id string, replicas, isr []string, leader string, reassigning bool) {
	t.Helper()
	st := mustState(t, c, id)
	t.Logf("判定依据: 期望 replicas=%v isr=%v leader=%q reassigning=%v", replicas, isr, leader, reassigning)
	t.Logf("输出: 实际 replicas=%v isr=%v leader=%q reassigning=%v", st.Replicas, st.ISR, st.Leader, st.Reassigning)
	if !reflect.DeepEqual(st.Replicas, replicas) ||
		!reflect.DeepEqual(st.ISR, isr) ||
		st.Leader != leader ||
		st.Reassigning != reassigning {
		t.Fatalf("状态不一致: 期望 replicas=%v isr=%v leader=%q reassigning=%v, 实际 %+v",
			replicas, isr, leader, reassigning, st)
	}
}

func assertErr(t *testing.T, op string, got, want error) {
	t.Helper()
	t.Logf("输入: %s; 输出: err=%v; 判定依据: 期望 %v", op, got, want)
	if !errors.Is(got, want) {
		t.Fatalf("%s: 期望错误 %v, 实际 %v", op, want, got)
	}
}

// 追平前不移除旧副本：新副本全部追平前，旧副本保留在副本列表与 ISR 中。
func TestOldReplicasKeptUntilCaughtUp(t *testing.T) {
	c := newTestController(t, 2, "n1", "n2", "n3", "n4")
	c.CreatePartition("p", []string{"n1", "n2"})
	t.Logf("输入: CreatePartition(p, [n1 n2])")

	assertErr(t, "StartReassign(p, [n3 n4])", c.StartReassign("p", []string{"n3", "n4"}), nil)
	assertState(t, c, "p",
		[]string{"n1", "n2", "n3", "n4"}, []string{"n1", "n2"}, "n1", true)

	assertErr(t, "ReportCaughtUp(p, n3)", c.ReportCaughtUp("p", "n3"), nil)
	assertState(t, c, "p",
		[]string{"n1", "n2", "n3", "n4"}, []string{"n1", "n2", "n3"}, "n1", true)

	assertErr(t, "ReportCaughtUp(p, n4)", c.ReportCaughtUp("p", "n4"), nil)
	assertState(t, c, "p",
		[]string{"n3", "n4"}, []string{"n3", "n4"}, "n3", false)
}

// 并发上限：进行中数达到全局上限后，新的开始被拒绝且不改变状态。
func TestConcurrencyLimitRejected(t *testing.T) {
	c := newTestController(t, 1, "n1", "n2", "n3", "n4", "n5", "n6")
	c.CreatePartition("p1", []string{"n1", "n2"})
	c.CreatePartition("p2", []string{"n3", "n4"})
	t.Logf("输入: CreatePartition(p1, [n1 n2]); CreatePartition(p2, [n3 n4])")

	assertErr(t, "StartReassign(p1, [n5 n6])", c.StartReassign("p1", []string{"n5", "n6"}), nil)
	assertErr(t, "StartReassign(p2, [n5 n6])", c.StartReassign("p2", []string{"n5", "n6"}), ErrConcurrencyLimit)
	assertState(t, c, "p2",
		[]string{"n3", "n4"}, []string{"n3", "n4"}, "n3", false)

	assertErr(t, "CancelReassign(p1)", c.CancelReassign("p1"), nil)
	assertErr(t, "StartReassign(p2, [n5 n6])", c.StartReassign("p2", []string{"n5", "n6"}), nil)
	assertState(t, c, "p2",
		[]string{"n3", "n4", "n5", "n6"}, []string{"n3", "n4"}, "n3", true)
}

// 开始重分配的错误按所列次序只报第一个，且被拒绝时不改变状态。
func TestStartReassignErrorOrder(t *testing.T) {
	c := newTestController(t, 1, "n1", "n2", "n3", "n4")
	c.CreatePartition("p", []string{"n1", "n2"})
	t.Logf("输入: CreatePartition(p, [n1 n2])")

	assertErr(t, "StartReassign(ghost, [n3])", c.StartReassign("ghost", []string{"n3"}), ErrPartitionNotFound)

	assertErr(t, "StartReassign(p, [n3])", c.StartReassign("p", []string{"n3"}), nil)
	assertErr(t, "StartReassign(p, [n4])", c.StartReassign("p", []string{"n4"}), ErrReassignInProgress)
	assertErr(t, "CancelReassign(p)", c.CancelReassign("p"), nil)

	assertErr(t, "StartReassign(p, [])", c.StartReassign("p", nil), ErrInvalidTarget)
	assertErr(t, "StartReassign(p, [n3 n3])", c.StartReassign("p", []string{"n3", "n3"}), ErrInvalidTarget)
	assertErr(t, "StartReassign(p, [n3 ghost])", c.StartReassign("p", []string{"n3", "ghost"}), ErrInvalidTarget)

	c.NodeDown("n4")
	t.Logf("输入: NodeDown(n4)")
	assertErr(t, "StartReassign(p, [n3 n4])", c.StartReassign("p", []string{"n3", "n4"}), ErrTargetNodeDown)
	c.NodeUp("n4")
	t.Logf("输入: NodeUp(n4)")

	assertErr(t, "StartReassign(p, [n1 n2])", c.StartReassign("p", []string{"n1", "n2"}), ErrTargetUnchanged)

	c.CreatePartition("p2", []string{"n3", "n4"})
	t.Logf("输入: CreatePartition(p2, [n3 n4])")
	assertErr(t, "StartReassign(p, [n3])", c.StartReassign("p", []string{"n3"}), nil)
	assertErr(t, "StartReassign(p2, [n1])", c.StartReassign("p2", []string{"n1"}), ErrConcurrencyLimit)
	assertState(t, c, "p2",
		[]string{"n3", "n4"}, []string{"n3", "n4"}, "n3", false)
}

// 追平上报与取消的错误按所列次序只报第一个。
func TestReportAndCancelErrorOrder(t *testing.T) {
	c := newTestController(t, 2, "n1", "n2", "n3")
	c.CreatePartition("p", []string{"n1", "n2"})
	t.Logf("输入: CreatePartition(p, [n1 n2])")

	assertErr(t, "ReportCaughtUp(ghost, n1)", c.ReportCaughtUp("ghost", "n1"), ErrPartitionNotFound)
	assertErr(t, "ReportCaughtUp(p, n3)", c.ReportCaughtUp("p", "n3"), ErrNotInReplicas)

	c.NodeDown("n2")
	t.Logf("输入: NodeDown(n2); 判定依据: n2 退出 ISR，领导者仍为 n1")
	assertState(t, c, "p", []string{"n1", "n2"}, []string{"n1"}, "n1", false)
	assertErr(t, "ReportCaughtUp(p, n2)", c.ReportCaughtUp("p", "n2"), ErrNodeDown)
	assertErr(t, "ReportCaughtUp(p, n1)", c.ReportCaughtUp("p", "n1"), ErrAlreadyInISR)

	assertErr(t, "CancelReassign(ghost)", c.CancelReassign("ghost"), ErrPartitionNotFound)
	assertErr(t, "CancelReassign(p)", c.CancelReassign("p"), ErrNoReassign)
}

// 节点恢复只改存活标记，须再次追平上报才重新进入 ISR。
func TestNodeRecoveryRequiresReport(t *testing.T) {
	c := newTestController(t, 2, "n1", "n2")
	c.CreatePartition("p", []string{"n1", "n2"})
	t.Logf("输入: CreatePartition(p, [n1 n2])")

	c.NodeDown("n1")
	t.Logf("输入: NodeDown(n1); 判定依据: 领导者改选为 n2")
	assertState(t, c, "p", []string{"n1", "n2"}, []string{"n2"}, "n2", false)

	c.NodeUp("n1")
	t.Logf("输入: NodeUp(n1); 判定依据: 仅存活标记变化，ISR 不变")
	assertState(t, c, "p", []string{"n1", "n2"}, []string{"n2"}, "n2", false)

	assertErr(t, "ReportCaughtUp(p, n1)", c.ReportCaughtUp("p", "n1"), nil)
	assertState(t, c, "p", []string{"n1", "n2"}, []string{"n1", "n2"}, "n2", false)
}

// 分区无领导者时，首个追平加入 ISR 的副本成为领导者。
func TestFirstJoinerBecomesLeader(t *testing.T) {
	c := newTestController(t, 2, "n1", "n2")
	c.CreatePartition("p", []string{"n1", "n2"})
	t.Logf("输入: CreatePartition(p, [n1 n2])")

	c.NodeDown("n1")
	c.NodeDown("n2")
	t.Logf("输入: NodeDown(n1); NodeDown(n2); 判定依据: ISR 为空，无领导者")
	assertState(t, c, "p", []string{"n1", "n2"}, nil, "", false)

	c.NodeUp("n2")
	t.Logf("输入: NodeUp(n2)")
	assertErr(t, "ReportCaughtUp(p, n2)", c.ReportCaughtUp("p", "n2"), nil)
	assertState(t, c, "p", []string{"n1", "n2"}, []string{"n2"}, "n2", false)
}

// 并发调用：效果等价于某个串行顺序，不变量始终成立。
func TestConcurrentOperationsKeepInvariants(t *testing.T) {
	c := newTestController(t, 3, "n1", "n2", "n3", "n4", "n5", "n6")
	for _, p := range []string{"p1", "p2", "p3"} {
		c.CreatePartition(p, []string{"n1", "n2"})
	}
	t.Logf("输入: CreatePartition(p1|p2|p3, [n1 n2]); 并发执行开始/追平/取消/宕机/恢复/查询")

	var wg sync.WaitGroup
	ops := []func(){
		func() { _ = c.StartReassign("p1", []string{"n3", "n4"}) },
		func() { _ = c.StartReassign("p2", []string{"n4", "n5"}) },
		func() { _ = c.StartReassign("p3", []string{"n5", "n6"}) },
		func() { _ = c.ReportCaughtUp("p1", "n3") },
		func() { _ = c.ReportCaughtUp("p1", "n4") },
		func() { _ = c.ReportCaughtUp("p2", "n5") },
		func() { _ = c.CancelReassign("p3") },
		func() { c.NodeDown("n2") },
		func() { c.NodeUp("n2") },
		func() { _, _ = c.GetPartition("p1") },
	}
	for round := 0; round < 20; round++ {
		for i, op := range ops {
			wg.Add(1)
			go func(i int, op func()) {
				defer wg.Done()
				op()
				t.Logf("输出: 第 %d 个并发操作完成", i)
			}(i, op)
		}
	}
	wg.Wait()

	inflight := 0
	for _, id := range []string{"p1", "p2", "p3"} {
		st := mustState(t, c, id)
		inISR := make(map[string]bool, len(st.ISR))
		for _, r := range st.ISR {
			inISR[r] = true
			if !contains(st.Replicas, r) {
				t.Fatalf("不变量违反: ISR 成员 %s 不在副本列表 %v", r, st.Replicas)
			}
		}
		if st.Leader != "" && !inISR[st.Leader] {
			t.Fatalf("不变量违反: 领导者 %s 不在 ISR %v", st.Leader, st.ISR)
		}
		if st.Reassigning {
			inflight++
		}
		t.Logf("判定依据: %s 满足 ISR⊆replicas 且 leader∈ISR 或无; 状态 %+v", id, st)
	}
	if inflight > 3 {
		t.Fatalf("不变量违反: 进行中数 %d 超过并发上限 3", inflight)
	}
	t.Logf("判定依据: 进行中数 %d ≤ 并发上限 3", inflight)
}

// 相同事件序列得到完全相同的状态。
func TestDeterministicReplay(t *testing.T) {
	run := func() map[string]PartitionState {
		c := NewController(2)
		for _, n := range []string{"n1", "n2", "n3", "n4"} {
			c.AddNode(n)
		}
		c.CreatePartition("p", []string{"n1", "n2"})
		_ = c.StartReassign("p", []string{"n3", "n4"})
		_ = c.ReportCaughtUp("p", "n3")
		c.NodeDown("n1")
		_ = c.ReportCaughtUp("p", "n4")
		c.NodeUp("n1")
		st, _ := c.GetPartition("p")
		return map[string]PartitionState{"p": st}
	}
	first := run()
	for i := 0; i < 5; i++ {
		got := run()
		if !reflect.DeepEqual(first, got) {
			t.Fatalf("第 %d 次重放结果不同: %v vs %v", i, first, got)
		}
	}
	t.Logf("输入: 固定事件序列重放 6 次; 输出: %+v; 判定依据: 每次结果完全相同", first)
}

// 目标不含现领导者：完成时领导者改选 T 中第一个在 ISR 的成员（按 T 次序）。
func TestLeaderReelectedWhenTargetExcludesLeader(t *testing.T) {
	c := newTestController(t, 2, "n1", "n2", "n3")
	c.CreatePartition("p", []string{"n1", "n2"})
	t.Logf("输入: CreatePartition(p, [n1 n2]); 领导者为 n1")

	assertErr(t, "StartReassign(p, [n2 n3])", c.StartReassign("p", []string{"n2", "n3"}), nil)
	assertState(t, c, "p",
		[]string{"n1", "n2", "n3"}, []string{"n1", "n2"}, "n1", true)

	assertErr(t, "ReportCaughtUp(p, n3)", c.ReportCaughtUp("p", "n3"), nil)
	assertState(t, c, "p",
		[]string{"n2", "n3"}, []string{"n2", "n3"}, "n2", false)
}

// 仅调整次序：开始即完成且领导者不变。
func TestReorderOnlyCompletesImmediatelyLeaderUnchanged(t *testing.T) {
	c := newTestController(t, 2, "n1", "n2", "n3")
	c.CreatePartition("p", []string{"n1", "n2", "n3"})
	t.Logf("输入: CreatePartition(p, [n1 n2 n3]); 领导者为 n1")

	assertErr(t, "StartReassign(p, [n3 n1 n2])", c.StartReassign("p", []string{"n3", "n1", "n2"}), nil)
	assertState(t, c, "p",
		[]string{"n3", "n1", "n2"}, []string{"n3", "n1", "n2"}, "n1", false)
}

// 新增节点宕机后取消：回滚到原列表，ISR 与原列表取交，领导者保持可用。
func TestCancelRollbackAfterNewNodeDown(t *testing.T) {
	c := newTestController(t, 2, "n1", "n2", "n3")
	c.CreatePartition("p", []string{"n1", "n2"})
	t.Logf("输入: CreatePartition(p, [n1 n2])")

	assertErr(t, "StartReassign(p, [n2 n3])", c.StartReassign("p", []string{"n2", "n3"}), nil)
	assertErr(t, "ReportCaughtUp(p, n3)", c.ReportCaughtUp("p", "n3"), nil)
	assertState(t, c, "p",
		[]string{"n2", "n3"}, []string{"n2", "n3"}, "n2", false)

	// 再次重分配，引入 n1 之外的新节点场景：先回到含新节点的进行中状态。
	assertErr(t, "StartReassign(p, [n3 n1])", c.StartReassign("p", []string{"n3", "n1"}), nil)
	assertState(t, c, "p",
		[]string{"n2", "n3", "n1"}, []string{"n2", "n3"}, "n2", true)

	c.NodeDown("n1")
	t.Logf("输入: NodeDown(n1); 判定依据: n1 不在 ISR，仅存活标记变化")
	assertState(t, c, "p",
		[]string{"n2", "n3", "n1"}, []string{"n2", "n3"}, "n2", true)

	assertErr(t, "CancelReassign(p)", c.CancelReassign("p"), nil)
	assertState(t, c, "p",
		[]string{"n2", "n3"}, []string{"n2", "n3"}, "n2", false)
}

// 领导者宕机与完成交错：领导者宕机先改选，随后追平完成再按规则定领导者。
func TestLeaderDownInterleavedWithCompletion(t *testing.T) {
	c := newTestController(t, 2, "n1", "n2", "n3", "n4")
	c.CreatePartition("p", []string{"n1", "n2"})
	t.Logf("输入: CreatePartition(p, [n1 n2]); 领导者为 n1")

	assertErr(t, "StartReassign(p, [n3 n4])", c.StartReassign("p", []string{"n3", "n4"}), nil)

	c.NodeDown("n1")
	t.Logf("输入: NodeDown(n1); 判定依据: 领导者改选副本列表中第一个在 ISR 的成员 n2")
	assertState(t, c, "p",
		[]string{"n1", "n2", "n3", "n4"}, []string{"n2"}, "n2", true)

	assertErr(t, "ReportCaughtUp(p, n3)", c.ReportCaughtUp("p", "n3"), nil)
	assertErr(t, "ReportCaughtUp(p, n4)", c.ReportCaughtUp("p", "n4"), nil)
	assertState(t, c, "p",
		[]string{"n3", "n4"}, []string{"n3", "n4"}, "n3", false)
}

package reassign

import (
	"bytes"
	"io"
	"strings"
	"sync"
	"testing"
)

func newTestController(t *testing.T, maxConcurrent int, nodes ...string) (*Controller, *bytes.Buffer) {
	t.Helper()
	var buf bytes.Buffer
	c := NewWithLogger(maxConcurrent, io.MultiWriter(&buf, testWriter{t}))
	for _, node := range nodes {
		if err := c.AddNode(node); err != nil {
			t.Fatalf("AddNode(%q): %v", node, err)
		}
	}
	return c, &buf
}

type testWriter struct{ t *testing.T }

func (w testWriter) Write(p []byte) (int, error) {
	w.t.Log(strings.TrimRight(string(p), "\n"))
	return len(p), nil
}

func mustAddPartition(t *testing.T, c *Controller, name string, replicas ...string) {
	t.Helper()
	if err := c.AddPartition(name, replicas); err != nil {
		t.Fatalf("AddPartition(%q, %v): %v", name, replicas, err)
	}
}

func assertView(t *testing.T, c *Controller, name string, wantReplicas, wantISR []string, wantLeader string) {
	t.Helper()
	got, ok := c.Get(name)
	if !ok {
		t.Fatalf("partition %q not found", name)
	}
	if !eqSlice(got.Replicas, wantReplicas) {
		t.Errorf("partition %q replicas = %v, want %v", name, got.Replicas, wantReplicas)
	}
	if !eqSlice(got.ISR, wantISR) {
		t.Errorf("partition %q ISR = %v, want %v", name, got.ISR, wantISR)
	}
	if got.Leader != wantLeader {
		t.Errorf("partition %q leader = %q, want %q", name, got.Leader, wantLeader)
	}
}

func eqSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func assertReason(t *testing.T, err error, want Reason) {
	t.Helper()
	if want == "" {
		if err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
		return
	}
	re, ok := err.(*ReassignError)
	if !ok {
		t.Fatalf("err = %v, want *ReassignError(%s)", err, want)
	}
	if re.Reason != want {
		t.Fatalf("reason = %s, want %s", re.Reason, want)
	}
}

// 旧副本在新副本追平前不得被移除；全部追平后按目标次序完成。
func TestOldReplicasRetainedUntilCaughtUp(t *testing.T) {
	c, _ := newTestController(t, 4, "a", "b", "c")
	mustAddPartition(t, c, "p", "a", "b")

	if err := c.StartReassign("p", []string{"b", "c"}); err != nil {
		t.Fatal(err)
	}
	assertView(t, c, "p", []string{"a", "b", "c"}, []string{"a", "b"}, "a")

	if err := c.CatchUp("p", "c"); err != nil {
		t.Fatal(err)
	}
	assertView(t, c, "p", []string{"b", "c"}, []string{"b", "c"}, "b")
}

// 目标不含现领导者时，完成阶段按目标次序改选。
func TestElectionWhenTargetExcludesLeader(t *testing.T) {
	c, _ := newTestController(t, 4, "a", "b", "c")
	mustAddPartition(t, c, "p", "a", "b")

	if err := c.StartReassign("p", []string{"c", "b"}); err != nil {
		t.Fatal(err)
	}
	assertView(t, c, "p", []string{"a", "b", "c"}, []string{"a", "b"}, "a")

	if err := c.CatchUp("p", "c"); err != nil {
		t.Fatal(err)
	}
	assertView(t, c, "p", []string{"c", "b"}, []string{"c", "b"}, "c")
}

// 仅调整次序时开始即完成，领导者不变。
func TestReorderOnlyKeepsLeader(t *testing.T) {
	c, _ := newTestController(t, 4, "a", "b", "c")
	mustAddPartition(t, c, "p", "a", "b", "c")

	if err := c.StartReassign("p", []string{"c", "a", "b"}); err != nil {
		t.Fatal(err)
	}
	assertView(t, c, "p", []string{"c", "a", "b"}, []string{"c", "a", "b"}, "a")
	if c.RunningCount() != 0 {
		t.Fatalf("running count = %d, want 0", c.RunningCount())
	}
	assertReason(t, c.Cancel("p"), ReasonNotRunning)
}

// 新增节点宕机后取消：副本列表恢复为原列表，ISR 取与原列表的交集。
func TestCancelRollbackAfterNewNodeDown(t *testing.T) {
	c, _ := newTestController(t, 4, "a", "b", "c")
	mustAddPartition(t, c, "p", "a", "b")

	if err := c.StartReassign("p", []string{"b", "c"}); err != nil {
		t.Fatal(err)
	}
	// 进行中：旧副本 a,b 保留，新增 c 在末尾，尚未入 ISR。
	assertView(t, c, "p", []string{"a", "b", "c"}, []string{"a", "b"}, "a")
	c.NodeDown("c")
	assertView(t, c, "p", []string{"a", "b", "c"}, []string{"a", "b"}, "a")

	if err := c.Cancel("p"); err != nil {
		t.Fatal(err)
	}
	assertView(t, c, "p", []string{"a", "b"}, []string{"a", "b"}, "a")
	if c.RunningCount() != 0 {
		t.Fatalf("running count = %d, want 0", c.RunningCount())
	}
}

// 领导者宕机后与完成交错：宕机改选，追平后仍按规则完成。
func TestLeaderDownInterleavedWithCompletion(t *testing.T) {
	c, _ := newTestController(t, 4, "a", "b", "c")
	mustAddPartition(t, c, "p", "a", "b")

	if err := c.StartReassign("p", []string{"b", "c"}); err != nil {
		t.Fatal(err)
	}
	c.NodeDown("a")
	assertView(t, c, "p", []string{"a", "b", "c"}, []string{"b"}, "b")
	if c.RunningCount() != 1 {
		t.Fatalf("running count = %d, want 1", c.RunningCount())
	}

	if err := c.CatchUp("p", "c"); err != nil {
		t.Fatal(err)
	}
	assertView(t, c, "p", []string{"b", "c"}, []string{"b", "c"}, "b")

	c.NodeUp("a")
	assertView(t, c, "p", []string{"b", "c"}, []string{"b", "c"}, "b")
	assertReason(t, c.CatchUp("p", "a"), ReasonNodeNotReplica)
}

// 无领导者时首个追平者成为领导者。
func TestFirstCaughtUpBecomesLeaderWhenNoLeader(t *testing.T) {
	c, _ := newTestController(t, 4, "a", "b")
	mustAddPartition(t, c, "p", "a", "b")
	c.NodeDown("a")
	c.NodeDown("b")
	assertView(t, c, "p", []string{"a", "b"}, nil, "")

	c.NodeUp("b")
	assertReason(t, c.CatchUp("p", "b"), "")
	assertView(t, c, "p", []string{"a", "b"}, []string{"b"}, "b")
}

// 并发上限：达到上限的开始被拒，完成或取消释放名额后可再开始。
func TestConcurrencyLimitRejectsThenAllows(t *testing.T) {
	c, _ := newTestController(t, 2, "a", "b", "c", "d", "e", "f")
	mustAddPartition(t, c, "p1", "a", "b")
	mustAddPartition(t, c, "p2", "a", "b")
	mustAddPartition(t, c, "p3", "a", "b")

	if err := c.StartReassign("p1", []string{"a", "c"}); err != nil {
		t.Fatal(err)
	}
	if err := c.StartReassign("p2", []string{"a", "d"}); err != nil {
		t.Fatal(err)
	}
	assertReason(t, c.StartReassign("p3", []string{"a", "e"}), ReasonConcurrencyLimit)
	if c.RunningCount() != 2 {
		t.Fatalf("running count = %d, want 2", c.RunningCount())
	}

	if err := c.CatchUp("p1", "c"); err != nil {
		t.Fatal(err)
	}
	if err := c.StartReassign("p3", []string{"a", "e"}); err != nil {
		t.Fatal(err)
	}
	if c.RunningCount() != 2 {
		t.Fatalf("running count = %d, want 2", c.RunningCount())
	}
	if err := c.Cancel("p3"); err != nil {
		t.Fatal(err)
	}
	if err := c.StartReassign("p3", []string{"a", "f"}); err != nil {
		t.Fatal(err)
	}
}

// 开始错误按规定次序只报第一个，且拒绝不改变状态。
func TestStartErrorOrdering(t *testing.T) {
	c, _ := newTestController(t, 1, "a", "b", "down")
	mustAddPartition(t, c, "p", "a", "b")

	assertReason(t, c.StartReassign("missing", []string{"a"}), ReasonPartitionNotFound)

	if err := c.StartReassign("p", []string{"a", "x"}); err == nil {
		t.Fatal("expected unknown-node rejection")
	}
	// 用合法目标先占住"进行中"状态。
	mustAddPartition(t, c, "holder", "a", "b")

	// p 当前空闲：依次验证空、重复、未知、宕机、完全相同。
	assertReason(t, c.StartReassign("p", nil), ReasonEmptyTarget)
	assertReason(t, c.StartReassign("p", []string{"a", "a"}), ReasonDuplicateReplica)
	assertReason(t, c.StartReassign("p", []string{"a", "ghost"}), ReasonUnknownNode)

	c.NodeDown("down")
	assertReason(t, c.StartReassign("p", []string{"a", "down"}), ReasonNodeDown)
	assertReason(t, c.StartReassign("p", []string{"a", "b"}), ReasonTargetUnchanged)

	// 仅次序调整立即完成且不占名额，然后占用唯一名额触发上限。
	if err := c.StartReassign("p", []string{"b", "a"}); err != nil {
		t.Fatal(err)
	}
	if err := c.StartReassign("holder", []string{"a"}); err != nil {
		t.Fatal(err)
	}
	assertReason(t, c.StartReassign("p", []string{"a"}), ReasonConcurrencyLimit)

	// 所有拒绝后 p 的状态仅反映那次成功的次序调整。
	assertView(t, c, "p", []string{"b", "a"}, []string{"b", "a"}, "a")

	// "已有进行中"优先于其后的所有校验：holder 进行中时传空目标也报 already_running。
	assertReason(t, c.StartReassign("holder", nil), ReasonAlreadyRunning)
}

// 追平上报错误次序，且拒绝不改变 ISR。
func TestCatchUpErrorOrdering(t *testing.T) {
	c, _ := newTestController(t, 1, "a", "b", "x")
	mustAddPartition(t, c, "p", "a", "b")

	assertReason(t, c.CatchUp("missing", "a"), ReasonPartitionNotFound)
	assertReason(t, c.CatchUp("p", "x"), ReasonNodeNotReplica)
	c.NodeDown("b")
	assertReason(t, c.CatchUp("p", "b"), ReasonNodeDown)
	assertReason(t, c.CatchUp("p", "a"), ReasonAlreadyInISR)

	assertView(t, c, "p", []string{"a", "b"}, []string{"a"}, "a")
}

func TestCancelErrors(t *testing.T) {
	c, _ := newTestController(t, 1, "a", "b")
	assertReason(t, c.Cancel("missing"), ReasonPartitionNotFound)
	mustAddPartition(t, c, "p", "a", "b")
	assertReason(t, c.Cancel("p"), ReasonNotRunning)
}

// 节点宕机影响所有分区；ISR 清空时无领导者；恢复后须显式追平。
func TestNodeDownAcrossPartitions(t *testing.T) {
	c, _ := newTestController(t, 2, "a", "b")
	mustAddPartition(t, c, "p1", "a", "b")
	mustAddPartition(t, c, "p2", "a")

	c.NodeDown("a")
	assertView(t, c, "p1", []string{"a", "b"}, []string{"b"}, "b")
	assertView(t, c, "p2", []string{"a"}, nil, "")

	c.NodeUp("a")
	assertView(t, c, "p2", []string{"a"}, nil, "")
	if err := c.CatchUp("p2", "a"); err != nil {
		t.Fatal(err)
	}
	assertView(t, c, "p2", []string{"a"}, []string{"a"}, "a")
}

// 并发交错：结果必须等价于某串行顺序，不变量恒成立。
func TestConcurrentInterleaving(t *testing.T) {
	c, _ := newTestController(t, 4, "a", "b", "c", "d")
	mustAddPartition(t, c, "p", "a", "b")

	var wg sync.WaitGroup
	ops := []func(){
		func() { _ = c.StartReassign("p", []string{"c", "d"}) },
		func() { _ = c.CatchUp("p", "c") },
		func() { _ = c.CatchUp("p", "d") },
		func() { _ = c.Cancel("p") },
		func() { c.NodeDown("a") },
		func() { c.NodeUp("a") },
		func() {
			v, ok := c.Get("p")
			if ok {
				assertInvariants(t, v)
			}
		},
	}
	for i := 0; i < 10; i++ {
		for _, op := range ops {
			wg.Add(1)
			op := op
			go func() { defer wg.Done(); op() }()
		}
	}
	wg.Wait()

	view, ok := c.Get("p")
	if !ok {
		t.Fatal("partition missing")
	}
	assertInvariants(t, view)
	if c.RunningCount() > 4 || c.RunningCount() < 0 {
		t.Fatalf("running count out of range: %d", c.RunningCount())
	}
}

func assertInvariants(t *testing.T, v PartitionView) {
	t.Helper()
	isr := map[string]bool{}
	for _, node := range v.ISR {
		isr[node] = true
	}
	for _, node := range v.ISR {
		found := false
		for _, replica := range v.Replicas {
			if replica == node {
				found = true
			}
		}
		if !found {
			t.Errorf("invariant violated: ISR node %q not in replicas %v", node, v.Replicas)
		}
	}
	if v.Leader != "" && !isr[v.Leader] {
		t.Errorf("invariant violated: leader %q not in ISR %v", v.Leader, v.ISR)
	}
}

// 日志中包含输入、输出与判定依据。
func TestLogsContainInputOutputAndDecision(t *testing.T) {
	c, buf := newTestController(t, 4, "a", "b", "c")
	mustAddPartition(t, c, "p", "a", "b")

	if err := c.StartReassign("p", []string{"b", "c"}); err != nil {
		t.Fatal(err)
	}
	if err := c.CatchUp("p", "c"); err != nil {
		t.Fatal(err)
	}
	log := buf.String()
	for _, want := range []string{
		"StartReassign partition=\"p\"",
		"target=[b c]",
		"CatchUp",
		"all target members",
		"replicas=[b c]",
		"leader=\"b\"",
	} {
		if !strings.Contains(log, want) {
			t.Errorf("log missing %q\nfull log:\n%s", want, log)
		}
	}
}
